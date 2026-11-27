package transfer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ctWebTestAccount(server string) Account {
	return Account{ID: "ct", Kind: "ctweb", Endpoint: server, Secret: strings.Repeat("t", 40)}
}

func TestCtWebListMapsEntriesAndFolders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/home/file/browse/list" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+strings.Repeat("t", 40) {
			t.Errorf("missing bearer token, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":200,"data":{"folder_id":"d0","totalNum":3,"results":[`+
			`{"fid":123,"name":"报告.pdf","ext":"pdf","size":2048},`+
			`{"fid":"d9","name":"资料","ext":"folder","size":0},`+
			`{"id":456,"name":"照片.jpg","size":10}]}}`)
	}))
	defer srv.Close()
	page, e := ListAccountFiles(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "")
	if e != nil {
		t.Fatalf("list: %v", e)
	}
	if page.CurrentID != "d0" || len(page.Entries) != 3 {
		t.Fatalf("unexpected page %+v", page)
	}
	if page.Entries[0].ID != "123" || page.Entries[0].Folder || page.Entries[0].Size == nil || *page.Entries[0].Size != 2048 {
		t.Fatalf("file entry mismatch %+v", page.Entries[0])
	}
	if page.Entries[1].ID != "d9" || !page.Entries[1].Folder {
		t.Fatalf("folder entry mismatch %+v", page.Entries[1])
	}
	if page.Entries[2].ID != "456" || page.Entries[2].Folder {
		t.Fatalf("second file entry mismatch %+v", page.Entries[2])
	}
}

func TestCtWebListRejectsExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":401,"message":"无效的令牌或账户已锁定"}`)
	}))
	defer srv.Close()
	_, e := ListAccountFiles(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "d0")
	if e == nil || !strings.Contains(e.Error(), "登录已失效") {
		t.Fatalf("expected expired token error, got %v", e)
	}
}

func TestCtWebVerifySessionReturnsAccountName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":200,"data":{"nickname":"小明","email":"a@example.com"}}`)
	}))
	defer srv.Close()
	name, e := VerifyCtWebSession(context.Background(), srv.Client(), ctWebTestAccount(srv.URL))
	if e != nil {
		t.Fatalf("verify: %v", e)
	}
	if name != "小明" {
		t.Fatalf("unexpected name %q", name)
	}
}

func TestCtWebMkdirPostsFolderRequest(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/home/file/manage/create-folder" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&seen)
		io.WriteString(w, `{"code":200,"data":{"folder_id":"d77"}}`)
	}))
	defer srv.Close()
	id, e := ctWebMkdir(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "d0", "新目录")
	if e != nil {
		t.Fatalf("mkdir: %v", e)
	}
	if id != "d77" {
		t.Fatalf("unexpected folder id %q", id)
	}
	if seen["folder_id"] != "d0" || seen["name"] != "新目录" {
		t.Fatalf("unexpected payload %+v", seen)
	}
}

