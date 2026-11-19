package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func source(t *testing.T) File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "中文 #文件.txt")
	if err := os.WriteFile(p, []byte("real upload content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestWebDAVPayloadAndNoOverwrite(t *testing.T) {
	f := source(t)
	payload, _ := os.ReadFile(f.Path)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		user, pwd, ok := r.BasicAuth()
		if !ok || user != "user" || pwd != "password" {
			t.Error("missing auth")
		}
		if r.Method == "MKCOL" {
			if r.URL.Path != "/dav/备份" {
				t.Errorf("bad folder: %s", r.URL.Path)
			}
			w.WriteHeader(201)
			return
		}
		if r.Method != "PUT" || r.Header.Get("If-None-Match") != "*" {
			t.Error("missing no-overwrite condition")
		}
		if r.URL.Path != "/dav/备份/"+f.Name {
			t.Errorf("bad path %q", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		if !bytes.Equal(data, payload) || r.ContentLength != f.Size {
			t.Error("upload bytes differ")
		}
		w.WriteHeader(201)
	}))
	defer server.Close()
	u := &HTTPUploader{Client: server.Client()}
	if err := u.Upload(context.Background(), Account{Kind: "webdav", Endpoint: server.URL + "/dav", Username: "user", Secret: "password"}, f, "/备份", func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}
func TestLanzouMultipartAndAuthFailure(t *testing.T) {
	for _, code := range []string{"1", "9", "0"} {
		t.Run(code, func(t *testing.T) {
			f := source(t)
			var gotPost bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "phpdisk_info=example" {
					t.Error("missing cookie")
				}
				if r.URL.Path == "/mydisk.php" {
					io.WriteString(w, `<iframe src="/mydisk.php?item=files"></iframe>`)
					return
				}
				gotPost = true
				if !strings.Contains(r.Header.Get("Referer"), "?item=files") {
					t.Error("referer was not resolved")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				file, h, err := r.FormFile("upload_file")
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				b, _ := io.ReadAll(file)
				if string(b) != "real upload content\n" || h.Filename != f.Name || r.FormValue("folder_id_bb_n") != "-1" || r.FormValue("task") != "1" {
					t.Error("multipart content mismatch")
				}
				io.WriteString(w, `{"zt":`+code+`}`)
			}))
			defer server.Close()
			u := NewHTTPUploader()
			err := u.Upload(context.Background(), Account{Kind: "lanzou", Endpoint: server.URL, Secret: "phpdisk_info=example"}, f, "-1", func(int64) {})
			if (err == nil) != (code == "1") || !gotPost {
				t.Fatalf("unexpected result code=%s err=%v", code, err)
			}
		})
	}
}
func TestWebDAVFailuresAndChangedSource(t *testing.T) {
	f := source(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(412) }))
	defer server.Close()
	u := &HTTPUploader{Client: server.Client()}
	a := Account{Kind: "webdav", Endpoint: server.URL}
	if err := u.Upload(context.Background(), a, f, "/", func(int64) {}); err == nil || !strings.Contains(err.Error(), "同名") {
		t.Fatalf("conflict not reported: %v", err)
	}
	os.WriteFile(f.Path, []byte("changed"), 0600)
	if err := u.Upload(context.Background(), a, f, "/", func(int64) {}); err == nil || !strings.Contains(err.Error(), "变化") {
		t.Fatalf("source change not rejected: %v", err)
	}
}

type controlledUploader struct {
	mu      sync.Mutex
	calls   map[string]int
	gate    chan struct{}
	started chan string
}

