package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// CtWebAPI is the API used by the 诚通网盘 web client (https://my.ctfile.com).
// The retired OpenAPI session flow is replaced by the web login token, which is
// sent as a bearer token on every request.
const CtWebAPI = "https://api.ctfile.com"

// ctWebClient talks to the 诚通 web front-end API. Every endpoint is a POST
// with a JSON body; responses carry {"code":200,...} on success and
// {"code":<n>,"message":...} on failure.
type ctWebClient struct {
	client *http.Client
	a      Account
	ws     int
}

func newCtWebClient(client *http.Client, a Account) *ctWebClient {
	return &ctWebClient{client: client, a: a}
}

func (c *ctWebClient) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(c.a.Endpoint), "/")
	if base == "" {
		base = CtWebAPI
	}
	return base
}

type ctWebReply struct {
	Code       int             `json:"code"`
	Message    string          `json:"message"`
	MessageKey string          `json:"message_key"`
	ErrorType  string          `json:"error_type"`
	Data       json.RawMessage `json:"data"`
}

func ctWebFailure(reply ctWebReply) error {
	if reply.Code == 401 || reply.Code == 403 {
		return errors.New("诚通网页版登录已失效，请重新登录")
	}
	message := strings.TrimSpace(reply.Message)
	if message == "" {
		message = strings.TrimSpace(reply.MessageKey)
	}
	if message == "" {
		message = fmt.Sprintf("诚通返回错误代码 %d", reply.Code)
	}
	return errors.New("诚通：" + message)
}

// post sends one authenticated JSON request and decodes the reply into out.
func (c *ctWebClient) post(ctx context.Context, path string, payload any, out any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	body, e := json.Marshal(payload)
	if e != nil {
		return errors.New("诚通请求参数无效")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.endpoint()+path, strings.NewReader(string(body)))
	if e != nil {
		return errors.New("诚通接口地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("User-Agent", userAgent(c.a))
	if token := strings.TrimSpace(c.a.Secret); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("连接诚通接口失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if e != nil {
		return errors.New("诚通响应读取失败")
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode)
	}
	var reply ctWebReply
	if e := json.Unmarshal(raw, &reply); e != nil {
		return errors.New("诚通返回了无法识别的内容，接口可能已调整")
	}
	if reply.Code != 0 && reply.Code != 200 {
		return ctWebFailure(reply)
	}
	if out == nil {
		return nil
	}
	// Some endpoints put their payload at the top level while others nest it
	// under "data"; decode the raw body so both shapes work.
	if e := json.Unmarshal(raw, out); e != nil {
		return errors.New("诚通返回数据结构已变化，暂无法解析")
	}
	return nil
}

func ctWebMentionsWorkspace(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "workspace") || strings.Contains(text, "工作空间") || strings.Contains(text, "空间")
}

// postAdaptive keeps the workspace id the web client always sends. Personal
// accounts differ: some ignore the field, some want the default space id and
// some accept 0, so every request walks the same short ladder and remembers the
// value that worked.
func (c *ctWebClient) postAdaptive(ctx context.Context, path string, payload map[string]any, out any) error {
	var last error
	var spaceErr error
	tried := map[int]bool{}
	try := func(ws int) bool {
		if tried[ws] {
			return false
		}
		tried[ws] = true
		payload["workspace_id"] = ws
		if e := c.post(ctx, path, payload, out); e == nil {
			if ws > 0 {
				c.ws = ws
			}
			return true
		} else {
			last = e
		}
		return false
	}
	if try(c.ws) || try(0) {
		return nil
	}
	ws, spaceErr := c.workspace(ctx)
	if spaceErr == nil && ws > 0 && try(ws) {
		return nil
	}
	// Last resort: some endpoints reject the field outright.
	delete(payload, "workspace_id")
	if e := c.post(ctx, path, payload, out); e == nil {
		return nil
	} else {
		last = e
	}
	if spaceErr != nil {
		return fmt.Errorf("%v；%v", spaceErr, last)
	}
	return last
}