func TestCtWebUploadConfirmsCompletion(t *testing.T) {
	file := filepath.Join(t.TempDir(), "报告.txt")
	if e := os.WriteFile(file, []byte("hello ctfile"), 0600); e != nil {
		t.Fatal(e)
	}
	var putQuery url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home/file/manage/upload":
			io.WriteString(w, `{"code":200,"data":{"uploadUrl":"`+srvURL(r)+`/upload","file_name":"报告.txt"}}`)
		case "/upload":
			putQuery = r.URL.Query()
			if r.Method != "PUT" {
				t.Errorf("unexpected method %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "hello ctfile" {
				t.Errorf("unexpected upload body %q", body)
			}
			io.WriteString(w, `{"code":200,"message":"upload complete","id":9,"size":12,"name":"报告.txt"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	handle, e := os.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer handle.Close()
	info, _ := handle.Stat()
	var progressed int64
	uploader := NewHTTPUploader()
	uploader.Client = srv.Client()
	e = uploader.ctweb(context.Background(), ctWebTestAccount(srv.URL), File{Path: file, Name: "报告.txt", Size: info.Size()}, "d0", handle, func(n int64) { progressed = n })
	if e != nil {
		t.Fatalf("upload: %v", e)
	}
	if progressed != info.Size() {
		t.Fatalf("progress not reported, got %d", progressed)
	}
	if got := putQuery.Get("path"); got != "报告.txt" {
		t.Fatalf("unexpected upload path %q", got)
	}
	if got := putQuery.Get("contentlength"); got != "12" {
		t.Fatalf("unexpected content length %q", got)
	}
	if len(putQuery.Get("hash")) != 32 {
		t.Fatalf("expected md5 session hash, got %q", putQuery.Get("hash"))
	}
}

func TestCtWebUploadRejectsUnconfirmedReply(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.bin")
	if e := os.WriteFile(file, []byte("abc"), 0600); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/home/file/manage/upload" {
			io.WriteString(w, `{"code":200,"data":{"uploadUrl":"`+srvURL(r)+`/upload"}}`)
			return
		}
		io.WriteString(w, `{"code":200,"message":"uploading"}`)
	}))
	defer srv.Close()
	handle, e := os.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer handle.Close()
	info, _ := handle.Stat()
	uploader := NewHTTPUploader()
	uploader.Client = srv.Client()
	e = uploader.ctweb(context.Background(), ctWebTestAccount(srv.URL), File{Path: file, Name: "a.bin", Size: info.Size()}, "d0", handle, func(int64) {})
	if e == nil || !strings.Contains(e.Error(), "未确认完成") {
		t.Fatalf("expected confirmation failure, got %v", e)
	}
}

func TestCtWebDownloadURLFallsBackToStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home/workspace/manage/list":
			io.WriteString(w, `{"code":200,"total":0,"data":[]}`)
		case "/home/file/browse/fetch-url":
			io.WriteString(w, `{"code":404,"message":"文件不存在"}`)
		case "/home/file/browse/stream":
			io.WriteString(w, `{"code":200,"data":{"url":"https://cdn.example.com/a.bin"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	address, e := ctWebDownloadURL(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "d0", "123")
	if e != nil {
		t.Fatalf("download url: %v", e)
	}
	if address != "https://cdn.example.com/a.bin" {
		t.Fatalf("unexpected address %q", address)
	}
}

func TestValidCtWebFolder(t *testing.T) {
	for _, ok := range []string{"", "d0", "d123", "456"} {
		if !validCtWebFolder(ok) {
			t.Fatalf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"../x", "d", "abc", "d1/2"} {
		if validCtWebFolder(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func srvURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestCtWebWorkspaceIDAcceptsWebShapes(t *testing.T) {
	cases := map[string]int{
		`{"code":200,"total":1,"data":[{"id":17,"name":"我的空间"}]}`:                            17,
		`{"code":200,"data":{"list":[{"workspace_id":"23"}]}}`:                               23,
		`{"code":200,"data":{"workspaces":[{"ws_id":31,"is_default":1}]}}`:                   31,
		`{"code":200,"data":{"spaces":[{"id":"9","name":"共享"},{"id":44,"isDefault":true}]}}`: 44,
		`{"code":200,"data":{"41":{"name":"默认共享空间"}}}`:                                       41,
		`{"code":200,"data":[]}`: 0,
	}
	for body, want := range cases {
		if got := ctWebWorkspaceID([]byte(body)); got != want {
			t.Fatalf("%s: got %d want %d", body, got, want)
		}
	}
}

func TestCtWebListResolvesWorkspaceOnDemand(t *testing.T) {
	var listCalls, workspaceCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home/workspace/manage/list":
			workspaceCalls++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["pagesize"] == nil {
				io.WriteString(w, `{"code":200,"data":[]}`)
				return
			}
			io.WriteString(w, `{"code":200,"total":1,"data":[{"id":88,"name":"我的空间","is_default":1}]}`)
		case "/home/file/browse/list":
			listCalls++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["workspace_id"] == nil {
				io.WriteString(w, `{"code":400,"message":"workspace_id 参数缺失","message_key":"api.workspace.required"}`)
				return
			}
			if body["workspace_id"] != float64(88) {
				io.WriteString(w, `{"code":400,"message":"workspace_id 参数无效"}`)
				return
			}
			io.WriteString(w, `{"code":200,"data":{"folder_id":"d0","totalNum":1,"results":[{"fid":1,"name":"a.txt","size":3}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	page, e := ListAccountFiles(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "")
	if e != nil {
		t.Fatalf("list: %v", e)
	}
	if workspaceCalls != 1 || listCalls != 2 {
		t.Fatalf("unexpected call counts workspace=%d list=%d", workspaceCalls, listCalls)
	}
	if len(page.Entries) != 1 || page.Entries[0].Name != "a.txt" {
		t.Fatalf("unexpected page %+v", page)
	}
}

func TestCtWebListUsesCurrentSpaceWhenAccepted(t *testing.T) {
	var workspaceCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/home/workspace/manage/list" {
			workspaceCalls++
			io.WriteString(w, `{"code":200,"data":[]}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["workspace_id"] != float64(0) {
			io.WriteString(w, `{"code":400,"message":"workspace_id 参数无效"}`)
			return
		}
		io.WriteString(w, `{"code":200,"data":{"folder_id":"d0","totalNum":1,"results":[{"fid":1,"name":"a.txt","size":3}]}}`)
	}))
	defer srv.Close()
	page, e := ListAccountFiles(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "")
	if e != nil {
		t.Fatalf("list: %v", e)
	}
	if workspaceCalls != 0 {
		t.Fatalf("workspace list should not be needed, calls=%d", workspaceCalls)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("unexpected page %+v", page)
	}
}

func TestCtWebWorkspaceFailureShowsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/home/workspace/manage/list" {
			io.WriteString(w, `{"code":200,"total":0,"data":[],"note":"no space"}`)
			return
		}
		io.WriteString(w, `{"code":400,"message":"workspace_id 参数无效"}`)
	}))
	defer srv.Close()
	_, e := ListAccountFiles(context.Background(), srv.Client(), ctWebTestAccount(srv.URL), "")
	if e == nil || !strings.Contains(e.Error(), "无法确定诚通工作空间") || !strings.Contains(e.Error(), "no space") {
		t.Fatalf("expected workspace error with response preview, got %v", e)
	}
}

func TestCtWebMutationsCarryWorkspaceAndWebPayloads(t *testing.T) {
	var deleted, renamed, created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/home/workspace/manage/list":
			io.WriteString(w, `{"code":200,"total":1,"data":[{"id":7,"is_default":1}]}`)
			return
		case "/home/file/manage/delete":
			deleted = body
		case "/home/file/manage/rename":
			renamed = body
		case "/home/file/manage/create-folder":
			created = body
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		if body["workspace_id"] != float64(7) {
			io.WriteString(w, `{"code":400,"message":"workspace_id 参数无效"}`)
			return
		}
		if r.URL.Path == "/home/file/manage/create-folder" {
			io.WriteString(w, `{"code":200,"data":{"folder_id":"d9"}}`)
			return
		}
		io.WriteString(w, `{"code":200}`)
	}))
	defer srv.Close()
	account := ctWebTestAccount(srv.URL)
	if e := ctWebDelete(context.Background(), srv.Client(), account, "123"); e != nil {
		t.Fatalf("delete: %v", e)
	}
	if e := ctWebRename(context.Background(), srv.Client(), account, "123", "b.txt"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if _, e := ctWebMkdir(context.Background(), srv.Client(), account, "d0", "资料"); e != nil {
		t.Fatalf("mkdir: %v", e)
	}
	for name, body := range map[string]map[string]any{"delete": deleted, "rename": renamed, "create-folder": created} {
		if body == nil {
			t.Fatalf("%s request was not sent", name)
		}
		if body["workspace_id"] != float64(7) {
			t.Fatalf("%s workspace_id = %v, want 7", name, body["workspace_id"])
		}
	}
	if deleted["ids"] != "123" || deleted["force"] != float64(0) {
		t.Fatalf("unexpected delete payload %v", deleted)
	}
	if renamed["id"] != float64(123) || renamed["name"] != "b.txt" || renamed["is_rename"] != float64(1) {
		t.Fatalf("unexpected rename payload %v", renamed)
	}
	if created["folder_id"] != "d0" || created["name"] != "资料" {
		t.Fatalf("unexpected create payload %v", created)
	}
}
