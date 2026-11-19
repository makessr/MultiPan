package transfer

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyReceiptsPersistAndCheckNeverReplays(t *testing.T) {
	for _, mode := range []string{"success", "pending", "partial-tree", "extra-target", "source-tree-change", "final-save-failed", "initial-save-failed", "checkpoint-failed"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			a := Account{ID: "a", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "u", Secret: "fixture"}
			m.accounts = []Account{a}
			resources := map[string]bool{"/": true, "/source": true, "/dest": true, "/source/item": true, "/source/item/empty": true, "/source/item/sub": true, "/source/item/sub/file.txt": false}
			writes := 0
			complete := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/base"), "/")
				if p == "" {
					p = "/"
				}
				if r.Method == "COPY" {
					writes++
					if mode != "pending" {
						for id, folder := range resources {
							if id == "/source/item" || strings.HasPrefix(id, "/source/item/") {
								resources["/dest/item"+strings.TrimPrefix(id, "/source/item")] = folder
							}
						}
						complete = true
					}
					if mode == "extra-target" {
						resources["/dest/item/extra"] = false
					}
					if mode == "source-tree-change" {
						resources["/source/item/new"] = false
					}
					if mode == "final-save-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "partial-tree" {
						delete(resources, "/dest/item/sub/file.txt")
					}
					w.WriteHeader(201)
					return
				}
				if r.Method != "PROPFIND" {
					t.Error("unexpected mutation", r.Method)
					w.WriteHeader(400)
					return
				}
				if mode == "checkpoint-failed" && !complete {
					m.mu.Lock()
					m.path = t.TempDir()
					m.mu.Unlock()
				}
				w.WriteHeader(207)
				fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
				for id, folder := range resources {
					if id != p && path.Dir(id) != p {
						continue
					}
					u := url.URL{Scheme: "https", Host: "dav.fixture", Path: "/base" + id}
					res := ""
					if folder {
						u.Path += "/"
						res = "<d:collection/>"
					}
					fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype><d:getcontentlength>12</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, html.EscapeString(u.String()), res)
				}
				fmt.Fprint(w, `</d:multistatus>`)
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			old := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = old }()
			row := MoveBatchEntry{Folder: true, Input: MoveFileInput{AccountID: "a", ParentID: "/source", DestinationID: "/dest", EntryID: "/source/item", Name: "item"}}
			if mode == "initial-save-failed" {
				m.path = t.TempDir()
			}
			_, e = m.CopyEntry(context.Background(), row)
			if mode == "initial-save-failed" {
				if e == nil || writes != 0 || len(m.CopyHistory(a.ID)) != 0 {
					t.Fatal("initial save failure wrote", e, writes)
				}
				return
			}
			rows := m.CopyHistory(a.ID)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			want := "unknown"
			if mode == "success" {
				want = "confirmed"
			}
			if mode == "checkpoint-failed" {
				want = "not_sent"
			}
			if rows[0].State != want {
				t.Fatal(mode, rows, e)
			}
			expected := 1
			if mode == "checkpoint-failed" {
				expected = 0
			}
			if writes != expected {
				t.Fatal("wrong copy count", writes)
			}
			if _, e = m.CopyEntry(context.Background(), row); e == nil || writes != expected {
				t.Fatal("duplicate replayed", e, writes)
			}
			m.path = statePath
			m.Close()
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			rows = m.CopyHistory(a.ID)
			if len(rows) != 1 || rows[0].State != want {
				t.Fatal("receipt lost", rows)
			}
			if want == "unknown" {
				if n, e := m.ClearCopyHistory(a.ID); e != nil || n != 0 {
					t.Fatal("unknown cleared", n, e)
				}
				checked, e := m.CheckCopy(context.Background(), rows[0].ID)
				if e != nil || checked.State != map[bool]string{true: "confirmed", false: "unknown"}[mode == "final-save-failed"] || writes != expected {
					t.Fatal("check replayed or false confirmation", checked, e, writes)
				}
				delete(resources, "/dest/item/extra")
				delete(resources, "/source/item/new")
				resources["/dest/item"] = true
				resources["/dest/item/sub"] = true
				resources["/dest/item/empty"] = true
				resources["/dest/item/sub/file.txt"] = false
				checked, e = m.CheckCopy(context.Background(), rows[0].ID)
				if e != nil || checked.State != "confirmed" || writes != expected {
					t.Fatal("readonly reconciliation", checked, e, writes)
				}
			}
			m.accounts[0].Secret = "changed"
			if _, e = m.CheckCopy(context.Background(), rows[0].ID); e == nil {
				t.Fatal("changed account reused receipt")
			}
			if m.CopyHistory(a.ID)[0].CanCheck {
				t.Fatal("changed account exposed check")
			}
			m.accounts[0] = a
			m.path = t.TempDir()
			if n, e := m.ClearCopyHistory(a.ID); e == nil || n != 0 || len(m.CopyHistory(a.ID)) != 1 {
				t.Fatal("failed clear lost record")
			}
			m.path = statePath
			if n, e := m.ClearCopyHistory(a.ID); e != nil || n != 1 {
				t.Fatal("clear completed", n, e)
			}
		})
	}
}

