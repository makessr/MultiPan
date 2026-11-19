package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"testing"
	"time"
)

func TestCloudSearchAcrossProviderRootsAndLogicalPaths(t *testing.T) {
	for _, kind := range []string{"lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav", "ctfile"} {
		t.Run(kind, func(t *testing.T) {
			a := Account{ID: "a", Kind: kind}
			root := cloudRoot(kind)
			dir := "10"
			first, last, empty := "1", "99", "20"
			isPath := kind == "baidu" || kind == "ctdav" || kind == "webdav"
			if isPath {
				dir = "/docs"
				first = "/Report-root.txt"
				last = "/docs/report-child.txt"
				empty = "/docs/empty"
			}
			tree := map[string][]CloudEntry{root: {{ID: first, Name: "Report-root.txt"}, {ID: dir, Name: "docs", Folder: true}}, dir: {{ID: last, Name: "report-child.txt"}, {ID: empty, Name: "empty", Folder: true}}, empty: {}}
			list := func(ctx context.Context, a Account, id string) (FilePage, error) {
				return FilePage{CurrentID: id, Entries: tree[id]}, nil
			}
			run := func(query string, include bool) CloudSearchStatus {
				return scanCloudSearch(context.Background(), []Account{a}, CloudSearchInput{Query: query, IncludePath: include}, list, func(CloudSearchStatus) {})
			}
			got := run("REPORT", false)
			if got.State != "completed" || len(got.Items) != 2 || got.Directories != 3 || got.Scanned != 4 {
				t.Fatal(got)
			}
			if got.Items[1].ParentID != dir || len(got.Items[1].Trail) != 2 || got.Items[1].Trail[1].Name != "docs" {
				t.Fatal("logical parent trail lost", got.Items[1])
			}
			if len(run("docs", false).Items) != 1 || len(run("docs", true).Items) != 3 {
				t.Fatal("path matching scope incorrect")
			}
			if len(run("/docs/report-child.txt", true).Items) != 1 {
				t.Fatal("normalized path search failed")
			}
		})
	}
}

func TestCloudSearchPartialCancelLimitsAndInvalidation(t *testing.T) {
	a := Account{ID: "a", Kind: "quark"}
	for _, mode := range []string{"cycle", "duplicate", "failure", "cancel", "source", "entries", "matches", "directories", "depth"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			list := func(ctx context.Context, a Account, id string) (FilePage, error) {
				p := FilePage{CurrentID: id, Entries: []CloudEntry{}}
				if id == "0" {
					p.Entries = []CloudEntry{{ID: "f", Name: "report"}, {ID: "d", Name: "folder", Folder: true}}
				}
				switch mode {
				case "cycle":
					if id == "d" {
						p.Entries = []CloudEntry{{ID: "0", Name: "alias", Folder: true}}
					}
				case "duplicate":
					if id == "0" {
						p.Entries = append(p.Entries, CloudEntry{ID: "f", Name: "report"})
					}
				case "failure":
					if id == "d" {
						return p, errors.New("fixture directory unreadable")
					}
				case "cancel":
					if id == "d" {
						cancel()
						return p, ctx.Err()
					}
				case "source":
					if id == "d" {
						return p, errSearchSourceChanged
					}
				case "entries", "matches":
					if id == "0" {
						p.Entries = nil
						n := 5001
						if mode == "matches" {
							n = 1001
						}
						for i := 0; i < n; i++ {
							p.Entries = append(p.Entries, CloudEntry{ID: fmt.Sprint(i), Name: "report"})
						}
					}
				case "directories":
					if id == "0" {
						p.Entries = nil
						for i := 0; i < 501; i++ {
							p.Entries = append(p.Entries, CloudEntry{ID: fmt.Sprint(i + 1), Name: "folder", Folder: true})
						}
					}
				case "depth":
					p.Entries = []CloudEntry{{ID: id + "d", Name: "deep", Folder: true}}
				}
				return p, nil
			}
			query := "report"
			if mode == "entries" {
				query = "unmatched"
			}
			s := scanCloudSearch(ctx, []Account{a}, CloudSearchInput{Query: query}, list, func(CloudSearchStatus) {})
			want := "partial"
			switch mode {
			case "depth":
				want = "completed"
			case "cancel":
				want = "cancelled"
			case "source":
				want = "invalidated"
			case "entries", "matches", "directories":
				want = "limited"
			}
			if s.State != want {
				t.Fatal(mode, s)
			}
			if s.Scanned > 5000 || s.Directories > 500 || len(s.Items) > 1000 {
				t.Fatal("scan limits exceeded", s.Scanned, s.Directories, len(s.Items))
			}
			if mode == "source" && len(s.Items) != 0 {
				t.Fatal("invalidated source leaked results")
			}
			if mode == "cancel" && len(s.Items) != 1 {
				t.Fatal("cancel discarded completed partial result")
			}
		})
	}
}

