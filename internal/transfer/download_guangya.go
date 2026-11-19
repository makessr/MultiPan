package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

func readGuangyaFileHash(ctx context.Context, client *http.Client, a Account, parent string, entry CloudEntry, progress func(int64)) (string, error) {
	if entry.Size == nil || *entry.Size < 0 {
		return "", errors.New("光鸭文件缺少可核对的大小")
	}
	row, resp, _, err := openGuangyaDownload(ctx, client, a, DownloadInput{
		AccountID: a.ID, ParentID: parent, EntryID: entry.ID, Name: entry.Name,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	encoding := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != http.StatusOK || resp.Uncompressed || (encoding != "" && !strings.EqualFold(encoding, "identity")) ||
		resp.Header.Get("Content-Range") != "" || row.Size == nil || *row.Size != *entry.Size ||
		(resp.ContentLength >= 0 && resp.ContentLength != *entry.Size) {
		return "", errors.New("光鸭未返回与文件大小一致的完整内容")
	}
	digest, err := relayHash(ctx, resp.Body, *entry.Size, progress)
	if err != nil {
		return "", errors.New("光鸭文件读取不完整，无法核对 SHA256")
	}
	return digest, nil
}

func openGuangyaDownload(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, string, error) {
	c := gyClient{client, a}
	entries, e := c.list(ctx, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source gyEntry
	count := 0
	for _, entry := range entries {
		if entry.ID == in.EntryID && entry.Name == in.Name && entry.Type == 1 && entry.Size >= 0 {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("光鸭文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	var result struct {
		SignedURL   string `json:"signedURL"`
		DownloadURL string `json:"downloadUrl"`
	}
	if _, e = c.call(ctx, "/userres/v1/get_res_download_url", map[string]any{"fileId": source.ID}, &result); e != nil {
		return CloudEntry{}, nil, "", e
	}
	raw := strings.TrimSpace(result.SignedURL)
	if raw == "" {
		raw = strings.TrimSpace(result.DownloadURL)
	}
	if raw == "" {
		return CloudEntry{}, nil, "", errors.New("光鸭未返回文件下载地址 [GY-DOWNLOAD/0.22.0]")
	}
	u, e := safePublicDownloadURL(raw, nil)
	if e != nil {
		return CloudEntry{}, nil, "", errors.New("光鸭下载地址无效或不支持安全连接 [GY-DOWNLOAD/0.22.0]")
	}
	public := *client
	public.Jar = nil
	public.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	referer := "https://www.guangyapan.com/"
	for hop := 0; hop < 6; hop++ {
		if seen[u.String()] {
			return CloudEntry{}, nil, "", errors.New("光鸭下载地址循环，未保存文件 [GY-DOWNLOAD/0.22.0]")
		}
		seen[u.String()] = true
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return CloudEntry{}, nil, "", errors.New("光鸭下载地址无法读取 [GY-DOWNLOAD/0.22.0]")
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Referer", referer)
		req.Header.Set("User-Agent", userAgent(a))
		resp, err := public.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return CloudEntry{}, nil, "", ctx.Err()
			}
			return CloudEntry{}, nil, "", errors.New("光鸭下载节点连接失败，请稍后重试 [GY-DOWNLOAD/0.22.0]")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next := resp.Header.Get("Location")
			resp.Body.Close()
			if next == "" {
				return CloudEntry{}, nil, "", errors.New("光鸭下载跳转地址为空 [GY-DOWNLOAD/0.22.0]")
			}
			referer = u.Scheme + "://" + u.Host + "/"
			u, e = safePublicDownloadURL(next, u)
			if e != nil {
				return CloudEntry{}, nil, "", errors.New("光鸭下载跳转地址无效 [GY-DOWNLOAD/0.22.0]")
			}
			continue
		}
		return CloudEntry{ID: source.ID, Name: source.Name, Size: &source.Size}, resp, "", nil
	}
	return CloudEntry{}, nil, "", errors.New("光鸭下载跳转次数过多，未保存文件 [GY-DOWNLOAD/0.22.0]")
}
