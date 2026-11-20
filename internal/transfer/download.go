package transfer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type DownloadInput struct {
	AccountID string `json:"accountId"`
	ParentID  string `json:"parentId"`
	EntryID   string `json:"entryId"`
	Name      string `json:"name"`
}
type DownloadResult struct {
	Verification string `json:"verification,omitempty"`
	Details      string `json:"details,omitempty"`
	Warning      string `json:"warning,omitempty"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	Cancelled    bool   `json:"cancelled"`
}
type DownloadProgress struct {
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

func SuggestedDownloadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, " .")
	if name == "" {
		return "下载文件"
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		name = "_" + name
	}
	return name
}

func (m *Manager) downloadFile(ctx context.Context, in DownloadInput, destination string, progress func(DownloadProgress)) (DownloadResult, error) {
	m.mu.Lock()
	var a Account
	for _, account := range m.accounts {
		if account.ID == in.AccountID {
			a = account
			break
		}
	}
	closing := m.closing
	originals := append([]UploadFingerprint(nil), m.fingerprints...)
	m.mu.Unlock()
	if closing {
		return DownloadResult{}, errors.New("程序正在退出")
	}
	if a.ID == "" {
		return DownloadResult{}, errors.New("账号不存在")
	}
	return DownloadAccountFile(ctx, NewHTTPUploader().Client, a, in, destination, progress, originals...)
}

func DownloadAccountFile(ctx context.Context, client *http.Client, a Account, in DownloadInput, destination string, progress func(DownloadProgress), originals ...UploadFingerprint) (DownloadResult, error) {
	fail := func(message string) (DownloadResult, error) { return DownloadResult{}, errors.New(message) }
	if a.Kind != "ctdav" && a.Kind != "webdav" && a.Kind != "pan123" && a.Kind != "guangya" && a.Kind != "quark" && a.Kind != "baidu" && a.Kind != "lanzou" {
		return fail("此网盘暂未接入文件下载")
	}
	if !filepath.IsAbs(destination) || SuggestedDownloadName(filepath.Base(destination)) != filepath.Base(destination) {
		return fail("本机保存路径无效，请重新选择")
	}
	if _, e := os.Lstat(destination); !errors.Is(e, os.ErrNotExist) {
		return fail("保存位置已有同名项目或无法访问，请选择其他文件名；未覆盖本机文件")
	}
	// Neither the metadata request nor the content request may redirect the
	// authenticated session to another address.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var source CloudEntry
	var resp *http.Response
	var expectedMD5 string
	var e error
	if a.Kind == "pan123" {
		source, resp, expectedMD5, e = open123Download(ctx, &bounded, a, in)
	} else if a.Kind == "lanzou" {
		source, resp, expectedMD5, e = openLanzouDownload(ctx, &bounded, a, in)
	} else if a.Kind == "baidu" {
		source, resp, expectedMD5, e = openBaiduDownload(ctx, &bounded, a, in)
	} else if a.Kind == "quark" {
		source, resp, expectedMD5, e = openQuarkDownload(ctx, &bounded, a, in)
	} else if a.Kind == "guangya" {
		source, resp, expectedMD5, e = openGuangyaDownload(ctx, &bounded, a, in)
	} else {
		source, resp, e = openDAVDownload(ctx, &bounded, a, in)
	}
	if e != nil {
		return DownloadResult{}, e
	}
	defer func() { resp.Body.Close() }()
	// Delegated cloud content can use non-200 successful responses. File data
	// must still pass the exact metadata length and available MD5 checks below.
	statusOK := resp.StatusCode == 200 || ((a.Kind == "pan123" || a.Kind == "guangya" || a.Kind == "quark" || a.Kind == "baidu" || a.Kind == "lanzou") && resp.StatusCode >= 200 && resp.StatusCode < 300)
	if !statusOK {
		return fail(fmt.Sprintf("下载接口 HTTP=%d；未保存正式文件", resp.StatusCode))
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return fail("下载响应编码异常，未保存正式文件")
	}
	if a.Kind == "lanzou" && source.Size == nil {
		for _, r := range originals {
			candidate := source
			candidate.Size = &r.Size
			if originalFor(originals, a.ID, candidate) != "" {
				source = candidate
				break
			}
		}
	}
	total := resp.ContentLength
	if source.Size != nil {
		total = *source.Size
		if resp.ContentLength >= 0 && resp.ContentLength != total {
			return fail("文件大小已变化，请刷新后重新下载")
		}
	}
	// Quark's JSON md5 is an advisory reference: clients have observed it
	// differing from the downloaded bytes. A content digest on the actual
	// response is a separate, mandatory transport check when supplied.
	contentMD5 := ""
	if a.Kind == "quark" && len(resp.Header.Values("Content-MD5")) > 0 {
		values := resp.Header.Values("Content-MD5")
		digest, err := base64.StdEncoding.DecodeString(strings.TrimSpace(values[0]))
		if len(values) != 1 || err != nil || len(digest) != md5.Size {
			return fail("下载响应 Content-MD5 无效，未保存正式文件")
		}
		contentMD5 = hex.EncodeToString(digest)
	}
	resumePath, _ := ctx.Value(resumePathKey{}).(string)
	identity := resumeIdentity(a, in, source, resp, originalFor(originals, a.ID, source), expectedMD5, contentMD5)
	var tmp *os.File
	var offset int64
	if resumePath != "" {
		tmp, resp, offset, e = openResumeFile(resumePath, identity, ctx, &bounded, resp, total)
	} else {
		tmp, e = os.CreateTemp(filepath.Dir(destination), ".multipan-download-*.part")
	}
	if e != nil {
		if ctx.Err() != nil {
			return DownloadResult{}, ctx.Err()
		}
		return fail("无法创建下载临时文件，请检查磁盘和目录权限")
	}
	tempPath := tmp.Name()
	keepPartial := false
	defer func() {
		if !keepPartial {
			_ = os.Remove(tempPath)
			if resumePath != "" {
				_ = os.Remove(tempPath + ".resume")
			}
		}
	}()
	defer tmp.Close()
	checksum := md5.New()
	originalHash := sha256.New()
	expectedSHA := originalFor(originals, a.ID, source)
	writer := &downloadWriter{ctx: ctx, file: tmp, total: total, progress: progress}
	if progress != nil {
		progress(DownloadProgress{Received: offset, Total: total})
	}
	var body io.Reader = resp.Body
	if total >= 0 && total < math.MaxInt64 {
		body = io.LimitReader(body, total-offset+1)
	}
	var sink io.Writer = io.MultiWriter(writer, originalHash)
	if expectedMD5 != "" || contentMD5 != "" {
		sink = io.MultiWriter(writer, originalHash, checksum)
	}
	if offset > 0 {
		if _, e = io.CopyN(io.MultiWriter(originalHash, checksum), tmp, offset); e != nil {
			return fail("续传临时文件不可读，请重试")
		}
		writer.received = offset
	}
	if resumePath != "" && identity != "" {
		lastCheckpoint := time.Now()
		sink = &checkpointSink{writer: sink, after: func() {
			if time.Since(lastCheckpoint) > 2*time.Second {
				_ = saveResumeStamp(tmp, tempPath, identity, hex.EncodeToString(originalHash.Sum(nil)), writer.received)
				lastCheckpoint = time.Now()
			}
		}}
	}
	written, e := io.CopyBuffer(sink, body, make([]byte, 128*1024))
	written += offset
	if resumePath != "" && (e != nil || (total > 0 && written < total)) {
		keepPartial = saveResumeStamp(tmp, tempPath, identity, hex.EncodeToString(originalHash.Sum(nil)), written)
	}
	if e != nil {
		if ctx.Err() != nil {
			return DownloadResult{}, ctx.Err()
		}
		return fail("下载中断或磁盘写入失败，未保存正式文件")
	}
	if total >= 0 && written != total {
		return fail("下载长度与文件大小不一致，未保存正式文件")
	}
	actualMD5 := hex.EncodeToString(checksum.Sum(nil))
	actualSHA := hex.EncodeToString(originalHash.Sum(nil))
	if expectedSHA != "" && actualSHA != expectedSHA {
		return fail("原件 SHA256 校验失败：下载内容与本机上传记录不同，云端文件可能已变化；未保存正式文件")
	}
	if contentMD5 != "" && !strings.EqualFold(actualMD5, contentMD5) {
		return fail("下载内容与 Content-MD5 不一致，未保存正式文件，请刷新后重试")
	}
	warning := "下载完成，未验证原件完整性。"
	verification, details := "unverified", "没有可用的原件或可信内容摘要；大小符合不代表完整性已验证。"
	if expectedSHA != "" {
		verification, warning, details = "verified", "", "与本机上传原件 SHA256 一致："+actualSHA
	} else if contentMD5 != "" || (a.Kind == "pan123" && expectedMD5 != "") {
		verification, warning, details = "verified", "", "下载内容与网盘提供的内容 MD5 一致。"
	}
	if expectedMD5 != "" && !strings.EqualFold(actualMD5, expectedMD5) {
		if a.Kind != "quark" || (total < 0 && expectedSHA == "" && contentMD5 == "") {
			return fail("下载文件MD5不一致，未保存正式文件，请刷新后重试")
		}
		details += fmt.Sprintf(" 夸克接口参考 MD5 与下载内容不同，该参考值不用于判定原件一致性。实际 MD5：%s；接口参考：%s", actualMD5, expectedMD5)
	}
	if e = ctx.Err(); e != nil {
		return DownloadResult{}, e
	}
	if e = tmp.Sync(); e != nil {
		return fail("下载写入磁盘失败，未保存正式文件")
	}
	if e = tmp.Close(); e != nil {
		return fail("下载文件关闭失败，未保存正式文件")
	}
	if e = ctx.Err(); e != nil {
		return DownloadResult{}, e
	}
	if e = commitNewDownload(tempPath, destination); e != nil {
		return fail("无法保存下载文件，目标可能已存在或不可写；未覆盖本机文件")
	}
	if progress != nil {
		progress(DownloadProgress{Received: written, Total: total})
	}
	return DownloadResult{Path: destination, Bytes: written, Warning: warning, Verification: verification, Details: details}, nil
}

type downloadWriter struct {
	ctx             context.Context
	file            *os.File
	received, total int64
	last            time.Time
	progress        func(DownloadProgress)
}

func (w *downloadWriter) Write(p []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := w.file.Write(p)
	w.received += int64(n)
	if w.progress != nil && time.Since(w.last) >= 150*time.Millisecond {
		w.progress(DownloadProgress{Received: w.received, Total: w.total})
		w.last = time.Now()
	}
	return n, e
}

func openDAVDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, error) {
	p, e := ListAccountFiles(ctx, client, a, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, e
	}
	var source CloudEntry
	count := 0
	for _, entry := range p.Entries {
		if entry.ID == in.EntryID && !entry.Folder && entry.Name == in.Name {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, errors.New("文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	base, _ := url.Parse(a.Endpoint)
	base.Path = strings.TrimRight(base.Path, "/") + source.ID
	base.RawPath = ""
	req, e := http.NewRequestWithContext(ctx, "GET", base.String(), nil)
	if e != nil {
		return CloudEntry{}, nil, errors.New("下载地址无效")
	}
	req.SetBasicAuth(a.Username, a.Secret)
	req.Header.Set("Accept-Encoding", "identity")
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return CloudEntry{}, nil, ctx.Err()
		}
		return CloudEntry{}, nil, errors.New("下载连接失败，请检查网络后重试")
	}

	return source, resp, nil
}
