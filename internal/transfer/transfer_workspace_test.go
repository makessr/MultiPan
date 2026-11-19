package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUploadTreePlanAndValidation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if e := os.MkdirAll(filepath.Join(root, "empty"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	entries, e := ScanUploadTree(root)
	if e != nil || len(entries) != 3 {
		t.Fatalf("%+v %v", entries, e)
	}
	for _, f := range entries {
		if _, e := inspectUploadEntry(f); e != nil {
			t.Fatal(e)
		}
	}
	bad := entries[len(entries)-1]
	bad.RelativePath = "../test.txt"
	if _, e := inspectUploadEntry(bad); e == nil {
		t.Fatal("traversal accepted")
	}
}

func TestTransferSettingsAndQueueSurviveRestart(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.accounts = []Account{{ID: "fixture", Kind: "webdav", Endpoint: "https://fixture.invalid/dav", Username: "fixture", Secret: "fixture-secret"}}
	m.downloadActive["hold"] = func() {}
	settings := TransferSettings{UploadConcurrency: 2, DownloadConcurrency: 1, UploadKiB: 128, DownloadKiB: 256, CloseToTray: true}
	if e = m.SetTransferSettings(settings); e != nil {
		t.Fatal(e)
	}
	inputs := []DownloadInput{{AccountID: "fixture", ParentID: "/", EntryID: "/test.txt", Name: "test.txt"}}
	if n, e := m.QueueDownloads(inputs, t.TempDir()); e != nil || n != 1 {
		t.Fatalf("queue %d %v", n, e)
	}
	if _, e := m.QueueDownloads(inputs, filepath.Dir(m.downloads[0].Destination)); e == nil {
		t.Fatal("duplicate target accepted")
	}
	m.Close()
	restored, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	snapshot := restored.Snapshot()
	if snapshot.Settings != settings || len(snapshot.Downloads) != 1 || snapshot.Downloads[0].Status != "paused" || len(restored.downloadActive) != 0 {
		t.Fatalf("restart lost state: %+v", snapshot)
	}
	invalid := settings
	invalid.UploadConcurrency = 0
	if e = restored.SetTransferSettings(invalid); e == nil {
		t.Fatal("invalid settings accepted")
	}
}

func TestEncryptedUploadCheckpointIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.enc")
	a := Account{ID: "fixture", Kind: "quark", Secret: "fixture-cookie"}
	f := File{Path: "C:\\fixture.bin", Name: "fixture.bin", Size: 10, Modified: 12}
	ctx := prepareUploadCheckpoint(withUploadResumePath(context.Background(), path), a, f, "root", "sha-fixture")
	cp := checkpoint(ctx)
	cp.Meta = []byte(`{"secret":"temporary-upload-fixture"}`)
	cp.Parts[1] = "etag-one"
	if e := cp.save(); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "temporary-upload-fixture") {
		t.Fatal("checkpoint not encrypted")
	}
	restored := checkpoint(prepareUploadCheckpoint(withUploadResumePath(context.Background(), path), a, f, "root", "sha-fixture"))
	if restored.Parts[1] != "etag-one" {
		t.Fatal("part receipt lost")
	}
	changed := checkpoint(prepareUploadCheckpoint(withUploadResumePath(context.Background(), path), a, f, "root", "new-sha"))
	if len(changed.Parts) != 0 {
		t.Fatal("changed source reused stale parts")
	}
	a.Secret = "different-fixture"
	changed = checkpoint(prepareUploadCheckpoint(withUploadResumePath(context.Background(), path), a, f, "root", "sha-fixture"))
	if len(changed.Parts) != 0 {
		t.Fatal("changed account reused parts")
	}
}

