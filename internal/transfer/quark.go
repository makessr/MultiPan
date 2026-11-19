package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const QuarkAPI = "https://drive.quark.cn/1/clouddrive"
const quarkOSSAgent = "aliyun-sdk-js/6.6.1 Chrome 98.0.4758.80 on Windows 10 64-bit"

var quarkIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validQuarkFolder(id string) bool { return quarkIDPattern.MatchString(id) }

type quarkClient struct {
	client  *http.Client
	account Account
	cookie  string
}

func newQuarkClient(client *http.Client, a Account) *quarkClient {
	return &quarkClient{client: client, account: a, cookie: a.Secret}
}
func (c *quarkClient) request(ctx context.Context, method, path string, query url.Values, payload any, out any) (json.RawMessage, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("pr", "ucpro")
	query.Set("fr", "pc")
	var body io.Reader
	if payload != nil {
		data, e := json.Marshal(payload)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.account.Endpoint+path+"?"+query.Encode(), body)
	if e != nil {
		return nil, errors.New("夸克接口地址无效")
	}
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set("User-Agent", userAgent(c.account))
	req.Header.Set("Referer", "https://pan.quark.cn/")
	req.Header.Set("Origin", "https://pan.quark.cn")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("连接夸克失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, errors.New("夸克登录失效或访问被拒绝，请重新登录")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp.StatusCode)
	}
	if updates := resp.Cookies(); len(updates) > 0 {
		c.cookie = mergeCookies(c.cookie, updates)
	}
	var envelope struct {
		Status   int             `json:"status"`
		Code     *int            `json:"code"`
		Data     json.RawMessage `json:"data"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&envelope) != nil {
		return nil, errors.New("夸克返回内容无法识别，请检查登录状态")
	}
	if envelope.Code == nil {
		return nil, errors.New("夸克未返回有效的接口状态")
	}
	if envelope.Status >= 400 || *envelope.Code != 0 {
		return nil, fmt.Errorf("夸克拒绝请求（状态 %d，代码 %d），请核对登录、权限和网盘额度", envelope.Status, *envelope.Code)
	}
	if out != nil {
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return nil, errors.New("夸克未返回请求结果")
		}
		if json.Unmarshal(envelope.Data, out) != nil {
			return nil, errors.New("夸克接口数据格式不兼容")
		}
	}
	return envelope.Metadata, nil
}
func mergeCookies(header string, updates []*http.Cookie) string {
	values := map[string]string{}
	r := http.Request{Header: http.Header{"Cookie": []string{header}}}
	for _, v := range r.Cookies() {
		values[v.Name] = v.Value
	}
	for _, v := range updates {
		if v.MaxAge < 0 {
			delete(values, v.Name)
		} else {
			values[v.Name] = v.Value
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+values[k])
	}
	return strings.Join(pairs, "; ")
}

type quarkEntry struct {
	ParentID  json.RawMessage `json:"pdir_fid"`
	UpdatedAt json.Number     `json:"updated_at"`
	FID       string          `json:"fid"`
	Name      string          `json:"file_name"`
	Dir       bool            `json:"dir"`
	FileType  *int            `json:"file_type"`
	Size      *int64          `json:"size"`
}

func (e quarkEntry) isDir() bool { return e.Dir || (e.FileType != nil && *e.FileType == 0) }
func (c *quarkClient) list(ctx context.Context, parent string) ([]quarkEntry, error) {
	if !validQuarkFolder(parent) {
		return nil, errors.New("夸克目录无效")
	}
	items := []quarkEntry{}
	seen := map[string]bool{}
	for page := 1; page <= 10000; page++ {
		q := url.Values{"pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {"200"}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,file_name:asc"}, "fetch_all_file": {"1"}, "fetch_risk_file_name": {"1"}}
		var data struct {
			List []quarkEntry `json:"list"`
		}
		if _, e := c.request(ctx, "GET", "/file/sort", q, nil, &data); e != nil {
			return nil, e
		}
		added := 0
		for _, entry := range data.List {
			if entry.FID != "" && !seen[entry.FID] {
				items = append(items, entry)
				seen[entry.FID] = true
				added++
			}
		}
		if len(data.List) < 200 {
			return items, nil
		}
		if added == 0 {
			return nil, errors.New("夸克目录分页重复，请刷新后重试")
		}
	}
	return nil, errors.New("目录项目过多，读取已停止")
}
func VerifyQuarkSession(ctx context.Context, client *http.Client, a Account) error {
	_, e := CheckQuarkSession(ctx, client, a)
	return e
}
func CheckQuarkSession(ctx context.Context, client *http.Client, a Account) (string, error) {
	var data struct {
		List []quarkEntry `json:"list"`
	}
	c := newQuarkClient(client, a)
	_, e := c.request(ctx, "GET", "/file/sort", url.Values{"pdir_fid": {"0"}, "_page": {"1"}, "_size": {"1"}}, nil, &data)
	return c.cookie, e
}
func ListQuarkFolders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	page := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: "0", Name: "根目录"}}}
	entries, e := newQuarkClient(client, a).list(ctx, parent)
	if e != nil {
		return page, e
	}
	for _, entry := range entries {
		if entry.isDir() && entry.FID != parent {
			page.Folders = append(page.Folders, CloudFolder{ID: entry.FID, Name: entry.Name})
		}
	}
	if parent != "0" {
		name := "目录 " + parent
		if a.Folder == parent && a.FolderDisplay != "" {
			name = strings.TrimPrefix(a.FolderDisplay, "根目录 / ")
		}
		page.Breadcrumbs = append(page.Breadcrumbs, CloudFolder{ID: parent, Name: name})
	}
	return page, nil
}

type quarkPre struct {
	FID       string          `json:"fid"`
	TaskID    string          `json:"task_id"`
	ObjKey    string          `json:"obj_key"`
	UploadID  string          `json:"upload_id"`
	Bucket    string          `json:"bucket"`
	UploadURL string          `json:"upload_url"`
	AuthInfo  string          `json:"auth_info"`
	Callback  json.RawMessage `json:"callback"`
}
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func quarkObjectURL(pre quarkPre) (string, error) {
	node := strings.TrimSpace(pre.UploadURL)
	if strings.HasPrefix(node, "//") {
		node = "https:" + node
	} else if !strings.Contains(node, "://") {
		node = "https://" + node
	}
	endpoint, e := url.Parse(node)
	if e != nil || endpoint == nil {
		return "", errors.New("夸克节点无法解析 [QK-NODE-PARSE/0.4.2]")
	}
	scheme := strings.ToLower(endpoint.Scheme)
	if (scheme != "http" && scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() != "" || strings.Trim(endpoint.Path, "/") != "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(pre.Bucket) {
		return "", quarkNodeError("FORMAT", endpoint)
	}
	host := strings.ToLower(endpoint.Hostname())
	// Quark may describe its OSS endpoint with http://. Only the host is used:
	// file bytes and signatures are always transmitted over HTTPS.
	host = strings.TrimPrefix(host, pre.Bucket+".")
	// This endpoint is delegated by the authenticated, fixed Quark API. It may
	// use an OSS CNAME rather than an aliyuncs.com hostname; do not guess a suffix.
	// The account Cookie is never forwarded, and TLS validation stays enabled.
	if !validUploadDNS(host) {
		return "", quarkNodeError("HOST", endpoint)
	}
	u := url.URL{Scheme: "https", Host: pre.Bucket + "." + host, Path: "/" + strings.TrimLeft(pre.ObjKey, "/")}
	return u.String(), nil
}
func validUploadDNS(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`).MatchString(label) {
			return false
		}
	}
	if net.ParseIP(host) != nil || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") {
		return false
	}
	return true
}
func quarkNodeError(code string, node *url.URL) error {
	host := strings.ToLower(node.Hostname())
	if len(host) > 253 || !regexp.MustCompile(`^[a-z0-9.-]*$`).MatchString(host) {
		host = "<invalid-host>"
	}
	return fmt.Errorf("夸克上传节点异常 [QK-NODE-%s/0.4.2] host=%s；未发送文件，请提供此诊断信息（不含 Cookie 或签名）", code, host)
}
func (c *quarkClient) sign(ctx context.Context, pre quarkPre, canonical string) (string, error) {
	var result struct {
		Key string `json:"auth_key"`
	}
	_, e := c.request(ctx, "POST", "/file/upload/auth", nil, map[string]any{"task_id": pre.TaskID, "auth_info": pre.AuthInfo, "auth_meta": canonical}, &result)
	if e != nil {
		return "", e
	}
	if result.Key == "" || strings.ContainsAny(result.Key, "\r\n") {
		return "", errors.New("夸克未提供有效上传授权")
	}
	return result.Key, nil
}
func (c *quarkClient) oss(ctx context.Context, method, address, contentType, date, auth string, body io.Reader, length int64, headers map[string]string) (http.Header, error) {
	req, e := http.NewRequestWithContext(ctx, method, address, body)
	if e != nil {
		return nil, errors.New("上传地址无效")
	}
	req.ContentLength = length
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-oss-date", date)
	req.Header.Set("x-oss-user-agent", quarkOSSAgent)
	req.Header.Set("Referer", "https://pan.quark.cn/")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Never forward the account Cookie to object-storage hosts.
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("夸克存储节点连接失败 [QK-OSS-CONNECT/0.4.2] host=%s，请检查网络后重试", req.URL.Hostname())
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return nil, errors.New("夸克上传结果读取失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("夸克对象传输失败 [QK-OSS-HTTP-%d/0.4.2] host=%s：%w", resp.StatusCode, req.URL.Hostname(), statusError(resp.StatusCode))
	}
	if bytes.Contains(data, []byte("<Error>")) {
		return nil, errors.New("对象存储未接受上传，请重试")
	}
	return resp.Header, nil
}
func (u *HTTPUploader) quark(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	return u.quarkUpload(ctx, a, f, folder, file, progress, nil)
}

