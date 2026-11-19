package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type brokenDownloadReader struct{}

func (brokenDownloadReader) Read(p []byte) (int, error) {
	return copy(p, []byte("part")), io.ErrUnexpectedEOF
}

func TestDAVDownloadStreamsAndCommitsOnlyCompleteNewFiles(t *testing.T) {
	for _, kind := range []string{"webdav", "ctdav"} {
		for _, mode := range []string{"success", "zero", "unknown-size", "cancel", "truncated", "overlong", "interrupted", "redirect", "list-redirect", "unauthorized", "header-size", "encoding", "race-existing", "source-folder", "source-name", "status-210"} {
			t.Run(kind+mode, func(t *testing.T) {
				root := t.TempDir()
				destination := filepath.Join(root, "中文 空格 & 文件.txt")
				payload := "hello world!"
				if mode == "zero" {
					payload = ""
				}
				a := Account{Kind: kind, Endpoint: "https://dav.example/dav", Username: "fixture-user", Secret: "synthetic-password"}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				gets := 0
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					user, secret, ok := r.BasicAuth()
					if !ok || user != a.Username || secret != a.Secret || r.URL.Host != "dav.example" {
						t.Error("unexpected origin/credentials")
					}
					if r.Method == "PROPFIND" {
						if mode == "list-redirect" {
							res := quarkResponse("", nil)
							res.StatusCode = 302
							res.Header.Set("Location", "https://other.example/steal")
							return res, nil
						}
						size := fmt.Sprintf("<d:getcontentlength>%d</d:getcontentlength>", len(payload))
						if mode == "unknown-size" {
							size = ""
						}
						resource := ""
						if mode == "source-folder" {
							resource = "<d:collection/>"
						}
						res := quarkResponse(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/file.txt</d:href><d:propstat><d:prop><d:resourcetype>`+resource+`</d:resourcetype>`+size+`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, nil)
						res.StatusCode = 207
						return res, nil
					}
					if r.Method != "GET" || r.URL.Path != "/dav/file.txt" || r.Header.Get("Accept-Encoding") != "identity" {
						t.Error("incorrect download request")
					}
					gets++
					data := payload
					if mode == "truncated" {
						data = "short"
					}
					if mode == "overlong" {
						data += "extra"
					}
					res := quarkResponse(data, nil)
					if mode == "status-210" {
						res.StatusCode = 210
					}
					res.ContentLength = int64(len(payload))
					if mode == "unknown-size" || mode == "overlong" {
						res.ContentLength = -1
					}
					if mode == "header-size" {
						res.ContentLength++
					}
					if mode == "encoding" {
						res.Header.Set("Content-Encoding", "gzip")
					}
					if mode == "interrupted" {
						res.Body = io.NopCloser(brokenDownloadReader{})
					}
					if mode == "redirect" {
						res.StatusCode = 302
						res.Header.Set("Location", "https://other.example/steal")
					}
					if mode == "unauthorized" {
						res.StatusCode = 401
					}
					return res, nil
				})}
				name := "file.txt"
				if mode == "source-name" {
					name = "stale.txt"
				}
				progressCalls := 0
				result, e := DownloadAccountFile(ctx, client, a, DownloadInput{ParentID: "/", EntryID: "/file.txt", Name: name}, destination, func(p DownloadProgress) {
					progressCalls++
					if p.Received > 0 {
						if mode == "cancel" {
							cancel()
						}
						if mode == "race-existing" {
							os.WriteFile(destination, []byte("keep original"), 0600)
						}
					}
				})
				if mode == "success" || mode == "zero" || mode == "unknown-size" {
					data, readErr := os.ReadFile(destination)
					if e != nil || readErr != nil || string(data) != payload || result.Path != destination || result.Bytes != int64(len(payload)) || gets != 1 || progressCalls == 0 {
						t.Fatalf("download result %+v %v read=%v GET=%d", result, e, readErr, gets)
					}
				} else {
					if e == nil {
						t.Fatal("invalid or interrupted download reported success")
					}
					if mode == "cancel" && !errors.Is(e, context.Canceled) {
						t.Fatal("cancellation not preserved")
					}
					if mode == "race-existing" {
						data, _ := os.ReadFile(destination)
						if string(data) != "keep original" {
							t.Fatal("racing destination overwritten")
						}
					} else if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("failed download left a final file")
					}
				}
				parts, _ := filepath.Glob(filepath.Join(root, ".multipan-download-*"))
				if len(parts) != 0 {
					t.Fatal("temporary file leaked")
				}
				if gets > 1 {
					t.Fatal("followed redirect or retried GET")
				}
				if (mode == "source-folder" || mode == "source-name") && gets != 0 {
					t.Fatal("invalid source downloaded")
				}
			})
		}
	}
}

func TestDownloadRefusesExistingAndInvalidDestinations(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: quarkTransport(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected") })}
	existing := filepath.Join(t.TempDir(), "keep.txt")
	os.WriteFile(existing, []byte("keep"), 0600)
	for _, dest := range []string{existing, "relative.txt", filepath.Join(t.TempDir(), "bad:name.txt")} {
		if _, e := DownloadAccountFile(context.Background(), client, Account{Kind: "webdav"}, DownloadInput{}, dest, nil); e == nil {
			t.Fatal("invalid destination accepted")
		}
	}
	if _, e := DownloadAccountFile(context.Background(), client, Account{Kind: "baidu"}, DownloadInput{}, filepath.Join(t.TempDir(), "new.txt"), nil); e == nil {
		t.Fatal("unsupported provider accepted")
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "keep" || requests != 0 {
		t.Fatal("existing file changed or preflight sent request")
	}
	for name, want := range map[string]string{".gitignore": ".gitignore", "NUL.txt": "_NUL.txt", "CON": "_CON", "a/b?.txt": "a_b_.txt", "...": "下载文件"} {
		if got := SuggestedDownloadName(name); got != want {
			t.Errorf("filename %q => %q, want %q", name, got, want)
		}
	}
}
