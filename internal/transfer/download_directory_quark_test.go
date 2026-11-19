package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuarkDirectoryProtocolAndFiles(t *testing.T) {
	for _, mode := range []string{"normal", "optional-parent", "numeric-parent", "wrong-parent", "cycle", "repeated-id", "unsafe-id", "negative-size", "collision", "stale-root", "scan-error", "expired", "changed-file", "cancel-transfer"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := Account{ID: "q", Kind: "quark", Endpoint: QuarkAPI, Secret: "__uid=fixture-session; __puus=old"}
			scanFinished := false
			expectedCookie := "old"
			downloadCalls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if e := r.Context().Err(); e != nil {
					return nil, e
				}
				if r.URL.Host == "drive.quark.cn" {
					if !strings.Contains(r.Header.Get("Cookie"), "__uid=fixture-session") {
						t.Error("API session missing")
					}
					if r.URL.Path == "/1/clouddrive/file/sort" {
						id := r.URL.Query().Get("pdir_fid")
						if !scanFinished {
							cookie, e := r.Cookie("__puus")
							if e != nil || cookie.Value != expectedCookie {
								t.Error("scanning lost rotated session")
							}
						}
						if mode == "expired" {
							res := quarkResponse("", nil)
							res.StatusCode = 401
							return res, nil
						}
						if mode == "scan-error" && id == "sub" {
							return quarkResponse(`{"code":500,"status":500}`, nil), nil
						}
						entry := func(fid, name string, dir bool, parent string) map[string]any {
							m := map[string]any{"fid": fid, "file_name": name, "dir": dir, "pdir_fid": parent, "updated_at": 123}
							if !dir {
								m["size"] = 3
							}
							if mode == "numeric-parent" && parent == "0" {
								m["pdir_fid"] = 0
							}
							if mode == "optional-parent" {
								delete(m, "pdir_fid")
							}
							return m
						}
						var rows []any
						switch id {
						case "0":
							rows = []any{entry("root", "Root", true, "0")}
						case "root":
							one := entry("a", "alpha.txt", false, "root")
							if mode == "wrong-parent" {
								one["pdir_fid"] = "outside"
							}
							if mode == "unsafe-id" {
								one["fid"] = "../escape"
							}
							if mode == "negative-size" {
								one["size"] = -1
							}
							if mode == "changed-file" && scanFinished {
								one["file_name"] = "new.txt"
							}
							rows = []any{one, entry("sub", "Sub", true, "root"), entry("empty", "Empty", true, "root")}
							if mode == "cycle" {
								rows = append(rows, entry("root", "Loop", true, "root"))
							}
							if mode == "collision" {
								rows = append(rows, entry("same", "ALPHA.TXT", false, "root"))
							}
						case "sub":
							rows = []any{entry("b", "beta.txt", false, "sub")}
							if mode == "repeated-id" {
								rows = append(rows, entry("a", "again.txt", false, "sub"))
							}
						case "empty":
							rows = []any{}
						default:
							t.Error("unexpected source directory")
						}
						expectedCookie = id + "-rotated"
						res := jsonResponse(map[string]any{"code": 0, "status": 200, "data": map[string]any{"list": rows}})
						res.Header.Add("Set-Cookie", "__puus="+expectedCookie+"; Path=/; Secure")
						return res, nil
					}
					if r.URL.Path != "/1/clouddrive/file/download" {
						t.Error("unexpected API")
					}
					var body struct {
						FIDs []string `json:"fids"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					id := body.FIDs[0]
					cookie, e := r.Cookie("__puus")
					if e != nil || !strings.HasSuffix(cookie.Value, "-rotated") {
						t.Error("file metadata lost session")
					}
					host := "https://dl.quark.cn/"
					if id == "b" {
						host = "https://cdn.example/"
					}
					res := jsonResponse(map[string]any{"code": 0, "status": 200, "data": []any{map[string]any{"fid": id, "download_url": host + id, "md5": strings.Repeat("0", 32)}}})
					res.Header.Add("Set-Cookie", "__puus=link-rotated; Path=/; Secure")
					return res, nil
				}
				if r.URL.Host == "cdn.example" {
					if r.Header.Get("Cookie") != "" {
						t.Error("session leaked to CDN")
					}
				} else {
					cookie, e := r.Cookie("__puus")
					if e != nil || cookie.Value != "link-rotated" {
						t.Error("fresh official content session missing")
					}
				}
				data := "aaa"
				if r.URL.Path == "/b" {
					data = "bbb"
				}
				res := quarkResponse(data, nil)
				res.ContentLength = 3
				return res, nil
			})}
			digest := sha256.Sum256([]byte("aaa"))
			originals := []UploadFingerprint{{AccountID: "q", EntryID: "a", Name: "alpha.txt", Size: 3, Revision: "123", SHA256: hex.EncodeToString(digest[:])}}
			parent := t.TempDir()
			name := "Root"
			if mode == "stale-root" {
				name = "Changed"
			}
			result, e := downloadQuarkDirectory(ctx, client, a, DownloadInput{AccountID: "q", ParentID: "0", EntryID: "root", Name: name}, parent, func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
				scanFinished = true
				downloadCalls++
				return DownloadAccountFile(ctx, client, a, in, dest, func(progress DownloadProgress) {
					p(progress)
					if mode == "cancel-transfer" && progress.Received > 0 {
						cancel()
					}
				}, originals...)
			}, nil)
			valid := mode == "normal" || mode == "optional-parent" || mode == "numeric-parent" || mode == "changed-file" || mode == "cancel-transfer"
			if !valid {
				if e == nil || downloadCalls != 0 {
					t.Fatal("invalid scan started transfer", e)
				}
				entries, _ := os.ReadDir(parent)
				if len(entries) != 0 {
					t.Fatal("scan failure created local tree")
				}
				return
			}
			if e != nil || len(result.Items) != 2 {
				t.Fatal("directory failed", e, result)
			}
			if _, e = os.Stat(filepath.Join(parent, "Root", "Empty")); e != nil {
				t.Fatal("empty folder lost")
			}
			if mode == "cancel-transfer" {
				if !result.Cancelled || downloadCalls != 1 || result.Items[1].Status != "skipped" {
					t.Fatal("cancelled tree continued")
				}
				return
			}
			if mode == "changed-file" {
				if result.Items[0].Status != "failed" || result.Items[1].Status != "saved" {
					t.Fatal("stale file not isolated")
				}
				return
			}
			if result.Items[0].Result.Verification != "verified" || result.Items[1].Result.Verification != "unverified" {
				t.Fatal("incorrect content integrity classification")
			}
			for rel, want := range map[string]string{"alpha.txt": "aaa", filepath.Join("Sub", "beta.txt"): "bbb"} {
				data, e := os.ReadFile(filepath.Join(parent, "Root", rel))
				if e != nil || string(data) != want {
					t.Fatal("wrong tree contents", e)
				}
			}
		})
	}
}
