package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuangyaDownloadOfficialLinkAndIsolatedContent(t *testing.T) {
	for _, parent := range []string{guangyaRoot, "child-id"} {
		for _, mode := range []string{"direct", "no-code", "fallback-url", "case-url", "redirect", "relative", "http-upgrade", "status210", "zero", "json-file", "folder", "stale-name", "expired", "api-error", "bad-json", "empty-url", "unsafe-url", "private-redirect", "loop", "too-many", "truncated", "encoding", "cancel"} {
			t.Run(parent+mode, func(t *testing.T) {
				root := t.TempDir()
				destination := filepath.Join(root, "中文 空格.txt")
				payload := "hello world!"
				if mode == "zero" {
					payload = ""
				}
				if mode == "json-file" {
					payload = `{"data":{"redirect_url":"https://not-a-control.example/file"}}`
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				infos, gets := 0, 0
				jar, _ := cookiejar.New(nil)
				node, _ := url.Parse("https://cdn.example/")
				jar.SetCookies(node, []*http.Cookie{{Name: "session", Value: "synthetic-cookie"}})
				client := &http.Client{Jar: jar, Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if e := r.Context().Err(); e != nil {
						return nil, e
					}
					if r.URL.Host == "api.guangyapan.com" {
						if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("Did") != "fixture-device" {
							t.Error("wrong official authentication")
						}
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						if r.URL.Path == "/userres/v1/file/get_file_list" {
							pid := parent
							if pid == guangyaRoot {
								pid = ""
							}
							if body["parentId"] != pid {
								t.Error("incorrect root/parent mapping")
							}
							if mode == "expired" {
								res := quarkResponse("", nil)
								res.StatusCode = 401
								return res, nil
							}
							typ := 1
							if mode == "folder" {
								typ = 2
							}
							result := map[string]any{"data": map[string]any{"list": []any{map[string]any{"fileId": "file-id", "fileName": "中文 文件.txt", "resType": typ, "fileSize": len(payload)}}, "total": 1}}
							if mode != "no-code" {
								result["code"] = 0
							}
							return jsonResponse(result), nil
						}
						infos++
						if r.URL.Path != "/userres/v1/get_res_download_url" || body["fileId"] != "file-id" {
							t.Error("wrong official download request")
						}
						if mode == "api-error" {
							return quarkResponse(`{"code":403,"data":{}}`, nil), nil
						}
						if mode == "bad-json" {
							return quarkResponse("<html>error</html>", nil), nil
						}
						link := "https://cdn.example/file?sig=secret-signature"
						if mode == "http-upgrade" {
							link = strings.Replace(link, "https:", "http:", 1)
						}
						if mode == "empty-url" {
							link = ""
						}
						if mode == "unsafe-url" {
							link = "https://user:password@cdn.example/file"
						}
						key := "signedURL"
						if mode == "fallback-url" {
							key = "downloadUrl"
						}
						if mode == "case-url" {
							key = "SignedURL"
						}
						result := map[string]any{"data": map[string]any{key: link}}
						if mode != "no-code" {
							result["code"] = 0
						}
						return jsonResponse(result), nil
					}
					gets++
					if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Did") != "" || r.Header.Get("Dt") != "" || strings.Contains(r.Header.Get("Referer"), "secret-signature") {
						t.Error("public node received unsafe credentials or address")
					}
					if r.URL.Host != "cdn.example" && r.URL.Host != "edge.example" {
						t.Error("unexpected public host")
					}
					if mode == "loop" || mode == "too-many" || (gets == 1 && (mode == "redirect" || mode == "relative" || mode == "private-redirect")) {
						location := "https://edge.example/file"
						if mode == "relative" {
							location = "/next"
						}
						if mode == "private-redirect" {
							location = "https://127.0.0.1/private"
						}
						if mode == "loop" {
							location = "https://cdn.example/file?sig=secret-signature"
						}
						if mode == "too-many" {
							location = "https://cdn.example/" + strings.Repeat("a", gets)
						}
						res := quarkResponse("", nil)
						res.StatusCode = 302
						res.Header.Set("Location", location)
						return res, nil
					}
					data := payload
					if mode == "truncated" {
						data = "part"
					}
					res := quarkResponse(data, nil)
					res.ContentLength = int64(len(payload))
					if mode == "encoding" {
						res.Header.Set("Content-Encoding", "gzip")
					}
					if mode == "status210" {
						res.StatusCode = 210
					}
					if mode == "json-file" {
						res.Header.Set("Content-Type", "application/json")
					}
					return res, nil
				})}
				name := "中文 文件.txt"
				if mode == "stale-name" {
					name = "旧名.txt"
				}
				result, e := DownloadAccountFile(ctx, client, Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-token", Username: "fixture-device"}, DownloadInput{ParentID: parent, EntryID: "file-id", Name: name}, destination, func(p DownloadProgress) {
					if mode == "cancel" && p.Received > 0 {
						cancel()
					}
				})
				ok := map[string]bool{"direct": true, "no-code": true, "fallback-url": true, "case-url": true, "redirect": true, "relative": true, "http-upgrade": true, "status210": true, "zero": true, "json-file": true}[mode]
				if ok {
					data, readErr := os.ReadFile(destination)
					if e != nil || readErr != nil || string(data) != payload || result.Bytes != int64(len(payload)) || infos != 1 {
						t.Fatalf("download %+v %v read=%v info=%d", result, e, readErr, infos)
					}
				} else {
					if e == nil {
						t.Fatal("invalid download accepted")
					}
					if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("failed download saved final file")
					}
					if strings.Contains(e.Error(), "synthetic-token") || strings.Contains(e.Error(), "secret-signature") {
						t.Fatal("credential/link leaked in error")
					}
				}
				if (mode == "expired" || mode == "folder" || mode == "stale-name") && infos != 0 {
					t.Fatal("invalid source requested link")
				}
				if infos > 1 || gets > 6 {
					t.Fatal("unbounded requests")
				}
				if mode == "json-file" && gets != 1 {
					t.Fatal("actual JSON interpreted as resolver")
				}
				parts, _ := filepath.Glob(filepath.Join(root, ".multipan-download-*"))
				if len(parts) != 0 {
					t.Fatal("temporary file leaked")
				}
			})
		}
	}
}
