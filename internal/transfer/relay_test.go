package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

type relayFixture struct {
	mu                      sync.Mutex
	mode                    string
	puts, gets, sourceLists int
	data                    []byte
	declaredSize            int64
	target                  map[string][]byte
	m                       *Manager
	badPath                 string
	entered                 chan struct{}
	release                 chan struct{}
}

func newRelayFixture(t *testing.T, mode string) (*relayFixture, *Manager, RelayInput) {
	t.Helper()
	f := &relayFixture{mode: mode, data: []byte("relay original\x00内容"), target: map[string][]byte{}, entered: make(chan struct{}, 1), release: make(chan struct{}), badPath: t.TempDir()}
	server := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	original := http.DefaultTransport
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	t.Cleanup(func() { close(f.release); server.Close() })
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	f.m = m
	m.accounts = []Account{{ID: "source", Name: "源盘", Kind: "webdav", Endpoint: server.URL + "/src", Username: "source-user", Secret: "source-password"}, {ID: "target", Name: "目标盘", Kind: "ctdav", Endpoint: server.URL + "/dst", Username: "target-user", Secret: "target-password"}}
	t.Cleanup(m.Close)
	return f, m, RelayInput{SourceAccountID: "source", ParentID: "/", EntryID: "/one.txt", Name: "one.txt", Size: int64(len(f.data)), TargetAccountID: "target", DestinationID: "/"}
}
func (f *relayFixture) serve(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	isSource := strings.HasPrefix(r.URL.Path, "/src/")
	expectedUser, expectedPass := "target-user", "target-password"
	if isSource {
		expectedUser, expectedPass = "source-user", "source-password"
	}
	if !ok || user != expectedUser || pass != expectedPass {
		w.WriteHeader(401)
		return
	}
	f.mu.Lock()
	mode := f.mode
	if r.Method == "PROPFIND" {
		size := int64(len(f.data))
		if f.declaredSize > 0 {
			size = f.declaredSize
		}
		if isSource {
			f.sourceLists++
		}
		lists := f.sourceLists
		if mode == "checkpoint-save-failed" && isSource && lists == 3 {
			f.m.mu.Lock()
			f.m.path = f.badPath
			f.m.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(207)
		fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, r.URL.Path)
		if isSource {
			tag := "&quot;version|1&quot;"
			if mode == "weak" {
				tag = "W/&quot;version1&quot;"
			}
			if mode == "source-changed" && lists >= 3 {
				tag = "&quot;version2&quot;"
			}
			for _, name := range []string{"one.txt", "two.txt"} {
				fmt.Fprintf(w, `<d:response><d:href>/src/%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getetag>%s</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, name, size, tag)
			}
		} else {
			for name, data := range f.target {
				fmt.Fprintf(w, `<d:response><d:href>/dst/%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, name, len(data))
			}
		}
		fmt.Fprint(w, `</d:multistatus>`)
		f.mu.Unlock()
		return
	}
	if r.Method == "GET" && isSource {
		f.gets++
		f.mu.Unlock()
		if r.Header.Get("If-Match") != `"version|1"` {
			w.WriteHeader(412)
			return
		}
		if mode == "blocked-download" {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-f.release:
			}
		}
		tag := `"version|1"`
		if mode == "wrong-etag" {
			tag = `"changed"`
		}
		w.Header().Set("ETag", tag)
		if mode == "short-download" {
			w.Write(f.data[:1])
		} else {
			w.Write(f.data)
		}
		return
	}
	if r.Method == "PUT" && !isSource {
		f.puts++
		if r.Header.Get("If-None-Match") != "*" {
			f.mu.Unlock()
			w.WriteHeader(428)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/dst/")
		if _, exists := f.target[name]; exists {
			f.mu.Unlock()
			w.WriteHeader(412)
			return
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			f.mu.Unlock()
			w.WriteHeader(400)
			return
		}
		if mode == "mismatch" || mode == "batch-stop" {
			body = []byte(strings.Repeat("x", len(f.data)))
		}
		f.target[name] = body
		if mode == "final-save-failed" {
			f.m.mu.Lock()
			f.m.path = f.badPath
			f.m.mu.Unlock()
		}
		if mode == "blocked-upload" {
			f.mu.Unlock()
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-f.release:
			}
			w.WriteHeader(201)
			return
		}
		f.mu.Unlock()
		if mode == "ack-lost" {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(201)
		}
		return
	}
	if r.Method == "GET" && !isSource {
		body, exists := f.target[strings.TrimPrefix(r.URL.Path, "/dst/")]
		f.mu.Unlock()
		if mode == "read-unavailable" {
			w.WriteHeader(503)
			return
		}
		if !exists {
			w.WriteHeader(404)
			return
		}
		w.Write(body)
		return
	}
	f.mu.Unlock()
	w.WriteHeader(405)
}
func awaitRelay(t *testing.T, m *Manager) []RelayJob {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		active := m.relayActive
		m.mu.Unlock()
		if active == "" {
			return m.RelayJobs()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("relay worker timeout")
	return nil
}
func TestRelayProtocolAndDurableRecovery(t *testing.T) {
	for _, mode := range []string{"success", "empty", "ack-lost", "mismatch", "read-unavailable", "wrong-etag", "short-download", "source-changed", "checkpoint-save-failed", "final-save-failed"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newRelayFixture(t, mode)
			if mode == "empty" {
				f.data = []byte{}
				in.Size = 0
			}
			originalPath := m.path
			n, e := m.QueueRelays(context.Background(), []RelayInput{in})
			if e != nil || n != 1 {
				t.Fatalf("queue %d %v", n, e)
			}
			jobs := awaitRelay(t, m)
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			j := jobs[0]
			expected := "verified"
			puts := 1
			switch mode {
			case "wrong-etag", "short-download", "source-changed", "checkpoint-save-failed":
				expected = "failed"
				puts = 0
			case "mismatch", "read-unavailable", "final-save-failed":
				expected = "unknown"
			}
			if j.State != expected {
				t.Fatalf("state=%s expected %s: %s", j.State, expected, j.Message)
			}
			f.mu.Lock()
			actual := f.puts
			f.mu.Unlock()
			if actual != puts {
				t.Fatalf("writes %d expected %d", actual, puts)
			}
			if expected == "verified" {
				sum := sha256.Sum256(f.data)
				if j.SHA256 != hex.EncodeToString(sum[:]) || !j.CanClear || j.CanResume || j.CanCheck {
					t.Fatal("missing proof/controls", j)
				}
			}
			if expected == "unknown" {
				if e = m.ResumeRelay(j.ID); e == nil {
					t.Fatal("unknown replay allowed")
				}
				if e = m.ClearRelay(j.ID); e == nil {
					t.Fatal("unknown cleared")
				}
				f.mu.Lock()
				f.mode = "success"
				f.target["one.txt"] = append([]byte{}, f.data...)
				f.mu.Unlock()
				m.mu.Lock()
				m.path = originalPath
				m.mu.Unlock()
				if e = m.CheckRelay(j.ID); e != nil {
					t.Fatal(e)
				}
				if got := awaitRelay(t, m)[0]; got.State != "verified" {
					t.Fatal(got)
				}
				f.mu.Lock()
				if f.puts != 1 {
					t.Error("read-only check repeated PUT", f.puts)
				}
				f.mu.Unlock()
			}
			m.mu.Lock()
			m.path = originalPath
			m.mu.Unlock()
			m.Close()
			reloaded, e := NewManager(originalPath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer reloaded.Close()
			persisted := reloaded.RelayJobs()
			if len(persisted) != 1 {
				t.Fatal("lost receipt")
			}
			if expected == "failed" && persisted[0].State != "paused" {
				t.Fatal("restart must pause", persisted)
			}
		})
	}
}
func TestRelayPreflightAndLimits(t *testing.T) {
	for _, mode := range []string{"weak", "conflict", "duplicate", "save-failed", "oversize", "same-account"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newRelayFixture(t, mode)
			rows := []RelayInput{in}
			switch mode {
			case "conflict":
				f.target["ONE.TXT"] = []byte("foreign")
			case "duplicate":
				rows = append(rows, in)
			case "save-failed":
				m.path = t.TempDir()
			case "oversize":
				rows[0].Size = relayMaxFile + 1
			case "same-account":
				rows[0].TargetAccountID = in.SourceAccountID
			}
			if _, e := m.QueueRelays(context.Background(), rows); e == nil {
				t.Fatal("preflight allowed", mode)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.puts != 0 || f.gets != 0 || len(m.RelayJobs()) != 0 {
				t.Fatal("preflight performed content transfer")
			}
		})
	}
}
func TestRelayStopAndUnknownRestart(t *testing.T) {
	for _, mode := range []string{"blocked-download", "blocked-upload", "batch-stop"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newRelayFixture(t, mode)
			second := in
			second.EntryID = "/two.txt"
			second.Name = "two.txt"
			if _, e := m.QueueRelays(context.Background(), []RelayInput{in, second}); e != nil {
				t.Fatal(e)
			}
			if mode != "batch-stop" {
				select {
				case <-f.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("not entered")
				}
				if e := m.PauseRelays(); e != nil {
					t.Fatal(e)
				}
			}
			jobs := awaitRelay(t, m)
			want := "unknown"
			puts := 1
			if mode == "blocked-download" {
				want = "paused"
				puts = 0
			}
			if jobs[0].State != want || jobs[1].State != "paused" {
				t.Fatal(jobs)
			}
			f.mu.Lock()
			if f.puts != puts {
				t.Error("unexpected writes", f.puts)
			}
			f.mu.Unlock()
			m.Close()
			again, e := NewManager(m.path, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer again.Close()
			got := again.RelayJobs()
			if got[0].State != want {
				t.Fatal("restart state", got)
			}
			if want == "unknown" {
				if e := again.ResumeRelay(got[0].ID); e == nil {
					t.Fatal("replayed unknown")
				}
			}
		})
	}
}
func TestRelayCacheOwnershipAndLockedOriginal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "relay-cache")
	p, e := relayCache(root, "job-1")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := relayOpenCache(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte("changed"), 0600); e == nil {
		t.Error("locked cache writable")
	}
	f.Close()
	foreign := filepath.Join(root, "foreign")
	os.MkdirAll(foreign, 0700)
	os.WriteFile(filepath.Join(foreign, "keep.txt"), []byte("keep"), 0600)
	if _, e = relayCache(root, "foreign"); e == nil {
		t.Fatal("foreign cache adopted")
	}
	if _, e = relaySafePath(root, "../escape"); e == nil {
		t.Fatal("path escaped")
	}
	linked := filepath.Join(root, "linked")
	if e = os.Symlink(t.TempDir(), linked); e == nil {
		if _, e = relayCache(root, "linked"); e == nil {
			t.Fatal("link adopted")
		}
	}
	os.WriteFile(filepath.Join(root, "job-1", ".owner"), []byte("other"), 0600)
	if _, e = relayCache(root, "job-1"); e == nil {
		t.Fatal("wrong owner adopted")
	}
}
func TestRelayMoveRespectsAccountReservation(t *testing.T) {
	_, m, _ := newRelayFixture(t, "success")
	m.renaming = map[string]bool{"source": true}
	if _, e := m.MoveFile(context.Background(), MoveFileInput{AccountID: "source"}); e == nil {
		t.Fatal("move overrode reservation")
	}
	if !m.renaming["source"] {
		t.Fatal("move cleared other reservation")
	}
}

