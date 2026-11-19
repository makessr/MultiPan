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
	"regexp"
	"strconv"
	"strings"
	"time"
)

const CtFileAPI = "https://rest.ctfile.com/v1"

var ctFolderPattern = regexp.MustCompile(`^d(?:0|[1-9][0-9]{0,19})$`)
var ctFilePattern = regexp.MustCompile(`^f[1-9][0-9]{0,19}$`)

func validCtFolder(s string) bool { return ctFolderPattern.MatchString(s) }
func ctSpace(space string) string {
	if space == "private" {
		return "private"
	}
	return "public"
}

type CtLoginInput struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Folder    string `json:"folder"`
	Space     string `json:"space"`
	Mode      string `json:"mode"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	Phone     string `json:"phone"`
	Code      string `json:"code"`
	Timestamp int64  `json:"timestamp"`
	Checksum  string `json:"checksum"`
}
type CtSMSChallenge struct {
	Timestamp int64  `json:"timestamp"`
	Checksum  string `json:"checksum"`
}
type ctClient struct {
	client  *http.Client
	account Account
}

func (c ctClient) call(ctx context.Context, stage string, payload map[string]any, out any) error {
	if c.account.Endpoint != CtFileAPI {
		return errors.New("诚通接口来源无效 [CT-ORIGIN/0.7.1]")
	}
	data := map[string]any{}
	for k, v := range payload {
		data[k] = v
	}
	if c.account.Secret != "" {
		data["session"] = c.account.Secret
	}
	body, e := json.Marshal(data)
	if e != nil {
		return e
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", CtFileAPI+stage, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent(c.account))
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("诚通连接失败 [CT-CONNECT/0.7.1] stage=%s", stage)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("诚通接口失败 [CT-HTTP/0.7.1] stage=%s HTTP=%d", stage, resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if e != nil {
		return errors.New("诚通响应读取失败 [CT-READ/0.7.1]")
	}
	if e = checkCtCode(raw, stage); e != nil {
		return e
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("诚通数据不兼容 [CT-DATA/0.7.1] stage=%s", stage)
	}
	return nil
}
func checkCtCode(raw []byte, stage string) error {
	var response struct {
		Code json.Number `json:"code"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Code == "" {
		return fmt.Errorf("诚通响应格式无效 [CT-JSON/0.7.1] stage=%s", stage)
	}
	if response.Code == "401" {
		return errors.New("诚通登录已失效，请重新登录 [CT-AUTH/0.7.1]")
	}
	if response.Code != "200" {
		return fmt.Errorf("诚通拒绝请求 [CT-API/0.7.1] stage=%s code=%s，请核对验证码、登录信息、权限或网盘额度", stage, response.Code)
	}
	return nil
}
func VerifyCtSession(ctx context.Context, client *http.Client, a Account) error {
	var result struct {
		ID json.Number `json:"userid"`
	}
	if e := (ctClient{client, a}).call(ctx, "/user/info/profile", nil, &result); e != nil {
		return e
	}
	id, e := strconv.ParseUint(string(result.ID), 10, 64)
	if e != nil || id == 0 {
		return errors.New("诚通未返回有效用户，请重新登录 [CT-PROFILE/0.7.1]")
	}
	return nil
}

const ctRetiredLoginMessage = "诚通旧短信及密码登录接口已停用（HTTP 410）。请打开官方密钥管理页，登录后生成或查看开放接口 Session，再回到此处保存。"

func RequestCtSMS(ctx context.Context, client *http.Client, phone string) (CtSMSChallenge, error) {
	return CtSMSChallenge{}, errors.New(ctRetiredLoginMessage)
}
func LoginCtSession(ctx context.Context, client *http.Client, in CtLoginInput) (string, error) {
	return "", errors.New(ctRetiredLoginMessage)
}

type ctEntry struct {
	Key    string      `json:"key"`
	Name   string      `json:"name"`
	Size   json.Number `json:"size"`
	Status json.Number `json:"status"`
}

