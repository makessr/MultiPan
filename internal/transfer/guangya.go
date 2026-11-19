package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const GuangyaAPI = "https://api.guangyapan.com"
const guangyaRoot = "root"

type gyClient struct {
	client  *http.Client
	account Account
}

func (c gyClient) call(ctx context.Context, stage string, body any, out any, allowed ...int) (int, error) {
	if c.account.Endpoint != GuangyaAPI {
		return 0, errors.New("光鸭接口来源无效 [GY-ORIGIN/0.8.1]")
	}
	var payload io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return 0, e
		}
		payload = bytes.NewReader(data)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", GuangyaAPI+stage, payload)
	req.Header.Set("Authorization", "Bearer "+pan123Token(c.account.Secret))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent(c.account))
	req.Header.Set("Dt", "4")
	if c.account.Username != "" {
		req.Header.Set("Did", c.account.Username)
	}
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("光鸭连接失败 [GY-CONNECT/0.8.1] stage=%s", stage)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return 0, errors.New("光鸭登录失效或访问被拒绝，请重新登录 [GY-AUTH/0.8.1]")
	}
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("光鸭接口失败 [GY-HTTP/0.8.1] stage=%s HTTP=%d", stage, resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if e != nil {
		return 0, errors.New("光鸭响应读取失败")
	}
	var envelope struct {
		Code      json.RawMessage `json:"code"`
		Data      json.RawMessage `json:"data"`
		Error     json.RawMessage `json:"error"`
		ErrorCode json.RawMessage `json:"error_code"`
	}
	invalid := func(reason string) (int, error) {
		return 0, fmt.Errorf("光鸭响应格式无效 [GY-JSON/0.12.1] stage=%s HTTP=%d reason=%s", stage, resp.StatusCode, reason)
	}
	// Rename returns an acknowledgement, not file metadata. The official web
	// client does not consume its data field. Final success is established by
	// RenameAccountEntry's separate parent-list ID/name/type confirmation.
	renameAck := stage == "/userres/v1/file/rename" && out == nil
	if renameAck {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return invalid("invalid-ack")
		}
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return invalid("invalid-json")
	}
	if renameAck && (len(envelope.Error) > 0 || len(envelope.ErrorCode) > 0) {
		return invalid("error-envelope")
	}
	code := 0
	if len(envelope.Code) > 0 {
		if bytes.Equal(bytes.TrimSpace(envelope.Code), []byte("null")) || json.Unmarshal(envelope.Code, &code) != nil {
			return invalid("invalid-code")
		}
	} else {
		// The official web client accepts a missing business code as success.
		// Reads still require an object payload: an empty acknowledgement must
		// never become a successfully verified account or an empty file list.
		payload := bytes.TrimSpace(envelope.Data)
		if (!renameAck && (len(payload) == 0 || payload[0] != '{')) || len(envelope.Error) > 0 || len(envelope.ErrorCode) > 0 {
			return invalid("missing-success-data")
		}
	}
	ok := code == 0
	for _, v := range allowed {
		if code == v {
			ok = true
		}
	}
	if !ok {
		return code, fmt.Errorf("光鸭拒绝请求 [GY-CODE/0.8.1] stage=%s code=%d", stage, code)
	}
	if out != nil && json.Unmarshal(envelope.Data, out) != nil {
		return code, fmt.Errorf("光鸭数据格式不兼容 [GY-DATA/0.8.1] stage=%s HTTP=%d", stage, resp.StatusCode)
	}
	return code, nil
}

type gyEntry struct {
	ID     string `json:"fileId"`
	Parent string `json:"parentId"`
	Name   string `json:"fileName"`
	Size   int64  `json:"fileSize"`
	Type   int    `json:"resType"`
}