func (u *controlledUploader) Upload(ctx context.Context, a Account, f File, _ string, p func(int64)) error {
	u.mu.Lock()
	u.calls[a.Name]++
	u.mu.Unlock()
	u.started <- a.Name
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-u.gate:
	}
	if a.Name == "bad" {
		return errors.New("test account failure")
	}
	p(f.Size)
	return nil
}
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for tasks")
}
func TestFanoutIsolationAndEncryptedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	u := &controlledUploader{calls: map[string]int{}, gate: make(chan struct{}), started: make(chan string, 10)}
	m, err := NewManager(path, u)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var targets []Target
	for _, name := range []string{"good", "bad"} {
		a, err := m.SaveAccount(AccountInput{Name: name, Kind: "webdav", Endpoint: "https://example.com/dav", Folder: "/", Secret: "test-secret"})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, Target{a.ID, "/"})
	}
	f := source(t)
	raw, _ := json.Marshal(f)
	var roundtrip File
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	n, err := m.Enqueue([]File{roundtrip}, targets)
	if err != nil || n != 2 {
		t.Fatalf("enqueue %d %v", n, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-u.started:
		case <-time.After(5 * time.Second):
			t.Fatal("targets did not run concurrently")
		}
	}
	close(u.gate)
	waitFor(t, func() bool {
		s := m.Snapshot()
		return len(s.Tasks) == 2 && s.Tasks[0].Status == "success" && s.Tasks[1].Status == "failed"
	})
	state, _ := os.ReadFile(path)
	if bytes.Contains(state, []byte("test-secret")) || bytes.Contains(state, []byte(f.Path)) {
		t.Fatal("state stored in plaintext")
	}
	restored, err := NewManager(path, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Snapshot().Tasks) != 2 {
		t.Fatal("missing persisted tasks")
	}
	restored.mu.Lock()
	secret := restored.accounts[0].Secret
	restored.mu.Unlock()
	if secret != "test-secret" {
		t.Fatal("credential not restored")
	}
	serialized, _ := json.Marshal(m.Snapshot())
	if bytes.Contains(serialized, []byte("test-secret")) {
		t.Fatal("credential leaked in snapshot")
	}
}
func TestStopRetryAndPerAccountSerialization(t *testing.T) {
	u := &controlledUploader{calls: map[string]int{}, gate: make(chan struct{}), started: make(chan string, 10)}
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), u)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.SaveAccount(AccountInput{Name: "good", Kind: "webdav", Endpoint: "https://example.com", Folder: "/", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	f := source(t)
	second := source(t)
	secondPath := filepath.Join(filepath.Dir(second.Path), "second.txt")
	if e := os.Rename(second.Path, secondPath); e != nil {
		t.Fatal(e)
	}
	second, e := Inspect(secondPath)
	if e != nil {
		t.Fatal(e)
	}
	if _, err = m.Enqueue([]File{f, second}, []Target{{a.ID, "/"}}); err != nil {
		t.Fatal(err)
	}
	<-u.started
	s := m.Snapshot()
	if s.Tasks[0].Status != "running" || s.Tasks[1].Status != "queued" {
		t.Fatalf("not serialized: %+v", s.Tasks)
	}
	if err = m.Stop(s.Tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); _, ok := m.active[s.Tasks[0].ID]; return !ok })
	if err = m.Retry(s.Tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	close(u.gate)
	waitFor(t, func() bool {
		s := m.Snapshot()
		return s.Tasks[0].Status == "success" && s.Tasks[1].Status == "success"
	})
	if _, err = os.Stat(f.Path); err != nil {
		t.Fatal("source file removed")
	}
}
func TestRestartPausesPendingWithoutAutoUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	if err := writeState(path, diskState{Tasks: []Task{{ID: "one", Status: "running"}, {ID: "two", Status: "queued"}}}); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range m.Snapshot().Tasks {
		if task.Status != "paused" {
			t.Fatal("task was not paused")
		}
	}
	if len(m.active) != 0 {
		t.Fatal("unexpected automatic upload")
	}
}
func TestInvalidTargetsAndMissingSourceAreAtomic(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := source(t)
	if _, err = m.Enqueue([]File{f}, []Target{{"missing", "/"}}); err == nil {
		t.Fatal("missing account accepted")
	}
	a, err := m.SaveAccount(AccountInput{Name: "blue", Kind: "lanzou", Secret: "phpdisk_info=test", Folder: "-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Enqueue([]File{f}, []Target{{a.ID, "not a folder"}}); err == nil {
		t.Fatal("invalid folder accepted")
	}
	os.Remove(f.Path)
	if _, err = m.Enqueue([]File{f}, []Target{{a.ID, "-1"}}); err == nil {
		t.Fatal("missing source accepted")
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("partial batch was created")
	}
}