func TestCopyManifestRejectsInvalidPersistedSnapshot(t *testing.T) {
	size := int64(0)
	d := diskCopy{Input: MoveBatchEntry{Input: MoveFileInput{AccountID: "a", ParentID: "/s", DestinationID: "/d", EntryID: "/s/f", Name: "f"}}, Source: CloudEntry{ID: "/s/f", Name: "f", Size: &size}, Manifest: []CopyManifestItem{{Size: 0}}}
	if !validCopyManifest(d) {
		t.Fatal("zero byte manifest rejected")
	}
	d.Manifest = append(d.Manifest, CopyManifestItem{Path: "../escape"})
	if validCopyManifest(d) {
		t.Fatal("invalid manifest accepted")
	}
}

func TestCopyManifestLimits(t *testing.T) {
	for _, mode := range []string{"count", "depth"} {
		t.Run(mode, func(t *testing.T) {
			a := Account{ID: "a", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "u", Secret: "fixture"}
			f, tr := folderMoveTransport(t, a, "/目标", "success")
			root := "/来源/资料 & 备份"
			if mode == "count" {
				for i := 0; i < 501; i++ {
					f.resources[fmt.Sprintf("%s/file-%d", root, i)] = false
				}
			} else {
				next := root
				for i := 0; i < 22; i++ {
					next += "/deep"
					f.resources[next] = true
				}
			}
			_, e := copyManifest(context.Background(), &http.Client{Transport: tr}, a, CloudEntry{ID: root, Name: path.Base(root), Folder: true})
			if e == nil || f.writes != 0 {
				t.Fatal("manifest bound not enforced", e, f.writes)
			}
		})
	}
}

func TestCopyReceiptCrashStatesAndCorruption(t *testing.T) {
	in := MoveBatchEntry{Input: MoveFileInput{AccountID: "a", ParentID: "/s", DestinationID: "/d", EntryID: "/s/f", Name: "f"}}
	d := diskCopy{Scope: "scope", Input: in, Receipt: CopyReceipt{ID: "receipt", AccountID: "a", Name: "f", Destination: "/d", State: "working", Created: time.Now()}}
	m := &Manager{}
	if e := m.restoreCopies([]diskCopy{d}); e != nil || m.copies["receipt"].saved.Receipt.State != "not_sent" {
		t.Fatal("unsubmitted crash", e)
	}
	size := int64(0)
	d.Submitted = true
	d.Source = CloudEntry{ID: "/s/f", Name: "f", Size: &size}
	d.Manifest = []CopyManifestItem{{Size: 0}}
	if e := m.restoreCopies([]diskCopy{d}); e != nil || m.copies["receipt"].saved.Receipt.State != "unknown" {
		t.Fatal("submitted crash", e)
	}
	if e := m.restoreCopies([]diskCopy{d, d}); e == nil {
		t.Fatal("duplicate disk receipt accepted")
	}
	d.Manifest[0].Size = -1
	if e := m.restoreCopies([]diskCopy{d}); e == nil {
		t.Fatal("corrupt snapshot accepted")
	}
}