func (c gyClient) list(ctx context.Context, parent string) ([]gyEntry, error) {
	if !validQuarkFolder(parent) {
		return nil, errors.New("光鸭目录 ID 无效")
	}
	if parent == guangyaRoot {
		parent = ""
	}
	entries := []gyEntry{}
	seen := map[string]bool{}
	for page := 0; page < 10000; page++ {
		var data struct {
			List  []gyEntry `json:"list"`
			Total int       `json:"total"`
		}
		if _, e := c.call(ctx, "/userres/v1/file/get_file_list", map[string]any{"parentId": parent, "page": page, "pageSize": 100, "orderBy": 3, "sortType": 1}, &data); e != nil {
			return nil, e
		}
		added := 0
		for _, f := range data.List {
			if !validQuarkFolder(f.ID) || f.ID == guangyaRoot {
				return nil, errors.New("光鸭返回无效文件标识")
			}
			if !seen[f.ID] {
				seen[f.ID] = true
				entries = append(entries, f)
				added++
			}
		}
		if len(data.List) < 100 || (data.Total > 0 && len(entries) >= data.Total) {
			return entries, nil
		}
		if added == 0 {
			return nil, errors.New("光鸭分页未推进")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return nil, errors.New("光鸭目录页数超出限制")
}
func VerifyGuangyaSession(ctx context.Context, client *http.Client, a Account) error {
	_, e := (gyClient{client, a}).list(ctx, guangyaRoot)
	return e
}
func ListGuangyaFolders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	if parent == "" {
		parent = guangyaRoot
	}
	p := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: guangyaRoot, Name: "根目录"}}}
	files, e := (gyClient{client, a}).list(ctx, parent)
	if e != nil {
		return p, e
	}
	for _, f := range files {
		if f.Type == 2 {
			p.Folders = append(p.Folders, CloudFolder{ID: f.ID, Name: f.Name})
		}
	}
	if parent != guangyaRoot {
		name := "目录 " + parent
		if parent == a.Folder && a.FolderDisplay != "" {
			name = a.FolderDisplay
		}
		p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: parent, Name: name})
	}
	return p, nil
}

type gyCreds struct {
	AK    string `json:"accessKeyID"`
	SK    string `json:"secretAccessKey"`
	Token string `json:"sessionToken"`
}
type gyTicket struct {
	Task         string  `json:"taskId"`
	Object       string  `json:"objectPath"`
	Bucket       string  `json:"bucketName"`
	Endpoint     string  `json:"endPoint"`
	FullEndpoint string  `json:"fullEndPoint"`
	Provider     int     `json:"provider"`
	Creds        gyCreds `json:"creds"`
	AK           string  `json:"accessKeyID"`
	SK           string  `json:"secretAccessKey"`
	Token        string  `json:"sessionToken"`
}

func (u *HTTPUploader) guangya(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	return u.guangyaUpload(ctx, a, f, folder, file, progress, nil)
}

