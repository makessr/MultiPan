package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ctResponse(value any) *http.Response {
	b, _ := json.Marshal(value)
	return quarkResponse(string(b), nil)
}
func TestCtRetiredLoginDoesNotSendRequests(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("retired endpoint called")
	})}
	if _, e := RequestCtSMS(context.Background(), client, "13800138000"); e == nil {
		t.Fatal("retired SMS accepted")
	}
	if _, e := LoginCtSession(context.Background(), client, CtLoginInput{Mode: "email", Email: "test@example.com", Password: "synthetic-password"}); e == nil {
		t.Fatal("retired password login accepted")
	}
	if calls != 0 {
		t.Fatal("request sent to retired login endpoint")
	}
}
func TestCtUploadProtocols(t *testing.T) {
	for _, mode := range []string{"normal", "rapid", "empty", "private", "bad-upload"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("诚通流式上传\x00\xff")
			if mode == "empty" {
				payload = nil
			}
			p := filepath.Join(t.TempDir(), "示例 +.zip")
			os.WriteFile(p, payload, 0600)
			stat, _ := os.Stat(p)
			f := File{Path: p, Name: filepath.Base(p), Size: stat.Size(), Modified: stat.ModTime().UnixMilli()}
			space := "public"
			if mode == "private" {
				space = "private"
			}
			a := Account{Kind: "ctfile", Endpoint: CtFileAPI, Username: space, Secret: "synthetic-session-token"}
			committed := false
			uploads, tickets := 0, 0
			h := md5.Sum(payload)
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "delegated-storage.example" {
					uploads++
					if r.URL.Scheme != "https" || r.URL.RawQuery != "signed=keep%2Bexact%2Fvalue" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Fatal("storage credential leak or URL changed")
					}
					b, _ := io.ReadAll(r.Body)
					if int64(len(b)) != r.ContentLength {
						t.Error("content length")
					}
					r.Body = io.NopCloser(bytes.NewReader(b))
					reader, e := r.MultipartReader()
					if e != nil {
						t.Fatal(e)
					}
					fields := map[string]string{}
					for {
						part, e := reader.NextPart()
						if e == io.EOF {
							break
						}
						if e != nil {
							t.Fatal(e)
						}
						data, _ := io.ReadAll(part)
						fields[part.FormName()] = string(data)
						if part.FormName() == "file" && part.FileName() != f.Name {
							t.Error("filename")
						}
					}
					if fields["name"] != f.Name || fields["filesize"] != strconv.FormatInt(f.Size, 10) || fields["file"] != string(payload) {
						t.Fatal("multipart payload mismatch")
					}
					if mode == "bad-upload" {
						return ctResponse(map[string]any{"code": 500}), nil
					}
					committed = true
					return ctResponse(map[string]any{"code": 200}), nil
				}
				if r.URL.Host != "rest.ctfile.com" {
					t.Fatal("unexpected origin")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["session"] != a.Secret {
					t.Fatal("missing session")
				}
				if strings.Contains(r.URL.Path, "/folder/") || strings.Contains(r.URL.Path, "/file/") {
					if !strings.HasPrefix(r.URL.Path, "/v1/"+space+"/") || body["folder_id"] != "d42" {
						t.Error("wrong cloud or folder")
					}
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/folder/list"):
					return ctResponse(map[string]any{"code": 200, "results": []any{}}), nil
				case strings.HasSuffix(r.URL.Path, "/file/list"):
					items := []any{}
					if committed && body["start"] == float64(0) {
						items = append(items, map[string]any{"key": "f9223372036854775000", "name": f.Name, "size": strconv.FormatInt(f.Size, 10), "status": "1"})
					}
					return ctResponse(map[string]any{"code": 200, "results": items}), nil
				case strings.HasSuffix(r.URL.Path, "/file/upload"):
					tickets++
					if body["name"] != f.Name || body["size"] != float64(f.Size) || body["checksum"] != hex.EncodeToString(h[:])+"-"+strconv.FormatInt(f.Size, 10) {
						t.Error("ticket checksum/content")
					}
					if mode == "rapid" {
						committed = true
						return ctResponse(map[string]any{"code": 200, "exists": "1"}), nil
					}
					return ctResponse(map[string]any{"code": 200, "exists": 0, "upload_url": "http://delegated-storage.example/post?signed=keep%2Bexact%2Fvalue"}), nil
				}
				t.Fatalf("unexpected endpoint %s", r.URL.Path)
				return nil, nil
			})}
			var sent int64
			e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "d42", func(n int64) { sent = n })
			if mode == "bad-upload" {
				if e == nil {
					t.Fatal("upload failure accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			want := 1
			if mode == "rapid" {
				want = 0
			}
			if uploads != want || tickets != 1 || sent != f.Size {
				t.Fatal("wrong upload count or progress")
			}
		})
	}
}

