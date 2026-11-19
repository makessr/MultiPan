package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const BaiduAPI = "https://pan.baidu.com/api"
const baiduUploadURL = "https://d.pcs.baidu.com/rest/2.0/pcs/superfile2"
const baiduChunk int64 = 4 << 20

func validBaiduPath(p string) bool {
	if p == "" || len(p) > 4096 || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") || path.Clean(p) != p {
		return false
	}
	return true
}
func baiduCookie(header, name string) string {
	req := http.Request{Header: http.Header{"Cookie": {header}}}
	for _, c := range req.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

type baiduClient struct {
	client        *http.Client
	account       Account
	cookie, token string
}

func newBaiduClient(client *http.Client, a Account) *baiduClient {
	return &baiduClient{client: client, account: a, cookie: a.Secret}
}

type baiduAPIError struct {
	stage string
	code  int
}

func (e *baiduAPIError) Error() string {
	switch e.code {
	case -6, 111, 31045:
		return "百度登录已失效，请重新登录 [BD-AUTH/0.6.0]"
	case -2, 2:
		return fmt.Sprintf("百度请求凭据或参数被拒绝 [BD-API/0.6.0] stage=%s errno=%d，请重新登录后重试", e.stage, e.code)
	case 132:
		return "百度要求安全验证，请在官方网页完成验证后重试 [BD-VERIFY/0.6.0]"
	}
	return fmt.Sprintf("百度请求失败 [BD-API/0.6.0] stage=%s errno=%d，请核对网盘额度、权限或文件限制", e.stage, e.code)
}
func (c *baiduClient) api(ctx context.Context, method, stage string, q, form url.Values, out any) error {
	return c.request(ctx, method, stage, q, form, out, false)
}

// Share endpoints live outside /api; keep their authenticated origin fixed.
func (c *baiduClient) shareAPI(ctx context.Context, method, stage string, q, form url.Values, out any) error {
	if stage != "pset" && stage != "record" && stage != "surlinfoinrecord" && stage != "cancel" && stage != "mexpire" && stage != "resetstatus" {
		return errors.New("百度分享操作无效")
	}
	if (stage == "cancel" || stage == "mexpire" || stage == "resetstatus") && method != "POST" {
		return errors.New("百度分享设置操作必须明确提交")
	}
	return c.request(ctx, method, stage, q, form, out, true)
}

func (c *baiduClient) request(ctx context.Context, method, stage string, q, form url.Values, out any, share bool) error {
	if c.account.Endpoint != BaiduAPI {
		return errors.New("百度接口来源无效 [BD-ORIGIN/0.6.0]")
	}
	query := url.Values{"app_id": {"250528"}, "channel": {"chunlei"}, "web": {"1"}, "clienttype": {"0"}}
	for k, v := range q {
		query[k] = v
	}
	if c.token != "" {
		query.Set("bdstoken", c.token)
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	base := BaiduAPI
	if share {
		base = "https://pan.baidu.com/share"
	}
	req, _ := http.NewRequestWithContext(ctx, method, base+"/"+stage+"?"+query.Encode(), body)
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set("User-Agent", userAgent(c.account))
	req.Header.Set("Referer", "https://pan.baidu.com/disk/main")
	req.Header.Set("Origin", "https://pan.baidu.com")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("百度连接失败 [BD-CONNECT/0.6.0] stage=%s", stage)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("百度请求失败 [BD-HTTP/0.6.0] stage=%s HTTP=%d", stage, resp.StatusCode)
	}
	c.cookie = mergeCookies(c.cookie, resp.Cookies())
	data, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if e != nil {
		return errors.New("百度响应读取失败 [BD-READ/0.6.0]")
	}
	var envelope struct {
		Errno *int `json:"errno"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Errno == nil {
		return fmt.Errorf("百度返回格式无法识别 [BD-JSON/0.6.0] stage=%s", stage)
	}
	if *envelope.Errno != 0 {
		return &baiduAPIError{stage, *envelope.Errno}
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return fmt.Errorf("百度数据格式不兼容 [BD-DATA/0.6.0] stage=%s", stage)
	}
	return nil
}
func (c *baiduClient) session(ctx context.Context) error {
	var response struct {
		Result struct {
			Token string `json:"bdstoken"`
		} `json:"result"`
	}
	if e := c.api(ctx, "GET", "gettemplatevariable", url.Values{"fields": {`["bdstoken"]`}}, nil, &response); e != nil {
		return e
	}
	if len(response.Result.Token) < 8 || len(response.Result.Token) > 512 || strings.ContainsAny(response.Result.Token, " \r\n\t") {
		return errors.New("百度未返回有效登录凭据，请重新登录 [BD-TOKEN/0.6.0]")
	}
	c.token = response.Result.Token
	return nil
}
func CheckBaiduSession(ctx context.Context, client *http.Client, a Account) (string, error) {
	c := newBaiduClient(client, a)
	if e := c.session(ctx); e != nil {
		return "", e
	}
	if _, e := c.list(ctx, "/"); e != nil {
		return "", e
	}
	return c.cookie, nil
}

type baiduEntry struct {
	ID   json.Number `json:"fs_id"`
	Path string      `json:"path"`
	Name string      `json:"server_filename"`
	Size *int64      `json:"size"`
	Dir  *int        `json:"isdir"`
	MD5  string      `json:"md5"`
}

func (c *baiduClient) list(ctx context.Context, folder string) ([]baiduEntry, error) {
	if !validBaiduPath(folder) {
		return nil, errors.New("百度目录路径无效")
	}
	entries := []baiduEntry{}
	seen := map[string]bool{}
	for page := 1; page <= 10000; page++ {
		var result struct {
			List []baiduEntry `json:"list"`
		}
		q := url.Values{"dir": {folder}, "page": {strconv.Itoa(page)}, "num": {"1000"}, "order": {"name"}, "desc": {"0"}, "showempty": {"0"}}
		if e := c.api(ctx, "GET", "list", q, nil, &result); e != nil {
			return nil, e
		}
		added := 0
		for _, entry := range result.List {
			if !validBaiduPath(entry.Path) || path.Dir(entry.Path) != folder || entry.Path == folder || entry.Dir == nil {
				return nil, errors.New("百度目录条目无效 [BD-LIST/0.6.0]")
			}
			if !seen[entry.Path] {
				seen[entry.Path] = true
				entries = append(entries, entry)
				added++
			}
		}
		if len(result.List) < 1000 {
			return entries, nil
		}
		if added == 0 {
			return nil, errors.New("百度目录分页未推进 [BD-PAGE/0.6.0]")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return nil, errors.New("百度目录页数超出限制")
}
func ListBaiduFolders(ctx context.Context, client *http.Client, a Account, folder string) (FolderPage, error) {
	if folder == "" {
		folder = "/"
	}
	p := FolderPage{CurrentID: folder, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: "/", Name: "根目录"}}}
	entries, e := newBaiduClient(client, a).list(ctx, folder)
	if e != nil {
		return p, e
	}
	for _, entry := range entries {
		if *entry.Dir == 1 {
			p.Folders = append(p.Folders, CloudFolder{ID: entry.Path, Name: path.Base(entry.Path)})
		}
	}
	current := ""
	if folder != "/" {
		for _, part := range strings.Split(strings.TrimPrefix(folder, "/"), "/") {
			current += "/" + part
			p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: current, Name: part})
		}
	}
	return p, nil
}
func validBaiduFile(entry baiduEntry, target string, size int64) bool {
	id, e := strconv.ParseInt(string(entry.ID), 10, 64)
	return e == nil && id > 0 && entry.Path == target && entry.Size != nil && *entry.Size == size && entry.Dir != nil && *entry.Dir == 0
}
func (c *baiduClient) confirm(ctx context.Context, target string, size int64) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	targets, _ := json.Marshal([]string{target})
	for {
		var result struct {
			Info []baiduEntry `json:"info"`
			List []baiduEntry `json:"list"`
		}
		e := c.api(ctx, "GET", "filemetas", url.Values{"target": {string(targets)}, "dlink": {"0"}}, nil, &result)
		if e == nil {
			all := append(result.Info, result.List...)
			if len(all) == 1 && validBaiduFile(all[0], target, size) {
				return nil
			}
			if len(all) > 0 {
				return errors.New("百度最终文件元数据不匹配 [BD-RESULT/0.6.0]；请核对网盘，未自动重传")
			}
		}
		if e != nil {
			var apiErr *baiduAPIError
			if !errors.As(e, &apiErr) || apiErr.code != -9 {
				return e
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("百度云端结果暂未确认 [BD-PENDING/0.6.0]；文件可能已保存，请先核对网盘")
		case <-time.After(time.Second):
		}
	}
}
func (u *HTTPUploader) baidu(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	if !validBaiduPath(folder) || path.Base(f.Name) != f.Name || strings.ContainsAny(f.Name, "\\\x00") {
		return errors.New("百度上传目标路径无效")
	}
	if f.Size == 0 {
		return errors.New("百度当前上传接口暂不支持空文件，未创建云端文件")
	}
	if (f.Size+baiduChunk-1)/baiduChunk > 10000 {
		return errors.New("百度文件分片数超出当前支持范围，请检查文件大小限制")
	}
	c := newBaiduClient(u.Client, a)
	entries, e := c.list(ctx, folder)
	if e != nil {
		return e
	}
	target := path.Join(folder, f.Name)
	for _, entry := range entries {
		if entry.Path == target {
			return errors.New("百度目标已存在同名文件或目录，未覆盖；请更改保存目录或本地文件名")
		}
	}
	hashes := []string{}
	for offset := int64(0); offset < f.Size; offset += baiduChunk {
		h := md5.New()
		if _, e = io.Copy(h, ctxReader{ctx, io.NewSectionReader(file, offset, min(baiduChunk, f.Size-offset))}); e != nil {
			return e
		}
		hashes = append(hashes, hex.EncodeToString(h.Sum(nil)))
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	if e = c.session(ctx); e != nil {
		return e
	}
	encoded, _ := json.Marshal(hashes)
	form := url.Values{"path": {target}, "size": {strconv.FormatInt(f.Size, 10)}, "isdir": {"0"}, "autoinit": {"1"}, "block_list": {string(encoded)}, "rtype": {"0"}, "target_path": {strings.TrimRight(folder, "/") + "/"}}
	var pre struct {
		UploadID   string `json:"uploadid"`
		ReturnType *int   `json:"return_type"`
		Blocks     []int  `json:"block_list"`
	}
	if e = c.api(ctx, "POST", "precreate", nil, form, &pre); e != nil {
		return e
	}
	if pre.ReturnType != nil && *pre.ReturnType == 2 {
		if e = c.confirm(ctx, target, f.Size); e != nil {
			return e
		}
		progress(f.Size)
		return unchanged(file, f)
	}
	if pre.UploadID == "" || len(pre.UploadID) > 4096 {
		return errors.New("百度未返回上传会话 [BD-PRE/0.6.0]")
	}
	needed := pre.Blocks
	if needed == nil {
		for i := range hashes {
			needed = append(needed, i)
		}
	}
	seen := map[int]bool{}
	for _, part := range needed {
		if part < 0 || part >= len(hashes) || seen[part] {
			return errors.New("百度返回无效待上传分片 [BD-PART-LIST/0.6.0]")
		}
		seen[part] = true
	}
	sent := int64(0)
	for _, part := range needed {
		offset := int64(part) * baiduChunk
		length := min(baiduChunk, f.Size-offset)
		if e = c.uploadPart(ctx, pre.UploadID, target, part, file, offset, length, hashes[part], func(n int64) { progress(sent + n) }); e != nil {
			return e
		}
		sent += length
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	create := url.Values{"path": {target}, "size": {strconv.FormatInt(f.Size, 10)}, "isdir": {"0"}, "uploadid": {pre.UploadID}, "block_list": {string(encoded)}, "rtype": {"0"}, "type": {"2"}}
	var result baiduEntry
	if e = c.api(ctx, "POST", "create", url.Values{"a": {"commit"}, "type": {"2"}}, create, &result); e != nil {
		return e
	}
	if !validBaiduFile(result, target, f.Size) {
		// Some responses acknowledge completion without the full final metadata.
		// Resolve the exact path via the read-only metadata API, never resubmit create.
		if result.Path != "" && result.Path != target {
			return errors.New("百度完成路径与目标不一致 [BD-CREATE-PATH/0.6.0]，请核对网盘")
		}
		if e = c.confirm(ctx, target, f.Size); e != nil {
			return e
		}
	}
	progress(f.Size)
	return unchanged(file, f)
}
func (c *baiduClient) uploadPart(ctx context.Context, uploadID, target string, part int, file *os.File, offset, length int64, digest string, progress func(int64)) error {
	// Only the fixed first-party PCS endpoint receives a BDUSS cookie; pan-only
	// Cookie values, bdstoken, and desktop bindings are never forwarded elsewhere.
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)
	if _, e := writer.CreateFormFile("file", "file"); e != nil {
		return e
	}
	head := append([]byte{}, prefix.Bytes()...)
	prefix.Reset()
	if e := writer.Close(); e != nil {
		return e
	}
	tail := prefix.Bytes()
	body := io.MultiReader(bytes.NewReader(head), &progressReader{r: io.NewSectionReader(file, offset, length), fn: progress}, bytes.NewReader(tail))
	q := url.Values{"method": {"upload"}, "app_id": {"250528"}, "type": {"tmpfile"}, "path": {target}, "uploadid": {uploadID}, "partseq": {strconv.Itoa(part)}}
	req, _ := http.NewRequestWithContext(ctx, "POST", baiduUploadURL+"?"+q.Encode(), body)
	req.ContentLength = int64(len(head)) + length + int64(len(tail))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", "BDUSS="+baiduCookie(c.cookie, "BDUSS"))
	req.Header.Set("User-Agent", userAgent(c.account))
	req.Header.Set("Referer", "https://pan.baidu.com/")
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("百度分片连接失败 [BD-PUT/0.6.0] part=%d", part)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("百度分片失败 [BD-PUT-HTTP/0.6.0] part=%d HTTP=%d", part, resp.StatusCode)
	}
	var result struct {
		MD5   string `json:"md5"`
		Code  int    `json:"error_code"`
		Errno int    `json:"errno"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || result.Code != 0 || result.Errno != 0 || !strings.EqualFold(result.MD5, digest) {
		return fmt.Errorf("百度分片未通过校验 [BD-PART-MD5/0.6.0] part=%d errno=%d code=%d", part, result.Errno, result.Code)
	}
	progress(length)
	return nil
}
