package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanzouDownloadFlow(t *testing.T) {
	for _, mode := range []string{"iframe", "modern", "primary", "dns-fallback", "both-dns", "timeout-no-fallback", "business-no-fallback", "cancel-ajax", "foreign-fallback", "mismatched-fallback", "variable", "password", "ajax-query", "redirect", "json", "verified", "bad-original", "bad-sign", "bad-ajax", "challenge", "hidden-challenge", "loop", "private", "truncated", "cancel", "stale", "expired"} {
		t.Run(mode, func(t *testing.T) {
			data := "hello world!"
			name := "file.zip"
			if mode == "json" {
				data = `{"test":true}`
				name = "file.json"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gets := 0
			primaryCalls, secondaryCalls := 0, 0
			modern := mode == "modern" || mode == "primary" || strings.Contains(mode, "fallback") || mode == "both-dns" || mode == "cancel-ajax"
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "up.woozooo.com" {
					if !strings.Contains(r.Header.Get("Cookie"), "private-cookie") {
						t.Error("missing auth")
					}
					r.ParseForm()
					switch r.Form.Get("task") {
					case "5":
						if mode == "expired" {
							return quarkResponse(`{"zt":9}`, nil), nil
						}
						return jsonResponse(map[string]any{"zt": 1, "text": []any{map[string]any{"id": "123", "name_all": name, "size": "12 B"}}}), nil
					case "22":
						on := "0"
						if mode == "password" {
							on = "1"
						}
						return jsonResponse(map[string]any{"zt": 1, "info": map[string]any{"is_newd": "https://share.example", "f_id": "i123", "onof": on, "pwd": "fixture-pass"}}), nil
					default:
						t.Error("wrong task")
					}
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("account credential leaked")
				}
				if r.URL.Host == "share.example" {
					switch r.URL.Path {
					case "/i123":
						if mode == "password" {
							return quarkResponse(`<script>data:'action=downprocess&sign=abc123&p='</script>`, nil), nil
						}
						return quarkResponse(`<iframe src="/fn?token=x"></iframe>`, nil), nil
					case "/fn":
						page := `data:{'sign':'abc123',ves:1}`
						if modern {
							page = `var wp_sign='abc123';var ajaxdata='0NOP';var domain2='https://apifile.lanzouw.com/ajaxfile.php?file=123';url:dom_ajaxs;data:{'sign':wp_sign};`
						}
						if modern && mode != "modern" {
							page += "var domain1='https://apifile.woozooo.com/ajaxfile.php?file=123';"
							if mode == "foreign-fallback" {
								page = strings.ReplaceAll(page, "https://apifile.lanzouw.com/", "https://foreign.example/")
							}
							if mode == "mismatched-fallback" {
								page = strings.ReplaceAll(page, "lanzouw.com/ajaxfile.php?file=123", "lanzouw.com/ajaxfile.php?file=999")
							}
						}
						if mode == "variable" {
							page = `var key='abc123';data:{'sign':key,ves:1}`
						}
						if mode == "bad-sign" {
							page = "changed"
						}
						if mode == "ajax-query" {
							page += `;url:'/ajaxm.php?file=123'`
						}
						return quarkResponse(page, nil), nil
					case "/ajaxm.php":
						r.ParseForm()
						if r.Form.Get("sign") != "abc123" {
							t.Error("sign not resolved")
						}
						if mode == "password" && r.Form.Get("p") != "fixture-pass" {
							t.Error("password absent")
						}
						if mode == "ajax-query" && r.URL.Query().Get("file") != "123" {
							t.Error("ajax query lost")
						}
						zt := 1
						if mode == "bad-ajax" {
							zt = 0
						}
						return jsonResponse(map[string]any{"zt": zt, "dom": "https://cdn.example", "url": "token"}), nil
					}
				}
				if modern && (r.URL.Host == "apifile.lanzouw.com" || r.URL.Host == "apifile.woozooo.com") {
					if r.URL.Host == "apifile.woozooo.com" {
						primaryCalls++
						if mode == "cancel-ajax" {
							cancel()
							return nil, context.Canceled
						}
						if mode == "timeout-no-fallback" {
							return nil, &net.DNSError{Err: "fixture timeout", Name: r.URL.Host, IsTimeout: true}
						}
						if mode == "business-no-fallback" {
							return jsonResponse(map[string]any{"zt": 0}), nil
						}
						if mode != "primary" {
							return nil, &net.DNSError{Err: "fixture missing", Name: r.URL.Host, IsNotFound: true}
						}
					} else {
						secondaryCalls++
						if mode == "both-dns" {
							return nil, &net.DNSError{Err: "fixture missing", Name: r.URL.Host, IsNotFound: true}
						}
					}

					r.ParseForm()
					if r.URL.Path != "/ajaxfile.php" || r.Form.Get("websignkey") != "0NOP" || r.Form.Get("kd") != "0" || r.Form.Get("sign") != "abc123" {
						t.Error("modern request mismatch")
					}
					return jsonResponse(map[string]any{"zt": 1, "dom": "https://unused.example", "url": "token"}), nil
				}
				if modern && r.URL.Host != "developer2oss.lanzouc.com:661" {
					t.Error("modern content node not selected")
				}
				gets++
				if mode == "loop" || (mode == "redirect" && gets == 1) || (mode == "private" && gets == 1) {
					res := quarkResponse("", nil)
					res.StatusCode = 302
					next := "https://cdn.example/final"
					if mode == "loop" {
						next = "https://cdn.example/file/token"
					}
					if mode == "private" {
						next = "https://127.0.0.1/file"
					}
					res.Header.Set("Location", next)
					return res, nil
				}
				body := data
				if mode == "hidden-challenge" {
					body = "<!DOCTYPE html><html>verification</html>"
				}
				if mode == "truncated" {
					body = "x"
				}
				res := quarkResponse(body, nil)
				res.ContentLength = int64(len(data))
				if mode == "challenge" {
					res.Header.Set("Content-Type", "text/html")
				}
				return res, nil
			})}
			var originals []UploadFingerprint
			if mode == "verified" || mode == "bad-original" {
				sum := sha256.Sum256([]byte(data))
				if mode == "bad-original" {
					sum = sha256.Sum256([]byte("different"))
				}
				originals = []UploadFingerprint{{AccountID: "a", EntryID: "123", Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}
			}
			inputName := name
			if mode == "stale" {
				inputName = "other.zip"
			}
			dest := filepath.Join(t.TempDir(), "saved.zip")
			result, e := DownloadAccountFile(ctx, client, Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "private-cookie"}, DownloadInput{ParentID: "-1", EntryID: "123", Name: inputName}, dest, func(p DownloadProgress) {
				if mode == "cancel" && p.Received > 0 {
					cancel()
				}
			}, originals...)
			ok := map[string]bool{"iframe": true, "modern": true, "primary": true, "dns-fallback": true, "variable": true, "password": true, "ajax-query": true, "redirect": true, "json": true, "verified": true}[mode]
			if ok {
				b, re := os.ReadFile(dest)
				if e != nil || re != nil || string(b) != data {
					t.Fatalf("result=%+v err=%v read=%v", result, e, re)
				}
				if mode == "verified" && result.Verification != "verified" {
					t.Fatal("original not verified")
				}
			} else {
				if e == nil {
					t.Fatal("invalid download accepted")
				}
				if _, se := os.Stat(dest); !errors.Is(se, os.ErrNotExist) {
					t.Fatal("failed final file")
				}
				if strings.Contains(e.Error(), "private-cookie") || strings.Contains(e.Error(), "fixture-pass") {
					t.Fatal("secret in error")
				}
			}
			parts, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".multipan-download-*"))
			if len(parts) > 0 {
				t.Fatal("temporary leak")
			}
			if modern && mode != "modern" {
				if primaryCalls != 1 {
					t.Fatalf("primary requests=%d", primaryCalls)
				}
				want := 0
				if mode == "dns-fallback" || mode == "both-dns" {
					want = 1
				}
				if secondaryCalls != want {
					t.Fatalf("secondary requests=%d want=%d", secondaryCalls, want)
				}
			}
			if gets > 6 {
				t.Fatal("unbounded redirects")
			}
		})
	}
}
