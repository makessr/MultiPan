package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func open123Download(ctx context.Context, client *http.Client, a Account, in DownloadInput) (CloudEntry, *http.Response, string, error) {
	entries, e := (pan123Client{client, a}).list(ctx, in.ParentID)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	var source pan123Entry
	count := 0
	for _, entry := range entries {
		if string(entry.ID) == in.EntryID && entry.Name == in.Name && entry.Type == 0 && entry.Size >= 0 {
			source = entry
			count++
		}
	}
	if count != 1 {
		return CloudEntry{}, nil, "", errors.New("123文件已变化、不在当前目录或为文件夹，请刷新后下载")
	}
	var result struct {
		URL string `json:"DownloadUrl"`
	}
	e = (pan123Client{client, a}).request(ctx, "POST", "/file/download_info", nil, map[string]any{"driveId": 0, "etag": source.MD5, "fileId": source.ID, "fileName": source.Name, "s3keyFlag": source.S3KeyFlag, "size": source.Size, "type": source.Type}, &result)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	u, e := safe123DownloadURL(result.URL, nil)
	if e != nil {
		return CloudEntry{}, nil, "", e
	}
	if params := u.Query().Get("params"); params != "" {
		if len(params) > 65536 {
			return CloudEntry{}, nil, "", errors.New("123下载参数过长")
		}
		var raw []byte
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			raw, e = encoding.DecodeString(strings.ReplaceAll(params, " ", "+"))
			if e == nil {
				break
			}
		}
		if e != nil {
			return CloudEntry{}, nil, "", errors.New("123下载地址参数无法解析")
		}
		u, e = safe123DownloadURL(string(raw), nil)
		if e != nil {
			return CloudEntry{}, nil, "", e
		}
	}
	// Fresh requests only: API bearer tokens/cookies are never sent to the
	// delegated resolver or CDN, and their cookie jar is deliberately disabled.
	public := *client
	public.Jar = nil
	public.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	referer := "https://yun.123pan.cn/"
	for hop := 0; hop < 6; hop++ {
		if seen[u.String()] {
			return CloudEntry{}, nil, "", errors.New("123下载地址循环，未保存文件")
		}
		seen[u.String()] = true
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		req.Header.Set("Referer", referer)
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := public.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return CloudEntry{}, nil, "", ctx.Err()
			}
			return CloudEntry{}, nil, "", errors.New("123下载节点连接失败，请稍后重试")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next := resp.Header.Get("Location")
			resp.Body.Close()
			if next == "" {
				return CloudEntry{}, nil, "", errors.New("123下载跳转地址为空")
			}
			referer = u.Scheme + "://" + u.Host + "/"
			u, e = safe123DownloadURL(next, u)
			if e != nil {
				return CloudEntry{}, nil, "", e
			}
			continue
		}
		// 123 resolvers can return a nonstandard successful status such as
		// 210. Parse its control body before deciding whether it is file data.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return CloudEntry{ID: string(source.ID), Name: source.Name, Size: &source.Size, revision: source.MD5}, resp, "", nil
		}
		reader := bufio.NewReader(resp.Body)
		peek, _ := reader.Peek(64)
		peek = bytes.TrimSpace(peek)
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json") || (len(peek) > 0 && peek[0] == '{') {
			probe, err := io.ReadAll(io.LimitReader(reader, 65537))
			if err != nil {
				resp.Body.Close()
				if ctx.Err() != nil {
					return CloudEntry{}, nil, "", ctx.Err()
				}
				return CloudEntry{}, nil, "", errors.New("123下载解析响应中断")
			}
			// An actual JSON file, even one containing redirect_url, must not be
			// interpreted as a control response when its file length matches.
			if len(probe) <= 65536 && int64(len(probe)) != source.Size {
				var parsed struct {
					Code *int `json:"code"`
					Data struct {
						URL string `json:"redirect_url"`
					} `json:"data"`
				}
				if json.Unmarshal(probe, &parsed) == nil && parsed.Data.URL != "" && (parsed.Code == nil || *parsed.Code == 0) {
					resp.Body.Close()
					referer = u.Scheme + "://" + u.Host + "/"
					u, e = safe123DownloadURL(parsed.Data.URL, u)
					if e != nil {
						return CloudEntry{}, nil, "", e
					}
					continue
				}
			}
			resp.Body = &downloadReadCloser{Reader: io.MultiReader(bytes.NewReader(probe), reader), Closer: resp.Body}
		} else {
			resp.Body = &downloadReadCloser{Reader: reader, Closer: resp.Body}
		}
		checksum := ""
		if digest, err := hex.DecodeString(source.MD5); err == nil && len(digest) == 16 {
			checksum = source.MD5
		}
		return CloudEntry{ID: string(source.ID), Name: source.Name, Size: &source.Size, revision: source.MD5}, resp, checksum, nil
	}
	return CloudEntry{}, nil, "", errors.New("123下载跳转次数过多，未保存文件")
}

type downloadReadCloser struct {
	io.Reader
	io.Closer
}

func safe123DownloadURL(raw string, base *url.URL) (*url.URL, error) {
	u, e := safePublicDownloadURL(raw, base)
	if e != nil {
		return nil, errors.New("123返回的下载地址无效或不支持安全连接")
	}
	return u, nil
}