func (c ctClient) folders(ctx context.Context, parent string) ([]ctEntry, error) {
	if !validCtFolder(parent) {
		return nil, errors.New("诚通目录 ID 无效，根目录为 d0")
	}
	var result struct {
		Results []ctEntry `json:"results"`
	}
	e := c.call(ctx, "/"+ctSpace(c.account.Username)+"/folder/list", map[string]any{"folder_id": parent, "orderby": "az"}, &result)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, f := range result.Results {
		if !validCtFolder(f.Key) || f.Key == "d0" || seen[f.Key] {
			return nil, errors.New("诚通目录结果无效 [CT-FOLDERS/0.7.1]")
		}
		seen[f.Key] = true
	}
	return result.Results, nil
}
func (c ctClient) files(ctx context.Context, parent string) ([]ctEntry, error) {
	if !validCtFolder(parent) {
		return nil, errors.New("诚通目录 ID 无效")
	}
	entries := []ctEntry{}
	seen := map[string]bool{}
	start := 0
	for page := 0; page < 10000; page++ {
		var result struct {
			Results []ctEntry `json:"results"`
		}
		if e := c.call(ctx, "/"+ctSpace(c.account.Username)+"/file/list", map[string]any{"folder_id": parent, "start": start, "orderby": "az"}, &result); e != nil {
			return nil, e
		}
		if len(result.Results) == 0 {
			return entries, nil
		}
		added := 0
		for _, f := range result.Results {
			if !ctFilePattern.MatchString(f.Key) && !validCtFolder(f.Key) {
				return nil, errors.New("诚通文件标识无效 [CT-FILE-ID/0.7.1]")
			}
			if !seen[f.Key] {
				seen[f.Key] = true
				entries = append(entries, f)
				added++
			}
		}
		if added == 0 {
			return nil, errors.New("诚通文件分页未推进 [CT-PAGE/0.7.1]")
		}
		start += len(result.Results)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, errors.New("诚通文件页数超出限制")
}
func ListCtFolders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	if parent == "" {
		parent = "d0"
	}
	p := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: "d0", Name: "根目录"}}}
	folders, e := (ctClient{client, a}).folders(ctx, parent)
	if e != nil {
		return p, e
	}
	for _, f := range folders {
		p.Folders = append(p.Folders, CloudFolder{ID: f.Key, Name: f.Name})
	}
	if parent != "d0" {
		name := "目录 " + parent
		if parent == a.Folder && a.FolderDisplay != "" {
			name = a.FolderDisplay
		}
		p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: parent, Name: name})
	}
	return p, nil
}
func (c ctClient) confirm(ctx context.Context, folder string, f File) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		files, e := c.files(ctx, folder)
		if e != nil {
			return e
		}
		names, matches := 0, 0
		for _, item := range files {
			if item.Name == f.Name {
				names++
				if item.Size == json.Number(strconv.FormatInt(f.Size, 10)) && item.Status == "1" && ctFilePattern.MatchString(item.Key) {
					matches++
				}
			}
		}
		if names == 1 && matches == 1 {
			return nil
		}
		if names > 1 {
			return errors.New("诚通目标存在多个同名项目，请核对网盘 [CT-AMBIGUOUS/0.7.1]")
		}
		select {
		case <-ctx.Done():
			return errors.New("诚通云端结果暂未确认 [CT-PENDING/0.7.1]；文件可能已保存，请核对网盘，未自动重传")
		case <-time.After(time.Second):
		}
	}
}
func (u *HTTPUploader) ctfile(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	c := ctClient{u.Client, a}
	folders, e := c.folders(ctx, folder)
	if e != nil {
		return e
	}
	files, e := c.files(ctx, folder)
	if e != nil {
		return e
	}
	for _, entry := range append(folders, files...) {
		if entry.Name == f.Name {
			return errors.New("诚通目标已存在同名项目，未覆盖；请更改保存目录或本地文件名")
		}
	}
	h := md5.New()
	if _, e = io.Copy(h, ctxReader{ctx, io.NewSectionReader(file, 0, f.Size)}); e != nil {
		return e
	}
	if e = unchanged(file, f); e != nil {
		return e
	}
	var ticket struct {
		URL    string      `json:"upload_url"`
		Exists json.Number `json:"exists"`
	}
	if e = c.call(ctx, "/"+ctSpace(a.Username)+"/file/upload", map[string]any{"folder_id": folder, "name": f.Name, "size": f.Size, "checksum": hex.EncodeToString(h.Sum(nil)) + "-" + strconv.FormatInt(f.Size, 10)}, &ticket); e != nil {
		return e
	}
	exists := int64(0)
	if ticket.Exists != "" {
		exists, e = ticket.Exists.Int64()
		if e != nil || exists < 0 {
			return errors.New("诚通返回无效上传状态 [CT-TICKET/0.7.1]")
		}
	}
	if exists == 0 {
		if e = c.upload(ctx, ticket.URL, f, file, progress); e != nil {
			return e
		}
	}
	if e = c.confirm(ctx, folder, f); e != nil {
		return e
	}
	progress(f.Size)
	return unchanged(file, f)
}
func (c ctClient) upload(ctx context.Context, address string, f File, file *os.File, progress func(int64)) error {
	node, e := url.Parse(address)
	if e != nil || node.User != nil || node.Fragment != "" || (node.Scheme != "https" && node.Scheme != "http") || !validUploadDNS(strings.ToLower(node.Hostname())) {
		return errors.New("诚通上传节点无效 [CT-NODE/0.7.1]；未发送文件")
	}
	// The fixed authenticated API delegates this upload URL. Never forward the
	// account session or Cookie; keep its signed query intact and require TLS.
	node.Scheme = "https"
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if e = writer.WriteField("name", f.Name); e != nil {
		return e
	}
	if e = writer.WriteField("filesize", strconv.FormatInt(f.Size, 10)); e != nil {
		return e
	}
	if _, e = writer.CreateFormFile("file", f.Name); e != nil {
		return e
	}
	head := append([]byte{}, buffer.Bytes()...)
	buffer.Reset()
	if e = writer.Close(); e != nil {
		return e
	}
	tail := buffer.Bytes()
	body := io.MultiReader(bytes.NewReader(head), &progressReader{r: io.NewSectionReader(file, 0, f.Size), fn: progress}, bytes.NewReader(tail))
	req, _ := http.NewRequestWithContext(ctx, "POST", node.String(), body)
	req.ContentLength = int64(len(head)) + f.Size + int64(len(tail))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", userAgent(c.account))
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("诚通上传连接失败 [CT-UPLOAD/0.7.1] host=%s", node.Hostname())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("诚通上传失败 [CT-UPLOAD-HTTP/0.7.1] host=%s HTTP=%d", node.Hostname(), resp.StatusCode)
	}
	result, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return errors.New("诚通上传结果读取失败")
	}
	return checkCtCode(result, "upload-result")
}
