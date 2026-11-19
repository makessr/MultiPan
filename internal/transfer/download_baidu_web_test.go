package transfer

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaiduWebSignVector(t *testing.T) {
	got, e := baiduWebSign("Key", "Plaintext")
	if e != nil || got != "u/MW6NlArwrT" {
		t.Fatal("incorrect RC4/base64 protocol signature", got, e)
	}
	if _, e = baiduWebSign("", "data"); e == nil {
		t.Fatal("empty signing key accepted")
	}
}
func TestBaiduPolicyWebFallback(t *testing.T) {
	for _, mode := range []string{"success", "wrong-id", "missing-sign", "web-forbidden", "unrelated-403"} {
		t.Run(mode, func(t *testing.T) {
			templates, links, contents := 0, 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "pan.baidu.com" {
					switch r.URL.Path {
					case "/api/list":
						return quarkResponse(`{"errno":0,"list":[{"fs_id":123,"path":"/f.txt","server_filename":"f.txt","isdir":0,"size":3}]}`, nil), nil
					case "/api/filemetas":
						return quarkResponse(`{"errno":0,"info":[{"fs_id":123,"dlink":"https://d.pcs.baidu.com/dlna"}]}`, nil), nil
					case "/api/gettemplatevariable":
						templates++
						sign := "Key"
						if mode == "missing-sign" {
							sign = ""
						}
						return jsonResponse(map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "token-fixture", "sign1": "Plaintext", "sign3": sign, "timestamp": 12345}}), nil
					case "/api/download":
						links++
						q := r.URL.Query()
						if q.Get("sign") != "u/MW6NlArwrT" || q.Get("fidlist") != "[123]" || q.Get("timestamp") != "12345" || q.Get("bdstoken") != "token-fixture" {
							t.Error("wrong web request")
						}
						id := 123
						if mode == "wrong-id" {
							id = 124
						}
						return jsonResponse(map[string]any{"errno": 0, "dlink": []any{map[string]any{"fs_id": id, "dlink": "https://foreign.example/web"}}}), nil
					}
					t.Error("unexpected API")
				}
				contents++
				if r.URL.Host == "foreign.example" && r.Header.Get("Cookie") != "" {
					t.Error("cookie leak")
				}
				if r.URL.Path == "/dlna" || mode == "web-forbidden" {
					code := 31329
					if mode == "unrelated-403" {
						code = 999
					}
					res := jsonResponse(map[string]any{"error_code": code, "error_msg": "hit black userlist"})
					res.StatusCode = 403
					return res, nil
				}
				res := quarkResponse("abc", nil)
				res.ContentLength = 3
				return res, nil
			})}
			dest := filepath.Join(t.TempDir(), "saved.txt")
			result, e := DownloadAccountFile(context.Background(), client, Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture"}, DownloadInput{ParentID: "/", EntryID: "/f.txt", Name: "f.txt"}, dest, nil)
			if mode == "success" {
				b, re := os.ReadFile(dest)
				if e != nil || re != nil || string(b) != "abc" || result.Verification != "unverified" {
					t.Fatal("fallback failed", e, re)
				}
			} else {
				if e == nil {
					t.Fatal("invalid fallback accepted")
				}
				if _, se := os.Stat(dest); !os.IsNotExist(se) {
					t.Fatal("failed final file")
				}
				if strings.Contains(e.Error(), "token-fixture") {
					t.Fatal("session leaked")
				}
			}
			if templates > 1 || links > 1 || contents > 2 {
				t.Fatal("unbounded fallback")
			}
			if mode == "unrelated-403" && templates != 0 {
				t.Fatal("retried unrelated policy")
			}
		})
	}
}
