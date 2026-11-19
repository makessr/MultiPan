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
)

func baiduResponse(value any) *http.Response {
	b, _ := json.Marshal(value)
	return quarkResponse(string(b), nil)
}
func TestBaiduUploadWorkflow(t *testing.T) {
	for _, mode := range []string{"single", "multi", "partial", "cached", "rapid", "result-query", "bad-part", "bad-indices"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("百度上传字节\x00\xff")
			if mode == "multi" || mode == "partial" {
				payload = bytes.Repeat([]byte{0x27}, int(baiduChunk)+27)
			}
			local := filepath.Join(t.TempDir(), "样本 +.zip")
			os.WriteFile(local, payload, 0600)
			info, _ := os.Stat(local)
			f := File{Path: local, Name: filepath.Base(local), Size: info.Size(), Modified: info.ModTime().UnixMilli()}
			a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=synthetic-session; STOKEN=pan-only", Folder: "/备份/中文 +"}
			target := a.Folder + "/" + f.Name
			expected := []string{}
			for offset := int64(0); offset < f.Size; offset += baiduChunk {
				h := md5.Sum(payload[offset:min(offset+baiduChunk, f.Size)])
				expected = append(expected, hex.EncodeToString(h[:]))
			}
			final := map[string]any{"fs_id": 9223372036854775000, "path": target, "size": f.Size, "isdir": 0}
			parts := []int{}
			created, metas, preCalls := 0, 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "d.pcs.baidu.com" {
					if r.Header.Get("Cookie") != "BDUSS=synthetic-session" || r.URL.Query().Get("bdstoken") != "" {
						t.Fatal("pan-only credentials leaked to storage")
					}
					if r.URL.Path != "/rest/2.0/pcs/superfile2" || r.URL.Scheme != "https" || r.URL.Query().Get("path") != target || r.URL.Query().Get("uploadid") != "private-upload-id" {
						t.Fatal("part request context changed")
					}
					n, _ := strconv.Atoi(r.URL.Query().Get("partseq"))
					parts = append(parts, n)
					reader, e := r.MultipartReader()
					if e != nil {
						t.Fatal(e)
					}
					part, e := reader.NextPart()
					if e != nil {
						t.Fatal(e)
					}
					b, _ := io.ReadAll(part)
					if part.FormName() != "file" || !bytes.Equal(b, payload[int64(n)*baiduChunk:min(int64(n+1)*baiduChunk, f.Size)]) {
						t.Fatal("multipart bytes wrong")
					}
					if _, e = reader.NextPart(); e != io.EOF {
						t.Fatal("unexpected extra part")
					}
					digest := expected[n]
					if mode == "bad-part" {
						digest = "wrong"
					}
					return baiduResponse(map[string]any{"md5": digest}), nil
				}
				if r.URL.Host != "pan.baidu.com" || r.Header.Get("Cookie") != a.Secret {
					t.Fatal("wrong API origin or cookie")
				}
				switch r.URL.Path {
				case "/api/list":
					if r.URL.Query().Get("dir") != a.Folder {
						t.Error("ignored selected path")
					}
					return baiduResponse(map[string]any{"errno": 0, "list": []any{}}), nil
				case "/api/gettemplatevariable":
					return baiduResponse(map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "synthetic-bdstoken"}}), nil
				case "/api/precreate":
					preCalls++
					r.ParseForm()
					if r.PostForm.Get("path") != target || r.PostForm.Get("size") != strconv.FormatInt(f.Size, 10) || r.URL.Query().Get("bdstoken") != "synthetic-bdstoken" || r.PostForm.Get("rtype") != "0" {
						t.Error("precreate request wrong")
					}
					var hashes []string
					json.Unmarshal([]byte(r.PostForm.Get("block_list")), &hashes)
					if fmt.Sprint(hashes) != fmt.Sprint(expected) {
						t.Fatal("incorrect block hashes")
					}
					needed := []int{}
					for i := range hashes {
						needed = append(needed, i)
					}
					if mode == "partial" {
						needed = []int{1}
					}
					if mode == "cached" {
						needed = []int{}
					}
					if mode == "bad-indices" {
						needed = []int{999}
					}
					returnType := 1
					if mode == "rapid" {
						returnType = 2
					}
					return baiduResponse(map[string]any{"errno": 0, "uploadid": "private-upload-id", "return_type": returnType, "block_list": needed}), nil
				case "/api/create":
					created++
					r.ParseForm()
					if r.PostForm.Get("rtype") != "0" || r.URL.Query().Get("a") != "commit" || r.PostForm.Get("uploadid") != "private-upload-id" {
						t.Error("commit context wrong")
					}
					if mode == "result-query" {
						return baiduResponse(map[string]any{"errno": 0}), nil
					}
					final["errno"] = 0
					return baiduResponse(final), nil
				case "/api/filemetas":
					metas++
					var targets []string
					json.Unmarshal([]byte(r.URL.Query().Get("target")), &targets)
					if len(targets) != 1 || targets[0] != target {
						t.Error("wrong final path query")
					}
					return baiduResponse(map[string]any{"errno": 0, "info": []any{final}}), nil
				}
				t.Fatalf("unexpected endpoint %s", r.URL.Path)
				return nil, nil
			})}
			var sent int64
			e := (&HTTPUploader{client}).Upload(context.Background(), a, f, a.Folder, func(n int64) { sent = n })
			if mode == "bad-part" || mode == "bad-indices" {
				if e == nil || created != 0 {
					t.Fatal("bad upload accepted or committed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if sent != f.Size || preCalls != 1 {
				t.Fatal("missing final progress or repeated precreate")
			}
			wantParts := len(expected)
			if mode == "partial" {
				wantParts = 1
			}
			if mode == "cached" || mode == "rapid" {
				wantParts = 0
			}
			if len(parts) != wantParts {
				t.Fatalf("parts=%v want=%d", parts, wantParts)
			}
			if mode == "rapid" {
				if created != 0 || metas != 1 {
					t.Fatal("rapid replayed write or missed verification")
				}
			} else if created != 1 {
				t.Fatal("commit count")
			}
		})
	}
}
func TestBaiduSessionAndFolders(t *testing.T) {
	a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=synthetic-session; STOKEN=old"}
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
			response := baiduResponse(map[string]any{"errno": 0, "result": map[string]string{"bdstoken": "synthetic-bdstoken"}})
			response.Header.Add("Set-Cookie", "STOKEN=new; Secure; Path=/")
			return response, nil
		}
		items := []any{}
		if r.URL.Query().Get("page") == "1" {
			for i := 0; i < 1000; i++ {
				items = append(items, map[string]any{"path": fmt.Sprintf("/dir%04d", i), "server_filename": fmt.Sprintf("dir%04d", i), "isdir": 1})
			}
		}
		return baiduResponse(map[string]any{"errno": 0, "list": items}), nil
	})}
	fresh, e := CheckBaiduSession(context.Background(), client, a)
	if e != nil || !strings.Contains(fresh, "STOKEN=new") {
		t.Fatalf("session %v", e)
	}
	p, e := ListBaiduFolders(context.Background(), client, a, "/")
	if e != nil || len(p.Folders) != 1000 || p.Folders[0].ID != "/dir0000" || calls != 5 {
		t.Fatalf("pagination %v calls=%d", e, calls)
	}
	client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		return baiduResponse(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/备份/中文 +/子目录", "isdir": 1}}}), nil
	})
	p, e = ListBaiduFolders(context.Background(), client, a, "/备份/中文 +")
	if e != nil || len(p.Breadcrumbs) != 3 || p.Breadcrumbs[2].ID != "/备份/中文 +" || len(p.Folders) != 1 {
		t.Fatal("nested folder crumbs")
	}
}
func TestBaiduErrorsAndMetadata(t *testing.T) {
	a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=synthetic-session"}
	for _, body := range []string{`{"errno":-6}`, `{"errno":132}`, `{"result":{}}`, `{"errno":0,"result":{"bdstoken":""}}`} {
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) { return quarkResponse(body, nil), nil })}
		if _, e := CheckBaiduSession(context.Background(), client, a); e == nil {
			t.Fatal("bad session accepted")
		}
	}
	for _, p := range []string{"relative", "/a/../b", "/a//b", "/a/", "/a\\b", "/a\x00"} {
		if validBaiduPath(p) {
			t.Fatalf("bad path accepted %q", p)
		}
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("secret-cookie=DO-NOT-LEAK private uploadid")
	})}
	if _, e := CheckBaiduSession(context.Background(), client, a); e == nil || strings.Contains(e.Error(), "DO-NOT-LEAK") {
		t.Fatal("network error leaked credentials")
	}
	a.Endpoint = "https://evil.example"
	if _, e := CheckBaiduSession(context.Background(), client, a); e == nil {
		t.Fatal("API override accepted")
	}
	c := newBaiduClient(&http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		return baiduResponse(map[string]any{"errno": 0, "info": []any{map[string]any{"fs_id": 123, "path": "/wrong", "size": 10, "isdir": 0}}}), nil
	})}, Account{Endpoint: BaiduAPI})
	if e := c.confirm(context.Background(), "/expected", 10); e == nil {
		t.Fatal("wrong final target accepted")
	}
}
func TestBaiduAccountPersistenceAndConflict(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.SaveAccount(AccountInput{Name: "百度", Kind: "baidu", Secret: "BDUSS=synthetic-session; STOKEN=private"})
	if e != nil {
		t.Fatal(e)
	}
	if a.Secret != "" || a.Folder != "/" || a.Endpoint != BaiduAPI {
		t.Fatal("account exposure/default")
	}
	if e = m.SetDefaultFolder(a.ID, "/备份/2026", "根目录 / 备份 / 2026"); e != nil {
		t.Fatal(e)
	}
	reopen, e := NewManager(state, NewHTTPUploader())
	if e != nil || reopen.accounts[0].Folder != "/备份/2026" {
		t.Fatal("folder persistence")
	}
	b, _ := os.ReadFile(state)
	if bytes.Contains(b, []byte("synthetic-session")) {
		t.Fatal("plaintext credentials")
	}
	m.Close()
	reopen.Close()
	f := source(t)
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return baiduResponse(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/" + f.Name, "isdir": 0}}}), nil
	})}
	if e := (&HTTPUploader{client}).Upload(context.Background(), Account{Kind: "baidu", Endpoint: BaiduAPI}, f, "/", func(int64) {}); e == nil || calls != 1 {
		t.Fatal("same-name conflict ignored")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(empty, nil, 0600)
	s, _ := Inspect(empty)
	if e := (&HTTPUploader{client}).Upload(context.Background(), Account{Kind: "baidu", Endpoint: BaiduAPI}, s, "/", func(int64) {}); e == nil || calls != 1 {
		t.Fatal("empty file falsely accepted")
	}
}
