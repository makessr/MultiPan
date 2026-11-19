package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestBaiduDownloadBoundaries(t *testing.T) {
	for _, mode := range []string{"direct", "download-agent", "forbidden", "redirect", "foreign", "lookalike", "json-file", "zero", "optional", "verified", "bad-original", "stale-original", "folder", "expired", "bad-path", "bad-id", "bad-size", "duplicate", "missing-url", "unsafe", "loop", "private", "truncated", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			payload := "hello world!"
			if mode == "zero" {
				payload = ""
			}
			if mode == "json-file" {
				payload = `{"actual":true}`
			}
			size := int64(len(payload))
			gets := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			jar, _ := cookiejar.New(nil)
			ju, _ := url.Parse("https://foreign.example/")
			jar.SetCookies(ju, []*http.Cookie{{Name: "jar", Value: "not-allowed"}})
			client := &http.Client{Jar: jar, Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "pan.baidu.com" {
					if r.Header.Get("User-Agent") != "fixture-browser" {
						t.Error("API browser agent changed")
					}
					if r.Header.Get("Cookie") == "" {
						t.Error("API cookie absent")
					}
					if r.URL.Path == "/api/list" {
						if mode == "expired" {
							return jsonResponse(map[string]any{"errno": -6}), nil
						}
						dir := 0
						if mode == "folder" {
							dir = 1
						}
						res := jsonResponse(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/file.txt", "server_filename": "file.txt", "fs_id": 123, "isdir": dir, "size": size, "md5": "opaque"}}})
						res.Header.Add("Set-Cookie", "BDUSS=rotated; Path=/; Secure")
						return res, nil
					}
					if r.URL.Path != "/api/filemetas" {
						t.Error("wrong API")
					}
					var targets []string
					json.Unmarshal([]byte(r.URL.Query().Get("target")), &targets)
					if len(targets) != 1 || targets[0] != "/file.txt" || r.URL.Query().Get("dlink") != "1" {
						t.Error("wrong metadata target")
					}
					if !strings.Contains(r.Header.Get("Cookie"), "rotated") {
						t.Error("stale cookie")
					}
					link := "https://d.pcs.baidu.com/file?sig=fixture-secret"
					if mode == "foreign" {
						link = "https://foreign.example/file"
					}
					if mode == "lookalike" {
						link = "https://baidu.com.foreign.example/file"
					}
					if mode == "unsafe" {
						link = "https://127.0.0.1/private"
					}
					if mode == "missing-url" {
						link = ""
					}
					info := map[string]any{"path": "/file.txt", "fs_id": 123, "size": size, "dlink": link}
					if mode == "bad-path" {
						info["path"] = "/other"
					}
					if mode == "bad-id" {
						info["fs_id"] = 124
					}
					if mode == "bad-size" {
						info["size"] = size + 1
					}
					if mode == "optional" {
						info = map[string]any{"dlink": link}
					}
					rows := []any{info}
					if mode == "duplicate" {
						rows = append(rows, info)
					}
					return jsonResponse(map[string]any{"errno": 0, "info": rows}), nil
				}
				if r.Header.Get("Referer") != "https://pan.baidu.com/disk/main" {
					t.Error("wrong referer")
				}
				if r.Header.Get("User-Agent") != "netdisk" {
					t.Error("content agent not preserved across redirects")
				}
				gets++
				if mode == "forbidden" || (mode == "download-agent" && r.Header.Get("User-Agent") != "netdisk") {
					res := quarkResponse("", nil)
					res.StatusCode = 403
					return res, nil
				}
				official := r.URL.Host == "d.pcs.baidu.com"
				if official {
					if !strings.Contains(r.Header.Get("Cookie"), "rotated") {
						t.Error("fresh cookie missing")
					}
				} else if r.Header.Get("Cookie") != "" {
					t.Error("cookie leaked")
				}
				if strings.Contains(r.Header.Get("Cookie"), "not-allowed") || r.Header.Get("Authorization") != "" {
					t.Error("unexpected credentials")
				}
				if mode == "loop" || ((mode == "redirect" || mode == "private") && gets == 1) {
					res := quarkResponse("", nil)
					res.StatusCode = 302
					next := "https://foreign.example/file"
					if mode == "loop" {
						next = "https://d.pcs.baidu.com/file?sig=fixture-secret"
					}
					if mode == "private" {
						next = "https://127.0.0.1/private"
					}
					res.Header.Set("Location", next)
					return res, nil
				}
				data := payload
				if mode == "truncated" {
					data = "x"
				}
				res := quarkResponse(data, nil)
				res.ContentLength = size
				return res, nil
			})}
			var originals []UploadFingerprint
			if mode == "verified" || mode == "bad-original" || mode == "stale-original" {
				digest := sha256.Sum256([]byte(payload))
				if mode == "bad-original" {
					digest = sha256.Sum256([]byte("different"))
				}
				revision := "123:opaque"
				if mode == "stale-original" {
					revision = "old"
				}
				originals = []UploadFingerprint{{AccountID: "a", EntryID: "/file.txt", Name: "file.txt", Size: size, Revision: revision, SHA256: hex.EncodeToString(digest[:])}}
			}
			dest := filepath.Join(t.TempDir(), "saved.txt")
			result, e := DownloadAccountFile(ctx, client, Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, UserAgent: "fixture-browser", Secret: "BDUSS=initial"}, DownloadInput{ParentID: "/", EntryID: "/file.txt", Name: "file.txt"}, dest, func(p DownloadProgress) {
				if mode == "cancel" && p.Received > 0 {
					cancel()
				}
			}, originals...)
			ok := map[string]bool{"direct": true, "download-agent": true, "redirect": true, "foreign": true, "lookalike": true, "json-file": true, "zero": true, "optional": true, "verified": true, "stale-original": true}[mode]
			if ok {
				data, re := os.ReadFile(dest)
				if e != nil || re != nil || string(data) != payload {
					t.Fatalf("result=%+v err=%v read=%v", result, e, re)
				}
				want := "unverified"
				if mode == "verified" {
					want = "verified"
				}
				if result.Verification != want {
					t.Fatal("wrong verification", result)
				}
			} else {
				if e == nil {
					t.Fatal("invalid download accepted")
				}
				if _, se := os.Stat(dest); !errors.Is(se, os.ErrNotExist) {
					t.Fatal("failed final file left")
				}
				if strings.Contains(e.Error(), "fixture-secret") || strings.Contains(e.Error(), "initial") {
					t.Fatal("sensitive error")
				}
			}
			parts, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".multipan-download-*"))
			if len(parts) > 0 {
				t.Fatal("temporary file leak")
			}
			if gets > 6 {
				t.Fatal("unbounded requests")
			}
		})
	}
	for raw, want := range map[string]bool{"https://d.pcs.baidu.com/a": true, "https://a.baidupcs.com/a": true, "https://baidu.com.evil.example/a": false, "http://baidu.com/a": false, "https://baidu.com:8443/a": false} {
		u, _ := url.Parse(raw)
		if baiduDownloadCookieScope(u) != want {
			t.Error("bad cookie boundary")
		}
	}
}