func TestCtFoldersPagingAndConflict(t *testing.T) {
	a := Account{Kind: "ctfile", Endpoint: CtFileAPI, Secret: "synthetic-session-token"}
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "folder/list") {
			return ctResponse(map[string]any{"code": 200, "results": []any{map[string]any{"key": "d9223372036854775000", "name": "大目录"}}}), nil
		}
		start := int(body["start"].(float64))
		items := []any{}
		if start < 2 {
			items = append(items, map[string]any{"key": fmt.Sprintf("f%d", start+1), "name": fmt.Sprintf("file%d", start), "size": 1, "status": 1})
		}
		return ctResponse(map[string]any{"code": 200, "results": items}), nil
	})}
	page, e := ListCtFolders(context.Background(), client, a, "d0")
	if e != nil || len(page.Folders) != 1 || page.Folders[0].ID != "d9223372036854775000" {
		t.Fatal("folder ID precision")
	}
	entries, e := (ctClient{client, a}).files(context.Background(), "d0")
	if e != nil || len(entries) != 2 || calls != 4 {
		t.Fatalf("pagination %v", e)
	}
	f := source(t)
	client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "folder/list") {
			return ctResponse(map[string]any{"code": 200, "results": []any{map[string]any{"key": "d1", "name": f.Name}}}), nil
		}
		if strings.HasSuffix(r.URL.Path, "file/list") {
			return ctResponse(map[string]any{"code": 200, "results": []any{}}), nil
		}
		t.Fatal("created duplicate")
		return nil, nil
	})
	if e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "d0", func(int64) {}); e == nil {
		t.Fatal("same-name folder ignored")
	}
}

func TestCtErrorsAndPersistence(t *testing.T) {
	a := Account{Kind: "ctfile", Endpoint: CtFileAPI, Secret: "synthetic-session-token"}
	for _, body := range []string{`{"code":401}`, `{"code":403}`, `{"userid":1}`, `{"code":200,"userid":0}`} {
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) { return quarkResponse(body, nil), nil })}
		if VerifyCtSession(context.Background(), client, a) == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) { return nil, fmt.Errorf("private-token=DO-NOT-LEAK") })}
	if e := VerifyCtSession(context.Background(), client, a); e == nil || strings.Contains(e.Error(), "DO-NOT-LEAK") {
		t.Fatal("unsafe error")
	}
	a.Endpoint = "https://evil.example"
	if VerifyCtSession(context.Background(), client, a) == nil {
		t.Fatal("API override accepted")
	}
	path := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(path, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	account, e := m.SaveAccount(AccountInput{Name: "诚通", Kind: "ctfile", Secret: "synthetic-session-token"})
	if e != nil {
		t.Fatal(e)
	}
	if account.Folder != "d0" || account.Username != "public" || account.Secret != "" {
		t.Fatal("default or secret exposure")
	}
	if e = m.SetDefaultFolder(account.ID, "d42", "根目录 / 目录"); e != nil {
		t.Fatal(e)
	}
	reopen, e := NewManager(path, NewHTTPUploader())
	if e != nil || reopen.accounts[0].Folder != "d42" {
		t.Fatal("folder persistence")
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("synthetic-session-token")) {
		t.Fatal("plaintext state")
	}
	m.Close()
	reopen.Close()
}

func TestCtIncompleteObjectNotSuccess(t *testing.T) {
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		items := []any{}
		if body["start"] == float64(0) {
			items = append(items, map[string]any{"key": "f1", "name": "pending.zip", "size": 123, "status": 2})
		}
		return ctResponse(map[string]any{"code": 200, "results": items}), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := (ctClient{client, Account{Endpoint: CtFileAPI}}).confirm(ctx, "d0", File{Name: "pending.zip", Size: 123}); e == nil {
		t.Fatal("incomplete object reported successful")
	}
}