// workspace resolves the default workspace id. The web client always sends it;
// older personal accounts accept requests without it, so this stays lazy.
func (c *ctWebClient) workspace(ctx context.Context) (int, error) {
	if c.ws > 0 {
		return c.ws, nil
	}
	// The web client always sends a pagination body here; an empty body makes
	// some accounts report an empty list, so try the documented shapes first.
	var last []byte
	for _, payload := range []map[string]any{
		{"start": 0, "pagesize": 100},
		{"start": 0},
		{},
	} {
		var raw json.RawMessage
		if e := c.post(ctx, "/home/workspace/manage/list", payload, &raw); e != nil {
			last = []byte(e.Error())
			continue
		}
		last = raw
		if id := ctWebWorkspaceID(raw); id > 0 {
			c.ws = id
			return id, nil
		}
	}
	return 0, fmt.Errorf("无法确定诚通工作空间（返回：%s），请重新登录后再试", ctWebPreview(last))
}

// ctWebPreview shortens a response for an error message so a failed lookup can
// be diagnosed from the UI without exposing a token.
func ctWebPreview(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if text == "" {
		return "空响应"
	}
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

// ctWebWorkspaceID finds a workspace id in a listing whose field names differ
// between account types (个人空间 vs 共享空间).
func ctWebWorkspaceID(raw []byte) int {
	var value any
	if e := json.Unmarshal(raw, &value); e != nil {
		return 0
	}
	fallback := 0
	var walk func(any) int
	walk = func(node any) int {
		switch typed := node.(type) {
		case map[string]any:
			for _, key := range []string{"id", "workspace_id", "ws_id", "space_id", "spaceId", "wid"} {
				id := ctWebIntValue(typed[key])
				if id <= 0 {
					continue
				}
				flag := false
				for _, name := range []string{"is_default", "isDefault", "default"} {
					if v, ok := typed[name]; ok {
						flag = fmt.Sprint(v) == "true" || fmt.Sprint(v) == "1"
					}
				}
				if flag {
					return id
				}
				if fallback == 0 {
					fallback = id
				}
			}
			for key, v := range typed {
				if id, e := strconv.Atoi(key); e == nil && id > 0 && fallback == 0 {
					if _, ok := v.(map[string]any); ok {
						fallback = id
					}
				}
			}
			for _, v := range typed {
				if found := walk(v); found > 0 {
					return found
				}
			}
		case []any:
			for _, v := range typed {
				if found := walk(v); found > 0 {
					return found
				}
			}
		}
		return 0
	}
	if found := walk(value); found > 0 {
		return found
	}
	return fallback
}

func ctWebIntValue(v any) int {
	switch typed := v.(type) {
	case nil:
		return 0
	case float64:
		if typed > 0 && typed < 1<<31 {
			return int(typed)
		}
	case json.Number:
		if n, e := strconv.Atoi(typed.String()); e == nil && n > 0 {
			return n
		}
	case string:
		if n, e := strconv.Atoi(strings.TrimSpace(typed)); e == nil && n > 0 {
			return n
		}
	}
	return 0
}

// ctWebEntry is one item of a 诚通 folder listing. Field names differ between
// list endpoints, so the identifiers stay raw and are normalised on use.
type ctWebEntry struct {
	FID    json.RawMessage `json:"fid"`
	ID     json.RawMessage `json:"id"`
	Key    json.RawMessage `json:"key"`
	Name   string          `json:"name"`
	Ext    string          `json:"ext"`
	Type   json.RawMessage `json:"type"`
	Folder *bool           `json:"is_folder"`
	Size   int64           `json:"size"`
	Date   int64           `json:"date"`
}

func ctWebScalar(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return ""
	}
	if unquoted, e := strconv.Unquote(text); e == nil {
		return strings.TrimSpace(unquoted)
	}
	return text
}

func (e ctWebEntry) ident() string {
	for _, raw := range []json.RawMessage{e.FID, e.ID, e.Key} {
		if id := ctWebScalar(raw); id != "" && id != "0" {
			return id
		}
	}
	return ""
}

func (e ctWebEntry) isFolder() bool {
	if e.Folder != nil {
		return *e.Folder
	}
	if strings.EqualFold(e.Ext, "folder") || strings.EqualFold(ctWebScalar(e.Type), "folder") {
		return true
	}
	return strings.HasPrefix(e.ident(), "d")
}

type ctWebList struct {
	Data    ctWebListData `json:"data"`
	Results []ctWebEntry  `json:"results"`
	Total   int           `json:"totalNum"`
}

type ctWebListData struct {
	Results  []ctWebEntry `json:"results"`
	FolderID string       `json:"folder_id"`
	TotalNum int          `json:"totalNum"`
}

