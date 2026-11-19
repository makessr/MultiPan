package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type resumePathKey struct{}

func withResumePath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, resumePathKey{}, path)
}

type resumeStamp struct {
	Identity  string
	PrefixSHA string
	Bytes     int64
}

func resumeIdentity(a Account, in DownloadInput, source CloudEntry, resp *http.Response, sha, md5, contentMD5 string) string {
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	if a.Kind == "quark" {
		md5 = ""
	}
	if etag == "" && sha == "" && md5 == "" && contentMD5 == "" {
		return ""
	}
	raw, _ := json.Marshal([]any{downloadScope(a), in, source.ID, source.Name, source.Size, etag, sha, md5, contentMD5})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func openResumeFile(path, identity string, ctx context.Context, client *http.Client, resp *http.Response, total int64) (*os.File, *http.Response, int64, error) {
	if identity != "" && total > 0 {
		raw, e := os.ReadFile(path + ".resume")
		var stamp resumeStamp
		if e == nil && len(raw) < 4096 && json.Unmarshal(raw, &stamp) == nil && stamp.Identity == identity && stamp.Bytes > 0 && stamp.Bytes < total {
			info, e := os.Lstat(path)
			if e == nil && info.Mode().IsRegular() && info.Size() >= stamp.Bytes {
				f, e := os.OpenFile(path, os.O_RDWR, 0600)
				if e == nil {
					hash := sha256.New()
					_, e = io.CopyN(hash, f, stamp.Bytes)
					if e == nil && hex.EncodeToString(hash.Sum(nil)) == stamp.PrefixSHA {
						if e = f.Truncate(stamp.Bytes); e != nil {
							f.Close()
							return nil, resp, 0, e
						}
						req := resp.Request.Clone(ctx)
						req.Header = req.Header.Clone()
						req.Header.Set("Range", fmt.Sprintf("bytes=%d-", stamp.Bytes))
						if tag := resp.Header.Get("ETag"); tag != "" && !strings.HasPrefix(tag, "W/") {
							req.Header.Set("If-Range", tag)
						}
						ranged, err := client.Do(req)
						if ctx.Err() != nil {
							f.Close()
							if ranged != nil {
								ranged.Body.Close()
							}
							return nil, resp, 0, ctx.Err()
						}
						if err == nil {
							exact := fmt.Sprintf("bytes %d-%d/%d", stamp.Bytes, total-1, total)
							if ranged.StatusCode == 206 && ranged.Header.Get("Content-Range") == exact && ranged.ContentLength == total-stamp.Bytes && (ranged.Header.Get("Content-Encoding") == "" || ranged.Header.Get("Content-Encoding") == "identity") && (resp.Header.Get("ETag") == "" || ranged.Header.Get("ETag") == resp.Header.Get("ETag")) {
								resp.Body.Close()
								_, e = f.Seek(0, 0)
								if e == nil {
									return f, ranged, stamp.Bytes, nil
								}
							}
							ranged.Body.Close()
						}
					}
					f.Close()
				}
			}
		}
	}
	// Only the per-job reserved temporary path is discarded; final destinations are never overwritten.
	_ = os.Remove(path)
	_ = os.Remove(path + ".resume")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	return f, resp, 0, e
}
func saveResumeStamp(file *os.File, path, identity, prefixSHA string, bytes int64) bool {
	if identity == "" || bytes <= 0 || file.Sync() != nil {
		return false
	}
	raw, e := json.Marshal(resumeStamp{identity, prefixSHA, bytes})
	if e != nil {
		return false
	}
	return os.WriteFile(path+".resume", raw, 0600) == nil
}

type checkpointSink struct {
	writer io.Writer
	after  func()
}

func (s *checkpointSink) Write(p []byte) (int, error) {
	n, e := s.writer.Write(p)
	if n > 0 {
		s.after()
	}
	return n, e
}