func (u *HTTPUploader) guangyaUpload(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64), recordID func(string) error) error {
	c := gyClient{u.Client, a}
	if recordID == nil {
		entries, err := c.list(ctx, folder)
		if err != nil {
			return err
		}
		for _, item := range entries {
			if item.Name == f.Name {
				return errors.New("光鸭目标已存在同名项目，未覆盖；请更改保存目录或本地文件名")
			}
		}
	}
	var e error
	h := md5.New()
	if _, e = io.Copy(h, ctxReader{ctx, io.NewSectionReader(file, 0, f.Size)}); e != nil {
		return e
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
	parent := folder
	if parent == guangyaRoot {
		parent = ""
	}
	var ticket gyTicket
	// Official web provider capability mask: ALI_OSS=2. Request that supported
	// storage protocol explicitly, rather than assuming all ticket providers are OSS.
	cp := checkpoint(ctx)
	code := 0
	if len(cp.Meta) > 0 {
		e = json.Unmarshal(cp.Meta, &ticket)
	} else {
		code, e = c.call(ctx, "/userres/v2/get_res_center_token", map[string]any{"capacity": 2, "name": f.Name, "parentId": parent, "res": map[string]any{"fileSize": f.Size, "md5": hex.EncodeToString(h.Sum(nil))}}, &ticket, 156)
	}
	if e == nil && code != 156 {
		cp.Meta, _ = json.Marshal(ticket)
		if e = cp.save(); e != nil {
			return errors.New("无法保存光鸭上传续传记录")
		}
	}
	if e != nil {
		return e
	}
	if ticket.Task == "" {
		return errors.New("光鸭缺少上传任务标识 [GY-TASK/0.8.1]")
	}
	if code != 156 && !cp.Merged {
		if e = u.gyOSS(ctx, ticket, f, file, progress); e != nil {
			return e
		}
	}
	verify, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	for {
		var result gyEntry
		code, e = c.call(verify, "/userres/v1/file/get_info_by_task_id", map[string]any{"taskId": ticket.Task}, &result, 145, 146, 147, 155, 163)
		if e != nil {
			return e
		}
		if result.ID != "" {
			if existingIDs[result.ID] {
				return errors.New("光鸭返回上传前已经存在的文件 ID，保持待核对")
			}
			// The task response may contain only the final ID. Resolve its metadata
			// within the chosen directory; never accept a task ID as a completed file.
			files, e := c.list(verify, folder)
			if e != nil {
				return e
			}
			for _, item := range files {
				if item.ID == result.ID {
					if item.Name != f.Name || item.Size != f.Size || item.Type != 1 {
						return errors.New("光鸭最终文件与目标不匹配 [GY-RESULT/0.8.1]")
					}
					progress(f.Size)
					if err := unchanged(file, f); err != nil {
						return err
					}
					if recordID != nil {
						return recordID(item.ID)
					}
					return nil
				}
			}
		}
		select {
		case <-verify.Done():
			return errors.New("光鸭云端结果暂未确认 [GY-PENDING/0.8.1]；请先核对网盘，未自动重传")
		case <-time.After(time.Second):
		}
	}
}
func (u *HTTPUploader) gyOSS(ctx context.Context, t gyTicket, f File, file *os.File, progress func(int64)) error {
	if t.Provider != 0 && t.Provider != 1 {
		return fmt.Errorf("光鸭返回与所申请能力不符的存储类型 [GY-PROVIDER/0.8.1] provider=%d；未发送文件", t.Provider)
	}
	if t.Endpoint == "" {
		t.Endpoint = t.FullEndpoint
	}
	if t.Creds.AK == "" {
		t.Creds = gyCreds{t.AK, t.SK, t.Token}
	}
	if t.Object == "" || t.Bucket == "" || t.Creds.AK == "" || t.Creds.SK == "" || t.Creds.Token == "" {
		return errors.New("光鸭上传授权不完整 [GY-CREDS/0.8.1]")
	}
	raw := strings.TrimSpace(t.Endpoint)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	node, e := url.Parse(raw)
	if e != nil || node.User != nil || node.RawQuery != "" || node.Fragment != "" || (node.Path != "" && node.Path != "/") || (node.Scheme != "https" && node.Scheme != "http") {
		return errors.New("光鸭上传节点无效 [GY-NODE/0.8.1]")
	}
	host := strings.TrimPrefix(strings.ToLower(node.Hostname()), strings.ToLower(t.Bucket)+".")
	if !validUploadDNS(host) || node.Port() != "" {
		return errors.New("光鸭上传节点主机无效 [GY-NODE-HOST/0.8.1]")
	}
	sdk, e := oss.New("https://"+host, t.Creds.AK, t.Creds.SK, oss.SecurityToken(t.Creds.Token), oss.HTTPClient(u.Client))
	if e != nil {
		return errors.New("光鸭存储客户端初始化失败")
	}
	bucket, e := sdk.Bucket(t.Bucket)
	if e != nil {
		return errors.New("光鸭存储桶信息无效")
	}
	fail := func(err error, stage string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var remote oss.ServiceError
		if errors.As(err, &remote) && quarkIDPattern.MatchString(remote.Code) {
			return fmt.Errorf("光鸭存储传输失败 [GY-OSS/0.8.1] stage=%s host=%s HTTP=%d code=%s", stage, host, remote.StatusCode, remote.Code)
		}
		return fmt.Errorf("光鸭存储传输失败 [GY-OSS/0.8.1] stage=%s host=%s，请检查网络或重新登录", stage, host)
	}
	if f.Size == 0 {
		if e = bucket.PutObject(t.Object, strings.NewReader(""), oss.WithContext(ctx)); e != nil {
			return fail(e, "object-transfer")
		}
		return nil
	}
	cp := checkpoint(ctx)
	upload := oss.InitiateMultipartUploadResult{Bucket: t.Bucket, Key: t.Object, UploadID: cp.MultipartID}
	if cp.MultipartID == "" {
		upload, e = bucket.InitiateMultipartUpload(t.Object, oss.Sequential(), oss.WithContext(ctx))
	}
	if e != nil {
		return fail(e, "object-transfer")
	}
	cp.MultipartID = upload.UploadID
	if e = cp.save(); e != nil {
		return e
	}
	chunk := int64(8 << 20)
	if f.Size/chunk >= 10000 {
		chunk = (f.Size + 9998) / 9999
	}
	parts := []oss.UploadPart{}
	for offset := int64(0); offset < f.Size; offset += chunk {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		length := min(chunk, f.Size-offset)
		number := len(parts) + 1
		if etag, ok := cp.Parts[number]; ok {
			parts = append(parts, oss.UploadPart{PartNumber: number, ETag: etag})
			progress(offset + length)
			continue
		}
		start := offset
		reader := &progressReader{r: io.NewSectionReader(file, offset, length), fn: func(n int64) { progress(start + n) }}
		part, e := bucket.UploadPart(upload, reader, length, len(parts)+1, oss.WithContext(ctx))
		if e != nil {
			return fail(e, "object-transfer")
		}
		parts = append(parts, part)
		cp.Parts[part.PartNumber] = part.ETag
		if e = cp.save(); e != nil {
			return e
		}
	}
	if _, e = bucket.CompleteMultipartUpload(upload, parts, oss.WithContext(ctx)); e != nil {
		return fail(e, "object-transfer")
	}
	cp.Merged = true
	if e = cp.save(); e != nil {
		return e
	}
	return nil
}
