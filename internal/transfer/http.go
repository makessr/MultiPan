package transfer

import (
	"bytes"
	"context"
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

type HTTPUploader struct{ Client *http.Client }

func NewHTTPUploader() *HTTPUploader {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 90 * time.Second
	return &HTTPUploader{Client: &http.Client{Transport: limitedTransport{base: t}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type progressReader struct {
	r     io.Reader
	total int64
	fn    func(int64)
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, e := p.r.Read(b)
	p.total += int64(n)
	if p.fn != nil && (time.Since(p.last) > 150*time.Millisecond || e == io.EOF) {
		p.fn(p.total)
		p.last = time.Now()
	}
	return n, e
}
func (u *HTTPUploader) do(ctx context.Context, a Account, method, address string, body io.Reader, ct string, length int64) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, method, address, body)
	if e != nil {
		return nil, errors.New("目标地址无效")
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if length >= 0 {
		req.ContentLength = length
	}
	if a.Kind == "webdav" || a.Kind == "ctdav" {
		req.SetBasicAuth(a.Username, a.Secret)
		if method == "PUT" {
			req.Header.Set("If-None-Match", "*")
		}
	} else {
		req.Header.Set("Cookie", a.Secret)
		req.Header.Set("User-Agent", userAgent(a))
		req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
	}
	resp, e := u.Client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("连接失败或请求超时，请检查网络和网盘地址")
	}
	return resp, nil
}
func userAgent(a Account) string {
	if a.UserAgent != "" {
		return a.UserAgent
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
}
func statusError(code int) error {
	switch code {
	case 401, 403:
		return errors.New("网盘拒绝访问，请检查账号权限和登录凭据")
	case 409:
		return errors.New("目标目录不存在或发生冲突")
	case 412:
		return errors.New("目标已存在同名文件，未覆盖；请更改目标目录或文件名")
	case 413:
		return errors.New("文件超过网盘或接口的大小限制")
	case 429:
		return errors.New("网盘请求过于频繁，请稍后重试")
	}
	return fmt.Errorf("网盘返回 HTTP %d，请核对地址、权限和文件限制", code)
}
func (u *HTTPUploader) Upload(ctx context.Context, a Account, f File, folder string, progress func(int64)) error {
	file, e := os.Open(f.Path)
	if e != nil {
		return errors.New("无法打开源文件")
	}
	defer file.Close()
	return u.uploadOpened(ctx, a, f, folder, file, progress)
}

func (u *HTTPUploader) uploadOpened(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	info, e := file.Stat()
	if e != nil || info.Size() != f.Size || info.ModTime().UnixMilli() != f.Modified {
		return errors.New("源文件发生变化，请重新选择")
	}
	if a.Kind == "lanzou" {
		return u.lanzou(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "quark" {
		return u.quark(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "pan123" {
		return u.pan123(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "baidu" {
		return u.baidu(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "ctfile" {
		return u.ctfile(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "ctweb" {
		return u.ctweb(ctx, a, f, folder, file, progress)
	}
	if a.Kind == "guangya" {
		return u.guangya(ctx, a, f, folder, file, progress)
	}
	if a.Kind != "webdav" && a.Kind != "ctdav" {
		return errors.New("不支持该网盘")
	}
	base, e := url.Parse(a.Endpoint)
	if e != nil {
		return errors.New("WebDAV 地址无效")
	}
	prefix := strings.TrimRight(base.Path, "/")
	for _, part := range strings.Split(strings.Trim(folder, "/"), "/") {
		if part == "" {
			continue
		}
		if part == ".." || part == "." {
			return errors.New("目标目录无效")
		}
		prefix += "/" + part
		dir := *base
		dir.Path = prefix
		dir.RawPath = ""
		resp, e := u.do(ctx, a, "MKCOL", dir.String(), nil, "", 0)
		if e != nil {
			return e
		}
		resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 405 {
			return statusError(resp.StatusCode)
		}
	}
	dest := *base
	dest.Path = prefix + "/" + f.Name
	dest.RawPath = ""
	reader := &progressReader{r: io.LimitReader(file, f.Size), fn: progress}
	resp, e := u.do(ctx, a, "PUT", dest.String(), reader, "application/octet-stream", f.Size)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	return unchanged(file, f)
}
func unchanged(file *os.File, f File) error {
	a, e := file.Stat()
	if e != nil || a.Size() != f.Size || a.ModTime().UnixMilli() != f.Modified {
		return errors.New("上传期间源文件发生变化，请核对远端文件")
	}
	return nil
}

var iframePattern = regexp.MustCompile(`(?i)<iframe[^>]+src\s*=\s*["']([^"']+)["']`)

func (u *HTTPUploader) lanzou(ctx context.Context, a Account, f File, folder string, file *os.File, progress func(int64)) error {
	if _, e := strconv.ParseInt(folder, 10, 64); e != nil {
		return errors.New("蓝奏云目录 ID 无效")
	}
	resp, e := u.do(ctx, a, "GET", a.Endpoint+"/mydisk.php", nil, "", 0)
	if e != nil {
		return e
	}
	page, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	if readErr != nil {
		return errors.New("读取蓝奏云登录页面失败")
	}
	if resp.StatusCode != 200 {
		return statusError(resp.StatusCode)
	}
	referer := a.Endpoint + "/mydisk.php"
	if match := iframePattern.FindSubmatch(page); len(match) > 1 {
		base, _ := url.Parse(a.Endpoint)
		frame, e := base.Parse(string(match[1]))
		if e == nil && frame.Host == base.Host && frame.Scheme == base.Scheme {
			referer = frame.String()
		}
	}
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)
	fields := [][2]string{{"task", "1"}, {"vie", "2"}, {"ve", "2"}, {"lastModifiedDate", time.UnixMilli(f.Modified).UTC().Format(time.RFC1123)}, {"type", "application/octet-stream"}, {"id", "WU_FILE_0"}, {"folder_id_bb_n", folder}, {"size", strconv.FormatInt(f.Size, 10)}, {"name", f.Name}}
	for _, v := range fields {
		if e := writer.WriteField(v[0], v[1]); e != nil {
			return e
		}
	}
	if _, e := writer.CreateFormFile("upload_file", f.Name); e != nil {
		return errors.New("文件名无效")
	}
	header := append([]byte(nil), prefix.Bytes()...)
	prefix.Reset()
	if e := writer.Close(); e != nil {
		return e
	}
	tail := append([]byte(nil), prefix.Bytes()...)
	body := io.MultiReader(bytes.NewReader(header), &progressReader{r: io.LimitReader(file, f.Size), fn: progress}, bytes.NewReader(tail))
	req, e := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/html5up.php", body)
	if e != nil {
		return errors.New("蓝奏云地址无效")
	}
	req.ContentLength = int64(len(header)+len(tail)) + f.Size
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", userAgent(a))
	resp, e = u.Client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("蓝奏云上传连接失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return statusError(resp.StatusCode)
	}
	var result struct {
		ZT json.RawMessage `json:"zt"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); e != nil {
		return errors.New("蓝奏云返回内容无法识别，登录可能已失效或接口已变化")
	}
	code := strings.Trim(string(result.ZT), "\"")
	if code == "9" {
		return errors.New("蓝奏云登录已失效，请重新导入 Cookie")
	}
	if code != "1" && code != "2" {
		return fmt.Errorf("蓝奏云未接受上传（状态 %s）；请检查登录、目录、文件类型和大小限制", safeCode(code))
	}
	return unchanged(file, f)
}
func safeCode(s string) string {
	if _, e := strconv.Atoi(s); e != nil || len(s) > 6 {
		return "未知"
	}
	return s
}