func TestDownloadResumeValidatesRangeAndRehashesPrefix(t *testing.T) {
	for _, mode := range []string{"resume", "ignored", "bad-range", "changed-etag", "corrupt-prefix"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "part")
			if e := os.WriteFile(path, []byte("part"), 0600); e != nil {
				t.Fatal(e)
			}
			digest := sha256.Sum256([]byte("part"))
			raw := fmt.Sprintf(`{"Identity":"fixture","PrefixSHA":"%s","Bytes":4}`, hex.EncodeToString(digest[:]))
			if e := os.WriteFile(path+".resume", []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if mode == "corrupt-prefix" {
				os.WriteFile(path, []byte("xxxx"), 0600)
			}
			req, _ := http.NewRequest("GET", "https://fixture.invalid/file", nil)
			full := quarkResponse("part!!", http.Header{"Etag": {`"version-one"`}})
			full.Request = req
			full.ContentLength = 6
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Range") != "bytes=4-" || r.Header.Get("If-Range") != `"version-one"` {
					t.Error("missing conditional range")
				}
				res := quarkResponse("!!", http.Header{"Content-Range": {"bytes 4-5/6"}, "Etag": {`"version-one"`}})
				res.StatusCode = 206
				res.ContentLength = 2
				if mode == "ignored" {
					res.StatusCode = 200
				}
				if mode == "bad-range" {
					res.Header.Set("Content-Range", "bytes 0-1/6")
				}
				if mode == "changed-etag" {
					res.Header.Set("ETag", `"new"`)
				}
				return res, nil
			})}
			file, resp, offset, e := openResumeFile(path, "fixture", context.Background(), client, full, 6)
			if e != nil {
				t.Fatal(e)
			}
			defer file.Close()
			defer resp.Body.Close()
			if mode == "resume" {
				if offset != 4 {
					t.Fatal("did not resume")
				}
				prefix, _ := io.ReadAll(io.LimitReader(file, offset))
				suffix, _ := io.ReadAll(resp.Body)
				if string(prefix)+string(suffix) != "part!!" {
					t.Fatal("resume corrupted content")
				}
			} else {
				if offset != 0 || resp != full {
					t.Fatal("invalid range appended")
				}
			}
			if mode == "corrupt-prefix" && calls != 0 {
				t.Fatal("corrupt partial sent range")
			}
		})
	}
}

func TestLimiterCancellation(t *testing.T) {
	limits := newTransferLimits(TransferSettings{UploadKiB: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &limitedReader{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", 32*1024))), ctx: ctx, limiter: limits.up}
	if _, e := r.Read(make([]byte, 32*1024)); !errors.Is(e, context.Canceled) {
		t.Fatalf("limit ignored cancellation: %v", e)
	}
}

func TestQueueDownloadResumesAfterManagerRestart(t *testing.T) {
	var gets atomic.Int32
	var ranges atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/test.txt</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>6</d:getcontentlength><d:getetag>stable-v1</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected method")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("ETag", `"stable-v1"`)
		if r.Header.Get("Range") == "bytes=4-" {
			ranges.Add(1)
			w.Header().Set("Content-Range", "bytes 4-5/6")
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(206)
			fmt.Fprint(w, "!!")
			return
		}
		n := gets.Add(1)
		w.Header().Set("Content-Length", "6")
		if n == 1 {
			fmt.Fprint(w, "part")
		} else {
			fmt.Fprint(w, "part!!")
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	profile := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Username: "fixture", Secret: "fixture-secret", Folder: "/"})
	if e != nil {
		t.Fatal(e)
	}
	destination := t.TempDir()
	_, e = m.QueueDownloads([]DownloadInput{{AccountID: a.ID, ParentID: "/", EntryID: "/test.txt", Name: "test.txt"}}, destination)
	if e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { return m.Snapshot().Downloads[0].Status == "failed" })
	id := m.Snapshot().Downloads[0].ID
	m.Close()
	restored, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.ControlDownload(id, true); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { return restored.Snapshot().Downloads[0].Status == "saved" })
	raw, e := os.ReadFile(filepath.Join(destination, "test.txt"))
	if e != nil || string(raw) != "part!!" || ranges.Load() != 1 {
		t.Fatalf("resume mismatch %q range=%d %v", raw, ranges.Load(), e)
	}
}

