package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const Pan123API = "https://api.123278.com/b/api"
const pan123Chunk int64 = 16 << 20

func pan123Token(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Bearer "))
}
func valid123Folder(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n >= 0 && strconv.FormatInt(n, 10) == s
}

type pan123Client struct {
	client  *http.Client
	account Account
}

// The web protocol signs the request path and minute in China Standard Time.
func pan123Signature(path string, now time.Time, nonce string) (string, string) {
	minute := []byte(now.In(time.FixedZone("CST", 8*3600)).Format("200601021504"))
	alphabet := "adefghlmyi"
	for i, c := range minute {
		minute[i] = alphabet[c-'0']
	}
	key := strconv.FormatUint(uint64(crc32.ChecksumIEEE(minute)), 10)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	checksum := crc32.ChecksumIEEE([]byte(strings.Join([]string{timestamp, nonce, path, "web", "3", key}, "|")))
	return key, timestamp + "-" + nonce + "-" + strconv.FormatUint(uint64(checksum), 10)
}

type pan123BusinessError struct {
	path string
	code int
}

func (e *pan123BusinessError) Error() string {
	return fmt.Sprintf("123 拒绝请求 [123-API-CODE/0.5.1] %s code=%d；请核对权限、额度或稍后重试", e.path, e.code)
}

func (c pan123Client) request(ctx context.Context, method, path string, q url.Values, payload, out any) error {
	if c.account.Endpoint != Pan123API {
		return errors.New("123 云盘接口来源无效 [123-API-ORIGIN/0.5.1]")
	}
	if q == nil {
		q = url.Values{}
	} else {
		copyQ := url.Values{}
		for k, v := range q {
			copyQ[k] = v
		}
		q = copyQ
	}
	prefix := "/b/api"
	// The web restore endpoints use /a/api.
	if path == "/file/trash" || path == "/file/recover/by_path" {
		prefix = "/a/api"
	}
	k, v := pan123Signature(prefix+path, time.Now(), strconv.Itoa(rand.IntN(10000000)))
	q.Set(k, v)
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.123278.com"+prefix+path+"?"+q.Encode(), body)
	if e != nil {
		return errors.New("123 请求地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+pan123Token(c.account.Secret))
	req.Header.Set("User-Agent", userAgent(c.account))
	req.Header.Set("platform", "web")
	req.Header.Set("app-version", "3")
	req.Header.Set("Origin", "https://yun.123pan.cn")
	req.Header.Set("Referer", "https://yun.123pan.cn/")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("123 接口连接失败 [123-API-CONNECT/0.5.1] %s", path)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return errors.New("123 云盘登录失效或访问被拒绝，请重新登录")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("123 接口失败 [123-API-HTTP/0.5.1] %s HTTP %d", path, resp.StatusCode)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&envelope) != nil || envelope.Code == nil {
		return fmt.Errorf("123 返回格式无法识别 [123-API-JSON/0.5.1] %s", path)
	}
	if *envelope.Code != 0 && !(path == "/file/recover/by_path" && *envelope.Code == 200) {
		if *envelope.Code == 401 {
			return errors.New("123 云盘登录已过期，请重新登录")
		}
		return &pan123BusinessError{path: path, code: *envelope.Code}
	}
	if out != nil && json.Unmarshal(envelope.Data, out) != nil {
		return fmt.Errorf("123 接口数据不兼容 [123-API-DATA/0.5.1] %s", path)
	}
	return nil
}
func Verify123Session(ctx context.Context, client *http.Client, a Account) error {
	var data struct {
		UID json.Number `json:"UID"`
	}
	if e := (pan123Client{client, a}).request(ctx, "GET", "/user/info", nil, nil, &data); e != nil {
		return e
	}
	if data.UID == "" || data.UID == "0" {
		return errors.New("123 未返回已登录用户，请重新登录")
	}
	return nil
}

type pan123Entry struct {
	ID        json.Number `json:"FileId"`
	Name      string      `json:"FileName"`
	Type      int         `json:"Type"`
	Size      int64       `json:"Size"`
	MD5       string      `json:"Etag"`
	S3KeyFlag string      `json:"S3KeyFlag"`
	ParentID  json.Number `json:"ParentFileId"`
}

func (c pan123Client) list(ctx context.Context, parent string) ([]pan123Entry, error) {
	if !valid123Folder(parent) {
		return nil, errors.New("123 目录 ID 无效")
	}
	result := []pan123Entry{}
	seen := map[string]bool{}
	for page := 1; page <= 10000; page++ {
		q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {"0"}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "parentFileId": {parent}, "trashed": {"false"}, "SearchData": {""}, "Page": {strconv.Itoa(page)}, "OnlyLookAbnormalFile": {"0"}, "event": {"homeListFile"}, "operateType": {"4"}, "inDirectSpace": {"false"}}
		var data struct {
			Next     json.RawMessage `json:"Next"`
			Total    int             `json:"Total"`
			InfoList []pan123Entry   `json:"InfoList"`
		}
		if e := c.request(ctx, "GET", "/file/list/new", q, nil, &data); e != nil {
			return nil, e
		}
		if len(data.InfoList) == 0 {
			return result, nil
		}
		added := 0
		for _, f := range data.InfoList {
			id := string(f.ID)
			if !valid123Folder(id) || id == "0" {
				return nil, errors.New("123 列表返回无效文件 ID")
			}
			if !seen[id] {
				seen[id] = true
				result = append(result, f)
				added++
			}
		}
		if added == 0 {
			return nil, errors.New("123 目录分页未推进，请刷新后重试")
		}
		if strings.Trim(string(data.Next), "\"") == "-1" || (data.Total > 0 && len(result) >= data.Total) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
	return nil, errors.New("123 目录页数超出限制")
}
func List123Folders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	if parent == "" {
		parent = "0"
	}
	p := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: "0", Name: "根目录"}}}
	entries, e := (pan123Client{client, a}).list(ctx, parent)
	if e != nil {
		return p, e
	}
	for _, f := range entries {
		if f.Type == 1 {
			p.Folders = append(p.Folders, CloudFolder{ID: string(f.ID), Name: f.Name})
		}
	}
	if parent != "0" {
		name := "目录 " + parent
		if parent == a.Folder && a.FolderDisplay != "" {
			name = a.FolderDisplay
		}
		p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: parent, Name: name})
	}
	return p, nil
}