func TestCloudSearchPathEscapeAndAccountIsolation(t *testing.T) {
	list := func(ctx context.Context, a Account, id string) (FilePage, error) {
		if a.ID == "bad" {
			return FilePage{}, errors.New("fixture unavailable")
		}
		return FilePage{CurrentID: id, Entries: []CloudEntry{{ID: "/report", Name: "report"}, {ID: "/outside/report", Name: "report"}}}, nil
	}
	s := scanCloudSearch(context.Background(), []Account{{ID: "bad", Kind: "webdav"}, {ID: "good", Kind: "webdav"}}, CloudSearchInput{Query: "report"}, list, func(CloudSearchStatus) {})
	if s.State != "partial" || len(s.Items) != 1 || s.Items[0].AccountID != "good" || s.Items[0].ParentID != "/" {
		t.Fatal(s)
	}
}

func TestCloudSearchManagerCancellationAndSourceChange(t *testing.T) {
	for _, mode := range []string{"cancel", "source", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/1/clouddrive/file/sort" {
					t.Error("unexpected search request", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("pdir_fid") == "10" {
					started <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
					}
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": []any{map[string]any{"fid": "1", "file_name": "report.txt", "size": 12, "pdir_fid": "0"}, map[string]any{"fid": "10", "file_name": "folder", "dir": true, "pdir_fid": "0"}}}})
			}))
			defer server.Close()
			defer close(release)
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			old := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = old }()
			m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.accounts = []Account{{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "session=fixture"}}
			in := CloudSearchInput{AccountIDs: []string{"a"}, Query: "report"}
			status, e := m.StartCloudSearch(in)
			if e != nil {
				t.Fatal(e)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("search did not list child")
			}
			if _, e = m.StartCloudSearch(in); e == nil {
				t.Fatal("concurrent search accepted")
			}
			clone, e := m.CloudSearchStatus(status.ID)
			if e != nil || len(clone.Items) != 1 {
				t.Fatal(clone, e)
			}
			clone.Items[0].Trail[0].Name = "tampered"
			*clone.Items[0].Entry.Size = 99
			fresh, _ := m.CloudSearchStatus(status.ID)
			if fresh.Items[0].Trail[0].Name == "tampered" || *fresh.Items[0].Entry.Size != 12 {
				t.Fatal("mutable snapshot exposed")
			}
			_ = m.CancelCloudSearch("unrelated-id")
			switch mode {
			case "source":
				m.mu.Lock()
				m.accounts[0].Secret = "changed"
				m.mu.Unlock()
				s, _ := m.CloudSearchStatus(status.ID)
				if s.State != "invalidated" || len(s.Items) != 0 {
					t.Fatal(s)
				}
			case "close":
				m.Close()
			default:
				_ = m.CancelCloudSearch(status.ID)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				m.mu.Lock()
				running := m.search.running
				m.mu.Unlock()
				if !running {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("search failed to stop")
				}
				time.Sleep(5 * time.Millisecond)
			}
			s, e := m.CloudSearchStatus(status.ID)
			want := "cancelled"
			if mode == "source" {
				want = "invalidated"
			}
			if e != nil || s.State != want {
				t.Fatal(s, e)
			}
		})
	}
}

func TestCloudSearchInputValidation(t *testing.T) {
	m := &Manager{}
	for _, in := range []CloudSearchInput{{}, {Query: "x", AccountIDs: []string{"missing"}}, {Query: "x", AccountIDs: []string{path.Join("a", "b")}}} {
		if _, e := m.StartCloudSearch(in); e == nil {
			t.Fatal("invalid search accepted")
		}
	}
}

