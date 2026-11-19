package transfer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type treeEntry struct {
	folder bool
	data   []byte
}
type directoryFixture struct {
	mu                          sync.Mutex
	mode                        string
	src, dst                    map[string]treeEntry
	creates, puts, sourceWrites int
	targetLists                 int
	m                           *Manager
	badPath                     string
	declaredSize                int64
}

func newDirectoryFixture(t *testing.T, mode string) (*directoryFixture, *Manager, RelayInput) {
	t.Helper()
	f := &directoryFixture{mode: mode, src: map[string]treeEntry{"/": {folder: true}, "/tree": {folder: true}, "/tree/empty": {folder: true}, "/tree/sub": {folder: true}, "/tree/file.txt": {data: []byte("original")}, "/tree/sub/中文 +.txt": {data: []byte("中文\x00原件")}}, dst: map[string]treeEntry{"/": {folder: true}}, badPath: t.TempDir()}
	switch mode {
	case "target-conflict":
		f.dst["/tree"] = treeEntry{folder: true}
	case "empty-file":
		f.src["/tree/zero"] = treeEntry{data: []byte{}}
	case "file-limit":
		for i := 0; i < 21; i++ {
			f.src[fmt.Sprintf("/tree/f%d", i)] = treeEntry{data: []byte("x")}
		}
	case "directory-limit":
		for i := 0; i < 20; i++ {
			f.src[fmt.Sprintf("/tree/d%d", i)] = treeEntry{folder: true}
		}
	case "depth-limit":
		p := "/tree"
		for i := 0; i < 8; i++ {
			p += "/d"
			f.src[p] = treeEntry{folder: true}
		}
	case "case-conflict":
		f.src["/tree/FILE.txt"] = treeEntry{data: []byte("x")}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	original := http.DefaultTransport
	http.DefaultTransport = tr
	t.Cleanup(func() { http.DefaultTransport = original; tr.CloseIdleConnections(); srv.Close() })
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	f.m = m
	m.accounts = []Account{{ID: "s", Name: "source", Kind: "webdav", Endpoint: srv.URL + "/src", Username: "src", Secret: "source-private"}, {ID: "t", Name: "target", Kind: "ctdav", Endpoint: srv.URL + "/dst", Username: "dst", Secret: "target-private"}}
	t.Cleanup(m.Close)
	return f, m, RelayInput{SourceAccountID: "s", TargetAccountID: "t", ParentID: "/", EntryID: "/tree", Name: "tree", DestinationID: "/"}
}
func (f *directoryFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source := strings.HasPrefix(r.URL.Path, "/src/")
	prefix := "/dst"
	entries := f.dst
	user, secret := "dst", "target-private"
	if source {
		prefix = "/src"
		entries = f.src
		user, secret = "src", "source-private"
	}
	u, p, ok := r.BasicAuth()
	if !ok || u != user || p != secret || r.Header.Get("Cookie") != "" {
		w.WriteHeader(401)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if id == "" {
		id = "/"
	}
	if source && r.Method != "PROPFIND" && r.Method != "GET" {
		f.sourceWrites++
		w.WriteHeader(405)
		return
	}
	switch r.Method {
	case "PROPFIND":
		if source && id == "/tree" && f.mode == "stopped-preflight" {
			_ = f.m.PauseRelays()
			f.mode = "success"
		}
		if source && id == "/tree" && f.mode == "source-config-changed" {
			f.m.mu.Lock()
			f.m.accounts[0].Secret = "different-source"
			f.m.mu.Unlock()
		}
		if !source && f.mode == "target-config-changed" {
			f.m.mu.Lock()
			f.m.accounts[1].Secret = "different-target"
			f.m.mu.Unlock()
		}
		if !source {
			f.targetLists++
			if f.mode == "checkpoint-failed" && f.targetLists == 2 {
				f.m.mu.Lock()
				f.m.path = f.badPath
				f.m.mu.Unlock()
			}
			if f.mode == "read-failed" && f.creates > 0 {
				w.WriteHeader(503)
				return
			}
			if f.mode == "final-save-failed" && f.creates > 0 {
				f.m.mu.Lock()
				f.m.path = f.badPath
				f.m.mu.Unlock()
			}
		}
		if row, ok := entries[id]; !ok || !row.folder {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(207)
		fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
		for name, row := range entries {
			if name != id && path.Dir(name) != id {
				continue
			}
			if name == "/" && id != "/" {
				continue
			}
			href := (&url.URL{Path: prefix + name}).EscapedPath()
			if row.folder && !strings.HasSuffix(href, "/") {
				href += "/"
			}
			fmt.Fprint(w, `<d:response><d:href>`)
			xml.EscapeText(w, []byte(href))
			fmt.Fprint(w, `</d:href><d:propstat><d:prop>`)
			if f.mode != "missing-type" || name != "/tree/file.txt" {
				if row.folder {
					fmt.Fprint(w, `<d:resourcetype><d:collection/></d:resourcetype>`)
				} else {
					fmt.Fprint(w, `<d:resourcetype/>`)
				}
			}
			if !row.folder {
				size := int64(len(row.data))
				if f.declaredSize > 0 {
					size = f.declaredSize
				}
				tag := "&quot;v1&quot;"
				if f.mode == "weak" {
					tag = "W/" + tag
				}
				if f.mode == "source-changed" && f.creates > 0 {
					tag = "&quot;v2&quot;"
				}
				fmt.Fprintf(w, `<d:getcontentlength>%d</d:getcontentlength><d:getetag>%s</d:getetag>`, size, tag)
			}
			fmt.Fprint(w, `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
		}
		if source && id == "/tree" && f.mode == "inaccessible" {
			fmt.Fprint(w, `<d:response><d:href>/src/tree/hidden</d:href><d:propstat><d:prop/><d:status>HTTP/1.1 403 Forbidden</d:status></d:propstat></d:response>`)
		}
		fmt.Fprint(w, `</d:multistatus>`)
	case "MKCOL":
		f.creates++
		saved, e := readState(f.m.path)
		if e != nil {
			w.WriteHeader(500)
			return
		}
		durable := false
		for _, d := range saved.Relays {
			if d.Folder && path.Join(d.Input.DestinationID, d.Input.Name) == id && d.Submitted && !d.CreatedAck {
				durable = true
			}
		}
		if !durable || r.Header.Get("If-None-Match") != "*" || !strings.HasSuffix(r.URL.Path, "/") {
			w.WriteHeader(428)
			return
		}
		if _, exists := entries[id]; exists {
			w.WriteHeader(405)
			return
		}
		if parent, exists := entries[path.Dir(id)]; !exists || !parent.folder {
			w.WriteHeader(409)
			return
		}
		entries[id] = treeEntry{folder: true}
		if f.mode == "stopped" {
			_ = f.m.PauseRelays()
			w.WriteHeader(201)
			return
		}
		if f.mode == "ack-save-failed" {
			f.m.mu.Lock()
			f.m.path = f.badPath
			f.m.mu.Unlock()
		}
		if f.mode == "ack-lost" {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(201)
	case "PUT":
		f.puts++
		if r.Header.Get("If-None-Match") != "*" {
			w.WriteHeader(428)
			return
		}
		if _, exists := entries[id]; exists {
			w.WriteHeader(412)
			return
		}
		if parent, exists := entries[path.Dir(id)]; !exists || !parent.folder {
			w.WriteHeader(409)
			return
		}
		data, e := io.ReadAll(r.Body)
		if e != nil {
			w.WriteHeader(400)
			return
		}
		entries[id] = treeEntry{data: data}
		w.WriteHeader(201)
	case "GET":
		row, ok := entries[id]
		if !ok || row.folder {
			w.WriteHeader(404)
			return
		}
		if source {
			if r.Header.Get("If-Match") != `"v1"` {
				w.WriteHeader(412)
				return
			}
			w.Header().Set("ETag", `"v1"`)
		}
		w.Write(row.data)
	default:
		w.WriteHeader(405)
	}
}

func TestRelayDirectoryPreflight(t *testing.T) {
	for _, mode := range []string{"weak", "inaccessible", "missing-type", "file-limit", "directory-limit", "depth-limit", "case-conflict", "target-conflict", "overlap", "wrong-provider", "cancelled", "queue-save-failed", "source-config-changed", "target-config-changed"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newDirectoryFixture(t, mode)
			ctx := context.Background()
			switch mode {
			case "overlap":
				m.accounts[1].Endpoint = m.accounts[0].Endpoint
			case "wrong-provider":
				m.accounts[1].Kind = "pan123"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "queue-save-failed":
				m.path = f.badPath
			}
			if _, e := m.QueueRelayDirectory(ctx, in); e == nil {
				t.Fatal("invalid tree accepted", mode)
			}
			if len(m.RelayJobs()) != 0 {
				t.Fatal("partial manifest queued")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creates != 0 || f.puts != 0 || f.sourceWrites != 0 {
				t.Fatal("preflight wrote to cloud")
			}
		})
	}
}

func TestRelayDirectoryCopyAndRecovery(t *testing.T) {
	for _, mode := range []string{"success", "empty-file", "checkpoint-failed", "ack-lost", "read-failed", "ack-save-failed", "final-save-failed", "source-changed", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newDirectoryFixture(t, mode)
			statePath := m.path
			n, e := m.QueueRelayDirectory(context.Background(), in)
			if e != nil || n < 5 {
				t.Fatal(n, e)
			}
			jobs := awaitRelay(t, m)
			if mode == "success" || mode == "empty-file" {
				for _, j := range jobs {
					if j.State != "verified" {
						t.Fatal(j.State, j.Message)
					}
					if j.Folder && j.SHA256 != "" {
						t.Fatal("directory claims content hash")
					}
				}
				f.mu.Lock()
				for p, src := range f.src {
					if p == "/" {
						continue
					}
					dst, ok := f.dst[p]
					if !ok || dst.folder != src.folder || string(dst.data) != string(src.data) {
						t.Error("tree mismatch", p)
					}
				}
				if f.sourceWrites != 0 {
					t.Error("source modified")
				}
				f.mu.Unlock()
				if jobs[0].CanClear {
					t.Fatal("parent cleared before children")
				}
				if e = m.ClearRelay(jobs[0].ID); e == nil {
					t.Fatal("parent clear allowed")
				}
				m.Close()
				restored, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer restored.Close()
				for _, j := range restored.RelayJobs() {
					if j.State != "verified" {
						t.Fatal("confirmed tree lost")
					}
				}
				for i := len(jobs) - 1; i >= 0; i-- {
					if e = restored.ClearRelay(jobs[i].ID); e != nil {
						t.Fatal("leaf-first clear", e)
					}
				}
				return
			}
			expected := "unknown"
			if mode == "checkpoint-failed" {
				expected = "failed"
			}
			if mode == "source-changed" {
				expected = "verified"
			}
			if jobs[0].State != expected {
				t.Fatal(mode, jobs[0].State, jobs[0].Message)
			}
			if mode != "source-changed" {
				for _, j := range jobs[1:] {
					if j.State != "paused" || j.CanResume {
						t.Fatal("child bypassed unknown parent", j.State)
					}
				}
			}
			f.mu.Lock()
			writes := f.creates
			if mode == "checkpoint-failed" && writes != 0 {
				t.Error("checkpoint failure created directory")
			}
			if f.puts != 0 {
				t.Error("unexpected file upload")
			}
			f.mode = "success"
			f.mu.Unlock()
			m.mu.Lock()
			m.path = statePath
			m.mu.Unlock()
			if expected == "unknown" {
				if e = m.ResumeRelay(jobs[0].ID); e == nil {
					t.Fatal("unknown directory repeated")
				}
				if e = m.ClearRelay(jobs[0].ID); e == nil {
					t.Fatal("unknown directory cleared")
				}
				m.Close()
				restored, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer restored.Close()
				if e = restored.CheckRelay(jobs[0].ID); e != nil {
					t.Fatal(e)
				}
				checked := awaitRelay(t, restored)
				hasAck := mode == "read-failed" || mode == "final-save-failed"
				if hasAck && checked[0].State != "verified" || !hasAck && checked[0].State != "unknown" {
					t.Fatal("ack proof ignored", mode, checked[0].State)
				}
				f.mu.Lock()
				if f.creates != writes || f.puts != 0 {
					t.Error("read-only check wrote")
				}
				f.mu.Unlock()
				if hasAck {
					for step := 0; step < len(checked); step++ {
						progress := false
						for _, j := range restored.RelayJobs()[1:] {
							if j.CanResume {
								if e = restored.ResumeRelay(j.ID); e != nil {
									t.Fatal(e)
								}
								awaitRelay(t, restored)
								progress = true
							}
						}
						if !progress {
							break
						}
					}
					for _, j := range restored.RelayJobs() {
						if j.State != "verified" {
							t.Fatal("child did not resume after parent confirmation", j.State, j.Message)
						}
					}
				}
			}
			raw, _ := os.ReadFile(statePath)
			if strings.Contains(string(raw), "source-private") || strings.Contains(string(raw), "target-private") {
				t.Fatal("unencrypted tree state")
			}
		})
	}
}

func TestRelayDirectoryDamagedState(t *testing.T) {
	_, m, in := newDirectoryFixture(t, "success")
	if _, e := m.QueueRelayDirectory(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	awaitRelay(t, m)
	m.Close()
	saved, e := readState(m.path)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"orphan", "forward", "wrong-path", "wrong-scope", "wrong-provider", "file-ack", "folder-flag", "confirmed-no-ack"} {
		t.Run(mode, func(t *testing.T) {
			s := saved
			s.Relays = append([]diskRelay(nil), saved.Relays...)
			switch mode {
			case "orphan":
				s.Relays[1].ParentJobID = "missing"
			case "forward":
				s.Relays[0].ParentJobID = s.Relays[1].Job.ID
			case "wrong-path":
				s.Relays[1].Input.DestinationID = "/elsewhere"
				s.Relays[1].Job.Destination = "/elsewhere"
			case "wrong-scope":
				s.Relays[1].SourceScope = strings.Repeat("b", 64)
			case "wrong-provider":
				s.Relays[0].TargetKind = "pan123"
			case "file-ack":
				for i := range s.Relays {
					if !s.Relays[i].Folder {
						s.Relays[i].CreatedAck = true
						break
					}
				}
			case "folder-flag":
				s.Relays[0].Job.Folder = false
			case "confirmed-no-ack":
				s.Relays[0].CreatedAck = false
			}
			p := filepath.Join(t.TempDir(), "damaged.enc")
			if e = writeState(p, s); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(p)
			if restored, err := NewManager(p, nil); err == nil {
				restored.Close()
				t.Fatal("invalid tree restored", mode)
			}
			after, _ := os.ReadFile(p)
			if string(before) != string(after) {
				t.Fatal("damaged state overwritten")
			}
		})
	}
}

func TestRelaySnapshotLimitsAndCancellation(t *testing.T) {
	for _, kind := range []string{"quark", "guangya"} {
		size := int64(2 << 30)
		row := CloudEntry{ID: "file", Size: &size}
		if kind == "quark" {
			row.revision = "123"
		}
		if !relaySnapshotEligible(kind, row) {
			t.Fatal(kind, "a 2 GiB cloud original should be eligible")
		}
		size++
		if relaySnapshotEligible(kind, row) {
			t.Fatal(kind, "a file over 2 GiB must be rejected")
		}
	}
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	ctx, finish := m.beginRelaySnapshotLocked(context.Background())
	m.mu.Unlock()
	defer finish()
	if m.RelayPreflight() == nil {
		t.Fatal("snapshot preflight must be visible before content reads")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 175*time.Minute {
		t.Fatal("large preflight inherited the former short timeout")
	}
	reached := make(chan struct{})
	result := make(chan error, 1)
	a := Account{Kind: "quark", Endpoint: QuarkAPI}
	client := &http.Client{Transport: quarkTransport(func(req *http.Request) (*http.Response, error) {
		close(reached)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	go func() {
		result <- readRelaySnapshotRows(ctx, client, a, []diskRelay{{Input: RelayInput{ParentID: "0", EntryID: "file", Name: "file.bin"}}}, m.updateRelayPreflight)
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture did not start reading")
	}
	if err := m.PauseRelays(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "已停止") {
			t.Fatal("canceled preflight reported the wrong result", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not cancel the active read")
	}
	if len(m.RelayJobs()) != 0 {
		t.Fatal("canceling preflight created a job")
	}
}

func TestRelayStopDuringPreflight(t *testing.T) {
	for _, directory := range []bool{true, false} {
		t.Run(fmt.Sprint(directory), func(t *testing.T) {
			f, m, in := newDirectoryFixture(t, "stopped-preflight")
			var e error
			if directory {
				_, e = m.QueueRelayDirectory(context.Background(), in)
			} else {
				in.ParentID = "/tree"
				in.EntryID = "/tree/file.txt"
				in.Name = "file.txt"
				in.Size = int64(len(f.src[in.EntryID].data))
				_, e = m.QueueRelays(context.Background(), []RelayInput{in})
			}
			if e == nil {
				t.Fatal("stop during read-only preflight still created a queue")
			}
			if len(m.RelayJobs()) != 0 {
				t.Fatal("stopped preflight saved partial work")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creates != 0 || f.puts != 0 {
				t.Fatal("stopped preflight wrote target")
			}
		})
	}
}