func TestRelayClearAndAccountIsolation(t *testing.T) {
	f, m, in := newRelayFixture(t, "read-unavailable")
	if _, e := m.QueueRelays(context.Background(), []RelayInput{in}); e != nil {
		t.Fatal(e)
	}
	j := awaitRelay(t, m)[0]
	m.mu.Lock()
	m.accounts[1].Secret = "changed-password"
	m.mu.Unlock()
	if got := m.RelayJobs()[0]; got.CanCheck || got.CanResume {
		t.Fatal("changed account controls", got)
	}
	if e := m.CheckRelay(j.ID); e == nil {
		t.Fatal("checked with changed account")
	}
	m.mu.Lock()
	m.accounts[1].Secret = "target-password"
	m.mu.Unlock()
	f.mu.Lock()
	f.mode = "success"
	f.mu.Unlock()
	if e := m.CheckRelay(j.ID); e != nil {
		t.Fatal(e)
	}
	j = awaitRelay(t, m)[0]
	cache := filepath.Join(m.relayRoot(), j.ID, "data.bin")
	if _, e := os.Stat(cache); e != nil {
		t.Fatal(e)
	}
	unknown := filepath.Join(m.relayRoot(), j.ID, "keep.txt")
	os.WriteFile(unknown, []byte("foreign"), 0600)
	original := m.path
	m.path = t.TempDir()
	if e := m.ClearRelay(j.ID); e == nil {
		t.Fatal("save failure cleared receipt")
	}
	if len(m.RelayJobs()) != 1 {
		t.Fatal("receipt rollback failed")
	}
	m.path = original
	if e := m.ClearRelay(j.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(cache); !os.IsNotExist(e) {
		t.Fatal("cache not cleared", e)
	}
	if b, e := os.ReadFile(unknown); e != nil || string(b) != "foreign" {
		t.Fatal("unknown file changed", e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.target) != 1 || f.puts != 1 {
		t.Fatal("cleanup modified cloud")
	}
}

func TestRelayUnsubmittedResumeAndCache(t *testing.T) {
	f, m, in := newRelayFixture(t, "checkpoint-save-failed")
	original := m.path
	if _, e := m.QueueRelays(context.Background(), []RelayInput{in}); e != nil {
		t.Fatal(e)
	}
	j := awaitRelay(t, m)[0]
	if j.State != "failed" {
		t.Fatal(j)
	}
	m.mu.Lock()
	m.path = original
	m.mu.Unlock()
	f.mu.Lock()
	f.mode = "success"
	f.mu.Unlock()
	if e := m.ResumeRelay(j.ID); e != nil {
		t.Fatal(e)
	}
	j = awaitRelay(t, m)[0]
	if j.State != "verified" {
		t.Fatal(j)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gets != 1 || f.puts != 1 {
		t.Fatal("expected complete cache reuse", f.gets, f.puts)
	}
}

func TestRelayRejectsCacheHardlink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache")
	other := filepath.Join(filepath.Dir(p), "other")
	os.WriteFile(p, []byte("original"), 0600)
	if e := os.Link(p, other); e != nil {
		t.Fatal(e)
	}
	if f, e := relayOpenCache(p); e == nil {
		f.Close()
		t.Fatal("hardlinked cache accepted")
	}
}