func TestFolderUploadCreatesTreeAndPreservesEmptyDirectory(t *testing.T) {
	var mu sync.Mutex
	dirs := map[string]bool{"/dav": true}
	files := map[string][]byte{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		current := strings.TrimRight(r.URL.Path, "/")
		switch r.Method {
		case "MKCOL":
			if dirs[current] {
				w.WriteHeader(405)
			} else {
				dirs[current] = true
				w.WriteHeader(201)
			}
		case "PUT":
			if _, ok := files[current]; ok {
				w.WriteHeader(412)
				return
			}
			b, _ := io.ReadAll(r.Body)
			files[current] = b
			w.WriteHeader(201)
		case "GET":
			if b, ok := files[current]; ok {
				w.Header().Set("ETag", `"fixture-stable"`)
				w.Write(b)
			} else {
				w.WriteHeader(404)
			}
		case "PROPFIND":
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
			render := func(href string, dir bool, size int) {
				resource := ""
				if dir {
					resource = "<d:collection/>"
				}
				fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype><d:getcontentlength>%d</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, href, resource, size)
			}
			render(current+"/", true, 0)
			for dir := range dirs {
				if path.Dir(dir) == current {
					render(dir+"/", true, 0)
				}
			}
			for file, b := range files {
				if path.Dir(file) == current {
					render(file, false, len(b))
				}
			}
			fmt.Fprint(w, "</d:multistatus>")
		default:
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	root := filepath.Join(t.TempDir(), "source")
	os.MkdirAll(filepath.Join(root, "empty"), 0700)
	os.MkdirAll(filepath.Join(root, "sub"), 0700)
	os.WriteFile(filepath.Join(root, "sub", "test.txt"), []byte("payload"), 0600)
	entries, e := ScanUploadTree(root)
	if e != nil {
		t.Fatal(e)
	}
	manager, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	account, e := manager.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Username: "fixture", Secret: "fixture-password", Folder: "/"})
	if e != nil {
		t.Fatal(e)
	}
	if n, e := manager.Enqueue(entries, []Target{{account.ID, "/"}}); e != nil || n != 4 {
		t.Fatalf("enqueue %d %v", n, e)
	}
	waitFor(t, func() bool {
		snapshot := manager.Snapshot()
		for _, task := range snapshot.Tasks {
			if task.Status == "failed" {
				t.Fatalf("tree task failed: %s", task.Message)
			}
			if task.Status != "success" {
				return false
			}
		}
		return true
	})
	downloadParent := t.TempDir()
	n, e := manager.QueueDownloadFolder(context.Background(), DownloadInput{AccountID: account.ID, ParentID: "/", EntryID: "/source", Name: "source"}, downloadParent)
	if e != nil || n != 1 {
		t.Fatalf("queue tree %d %v", n, e)
	}
	waitFor(t, func() bool { jobs := manager.Snapshot().Downloads; return len(jobs) == 1 && jobs[0].Status == "saved" })
	if _, e := os.Stat(filepath.Join(downloadParent, "source", "empty")); e != nil {
		t.Fatal("empty download directory lost", e)
	}
	bytes, e := os.ReadFile(filepath.Join(downloadParent, "source", "sub", "test.txt"))
	if e != nil || string(bytes) != "payload" {
		t.Fatal("queued tree bytes mismatch", e)
	}
	mu.Lock()
	defer mu.Unlock()
	if !dirs["/dav/source/empty"] || string(files["/dav/source/sub/test.txt"]) != "payload" {
		t.Fatalf("tree mismatch: %v %v", dirs, files)
	}
}

func TestLegacyEncryptedStateBackupBeforeMigration(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "state.enc")
	if e := writeState(profile, diskState{Accounts: []diskAccount{{Account: Account{ID: "fixture", Kind: "webdav"}, Secret: "fixture-secret"}}}); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(profile)
	if e != nil {
		t.Fatal(e)
	}
	manager, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	manager.Close()
	backup, e := os.ReadFile(profile + ".before-0.46.bak")
	if e != nil || string(before) != string(backup) {
		t.Fatal("original encrypted backup missing", e)
	}
	restored, e := readState(profile)
	if e != nil || restored.Schema != 1 || len(restored.Accounts) != 1 {
		t.Fatal("migration lost account/schema", e)
	}
	manager, e = NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	manager.Close()
	unchanged, _ := os.ReadFile(profile + ".before-0.46.bak")
	if string(unchanged) != string(before) {
		t.Fatal("original backup overwritten")
	}
}
