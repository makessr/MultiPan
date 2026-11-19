package transfer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
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

func TestQuarkDownloadCookieBoundaryAndFileIntegrity(t *testing.T) {
	for _, mode := range []string{"direct", "external", "optional-info", "no-source-size", "zero", "json-file", "status210", "redirect", "relative", "official-rotation", "external-poison", "lookalike", "expired", "folder", "stale-name", "empty-info", "wrong-id", "ambiguous-info", "wrong-name-info", "wrong-size-info", "api-error", "api-redirect", "unsafe-url", "private-redirect", "loop", "too-many", "checksum-mismatch", "header-match", "original-match", "original-mismatch", "original-stale", "header-mismatch", "header-invalid", "header-only", "unknown-size-mismatch", "truncated", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "中文 空格.txt")
			payload := "hello world!"
			if mode == "zero" {
				payload = ""
			}
			if mode == "json-file" {
				payload = `{"data":{"redirect_url":"https://not-control.example/file"}}`
			}
			digest := md5.Sum([]byte(payload))
			checksum := hex.EncodeToString(digest[:])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			infos, gets := 0, 0
			expectedCookie := "download-rotated"
			jar, _ := cookiejar.New(nil)
			for _, host := range []string{"https://cdn.example/", "https://dl-pc-api-drive.quark.cn/"} {
				u, _ := url.Parse(host)
				jar.SetCookies(u, []*http.Cookie{{Name: "unrelated", Value: "jar-secret"}})
			}
			apiOrigin, _ := url.Parse(QuarkAPI)
			client := &http.Client{Jar: jar, Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if e := r.Context().Err(); e != nil {
					return nil, e
				}
				if r.URL.Host == apiOrigin.Host {
					if r.Header.Get("User-Agent") != "fixture-agent" || r.Header.Get("Referer") != "https://pan.quark.cn/" {
						t.Error("wrong API headers")
					}
					if r.Method == "GET" {
						cookie, e := r.Cookie("__puus")
						if e != nil || cookie.Value != "old" {
							t.Error("initial authentication missing")
						}
						if mode == "expired" {
							res := quarkResponse("", nil)
							res.StatusCode = 401
							return res, nil
						}
						dir := mode == "folder"
						entry := map[string]any{"fid": "file-id", "file_name": "中文 文件.txt", "dir": dir, "size": len(payload), "updated_at": 123}
						if mode == "no-source-size" || mode == "unknown-size-mismatch" {
							delete(entry, "size")
						}
						res := jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": []any{entry}}})
						res.Header.Add("Set-Cookie", "__puus=list-rotated; Path=/; Secure")
						return res, nil
					}
					infos++
					cookie, e := r.Cookie("__puus")
					if e != nil || cookie.Value != "list-rotated" {
						t.Error("updated listing cookie not used")
					}
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if r.Method != "POST" || r.URL.Path != "/1/clouddrive/file/download" || body["fids"].([]any)[0] != "file-id" {
						t.Error("wrong download request")
					}
					if mode == "api-error" {
						return quarkResponse(`{"status":403,"code":403}`, nil), nil
					}
					if mode == "api-redirect" {
						res := quarkResponse("", nil)
						res.StatusCode = 302
						res.Header.Set("Location", "https://foreign.example/steal")
						return res, nil
					}
					link := "https://dl-pc-api-drive.quark.cn/file?sig=url-secret"
					if mode == "external" {
						link = "https://cdn.example/file?sig=url-secret"
					}
					if mode == "lookalike" {
						link = "https://dl.quark.cn.evil.example/file?sig=url-secret"
					}
					if mode == "unsafe-url" {
						link = "https://user:pass@cdn.example/file"
					}
					info := map[string]any{"fid": "file-id", "file_name": "中文 文件.txt", "size": len(payload), "download_url": link, "md5": checksum}
					if strings.HasPrefix(mode, "original-") {
						info["md5"] = strings.Repeat("0", 32)
					}
					if mode == "header-mismatch" {
						d := md5.Sum([]byte(strings.ToUpper(payload)))
						info["md5"] = hex.EncodeToString(d[:])
					}
					if mode == "unknown-size-mismatch" {
						delete(info, "size")
					}
					if mode == "header-only" {
						delete(info, "md5")
					}
					if mode == "optional-info" {
						info = map[string]any{"download_url": link}
					}
					if mode == "wrong-id" {
						info["fid"] = "different"
					}
					if mode == "wrong-name-info" {
						info["file_name"] = "changed"
					}
					if mode == "wrong-size-info" {
						info["size"] = len(payload) + 1
					}
					rows := []any{info}
					if mode == "empty-info" {
						rows = []any{}
					}
					if mode == "ambiguous-info" {
						rows = append(rows, info)
					}
					res := jsonResponse(map[string]any{"status": 200, "code": 0, "data": rows})
					res.Header.Add("Set-Cookie", "__puus=download-rotated; Path=/; Secure")
					return res, nil
				}
				gets++
				official := r.URL.Hostname() == "dl-pc-api-drive.quark.cn" || r.URL.Hostname() == "next.quark.cn"
				if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "fixture-agent" || r.Header.Get("Referer") != "https://pan.quark.cn/" || strings.Contains(r.Header.Get("Cookie"), "jar-secret") {
					t.Error("unsafe content headers")
				}
				if official {
					cookie, e := r.Cookie("__puus")
					if e != nil || cookie.Value != expectedCookie {
						t.Error("fresh official cookie missing or poisoned")
					}
				} else if r.Header.Get("Cookie") != "" {
					t.Error("account cookie leaked to foreign/lookalike domain")
				}
				redirect := mode == "loop" || mode == "too-many" || (gets == 1 && (mode == "redirect" || mode == "relative" || mode == "official-rotation" || mode == "external-poison" || mode == "private-redirect")) || (mode == "external-poison" && gets == 2)
				if redirect {
					next := "https://cdn.example/file"
					if mode == "relative" {
						next = "/next"
					}
					if mode == "official-rotation" || (mode == "external-poison" && gets == 2) {
						next = "https://next.quark.cn/file"
					}
					if mode == "private-redirect" {
						next = "https://127.0.0.1/private"
					}
					if mode == "loop" {
						next = "https://dl-pc-api-drive.quark.cn/file?sig=url-secret"
					}
					if mode == "too-many" {
						next = "https://cdn.example/" + strings.Repeat("a", gets)
					}
					res := quarkResponse("", nil)
					res.StatusCode = 302
					res.Header.Set("Location", next)
					if mode == "official-rotation" {
						expectedCookie = "content-rotated"
						res.Header.Add("Set-Cookie", "__puus=content-rotated; Path=/; Secure")
					}
					if mode == "external-poison" && gets == 2 {
						res.Header.Add("Set-Cookie", "__puus=poison; Domain=.quark.cn; Path=/; Secure")
					}
					return res, nil
				}
				data := payload
				if mode == "checksum-mismatch" || mode == "header-mismatch" || mode == "unknown-size-mismatch" {
					data = strings.ToUpper(data)
				}
				if mode == "truncated" {
					data = "part"
				}
				res := quarkResponse(data, nil)
				res.ContentLength = int64(len(payload))
				if mode == "unknown-size-mismatch" {
					res.ContentLength = -1
				}
				if mode == "header-match" || mode == "header-mismatch" || mode == "header-only" {
					res.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(digest[:]))
				}
				if mode == "header-invalid" {
					res.Header.Set("Content-MD5", "invalid")
				}
				if mode == "status210" {
					res.StatusCode = 210
				}
				return res, nil
			})}
			name := "中文 文件.txt"
			if mode == "stale-name" {
				name = "旧名.txt"
			}
			var originals []UploadFingerprint
			if strings.HasPrefix(mode, "original-") {
				digest := sha256.Sum256([]byte(payload))
				if mode == "original-mismatch" {
					digest = sha256.Sum256([]byte("other bytes!"))
				}
				revision := "123"
				if mode == "original-stale" {
					revision = "122"
				}
				originals = []UploadFingerprint{{AccountID: "fixture-a", EntryID: "file-id", Name: name, Size: int64(len(payload)), Revision: revision, SHA256: hex.EncodeToString(digest[:])}}
			}
			result, e := DownloadAccountFile(ctx, client, Account{ID: "fixture-a", Kind: "quark", Endpoint: QuarkAPI, Secret: "__uid=fixture-cookie; __puus=old", UserAgent: "fixture-agent"}, DownloadInput{ParentID: "0", EntryID: "file-id", Name: name}, destination, func(p DownloadProgress) {
				if mode == "cancel" && p.Received > 0 {
					cancel()
				}
			}, originals...)
			ok := map[string]bool{"direct": true, "external": true, "optional-info": true, "no-source-size": true, "zero": true, "json-file": true, "status210": true, "redirect": true, "relative": true, "official-rotation": true, "external-poison": true, "lookalike": true, "checksum-mismatch": true, "header-match": true, "header-only": true, "original-match": true, "original-stale": true}[mode]
			if ok {
				if mode == "checksum-mismatch" {
					payload = strings.ToUpper(payload)
				}
				data, readErr := os.ReadFile(destination)
				if e != nil || readErr != nil || string(data) != payload || result.Bytes != int64(len(payload)) {
					t.Fatalf("download %+v %v read=%v", result, e, readErr)
				}
			} else {
				if e == nil {
					t.Fatal("invalid download accepted")
				}
				if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed download left final file")
				}
				if strings.Contains(e.Error(), "fixture-cookie") || strings.Contains(e.Error(), "url-secret") || strings.Contains(e.Error(), "rotated") {
					t.Fatal("cookie/link leaked in error")
				}
			}
			if mode == "original-match" && (result.Verification != "verified" || result.Warning != "" || !strings.Contains(result.Details, "SHA256")) {
				t.Fatal("local original did not override reference warning")
			}
			if mode == "original-stale" && result.Verification != "unverified" {
				t.Fatal("stale original used")
			}
			if mode == "original-mismatch" && (e == nil || !strings.Contains(e.Error(), "SHA256")) {
				t.Fatal("changed content not rejected")
			}
			if (mode == "expired" || mode == "folder" || mode == "stale-name") && infos != 0 {
				t.Fatal("invalid source requested link")
			}
			if infos > 1 || gets > 6 {
				t.Fatal("replayed or unbounded requests")
			}
			if mode == "checksum-mismatch" && !strings.Contains(result.Details, "参考 MD5") {
				t.Fatal("reference mismatch must remain visible")
			}
			if ok && (mode == "header-match" || mode == "header-only") && result.Warning != "" {
				t.Fatal("unexpected warning")
			}
			parts, _ := filepath.Glob(filepath.Join(root, ".multipan-download-*"))
			if len(parts) != 0 {
				t.Fatal("temporary file leaked")
			}
		})
	}
}

func TestQuarkDownloadOriginAndCookieScope(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: quarkTransport(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected") })}
	if _, e := DownloadAccountFile(context.Background(), client, Account{Kind: "quark", Endpoint: "https://foreign.example"}, DownloadInput{}, filepath.Join(t.TempDir(), "new.txt"), nil); e == nil || requests != 0 {
		t.Fatal("untrusted API used")
	}
	for raw, want := range map[string]bool{"https://quark.cn/file": true, "https://dl.quark.cn/file": true, "https://dl.quark.cn:443/file": true, "https://dl.quark.cn:8443/file": false, "http://dl.quark.cn/file": false, "https://quark.cn.evil.example/file": false, "https://evilquark.cn/file": false} {
		u, _ := url.Parse(raw)
		if quarkDownloadCookieScope(u) != want {
			t.Errorf("bad cookie scope for %s", raw)
		}
	}
}
