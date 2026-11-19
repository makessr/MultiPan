package transfer

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func quarkDownloadCookieScope(u *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && (host == "quark.cn" || strings.HasSuffix(host, ".quark.cn"))
}

func openQuarkDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, string, error) {
	if a.Endpoint != QuarkAPI {
		return CloudEntry{}, nil, "", errors.New("夸克下载接口来源无效 [QK-DOWNLOAD/0.23.0]")
	}
	c := newQuarkClient(client, a)
	entries, e := c.list(ctx, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source quarkEntry
	count := 0
	for _, entry := range entries {
		if entry.FID == in.EntryID && entry.Name == in.Name && !entry.isDir() && (entry.Size == nil || *entry.Size >= 0) {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("夸克文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	type downloadInfo struct {
		FID  string `json:"fid"`
		Name string `json:"file_name"`
		Size *int64 `json:"size"`
		URL  string `json:"download_url"`
		MD5  string `json:"md5"`
	}
	var results []downloadInfo
	if _, e = c.request(ctx, "POST", "/file/download", nil, map[string]any{"fids": []string{source.FID}}, &results); e != nil {
		return CloudEntry{}, nil, "", e
	}
	var result downloadInfo
	count = 0
	for _, item := range results {
		if item.FID == source.FID || (item.FID == "" && len(results) == 1) {
			result = item
			count++
		}
	}
	if count != 1 || strings.TrimSpace(result.URL) == "" {
		return CloudEntry{}, nil, "", errors.New("夸克未返回当前文件的下载地址 [QK-DOWNLOAD/0.23.0]")
	}
	if (result.Name != "" && result.Name != source.Name) || (result.Size != nil && (*result.Size < 0 || (source.Size != nil && *result.Size != *source.Size))) {
		return CloudEntry{}, nil, "", errors.New("夸克文件信息已变化，请刷新后下载")
	}
	if source.Size == nil {
		source.Size = result.Size
	}
	u, e := safePublicDownloadURL(strings.TrimSpace(result.URL), nil)
	if e != nil {
		return CloudEntry{}, nil, "", errors.New("夸克下载地址无效或不支持安全连接 [QK-DOWNLOAD/0.23.0]")
	}
	checksum := ""
	if digest, err := hex.DecodeString(result.MD5); err == nil && len(digest) == 16 {
		checksum = result.MD5
	}
	content := *client
	content.Jar = nil
	content.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	for hop := 0; hop < 6; hop++ {
		if seen[u.String()] {
			return CloudEntry{}, nil, "", errors.New("夸克下载地址循环，未保存文件 [QK-DOWNLOAD/0.23.0]")
		}
		seen[u.String()] = true
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return CloudEntry{}, nil, "", errors.New("夸克下载地址无法读取 [QK-DOWNLOAD/0.23.0]")
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Referer", "https://pan.quark.cn/")
		req.Header.Set("User-Agent", userAgent(a))
		trusted := quarkDownloadCookieScope(u)
		if trusted {
			req.Header.Set("Cookie", c.cookie)
		}
		resp, err := content.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return CloudEntry{}, nil, "", ctx.Err()
			}
			return CloudEntry{}, nil, "", errors.New("夸克下载节点连接失败，请稍后重试 [QK-DOWNLOAD/0.23.0]")
		}
		if trusted {
			c.cookie = mergeCookies(c.cookie, resp.Cookies())
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next := resp.Header.Get("Location")
			resp.Body.Close()
			if next == "" {
				return CloudEntry{}, nil, "", errors.New("夸克下载跳转地址为空 [QK-DOWNLOAD/0.23.0]")
			}
			u, e = safePublicDownloadURL(next, u)
			if e != nil {
				return CloudEntry{}, nil, "", errors.New("夸克下载跳转地址无效 [QK-DOWNLOAD/0.23.0]")
			}
			continue
		}
		return CloudEntry{ID: source.FID, Name: source.Name, Size: source.Size, revision: string(source.UpdatedAt)}, resp, checksum, nil
	}
	return CloudEntry{}, nil, "", errors.New("夸克下载跳转次数过多，未保存文件 [QK-DOWNLOAD/0.23.0]")
}
