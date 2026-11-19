package transfer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadConcurrencySettingsSchedulingAndRestart(t *testing.T) {
	started := make(chan string, 10)
	gates := map[string]chan struct{}{}
	listing := `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		gates["/dav/"+name] = make(chan struct{})
		listing += fmt.Sprintf(`<d:response><d:href>/dav/%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>1</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, name)
	}
	listing += `</d:multistatus>`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			w.WriteHeader(207)
			fmt.Fprint(w, listing)
			return
		}
		gate := gates[r.URL.Path]
		if r.Method != "GET" || gate == nil {
			w.WriteHeader(404)
			return
		}
		started <- r.URL.Path
		select {
		case <-gate:
			w.Header().Set("Content-Length", "1")
			fmt.Fprint(w, "x")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	profile := filepath.Join(t.TempDir(), "state.enc")
	m, err := NewManager(profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Username: "fixture", Secret: "fixture", Folder: "/"})
	if err != nil {
		t.Fatal(err)
	}
	settings := m.Snapshot().Settings
	settings.DownloadConcurrency = 2
	if err = m.SetTransferSettings(settings); err != nil {
		t.Fatal(err)
	}
	inputs := []DownloadInput{}
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		inputs = append(inputs, DownloadInput{AccountID: a.ID, ParentID: "/", EntryID: "/" + name, Name: name})
	}
	if n, e := m.QueueDownloads(inputs, t.TempDir()); e != nil || n != 6 {
		t.Fatalf("queue %d %v", n, e)
	}
	next := func() string {
		t.Helper()
		select {
		case path := <-started:
			return path
		case <-time.After(4 * time.Second):
			t.Fatal("download did not start")
			return ""
		}
	}
	first, second := next(), next()
	select {
	case extra := <-started:
		t.Fatal("exceeded two workers", extra)
	case <-time.After(40 * time.Millisecond):
	}
	settings.DownloadConcurrency = 3
	if err = m.SetTransferSettings(settings); err != nil {
		t.Fatal(err)
	}
	third := next()
	settings.DownloadConcurrency = 1
	if err = m.SetTransferSettings(settings); err != nil {
		t.Fatal(err)
	}
	close(gates[first])
	close(gates[second])
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.downloadActive) == 1 })
	select {
	case extra := <-started:
		t.Fatal("decrease canceled/replaced active worker", extra)
	case <-time.After(40 * time.Millisecond):
	}
	close(gates[third])
	fourth := next()
	if fourth != "/dav/file4.txt" {
		t.Fatal("queue order changed", fourth)
	}
	var fourthID string
	for _, j := range m.Snapshot().Downloads {
		if j.Input.Name == "file4.txt" {
			fourthID = j.ID
		}
	}
	if err = m.ControlDownload(fourthID, false); err != nil {
		t.Fatal(err)
	}
	if fifth := next(); fifth != "/dav/file5.txt" {
		t.Fatal("pause did not release exactly one slot", fifth)
	}
	m.Close()
	select {
	case extra := <-started:
		t.Fatal("shutdown dispatched pending job", extra)
	default:
	}
	restored, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	s := restored.Snapshot()
	if s.Settings.DownloadConcurrency != 1 {
		t.Fatal("concurrency not persisted")
	}
	for i, j := range s.Downloads {
		want := "saved"
		if i >= 3 {
			want = "paused"
		}
		if j.Status != want {
			t.Fatalf("restart job%d=%s want%s", i, j.Status, want)
		}
	}
	select {
	case extra := <-started:
		t.Fatal("restart automatically downloaded", extra)
	default:
	}
}

func TestDownloadConcurrencyValidationMigrationAndSaveFailure(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "state.enc")
	if e := writeState(profile, diskState{Schema: 1, Settings: TransferSettings{UploadConcurrency: 3}}); e != nil {
		t.Fatal(e)
	}
	m, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	original := m.Snapshot().Settings
	if original.DownloadConcurrency != 1 {
		t.Fatal("legacy settings must default to serial")
	}
	for _, invalid := range []int{-1, 0, 9} {
		s := original
		s.DownloadConcurrency = invalid
		if m.SetTransferSettings(s) == nil {
			t.Fatal("invalid concurrency", invalid)
		}
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if e = os.WriteFile(blocked, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	m.path = filepath.Join(blocked, "state.enc")
	s := original
	s.DownloadConcurrency = 8
	if m.SetTransferSettings(s) == nil || m.Snapshot().Settings != original {
		t.Fatal("failed save changed settings")
	}
	// Immediate legacy transfers retain their exclusive slot even with an
	// increased queue limit. No queued worker is allowed past this guard.
	m.downloadImmediate = true
	m.downloads = []DownloadJob{{ID: "pending", Status: "queued"}}
	m.mu.Lock()
	m.dispatchDownloadsLocked()
	m.mu.Unlock()
	if len(m.downloadActive) != 0 || m.downloads[0].Status != "queued" {
		t.Fatal("queue overlapped immediate download")
	}
	m.downloadImmediate = false
	m.downloadActive["hold"] = context.CancelFunc(func() {})
}