func TestCloudSearchStopsAtTwoSubdirectoryLevels(t *testing.T) {
	for _, kind := range []string{"quark", "webdav"} {
		t.Run(kind, func(t *testing.T) {
			a := Account{ID: "a", Kind: kind}
			root := cloudRoot(kind)
			one, two, three := "one", "two", "three"
			file := func(id, name string) CloudEntry { return CloudEntry{ID: id, Name: name} }
			if kind == "webdav" {
				one = "/one"
				two = "/one/two"
				three = "/one/two/hit-three"
			}
			makeID := func(parent, name string) string {
				if kind == "webdav" {
					return path.Join(parent, name)
				}
				return parent + "-" + name
			}
			tree := map[string][]CloudEntry{
				root: {file(makeID(root, "hit-root"), "hit-root"), {ID: one, Name: "one", Folder: true}},
				one:  {file(makeID(one, "hit-one"), "hit-one"), {ID: two, Name: "two", Folder: true}},
				two:  {file(makeID(two, "hit-two"), "hit-two"), {ID: three, Name: "hit-three", Folder: true}},
			}
			visited := []string{}
			result := scanCloudSearch(context.Background(), []Account{a}, CloudSearchInput{Query: "hit"}, func(ctx context.Context, a Account, id string) (FilePage, error) {
				visited = append(visited, id)
				if id == three {
					t.Fatal("read beyond second subdirectory")
				}
				return FilePage{CurrentID: id, Entries: tree[id]}, nil
			}, func(CloudSearchStatus) {})
			if result.State != "completed" || len(visited) != 3 || len(result.Items) != 3 {
				t.Fatal(result.State, visited, result.Items)
			}
			for _, r := range result.Items {
				if r.Entry.ID == three {
					t.Fatal("third-level directory name included")
				}
			}
		})
	}
}

func TestCloudSearchSwitchesAccountImmediatelyOnFirstMatch(t *testing.T) {
	for _, mode := range []string{"all", "single", "all-with-one-account", "no-match-first", "folder-match"} {
		t.Run(mode, func(t *testing.T) {
			accounts := []Account{{ID: "a", Kind: "quark"}, {ID: "b", Kind: "quark"}}
			in := CloudSearchInput{Query: "hit"}
			if mode == "single" || mode == "all-with-one-account" {
				accounts = accounts[:1]
			}
			in.FirstMatchPerAccount = mode == "all-with-one-account"
			visits := []string{}
			result := scanCloudSearch(context.Background(), accounts, in, func(ctx context.Context, a Account, id string) (FilePage, error) {
				visits = append(visits, a.ID+"/"+id)
				p := FilePage{CurrentID: id}
				if mode == "no-match-first" && a.ID == "a" {
					if id == "0" {
						p.Entries = []CloudEntry{{ID: "child", Name: "docs", Folder: true}}
					}
					return p, nil
				}
				if mode == "folder-match" {
					p.Entries = []CloudEntry{{ID: "child", Name: "hit-folder", Folder: true}, {ID: "other", Name: "hit-second"}}
					return p, nil
				}
				if id == "0" {
					p.Entries = []CloudEntry{{ID: "child", Name: "docs", Folder: true}, {ID: "first", Name: "hit-first"}, {ID: "second", Name: "hit-second"}}
				} else {
					p.Entries = []CloudEntry{{ID: "nested", Name: "hit-nested"}}
				}
				return p, nil
			}, func(CloudSearchStatus) {})
			wantCount, wantVisits := 2, []string{"a/0", "b/0"}
			switch mode {
			case "single":
				wantCount = 3
				wantVisits = []string{"a/0", "a/child"}
			case "all-with-one-account":
				wantCount = 1
				wantVisits = []string{"a/0"}
			case "no-match-first":
				wantCount = 1
				wantVisits = []string{"a/0", "a/child", "b/0"}
			}
			if result.State != "completed" || len(result.Items) != wantCount || fmt.Sprint(visits) != fmt.Sprint(wantVisits) {
				t.Fatal(mode, result.State, len(result.Items), visits)
			}
			if mode == "all" && result.Items[0].Entry.ID != "first" {
				t.Fatal("incorrect first match")
			}
		})
	}
}
