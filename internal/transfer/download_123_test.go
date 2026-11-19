package transfer

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test123DownloadResolutionAndIntegrity(t *testing.T) {
	for _, mode := range []string{"direct", "params", "raw-params", "redirect", "relative-redirect", "json-resolver", "plain-json-resolver", "actual-json", "large-json", "http-upgrade", "zero", "no-md5", "checksum-mismatch", "expired", "folder", "stale-name", "empty-url", "bad-params", "unsafe-url", "unsafe-redirect", "loop", "too-many", "resolver-error", "cancel", "resolver-210", "bytes-210", "json-file-210", "error-210", "bad-md5-210", "loop-210", "private-210", "html-210"} {
		t.Run(mode, func(t *testing.T) {
			const id = "9007199254740993"
			payload := "hello world!"
			if mode == "actual-json" || mode == "json-file-210" {
				payload = `{"code":0,"data":{"redirect_url":"https://unexpected.example/do-not-follow"}}`
			}
			if mode == "large-json" {
				payload = `{"data":"` + strings.Repeat("x", 70000) + `"}`
			}
			if mode == "zero" {
				payload = ""
			}
			digest := md5.Sum([]byte(payload))
			etag := hex.EncodeToString(digest[:])
			if mode == "no-md5" {
				etag = ""
			}
			root := t.TempDir()
			destination := filepath.Join(root, "中文 空格.txt")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			initial := "https://resolver.example/start?sig=url-secret"
			gets, infos := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if e := r.Context().Err(); e != nil {
					return nil, e
				}
				if r.URL.Host == "api.123278.com" {
					if r.Header.Get("Authorization") != "Bearer synthetic-token" {
						t.Error("missing API authentication")
					}
					if r.Method == "GET" {
						if mode == "expired" {
							res := quarkResponse("", nil)
							res.StatusCode = 401
							return res, nil
						}
						typ := 0
						if mode == "folder" {
							typ = 1
						}
						return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"Next": -1, "InfoList": []any{map[string]any{"FileId": json.Number(id), "FileName": "文件.txt", "Type": typ, "Size": len(payload), "Etag": etag, "S3KeyFlag": "opaque-key"}}}}), nil
					}
					infos++
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					decoder.Decode(&body)
					if r.URL.Path != "/b/api/file/download_info" || body["fileId"] != json.Number(id) || body["s3keyFlag"] != "opaque-key" || body["etag"] != etag || body["fileName"] != "文件.txt" {
						t.Error("wrong download-info metadata")
					}
					link := initial
					if mode == "params" || mode == "raw-params" {
						enc := base64.StdEncoding
						if mode == "raw-params" {
							enc = base64.RawURLEncoding
						}
						link = "https://wrapper.example/path?params=" + url.QueryEscape(enc.EncodeToString([]byte(initial)))
					}
					if mode == "empty-url" {
						link = ""
					}
					if mode == "bad-params" {
						link = "https://wrapper.example/path?params=not_base64!!!"
					}
					if mode == "unsafe-url" {
						link = "https://name:password@resolver.example/start"
					}
					if mode == "http-upgrade" {
						link = strings.Replace(initial, "https:", "http:", 1)
					}
					return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": link}}), nil
				}
				gets++
				if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || strings.Contains(r.Header.Get("Referer"), "url-secret") {
					t.Error("unsafe public request or credential forwarding")
				}
				if r.URL.Host != "resolver.example" && r.URL.Host != "cdn.example" {
					t.Error("unexpected delegated host")
				}
				if mode == "loop" || mode == "too-many" {
					res := quarkResponse("", nil)
					res.StatusCode = 302
					location := initial
					if mode == "too-many" {
						location = "https://resolver.example/" + strings.Repeat("a", gets)
					}
					res.Header.Set("Location", location)
					return res, nil
				}
				if gets == 1 && (mode == "redirect" || mode == "relative-redirect" || mode == "unsafe-redirect") {
					res := quarkResponse("", nil)
					res.StatusCode = 302
					location := "https://cdn.example/file"
					if mode == "relative-redirect" {
						location = "/file"
					}
					if mode == "unsafe-redirect" {
						location = "https://127.0.0.1/internal"
					}
					res.Header.Set("Location", location)
					return res, nil
				}
				if gets == 1 && (mode == "json-resolver" || mode == "plain-json-resolver" || mode == "resolver-error" || mode == "resolver-210" || mode == "error-210" || mode == "loop-210" || mode == "private-210") {
					code := 0
					if mode == "resolver-error" || mode == "error-210" {
						code = 403
					}
					next := "https://cdn.example/file?sig=url-secret"
					if mode == "loop-210" {
						next = initial
					}
					if mode == "private-210" {
						next = "https://127.0.0.1/private"
					}
					res := jsonResponse(map[string]any{"code": code, "data": map[string]any{"redirect_url": next}})
					if strings.HasSuffix(mode, "-210") {
						res.StatusCode = 210
					}
					res.ContentLength = -1
					if mode == "plain-json-resolver" {
						res.Header.Set("Content-Type", "text/plain")
					} else {
						res.Header.Set("Content-Type", "application/json")
					}
					return res, nil
				}
				data := payload
				if mode == "checksum-mismatch" || mode == "bad-md5-210" {
					data = strings.ToUpper(payload)
				}
				if mode == "html-210" {
					data = "<html>error!"
				}
				res := quarkResponse(data, nil)
				if strings.HasSuffix(mode, "-210") {
					res.StatusCode = 210
				}
				res.ContentLength = int64(len(data))
				if mode == "actual-json" || mode == "large-json" || mode == "json-file-210" {
					res.Header.Set("Content-Type", "application/json")
				}
				return res, nil
			})}
			name := "文件.txt"
			if mode == "stale-name" {
				name = "旧名.txt"
			}
			result, e := DownloadAccountFile(ctx, client, Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-token"}, DownloadInput{AccountID: "a", ParentID: "0", EntryID: id, Name: name}, destination, func(p DownloadProgress) {
				if mode == "cancel" && p.Received > 0 {
					cancel()
				}
			})
			ok := map[string]bool{"direct": true, "params": true, "raw-params": true, "redirect": true, "relative-redirect": true, "json-resolver": true, "plain-json-resolver": true, "actual-json": true, "large-json": true, "http-upgrade": true, "zero": true, "no-md5": true, "resolver-210": true, "bytes-210": true, "json-file-210": true}[mode]
			if ok {
				data, readErr := os.ReadFile(destination)
				if e != nil || readErr != nil || string(data) != payload || result.Bytes != int64(len(payload)) {
					t.Fatalf("download result %+v %v read %v", result, e, readErr)
				}
			} else {
				if e == nil {
					t.Fatal("invalid download accepted")
				}
				if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed download left final file")
				}
				if strings.Contains(e.Error(), "synthetic-token") || strings.Contains(e.Error(), "url-secret") {
					t.Fatal("credentials/link leaked in error")
				}
			}
			if (mode == "expired" || mode == "folder" || mode == "stale-name") && infos != 0 {
				t.Fatal("invalid source requested a download")
			}
			if (mode == "bad-md5-210" || mode == "html-210") && (e == nil || !strings.Contains(e.Error(), "MD5")) {
				t.Fatal("invalid 210 content bypassed integrity check")
			}
			if mode == "resolver-210" && gets != 2 {
				t.Fatal("210 resolver was not followed to the content response")
			}
			if (mode == "private-210" || mode == "loop-210") && gets != 1 {
				t.Fatal("unsafe/looping 210 resolver followed")
			}
			if infos > 1 || gets > 6 {
				t.Fatal("request replay or unbounded redirects")
			}
			if (mode == "actual-json" || mode == "large-json") && gets != 1 {
				t.Fatal("actual file treated as resolver")
			}
			parts, _ := filepath.Glob(filepath.Join(root, ".multipan-download-*"))
			if len(parts) != 0 {
				t.Fatal("temporary file leaked")
			}
		})
	}
}
