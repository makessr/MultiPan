package transfer

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

type davMoveFixture struct {
	resources     map[string]bool
	writes, reads int
}

func folderMoveTransport(t *testing.T, a Account, destination, mode string) (*davMoveFixture, http.RoundTripper) {
	t.Helper()
	source := "/来源/资料 & 备份"
	f := &davMoveFixture{resources: map[string]bool{"/": true, "/来源": true, "/目标": true, source: true, source + "/子目录": true, source + "/子目录/文件.txt": false}}
	target := path.Join(destination, path.Base(source))
	if mode == "conflict" {
		f.resources[target] = true
	}
	if mode == "source-file" {
		f.resources[source] = false
	}
	base, _ := url.Parse(a.Endpoint)
	transport := quarkTransport(func(r *http.Request) (*http.Response, error) {
		user, password, ok := r.BasicAuth()
		if !ok || user != a.Username || password != a.Secret {
			t.Errorf("wrong synthetic DAV authentication")
		}
		p := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, base.Path), "/")
		if p == "" {
			p = "/"
		}
		if r.Method == "PROPFIND" {
			f.reads++
			if r.Header.Get("Depth") != "1" {
				t.Error("listing depth")
			}
			var body strings.Builder
			body.WriteString(`<d:multistatus xmlns:d="DAV:">`)
			for id, folder := range f.resources {
				if id != p && path.Dir(id) != p {
					continue
				}
				href := *base
				href.Path = base.Path + id
				if folder && !strings.HasSuffix(href.Path, "/") {
					href.Path += "/"
				}
				resource := ""
				if folder {
					resource = "<d:collection/>"
				}
				fmt.Fprintf(&body, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype><d:getcontentlength>12</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, html.EscapeString(href.String()), resource)
			}
			body.WriteString(`</d:multistatus>`)
			res := quarkResponse(body.String(), nil)
			res.StatusCode = 207
			return res, nil
		}
		if r.Method != "MOVE" {
			t.Errorf("unexpected mutation %s", r.Method)
			return nil, errors.New("unexpected request")
		}
		f.writes++
		dst, e := url.Parse(r.Header.Get("Destination"))
		if e != nil || dst.Host != base.Host || dst.Scheme != "https" || dst.Path != base.Path+target+"/" || r.URL.Path != base.Path+source+"/" || r.Header.Get("Overwrite") != "F" {
			t.Error("unsafe collection MOVE request")
		}
		for id, folder := range cloneResources(f.resources) {
			if id == source || strings.HasPrefix(id, source+"/") {
				f.resources[target+strings.TrimPrefix(id, source)] = folder
				if mode != "copy" {
					delete(f.resources, id)
				}
			}
		}
		if mode == "lost" || mode == "copy" || mode == "wrong-type" {
			if mode == "wrong-type" {
				f.resources[target] = false
			}
			return nil, errors.New("synthetic lost acknowledgement")
		}
		res := quarkResponse("", nil)
		res.StatusCode = 201
		if mode == "partial" {
			res.StatusCode = 207
		}
		return res, nil
	})
	return f, transport
}

func cloneResources(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestDAVFolderMoveUsesSingleCollectionMove(t *testing.T) {
	for _, kind := range []string{"webdav", "ctdav"} {
		for _, destination := range []string{"/", "/目标"} {
			for _, mode := range []string{"success", "lost", "copy", "wrong-type", "partial", "conflict", "source-file"} {
				t.Run(kind+destination+mode, func(t *testing.T) {
					a := Account{Kind: kind, Endpoint: "https://dav.example/dav", Username: "u", Secret: "synthetic-password"}
					fixture, transport := folderMoveTransport(t, a, destination, mode)
					in := MoveFileInput{ParentID: "/来源", DestinationID: destination, EntryID: "/来源/资料 & 备份", Name: "资料 & 备份"}
					entry, e := MoveAccountFolder(context.Background(), &http.Client{Transport: transport}, a, in)
					if mode == "success" || mode == "lost" {
						if e != nil || !entry.Folder || entry.ID != path.Join(destination, in.Name) || fixture.writes != 1 || fixture.reads != 4 {
							t.Fatalf("move result %+v %v writes=%d reads=%d", entry, e, fixture.writes, fixture.reads)
						}
						if _, ok := fixture.resources[entry.ID+"/子目录/文件.txt"]; !ok {
							t.Fatal("subtree missing")
						}
						if _, ok := fixture.resources[in.EntryID]; ok {
							t.Fatal("source remains")
						}
					}
					if mode != "success" && mode != "lost" && e == nil {
						t.Fatal("unconfirmed or conflicting move accepted")
					}
					if fixture.writes > 1 {
						t.Fatal("mutation replayed")
					}
					if (mode == "conflict" || mode == "source-file") && fixture.writes != 0 {
						t.Fatal("invalid source/conflict sent a mutation")
					}
				})
			}
		}
	}
}

func TestFolderMoveRejectsUnsafePathsBeforeRequests(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: quarkTransport(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected") })}
	for _, kind := range []string{"webdav", "ctdav", "lanzou", "quark", "pan123", "baidu", "guangya", "ctfile"} {
		for _, destination := range []string{"/来源/资料", "/来源/资料/子", "/来源/../目标"} {
			_, e := MoveAccountFolder(context.Background(), client, Account{Kind: kind}, MoveFileInput{ParentID: "/来源", EntryID: "/来源/资料", Name: "资料", DestinationID: destination})
			if e == nil {
				t.Fatal("unsafe/unsupported folder move accepted")
			}
		}
	}
	if requests != 0 {
		t.Fatal("preflight sent network requests")
	}
}

func TestFolderMoveUpdatesSavedDefaultsAndRetryPaths(t *testing.T) {
	// Use only a local HTTPS server and its trusted test certificate.
	var transport http.RoundTripper
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, e := transport.RoundTrip(r)
		if e != nil {
			w.WriteHeader(500)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	}))
	defer server.Close()
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	state := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Username: "u", Secret: "synthetic-password", Folder: "/来源/资料 & 备份/子目录"})
	if e != nil {
		m.Close()
		t.Fatal(e)
	}
	a.Secret = "synthetic-password"
	fixture, tr := folderMoveTransport(t, a, "/目标", "success")
	transport = tr
	m.mu.Lock()
	m.tasks = []Task{{ID: "retry", AccountID: a.ID, Status: "failed", Folder: a.Folder}, {ID: "history", AccountID: a.ID, Status: "success", Folder: a.Folder}, {ID: "sibling", AccountID: a.ID, Status: "paused", Folder: "/来源/资料 & 备份2"}}
	m.mu.Unlock()
	_, e = m.MoveFolder(context.Background(), MoveFileInput{AccountID: a.ID, ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/资料 & 备份", Name: "资料 & 备份"})
	m.Close()
	if e != nil || fixture.writes != 1 {
		t.Fatalf("manager move %v", e)
	}
	loaded, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer loaded.Close()
	s := loaded.Snapshot()
	if s.Accounts[0].Folder != "/目标/资料 & 备份/子目录" || s.Tasks[0].Folder != s.Accounts[0].Folder || s.Tasks[1].Folder != a.Folder || s.Tasks[2].Folder != "/来源/资料 & 备份2" {
		t.Fatal("default/retry/history/sibling persistence mismatch")
	}
	loaded.mu.Lock()
	loaded.tasks[0].Status = "queued"
	loaded.mu.Unlock()
	if _, e = loaded.MoveFolder(context.Background(), MoveFileInput{AccountID: a.ID}); e == nil {
		t.Fatal("folder move allowed during queued upload")
	}
}