type pan123Pre struct {
	FileID           json.Number  `json:"FileId"`
	Reuse            bool         `json:"Reuse"`
	Bucket           string       `json:"Bucket"`
	Key              string       `json:"Key"`
	UploadID         string       `json:"UploadId"`
	StorageNode      string       `json:"StorageNode"`
	Endpoint         string       `json:"EndPoint"`
	AccessKey        string       `json:"AccessKeyId"`
	SecretKey        string       `json:"SecretAccessKey"`
	SessionToken     string       `json:"SessionToken"`
	SliceSize        json.Number  `json:"SliceSize"`
	Info             *pan123Entry `json:"Info"`
	UploadFileStatus int          `json:"UploadFileStatus"`
}

func (u *HTTPUploader) pan123(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	c := pan123Client{u.Client, a}
	entries, e := c.list(ctx, folder)
	if e != nil {
		return e
	}
	var existing *pan123Entry
	for i := range entries {
		if entries[i].Name == f.Name {
			if existing != nil {
				return errors.New("123 目标存在多个同名项目，无法安全核验，未上传或覆盖")
			}
			existing = &entries[i]
		}
	}
	if existing != nil && (existing.Type != 0 || existing.Size != f.Size) {
		return errors.New("目标已存在同名文件或目录，内容不一致，未上传或覆盖")
	}
	hash := md5.New()
	if _, e = io.Copy(hash, &ctxReader{ctx: ctx, r: io.NewSectionReader(file, 0, f.Size)}); e != nil {
		return e
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	parent, _ := strconv.ParseInt(folder, 10, 64)
	var pre pan123Pre
	// duplicate=1 requests a new name if a concurrent upload creates a conflict.
	digest := hex.EncodeToString(hash.Sum(nil))
	if existing != nil {
		if !strings.EqualFold(strings.TrimSpace(existing.MD5), digest) {
			return errors.New("123 同名文件 MD5 不一致或缺失，未上传或覆盖")
		}
		progress(f.Size)
		return unchanged(file, f)
	}
	cp := checkpoint(ctx)
	if len(cp.Meta) > 0 {
		e = json.Unmarshal(cp.Meta, &pre)
	} else {
		e = c.request(ctx, "POST", "/file/upload_request", nil, map[string]any{"driveId": 0, "duplicate": 1, "RequestSource": nil, "etag": digest, "fileName": f.Name, "parentFileId": parent, "size": f.Size, "type": 0}, &pre)
	}
	if e != nil {
		return e
	}
	if pre.Reuse && pre.Info != nil {
		id, e := validate123Result(*pre.Info, folder, f, digest)
		if e != nil {
			return e
		}
		pre.FileID = id
	}
	if !valid123Folder(string(pre.FileID)) || (pre.FileID == "0" && !pre.Reuse) {
		return errors.New("123 未返回有效上传文件 ID [123-PRE-ID/0.5.1]")
	}
	if pre.Reuse {
		if pre.Info != nil {
			progress(f.Size)
			return unchanged(file, f)
		}
		// Reuse's allocation ID may not be the materialized object ID. Without
		// an explicit Info mapping, verify a unique exact-content target instead.
		id := pre.FileID
		if pre.Info == nil {
			id = "0"
		}
		if e = c.confirm(ctx, folder, id, f, digest); e != nil {
			return e
		}
		progress(f.Size)
		return unchanged(file, f)
	}
	if pre.Key == "" || pre.Bucket == "" {
		return errors.New("123 上传准备信息不完整 [123-PRE-FIELDS/0.5.1]")
	}
	chunk := pan123Chunk
	if pre.SliceSize != "" {
		chunk, e = pre.SliceSize.Int64()
		if e != nil || chunk < 1<<20 || chunk > 128<<20 {
			return errors.New("123 分片大小无效 [123-PRE-SLICE/0.5.1]；未发送文件")
		}
	}
	cp.Meta, _ = json.Marshal(pre)
	if e = cp.save(); e != nil {
		return errors.New("无法保存123上传续传记录")
	}
	if pre.UploadFileStatus == 255 {
		if e = c.complete123(ctx, pre, folder, f, digest, f.Size > chunk, false); e != nil {
			return e
		}
		progress(f.Size)
		return unchanged(file, f)
	}
	if pre.AccessKey != "" && pre.SecretKey != "" && pre.SessionToken != "" {
		if !cp.Merged {
			if e = c.uploadS3(ctx, pre, file, f.Size, progress); e != nil {
				return e
			}
			cp.Merged = true
			if e = cp.save(); e != nil {
				return e
			}
		}
		if e = unchanged(file, f); e != nil {
			return e
		}
		if e = c.request(ctx, "POST", "/file/upload_complete", nil, map[string]any{"fileId": pre.FileID}, nil); e != nil {
			return e
		}
		return c.confirm(ctx, folder, pre.FileID, f, digest)
	}
	parts := (f.Size + chunk - 1) / chunk
	if parts == 0 {
		parts = 1
	}
	if parts > 10000 {
		return errors.New("123 文件分片数超出限制")
	}
	for part := int64(1); part <= parts; part++ {
		if _, ok := cp.Parts[int(part)]; ok {
			progress(min(part*chunk, f.Size))
			continue
		}
		endpoint := "/file/s3_upload_object/auth"
		if parts > 1 {
			endpoint = "/file/s3_repare_upload_parts_batch"
		}
		var signed struct {
			URLs map[string]string `json:"presignedUrls"`
		}
		// Request only the current part so a large transfer cannot outlive its URLs.
		if e = c.request(ctx, "POST", endpoint, nil, map[string]any{"bucket": pre.Bucket, "key": pre.Key, "partNumberStart": part, "partNumberEnd": part + 1, "uploadId": pre.UploadID, "StorageNode": pre.StorageNode}, &signed); e != nil {
			return e
		}
		offset := (part - 1) * chunk
		length := min(chunk, f.Size-offset)
		if e = c.putSigned(ctx, signed.URLs[strconv.FormatInt(part, 10)], io.NewSectionReader(file, offset, length), length, func(n int64) { progress(offset + n) }); e != nil {
			return e
		}
		cp.Parts[int(part)] = "ack"
		if e = cp.save(); e != nil {
			return errors.New("无法保存123已上传分片记录")
		}
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	if e = c.complete123(ctx, pre, folder, f, digest, parts > 1, true); e != nil {
		return e
	}
	return unchanged(file, f)
}

func (c pan123Client) confirm(ctx context.Context, folder string, id json.Number, f File, digest string) error {
	var nameCount int
	var idSeen, sizeMatch, hashMatch bool
	for attempt := 0; attempt < 6; attempt++ {
		entries, e := c.list(ctx, folder)
		if e != nil {
			return e
		}
		matches := 0
		nameCount = 0
		idSeen = false
		sizeMatch = false
		hashMatch = false
		for _, entry := range entries {
			if entry.ID == id {
				idSeen = true
			}
			if entry.Name == f.Name {
				nameCount++
				if entry.Size == f.Size {
					sizeMatch = true
				}
				if strings.EqualFold(strings.TrimSpace(entry.MD5), digest) {
					hashMatch = true
				}
			}
			if entry.Type == 0 && entry.Name == f.Name && entry.Size == f.Size && strings.EqualFold(strings.TrimSpace(entry.MD5), digest) && (id == "0" || entry.ID == id) {
				matches++
			}
		}
		if nameCount > 1 {
			return errors.New("123 目标目录存在多个同名项目 [123-VERIFY-AMBIGUOUS/0.5.1]，未自动重传")
		}
		if matches == 1 {
			return nil
		}
		if matches > 1 {
			return errors.New("123 上传结果存在多个匹配文件 [123-VERIFY-AMBIGUOUS/0.5.1]")
		}
		if attempt < 5 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return fmt.Errorf("123 目录结果待核对 [123-VERIFY/0.5.1] nameCount=%d idSeen=%t sizeMatch=%t hashMatch=%t；文件可能已保存，请先核对网盘，未自动重传", nameCount, idSeen, sizeMatch, hashMatch)
}

// Signed URLs are delegated by the fixed, authenticated API. Keep their path and
// query byte-for-byte; never send the account Authorization or Cookie to storage.
func pan123StorageURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || !validUploadDNS(strings.ToLower(u.Hostname())) {
		return nil, errors.New("123 返回无效上传节点 [123-NODE/0.5.1]；未发送文件")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return nil, errors.New("123 上传节点端口无效 [123-NODE-PORT/0.5.1]")
		}
	}
	// S3 signatures cover host/path/query, not the URL scheme. Upgrade HTTP
	// descriptors before sending any bytes, keeping signed path/query intact.
	u.Scheme = "https"
	return u, nil
}
func (c pan123Client) putSigned(ctx context.Context, address string, reader io.Reader, length int64, progress func(int64)) error {
	target, e := pan123StorageURL(address)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", target.String(), &progressReader{r: reader, fn: progress})
	if e != nil {
		return errors.New("123 上传地址无效")
	}
	req.ContentLength = length
	if length == 0 {
		req.Body = http.NoBody
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("123 上传连接失败 [123-PUT-CONNECT/0.5.1] host=%s", target.Hostname())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("123 上传失败 [123-PUT-HTTP/0.5.1] host=%s HTTP %d", target.Hostname(), resp.StatusCode)
	}
	progress(length)
	return nil
}
