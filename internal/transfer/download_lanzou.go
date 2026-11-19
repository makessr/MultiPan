package transfer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

func lzDownloadError() error {
	return errors.New("蓝奏下载解析未完成：页面可能已变化或需要验证，请在官网确认后重试 [LZ-DOWNLOAD/0.26.0]")
}

// Keep transport causes available for bounded DNS failover without exposing URLs
// (including share tokens) in the user-facing error.
type lzTransportError struct {
	message string
	cause   error
}

func (e *lzTransportError) Error() string { return e.message }
func (e *lzTransportError) Unwrap() error { return e.cause }
func lzPublic(ctx context.Context, client *http.Client, a Account, raw, referer string, form url.Values) (*http.Response, *url.URL, error) {
	u, e := safePublicDownloadURL(raw, nil)
	if e != nil {
		return nil, nil, lzDownloadError()
	}
	public := *client
	public.Jar = nil
	public.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		if seen[u.String()] {
			return nil, nil, lzDownloadError()
		}
		seen[u.String()] = true
		method := "GET"
		var body io.Reader
		if form != nil {
			method = "POST"
			body = strings.NewReader(form.Encode())
		}
		req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
		if e != nil {
			return nil, nil, lzDownloadError()
		}
		req.Header.Set("User-Agent", userAgent(a))
		req.Header.Set("Referer", referer)
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		req.Header.Set("Accept-Encoding", "identity")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, e := public.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			kind := "connection"
			var timeout net.Error
			var dns *net.DNSError
			if errors.As(e, &timeout) && timeout.Timeout() {
				kind = "timeout"
			} else if errors.As(e, &dns) {
				kind = "dns"
			}
			return nil, nil, &lzTransportError{message: fmt.Sprintf("蓝奏下载网络请求失败；请检查网络后重试 [LZ-NET/0.32.1] request=%s network=%s host=%s", method, kind, u.Hostname()), cause: e}
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			next := res.Header.Get("Location")
			res.Body.Close()
			if form != nil {
				return nil, nil, fmt.Errorf("%w request=POST HTTP=%d redirect=true", lzDownloadError(), res.StatusCode)
			}
			u, e = safePublicDownloadURL(next, u)
			if e != nil {
				return nil, nil, lzDownloadError()
			}
			continue
		}
		return res, u, nil
	}
	return nil, nil, lzDownloadError()
}
func lzHTML(res *http.Response) (string, error) {
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || res.StatusCode != 200 || len(b) > 2<<20 {
		return "", fmt.Errorf("%w HTTP=%d read-error=%t oversized=%t", lzDownloadError(), res.StatusCode, e != nil, len(b) > 2<<20)
	}
	return string(b), nil
}
func lzSign(page string) (string, error) {
	re := regexp.MustCompile(`(?i)["']?sign["']?\s*:\s*(?:["']([^"'\r\n]+)["']|([a-zA-Z_$][\w$]*))`)
	m := re.FindStringSubmatch(page)
	if len(m) > 0 {
		if m[1] != "" {
			return m[1], nil
		}
		v := regexp.MustCompile(`(?:var\s+)?` + regexp.QuoteMeta(m[2]) + `\s*=\s*["']([^"'\r\n]+)["']`).FindStringSubmatch(page)
		if len(v) > 1 {
			return v[1], nil
		}
	}
	q := regexp.MustCompile(`sign=([\w-]+)&`).FindStringSubmatch(page)
	if len(q) > 1 {
		return q[1], nil
	}
	return "", lzDownloadError()
}
func openLanzouDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (out CloudEntry, response *http.Response, checksum string, failure error) {
	stage := "source"
	defer func() {
		if failure != nil {
			failure = fmt.Errorf("%w [LZ-DIAG/0.26.2] stage=%s", failure, stage)
		}
	}()
	if a.Endpoint != "https://up.woozooo.com" {
		return CloudEntry{}, nil, "", lzDownloadError()
	}
	entries, e := listLanzouFiles(ctx, client, a, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source CloudEntry
	count := 0
	for _, entry := range entries {
		if entry.ID == in.EntryID && entry.Name == in.Name && !entry.Folder {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("蓝奏文件已变化或不在当前目录，请刷新后下载")
	}
	stage = "share-info"
	var detail struct {
		Info struct {
			Domain   string          `json:"is_newd"`
			ID       string          `json:"f_id"`
			On       json.RawMessage `json:"onof"`
			Password string          `json:"pwd"`
		} `json:"info"`
	}
	if e = lanzouEditRequest(ctx, client, a, url.Values{"task": {"22"}, "file_id": {source.ID}}, &detail); e != nil {
		return CloudEntry{}, nil, "", e
	}
	d := detail.Info
	if d.ID == "" || d.ID == "i" {
		return CloudEntry{}, nil, "", errors.New("蓝奏未返回有效分享地址；该文件可能需先在管理页生成分享链接后重试 [LZ-SHARE/0.32.0]")
	}
	if strings.ContainsAny(d.ID, "/?#\\") {
		return CloudEntry{}, nil, "", lzDownloadError()
	}
	share, e := safePublicDownloadURL(strings.TrimRight(d.Domain, "/")+"/"+d.ID, nil)
	if e != nil {
		return CloudEntry{}, nil, "", lzDownloadError()
	}
	stage = "share-page"
	res, base, e := lzPublic(ctx, client, a, share.String(), "", nil)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	page, e := lzHTML(res)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	stage = fmt.Sprintf("share-parse(iframe=%t,sign=%t,redirect=%t)", strings.Contains(strings.ToLower(page), "<iframe"), strings.Contains(page, "sign"), strings.Contains(page, "location"))
	pwd := strings.Trim(string(d.On), "\"") == "1"
	if !pwd {
		frame := iframePattern.FindStringSubmatch(page)
		if len(frame) < 2 {
			return CloudEntry{}, nil, "", lzDownloadError()
		}
		u, e := safePublicDownloadURL(html.UnescapeString(frame[1]), base)
		if e != nil {
			return CloudEntry{}, nil, "", lzDownloadError()
		}
		stage = "iframe-page"
		res, base, e = lzPublic(ctx, client, a, u.String(), base.String(), nil)
		if e != nil {
			return CloudEntry{}, nil, "", e
		}
		page, e = lzHTML(res)
		if e != nil {
			return CloudEntry{}, nil, "", e
		}
	}
	stage = fmt.Sprintf("sign-parse(sign=%t,downprocess=%t,redirect=%t)", strings.Contains(page, "sign"), strings.Contains(page, "downprocess"), strings.Contains(page, "location"))
	sign, e := lzSign(page)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	form := url.Values{"action": {"downprocess"}, "sign": {sign}, "ves": {"1"}}
	if pwd {
		form.Set("p", d.Password)
	}
	for _, field := range []string{"ajaxdata", "websign", "websignkey"} {
		m := regexp.MustCompile(`(?:var\s+)?` + field + `\s*=\s*["']([^"']*)["']`).FindStringSubmatch(page)
		if len(m) > 1 {
			key := field
			if field == "ajaxdata" {
				key = "signs"
			}
			form.Set(key, m[1])
		}
	}
	ajax := base.ResolveReference(&url.URL{Path: "/ajaxm.php"})
	m := regexp.MustCompile(`["']?url["']?\s*:\s*["']([^"']*ajaxm\.php[^"']*)["']`).FindStringSubmatch(page)
	if len(m) > 1 {
		u, e := safePublicDownloadURL(html.UnescapeString(m[1]), base)
		if e != nil || u.Host != base.Host {
			return CloudEntry{}, nil, "", lzDownloadError()
		}
		ajax = u
	}
	modernAjax := regexp.MustCompile(`["']?url["']?\s*:\s*dom_ajaxs`).MatchString(page)
	if modernAjax {
		ajax, e = lzModernAjax(page, base, form)
		if e != nil {
			return CloudEntry{}, nil, "", e
		}
	}
	stage = "ajax-response"
	res, _, e = lzPublic(ctx, client, a, ajax.String(), base.String(), form)
	if modernAjax && e != nil {
		var dns *net.DNSError
		// DNS failure occurs before the POST reaches a server. Never replay
		// business failures, timeouts, challenges or canceled requests.
		if errors.As(e, &dns) && !dns.IsTimeout && ctx.Err() == nil {
			alternate, parseErr := lzModernEndpoint(page, base, "domain2")
			if ajax.Host == "apifile.lanzouw.com" {
				alternate, parseErr = lzModernEndpoint(page, base, "domain1")
			}
			if parseErr == nil && alternate.Host != ajax.Host && alternate.RawQuery == ajax.RawQuery {
				res, _, e = lzPublic(ctx, client, a, alternate.String(), base.String(), form)
			}
		}
	}
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	raw, e := lzHTML(res)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var result struct {
		ZT     json.RawMessage `json:"zt"`
		Domain string          `json:"dom"`
		URL    string          `json:"url"`
	}
	decodeErr := json.Unmarshal([]byte(raw), &result)
	stage = "ajax-parse(zt=" + safeCode(strings.Trim(string(result.ZT), "\"")) + ")"
	if decodeErr != nil || strings.Trim(string(result.ZT), "\"") != "1" || result.URL == "" || result.URL == "0" {
		return CloudEntry{}, nil, "", lzDownloadError()
	}
	if modernAjax && form.Get("kd") == "0" {
		result.Domain = "https://developer2oss.lanzouc.com:661"
	}
	stage = "content-request"
	res, _, e = lzPublic(ctx, client, a, strings.TrimRight(result.Domain, "/")+"/file/"+result.URL, base.String(), nil)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	stage = fmt.Sprintf("content-check(HTTP=%d)", res.StatusCode)
	reader := bufio.NewReader(res.Body)
	prefix, _ := reader.Peek(512)
	sniff := http.DetectContentType(prefix)
	res.Body = &downloadReadCloser{Reader: reader, Closer: res.Body}
	ext := strings.ToLower(filepath.Ext(source.Name))
	if (strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/html") || strings.Contains(sniff, "text/html")) && ext != ".html" && ext != ".htm" {
		res.Body.Close()
		return CloudEntry{}, nil, "", lzDownloadError()
	}
	return source, res, "", nil
}

// Support the current share-page endpoint selector without executing its JS.
func lzModernAjax(page string, base *url.URL, form url.Values) (*url.URL, error) {
	// The official page selects domain1 when killdnsweb is available.
	// Prefer that advertised primary; older pages may only supply domain2.
	field := "domain2"
	if regexp.MustCompile(`var\s+domain1\s*=`).MatchString(page) {
		field = "domain1"
	}
	endpoint, e := lzModernEndpoint(page, base, field)
	if e != nil {
		return nil, e
	}
	value := regexp.MustCompile(`var\s+ajaxdata\s*=\s*['"]([^'"\r\n]*)['"]`).FindStringSubmatch(page)
	if len(value) < 2 {
		return nil, lzDownloadError()
	}
	form.Set("signs", value[1])
	form.Set("websignkey", value[1])
	form.Set("websign", "")
	form.Set("kd", "0")
	return endpoint, nil
}
func lzModernEndpoint(page string, base *url.URL, field string) (*url.URL, error) {
	match := regexp.MustCompile(`var\s+` + regexp.QuoteMeta(field) + `\s*=\s*['"]([^'"\r\n]+)['"]`).FindStringSubmatch(page)
	if len(match) < 2 {
		return nil, lzDownloadError()
	}
	endpoint, e := safePublicDownloadURL(html.UnescapeString(match[1]), base)
	if e != nil || (endpoint.Host != "apifile.lanzouw.com" && endpoint.Host != "apifile.woozooo.com") || endpoint.Path != "/ajaxfile.php" {
		return nil, lzDownloadError()
	}
	return endpoint, nil
}