// list reads one folder. It pages until the reported total is reached so that
// large folders still show every item.
func (c *ctWebClient) list(ctx context.Context, parent string) (FilePage, error) {
	if strings.TrimSpace(parent) == "" {
		parent = cloudRoot(c.a.Kind)
	}
	page := filePage(c.a, parent)
	var items []ctWebEntry
	start := 0
	const pageSize = 200
	for hop := 0; hop < 50; hop++ {
		payload := map[string]any{"folder_id": parent, "start": start, "pagesize": pageSize}
		var reply ctWebList
		if e := c.postAdaptive(ctx, "/home/file/browse/list", payload, &reply); e != nil {
			return page, e
		}
		results, total := reply.Data.Results, reply.Data.TotalNum
		if results == nil {
			results, total = reply.Results, reply.Total
		}
		items = append(items, results...)
		if len(results) < pageSize || (total > 0 && len(items) >= total) {
			break
		}
		start += len(results)
	}
	for _, f := range items {
		id := f.ident()
		if id == "" || strings.TrimSpace(f.Name) == "" {
			continue
		}
		entry := CloudEntry{ID: id, Name: f.Name, Folder: f.isFolder()}
		if !entry.Folder {
			size := f.Size
			entry.Size = &size
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

// validCtWebFolder accepts the identifiers the web client uses: "d0" for the
// root, "d<digits>" for folders and plain digits for older file ids.
func validCtWebFolder(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	if len(s) > 64 {
		return false
	}
	body := s
	if body[0] == 'd' {
		body = body[1:]
	}
	if body == "" {
		return false
	}
	for _, r := range body {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ListCtWebFolders lists subfolders only, for the folder picker.
func ListCtWebFolders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	if strings.TrimSpace(parent) == "" {
		parent = cloudRoot(a.Kind)
	}
	page := filePage(a, parent)
	out := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: page.Breadcrumbs}
	entries, e := newCtWebClient(client, a).list(ctx, parent)
	if e != nil {
		return out, e
	}
	for _, entry := range entries.Entries {
		if entry.Folder {
			out.Folders = append(out.Folders, CloudFolder{ID: entry.ID, Name: entry.Name})
		}
	}
	return out, nil
}

// VerifyCtWebSession checks the web login token and returns the account name.
func VerifyCtWebSession(ctx context.Context, client *http.Client, a Account) (string, error) {
	if a.Kind != "ctweb" {
		return "", errors.New("不是诚通网页版账号")
	}
	var info struct {
		Data struct {
			Name     string `json:"name"`
			Nickname string `json:"nickname"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"data"`
		Name     string `json:"name"`
		Nickname string `json:"nickname"`
		Email    string `json:"email"`
	}
	if e := newCtWebClient(client, a).post(ctx, "/home/user/profile/info", nil, &info); e != nil {
		return "", e
	}
	for _, name := range []string{info.Data.Nickname, info.Data.Name, info.Data.Username, info.Data.Email, info.Nickname, info.Name, info.Email} {
		if text := strings.TrimSpace(name); text != "" {
			return text, nil
		}
	}
	return "", nil
}

// CheckCtWebSession verifies the token without exposing the account name.
func CheckCtWebSession(ctx context.Context, client *http.Client, a Account) error {
	_, e := VerifyCtWebSession(ctx, client, a)
	return e
}

func ctWebMkdir(ctx context.Context, client *http.Client, a Account, parent, name string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, `/\`) {
		return "", errors.New("目录名称无效")
	}
	c := newCtWebClient(client, a)
	if strings.TrimSpace(parent) == "" {
		parent = cloudRoot(a.Kind)
	}
	var reply struct {
		Data struct {
			FolderID string `json:"folder_id"`
		} `json:"data"`
		FolderID string `json:"folder_id"`
	}
	payload := map[string]any{"folder_id": parent, "name": name}
	e := c.postAdaptive(ctx, "/home/file/manage/create-folder", payload, &reply)
	if e != nil {
		if strings.Contains(e.Error(), "已存在") || strings.Contains(e.Error(), "exists") {
			return "", errors.New("目标目录已有同名项目，请更换名称")
		}
		return "", e
	}
	id := strings.TrimSpace(reply.Data.FolderID)
	if id == "" {
		id = strings.TrimSpace(reply.FolderID)
	}
	if id == "" {
		id = c.findFolderID(ctx, parent, name)
	}
	if id == "" {
		return "", errors.New("诚通已创建目录，但未返回目录标识；请刷新目录后重试")
	}
	return id, nil
}

func (c *ctWebClient) findFolderID(ctx context.Context, parent, name string) string {
	page, e := c.list(ctx, parent)
	if e != nil {
		return ""
	}
	for _, entry := range page.Entries {
		if entry.Folder && entry.Name == name {
			return entry.ID
		}
	}
	return ""
}

func ctWebRename(ctx context.Context, client *http.Client, a Account, id, name string) error {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, `/\`) {
		return errors.New("名称无效")
	}
	c := newCtWebClient(client, a)
	payload := map[string]any{"id": ctWebIDParam(id), "name": name, "is_rename": 1}
	return c.postAdaptive(ctx, "/home/file/manage/rename", payload, nil)
}

func ctWebDelete(ctx context.Context, client *http.Client, a Account, id string) error {
	c := newCtWebClient(client, a)
	// The web client joins the selected fids into one comma separated value.
	payload := map[string]any{"ids": strings.TrimSpace(id), "force": 0}
	return c.postAdaptive(ctx, "/home/file/manage/delete", payload, nil)
}

// ctWebDownloadURL asks the web API for a temporary direct link.
func ctWebDownloadURL(ctx context.Context, client *http.Client, a Account, parent, id string) (string, error) {
	c := newCtWebClient(client, a)
	var raw json.RawMessage
	payload := map[string]any{"file_id": ctWebIDParam(id), "carrier": 0}
	if strings.TrimSpace(parent) != "" {
		payload["folder_id"] = strings.TrimSpace(parent)
	}
	e := c.postAdaptive(ctx, "/home/file/browse/fetch-url", payload, &raw)
	if e != nil {
		// The web client falls back to the streaming endpoint; try it before
		// reporting the first failure.
		if e := c.postAdaptive(ctx, "/home/file/browse/stream", payload, &raw); e != nil {
			return "", e
		}
	}
	var value any
	if e := json.Unmarshal(raw, &value); e != nil {
		return "", errors.New("诚通下载地址无法解析")
	}
	address := ctWebFirstLink(value)
	if address == "" {
		return "", errors.New("诚通未返回下载地址，请稍后重试或到网页端确认文件状态")
	}
	parsed, e := url.Parse(address)
	if e != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("诚通返回的下载地址无效")
	}
	return address, nil
}

// ctWebFirstLink walks a decoded JSON body for the first https link.
func ctWebFirstLink(node any) string {
	switch typed := node.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if strings.HasPrefix(text, "https://") {
			return text
		}
		return ""
	case []any:
		for _, item := range typed {
			if found := ctWebFirstLink(item); found != "" {
				return found
			}
		}
	case map[string]any:
		for _, key := range []string{"url", "downurl", "down_url", "download_url", "direct_url", "playurl", "link"} {
			if v, ok := typed[key]; ok {
				if found := ctWebFirstLink(v); found != "" {
					return found
				}
			}
		}
		for _, v := range typed {
			if found := ctWebFirstLink(v); found != "" {
				return found
			}
		}
	}
	return ""
}

func openCtWebDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, string, error) {
	page, e := newCtWebClient(client, a).list(ctx, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source CloudEntry
	count := 0
	for _, entry := range page.Entries {
		if entry.ID == in.EntryID && entry.Name == in.Name && !entry.Folder {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("诚通文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	address, e := ctWebDownloadURL(ctx, client, a, in.ParentID, source.ID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	// The direct link is issued by the web API and usually points at a CDN that
	// does not accept the bearer token.
	public := *client
	public.Jar = nil
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return CloudEntry{}, nil, "", errors.New("诚通下载地址无效")
	}
	req.Header.Set("User-Agent", userAgent(a))
	resp, e := public.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return CloudEntry{}, nil, "", ctx.Err()
		}
		return CloudEntry{}, nil, "", errors.New("连接诚通下载地址失败，请稍后重试")
	}
	return source, resp, "", nil
}

// ctwebUpload mirrors the web client: ask for an upload slot, then PUT the
// bytes to the returned address and confirm the completion payload.
func (u *HTTPUploader) ctweb(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	if strings.TrimSpace(folder) == "" {
		folder = cloudRoot(a.Kind)
	}
	hash, e := ctWebHeadChecksum(file)
	if e != nil {
		return e
	}
	if _, e := file.Seek(0, io.SeekStart); e != nil {
		return errors.New("无法读取源文件")
	}
	c := newCtWebClient(u.Client, a)
	var init struct {
		Data struct {
			UploadURL   string `json:"uploadUrl"`
			UploadURL2  string `json:"upload_url"`
			FileName    string `json:"file_name"`
			Exists      bool   `json:"exists"`
			Instant     bool   `json:"instant_upload"`
			UploadHash  string `json:"upload_session_hash"`
			FileNameAlt string `json:"filename"`
		} `json:"data"`
		UploadURL  string `json:"uploadUrl"`
		Exists     bool   `json:"exists"`
		Instant    bool   `json:"instant_upload"`
		FileName   string `json:"file_name"`
		FileHash   string `json:"upload_session_hash"`
		UploadHash string `json:"hash"`
	}
	payload := map[string]any{"folder_id": folder, "path": f.Name, "size": f.Size, "checksum": hash, "carrier": 0}
	if e := c.postAdaptive(ctx, "/home/file/manage/upload", payload, &init); e != nil {
		return e
	}
	address := strings.TrimSpace(init.Data.UploadURL)
	if address == "" {
		address = strings.TrimSpace(init.Data.UploadURL2)
	}
	if address == "" {
		address = strings.TrimSpace(init.UploadURL)
	}
	if address == "" {
		if init.Exists || init.Instant || init.Data.Exists || init.Data.Instant {
			// The cloud already holds identical content; treat it as uploaded.
			return nil
		}
		return errors.New("诚通未返回上传地址，接口可能已调整")
	}
	name := strings.TrimSpace(init.Data.FileName)
	if name == "" {
		name = strings.TrimSpace(init.FileName)
	}
	if name == "" {
		name = f.Name
	}
	session := strings.TrimSpace(init.Data.UploadHash)
	if session == "" {
		session = strings.TrimSpace(init.UploadHash)
	}
	if session == "" {
		session = strings.TrimSpace(init.FileHash)
	}
	if session == "" {
		session = ctWebSessionHash(hash, f.Size)
	}
	target, e := ctWebUploadTarget(address, session, name, f.Size)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", target, &progressReader{r: io.LimitReader(file, f.Size), fn: progress})
	if e != nil {
		return errors.New("诚通上传地址无效")
	}
	req.ContentLength = f.Size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", userAgent(a))
	resp, e := u.Client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("上传到诚通失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return errors.New("诚通上传响应读取失败")
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode)
	}
	var done struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		ID      int64  `json:"id"`
		Size    int64  `json:"size"`
		Name    string `json:"name"`
	}
	if e := json.Unmarshal(raw, &done); e != nil {
		return errors.New("诚通上传未确认完成：返回内容无法解析，请到网页端核对文件")
	}
	if done.Code != 200 || done.ID <= 0 || (done.Size > 0 && done.Size != f.Size) {
		return errors.New("诚通上传未确认完成：请到网页端核对文件，必要时重新上传")
	}
	return nil
}

func ctWebUploadTarget(address, session, name string, size int64) (string, error) {
	parsed, e := url.Parse(strings.TrimSpace(address))
	if e != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("诚通返回的上传地址无效")
	}
	query := parsed.Query()
	query.Set("hash", session)
	query.Set("path", name)
	query.Set("contentlength", strconv.FormatInt(size, 10))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// ctWebIDParam keeps numeric ids numeric: the web client sends its fid as a
// JSON number and some endpoints reject the same value quoted.
func ctWebIDParam(id string) any {
	text := strings.TrimSpace(id)
	if text == "" {
		return ""
	}
	if n, e := strconv.ParseInt(text, 10, 64); e == nil {
		return n
	}
	return text
}

// ctWebSessionHash matches the web client's upload session id: the MD5 of the
// file checksum, its size and one transfer id, joined with NUL bytes. The
// transfer id keeps two uploads of the same file in separate sessions.
func ctWebSessionHash(checksum string, size int64) string {
	sum := md5.Sum([]byte(checksum + "\x00" + strconv.FormatInt(size, 10) + "\x00" + newID()))
	return hex.EncodeToString(sum[:])
}

// ctWebHeadChecksum matches the web client: the MD5 of the first megabyte.
func ctWebHeadChecksum(file *os.File) (string, error) {
	buffer := make([]byte, 1<<20)
	n, e := file.ReadAt(buffer, 0)
	if e != nil && e != io.EOF {
		return "", errors.New("无法读取源文件")
	}
	sum := md5.Sum(buffer[:n])
	return hex.EncodeToString(sum[:]), nil
}
