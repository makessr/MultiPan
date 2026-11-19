package transfer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLanzouUploadUsesChosenDirectory(t *testing.T) {
	f := source(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mydisk.php" {
			w.Write([]byte("<html></html>"))
			return
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("folder_id_bb_n") != "42" {
			t.Errorf("upload used wrong destination %q", r.FormValue("folder_id_bb_n"))
		}
		w.Write([]byte(`{"zt":1}`))
	}))
	defer server.Close()
	u := NewHTTPUploader()
	if e := u.Upload(context.Background(), Account{Kind: "lanzou", Endpoint: server.URL, Secret: "phpdisk_info=synthetic"}, f, "42", func(int64) {}); e != nil {
		t.Fatal(e)
	}
}

func TestFolderListingRootAndNested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/doupload.php" || r.Header.Get("Cookie") != "phpdisk_info=synthetic" {
			t.Error("incorrect directory request")
		}
		r.ParseForm()
		if r.Form.Get("task") != "47" {
			t.Error("listing must be read-only task 47")
		}
		if r.Form.Get("folder_id") == "-1" {
			w.Write([]byte(`{"zt":1,"info":[{"folderid":-1,"name":"首页"}],"text":[{"fol_id":123456789012345,"name":"资料 &amp; 备份"},{"fol_id":"42","name":"项目"}]}`))
			return
		}
		if r.Form.Get("folder_id") != "42" {
			t.Error("child ID not forwarded")
		}
		w.Write([]byte(`{"zt":"1","info":[{"folderid":-1,"name":"首页"},{"folderid":"42","name":"项目"}],"text":[{"fol_id":43,"name":"子目录"}]}`))
	}))
	defer server.Close()
	a := Account{Endpoint: server.URL, Secret: "phpdisk_info=synthetic"}
	root, e := ListLanzouFolders(context.Background(), server.Client(), a, "-1")
	if e != nil || len(root.Folders) != 2 || root.Folders[0].ID != "123456789012345" || root.Folders[0].Name != "资料 & 备份" || len(root.Breadcrumbs) != 1 {
		t.Fatalf("root parse failed: %+v %v", root, e)
	}
	child, e := ListLanzouFolders(context.Background(), server.Client(), a, "42")
	if e != nil || len(child.Folders) != 1 || child.Folders[0].ID != "43" || len(child.Breadcrumbs) != 2 || child.Breadcrumbs[1].Name != "项目" {
		t.Fatalf("child parse failed: %+v %v", child, e)
	}
}
func TestFolderEmptyAndExpiredSession(t *testing.T) {
	for _, body := range []string{`{"zt":1,"text":[],"info":[]}`, `{"zt":1,"text":null}`, `{"zt":9}`, `{"zt":0}`, `<html>login</html>`, `{"zt":1,"text":"invalid"}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer s.Close()
			p, e := ListLanzouFolders(context.Background(), s.Client(), Account{Endpoint: s.URL}, "-1")
			success := body == `{"zt":1,"text":[],"info":[]}` || body == `{"zt":1,"text":null}`
			if (e == nil) != success {
				t.Fatalf("unexpected result %v", e)
			}
			if success && (p.Folders == nil || len(p.Breadcrumbs) != 1) {
				t.Fatal("empty directory should remain selectable")
			}
		})
	}
}

type folderCapture struct{ sent chan string }

func (c folderCapture) Upload(_ context.Context, _ Account, _ File, folder string, _ func(int64)) error {
	c.sent <- folder
	return nil
}
func TestChosenFolderPersistsAndReachesUploader(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.enc")
	capture := folderCapture{make(chan string, 1)}
	m, e := NewManager(state, capture)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a, e := m.SaveAccount(AccountInput{Name: "test", Kind: "lanzou", Folder: "-1", Secret: "phpdisk_info=synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.SetDefaultFolder(a.ID, "42", "根目录 / 项目"); e != nil {
		t.Fatal(e)
	}
	f := source(t)
	if _, e = m.Enqueue([]File{f}, []Target{{a.ID, "42"}}); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { return m.Snapshot().Tasks[0].Status == "success" })
	if got := <-capture.sent; got != "42" {
		t.Fatalf("uploader received %q", got)
	}
	task := m.Snapshot().Tasks[0]
	if task.FolderDisplay != "根目录 / 项目" {
		t.Fatal("task lost display path")
	}
	restored, e := NewManager(state, capture)
	if e != nil {
		t.Fatal(e)
	}
	account := restored.Snapshot().Accounts[0]
	if account.Folder != "42" || account.FolderDisplay != "根目录 / 项目" {
		t.Fatal("default folder did not persist")
	}
}