func (u *HTTPUploader) quarkUpload(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64), recordID func(string) error) error {
	if a.Endpoint != QuarkAPI {
		return errors.New("夸克上传必须通过固定的官方接口初始化 [QK-API-ORIGIN/0.4.2]")
	}
	c := newQuarkClient(u.Client, a)
	if recordID == nil {
		entries, err := c.list(ctx, folder)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name == f.Name {
				return errors.New("夸克目标目录已存在同名项目；请更改目录或文件名，未覆盖远端文件")
			}
		}
	}
	var e error
	digestMD5, digestSHA := md5.New(), sha1.New()
	if _, e = io.Copy(io.MultiWriter(digestMD5, digestSHA), ctxReader{ctx, file}); e != nil {
		return errors.New("计算文件校验值失败或任务已停止")
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	var existingIDs map[string]bool
	if recordID != nil {
		existingIDs, e = readRelayNativeTargetIDs(ctx, u.Client, a, folder, f.Name)
		if e != nil {
			return e
		}
	}
	kind := mime.TypeByExtension(filepath.Ext(f.Name))
	if kind == "" {
		kind = "application/octet-stream"
	}
	kind = strings.Split(kind, ";")[0]
	var pre quarkPre
	cp := checkpoint(ctx)
	var metadata []byte
	if len(cp.Meta) > 0 {
		if e = json.Unmarshal(cp.Meta, &pre); e != nil {
			return errors.New("上传续传记录损坏，请从头重新上传")
		}
	} else {
		metadata, e = c.request(ctx, "POST", "/file/upload/pre", nil, map[string]any{"ccp_hash_update": true, "dir_name": "", "file_name": f.Name, "format_type": kind, "l_created_at": f.Modified, "l_updated_at": f.Modified, "pdir_fid": folder, "size": f.Size}, &pre)
	}
	if e != nil {
		return e
	}
	if pre.TaskID == "" {
		return errors.New("夸克预处理未返回任务标识")
	}
	type uploadResult struct {
		Finish bool   `json:"finish"`
		FID    string `json:"fid"`
	}
	var check uploadResult
	recordResult := func(result uploadResult) error {
		if err := unchanged(file, f); err != nil || recordID == nil {
			return err
		}
		id := result.FID
		if id == "" {
			id = pre.FID
		}
		if !result.Finish || !validRelayTargetFolderID(a.Kind, id) ||
			existingIDs[id] || pre.FID != "" && pre.FID != id {
			return errors.New("夸克上传缺少明确受理和新文件 ID，保持待核对")
		}
		return recordID(id)
	}
	if _, e = c.request(ctx, "POST", "/file/update/hash", nil, map[string]any{"task_id": pre.TaskID, "md5": hex.EncodeToString(digestMD5.Sum(nil)), "sha1": hex.EncodeToString(digestSHA.Sum(nil))}, &check); e != nil {
		return e
	}
	if check.Finish {
		progress(f.Size)
		return recordResult(check)
	}
	if pre.ObjKey == "" || pre.UploadID == "" || pre.AuthInfo == "" {
		return errors.New("夸克预处理未返回完整上传信息")
	}
	object, e := quarkObjectURL(pre)
	if e != nil {
		return e
	}
	var meta struct {
		PartSize int64 `json:"part_size"`
	}
	json.Unmarshal(metadata, &meta)
	partSize := meta.PartSize
	if cp.Chunk > 0 {
		partSize = cp.Chunk
	}
	if partSize <= 0 {
		partSize = 16 << 20
	}
	if partSize > 128<<20 {
		return errors.New("夸克分片大小超出支持范围")
	}
	cp.Chunk = partSize
	cp.Meta, _ = json.Marshal(pre)
	if e = cp.save(); e != nil {
		return errors.New("无法保存上传续传记录，未继续传输")
	}
	type part struct {
		Number int    `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
	}
	completion := struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{Parts: []part{}}
	for offset := int64(0); offset < f.Size; {
		n := f.Size - offset
		if n > partSize {
			n = partSize
		}
		number := len(completion.Parts) + 1
		if etag, ok := cp.Parts[number]; ok {
			completion.Parts = append(completion.Parts, part{number, etag})
			offset += n
			progress(offset)
			continue
		}
		date := time.Now().UTC().Format(http.TimeFormat)
		query := url.Values{"partNumber": {strconv.Itoa(number)}, "uploadId": {pre.UploadID}}
		canonical := fmt.Sprintf("PUT\n\n%s\n%s\nx-oss-date:%s\nx-oss-user-agent:%s\n/%s/%s?partNumber=%d&uploadId=%s", kind, date, date, quarkOSSAgent, pre.Bucket, pre.ObjKey, number, pre.UploadID)
		auth, e := c.sign(ctx, pre, canonical)
		if e != nil {
			return e
		}
		base := offset
		reader := &progressReader{r: io.NewSectionReader(file, offset, n), fn: func(sent int64) { progress(base + sent) }}
		headers, e := c.oss(ctx, "PUT", object+"?"+query.Encode(), kind, date, auth, reader, n, nil)
		if e != nil {
			return e
		}
		etag := strings.Trim(headers.Get("ETag"), "\"")
		if etag == "" {
			return errors.New("夸克上传分片未得到 ETag 确认")
		}
		completion.Parts = append(completion.Parts, part{number, "\"" + etag + "\""})
		cp.Parts[number] = "\"" + etag + "\""
		if e = cp.save(); e != nil {
			return errors.New("分片已上传但续传记录保存失败，请检查磁盘")
		}
		offset += n
		progress(offset)
	}
	if len(completion.Parts) == 0 {
		return errors.New("夸克未接受空文件秒传，暂无法上传此空文件")
	}
	encoded, e := xml.Marshal(completion)
	if e != nil {
		return e
	}
	sum := md5.Sum(encoded)
	md5b64 := base64.StdEncoding.EncodeToString(sum[:])
	callback := pre.Callback
	if len(callback) == 0 || string(callback) == "null" {
		callback = []byte("{}")
	}
	cb := base64.StdEncoding.EncodeToString(callback)
	date := time.Now().UTC().Format(http.TimeFormat)
	canonical := fmt.Sprintf("POST\n%s\napplication/xml\n%s\nx-oss-callback:%s\nx-oss-date:%s\nx-oss-user-agent:%s\n/%s/%s?uploadId=%s", md5b64, date, cb, date, quarkOSSAgent, pre.Bucket, pre.ObjKey, pre.UploadID)
	auth, e := c.sign(ctx, pre, canonical)
	if e != nil {
		return e
	}
	if _, e = c.oss(ctx, "POST", object+"?"+url.Values{"uploadId": {pre.UploadID}}.Encode(), "application/xml", date, auth, bytes.NewReader(encoded), int64(len(encoded)), map[string]string{"Content-MD5": md5b64, "x-oss-callback": cb}); e != nil {
		return e
	}
	var finish uploadResult
	var response any
	if recordID != nil {
		response = &finish
	}
	if _, e = c.request(ctx, "POST", "/file/upload/finish", nil, map[string]any{"task_id": pre.TaskID, "obj_key": pre.ObjKey}, response); e != nil {
		return e
	}
	progress(f.Size)
	return recordResult(finish)
}
