package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func baiduDownloadCookieScope(u *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && (host == "baidu.com" || strings.HasSuffix(host, ".baidu.com") || host == "baidupcs.com" || strings.HasSuffix(host, ".baidupcs.com"))
}
func openBaiduDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, string, error) {
	c := newBaiduClient(client, a)
	entries, e := c.list(ctx, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source baiduEntry
	count := 0
	for _, entry := range entries {
		if entry.Path == in.EntryID && entry.Name == in.Name && entry.Dir != nil && *entry.Dir == 0 && entry.Size != nil && *entry.Size >= 0 && entry.ID != "" {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("百度文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	var result struct {
		Info []struct {
			Path string      `json:"path"`
			ID   json.Number `json:"fs_id"`
			Size *int64      `json:"size"`
			URL  string      `json:"dlink"`
		} `json:"info"`
	}
	target, _ := json.Marshal([]string{source.Path})
	if e = c.api(ctx, "GET", "filemetas", url.Values{"target": {string(target)}, "dlink": {"1"}, "web": {"5"}, "origin": {"dlna"}}, nil, &result); e != nil {
		return CloudEntry{}, nil, "", e
	}
	if len(result.Info) != 1 {
		return CloudEntry{}, nil, "", errors.New("百度未返回唯一下载信息 [BD-DOWNLOAD/0.25.0]")
	}
	info := result.Info[0]
	if (info.Path != "" && info.Path != source.Path) || (info.ID != "" && info.ID != source.ID) || (info.Size != nil && *info.Size != *source.Size) {
		return CloudEntry{}, nil, "", errors.New("百度下载信息与源文件不符，请刷新后重试")
	}
	u, e := safePublicDownloadURL(info.URL, nil)
	if e != nil {
		return CloudEntry{}, nil, "", errors.New("百度下载地址无效 [BD-DOWNLOAD/0.25.0]")
	}
	sourceEntry := CloudEntry{ID: source.Path, Name: source.Name, Size: source.Size, revision: string(source.ID) + ":" + source.MD5}
	entry, resp, hash, downloadErr := openBaiduContent(ctx, client, a, c, sourceEntry, u)
	var denied *baiduContentDenied
	if errors.As(downloadErr, &denied) && (strings.Contains(denied.diagnostic, "code=31329 ") || strings.Contains(denied.diagnostic, "code=31119 ")) {
		webURL, err := c.webDownloadLink(ctx, source.ID)
		if err != nil {
			return CloudEntry{}, nil, "", fmt.Errorf("百度DLNA下载被拒绝，网页登录下载阶段失败：%w", err)
		}
		return openBaiduContent(ctx, client, a, c, sourceEntry, webURL)
	}
	return entry, resp, hash, downloadErr
}
func openBaiduContent(ctx context.Context, client *http.Client, a Account, c *baiduClient, source CloudEntry, u *url.URL) (CloudEntry, *http.Response, string, error) {
	var e error
	content := *client
	content.Jar = nil
	content.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	for hop := 0; hop < 6; hop++ {
		if seen[u.String()] {
			return CloudEntry{}, nil, "", errors.New("百度下载地址循环，未保存文件 [BD-DOWNLOAD/0.25.0]")
		}
		seen[u.String()] = true
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return CloudEntry{}, nil, "", errors.New("百度下载地址无法读取 [BD-DOWNLOAD/0.25.0]")
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Referer", "https://pan.baidu.com/disk/main")
		// DLNA dlinks require a download client agent, independent of the web login.
		req.Header.Set("User-Agent", "netdisk")
		trusted := baiduDownloadCookieScope(u)
		if trusted {
			req.Header.Set("Cookie", c.cookie)
		}
		resp, err := content.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return CloudEntry{}, nil, "", ctx.Err()
			}
			return CloudEntry{}, nil, "", errors.New("百度下载节点连接失败，请稍后重试 [BD-DOWNLOAD/0.25.0]")
		}
		if trusted {
			c.cookie = mergeCookies(c.cookie, resp.Cookies())
		}
		if resp.StatusCode == http.StatusForbidden {
			diagnostic := baiduForbiddenDiagnostic(resp)
			resp.Body.Close()
			return CloudEntry{}, nil, "", &baiduContentDenied{diagnostic: diagnostic}
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next := resp.Header.Get("Location")
			resp.Body.Close()
			if next == "" {
				return CloudEntry{}, nil, "", errors.New("百度下载跳转地址为空 [BD-DOWNLOAD/0.25.0]")
			}
			u, e = safePublicDownloadURL(next, u)
			if e != nil {
				return CloudEntry{}, nil, "", errors.New("百度下载跳转地址无效 [BD-DOWNLOAD/0.25.0]")
			}
			continue
		}
		return source, resp, "", nil
	}
	return CloudEntry{}, nil, "", errors.New("百度下载跳转次数过多，未保存文件 [BD-DOWNLOAD/0.25.0]")
}

// Only fixed categories and numeric codes are exposed, never raw error text.
func baiduForbiddenDiagnostic(resp *http.Response) string {
	var body struct {
		Code    json.RawMessage `json:"error_code"`
		Errno   json.RawMessage `json:"errno"`
		Message string          `json:"error_msg"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	kind := "other"
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		kind = "html"
	}
	code := "unknown"
	reason := "unclassified"
	if json.Unmarshal(raw, &body) == nil {
		kind = "json"
		if len(body.Code) > 0 {
			code = safeCode(strings.Trim(string(body.Code), "\""))
		} else if len(body.Errno) > 0 {
			code = safeCode(strings.Trim(string(body.Errno), "\""))
		}
		msg := strings.ToLower(body.Message)
		switch {
		case strings.Contains(msg, "black userlist"):
			reason = "account-policy"
		case strings.Contains(msg, "dlna"):
			reason = "dlna-policy"
		case strings.Contains(msg, "expire"):
			reason = "expired-link"
		case strings.Contains(msg, "signature") || strings.Contains(msg, "sign error"):
			reason = "signature"
		case strings.Contains(msg, "auth"):
			reason = "authorization"
		}
	}
	return fmt.Sprintf("body=%s code=%s reason=%s", kind, code, reason)
}
