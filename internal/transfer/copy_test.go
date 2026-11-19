package transfer

import (
	"context"
	"errors"
	"io"
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

func TestDAVCopyPreservesSourceAndNeverReplays(t *testing.T) {
	for _, kind := range []string{"webdav", "ctdav"} {
		for _, folder := range []bool{false, true} {
			for _, mode := range []string{"success", "conflict", "lost-ack", "partial", "pending", "wrong-type", "source-lost", "descendant"} {
				t.Run(kind+map[bool]string{true: "/folder/", false: "/file/"}[folder]+mode, func(t *testing.T) {
					a := Account{ID: "a", Kind: kind, Endpoint: "https://dav.fixture/base", Username: "u", Secret: "fixture"}
					f, tr := folderMoveTransport(t, a, "/目标", "success")
					source := "/来源/资料 & 备份"
					if !folder {
						source = "/来源/file.txt"
						f.resources[source] = false
					}
					input := MoveFileInput{AccountID: "a", ParentID: "/来源", DestinationID: "/目标", EntryID: source, Name: path.Base(source)}
					target := path.Join(input.DestinationID, input.Name)
					if mode == "conflict" {
						f.resources[target] = folder
					}
					if mode == "descendant" {
						input.DestinationID = source + "/子目录"
					}
					writes := 0
					client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method != "COPY" {
							return tr.RoundTrip(r)
						}
						writes++
						if r.Header.Get("Overwrite") != "F" || r.Header.Get("Depth") != map[bool]string{true: "infinity", false: "0"}[folder] {
							t.Error("unsafe copy headers", r.Header)
						}
						dst, _ := url.Parse(r.Header.Get("Destination"))
						if dst.Host != "dav.fixture" || strings.TrimSuffix(dst.Path, "/") != "/base"+target {
							t.Error("wrong copy destination", dst)
						}
						if mode != "pending" {
							for id, dir := range f.resources {
								if id == source || strings.HasPrefix(id, source+"/") {
									f.resources[target+strings.TrimPrefix(id, source)] = dir
								}
							}
						}
						if mode == "wrong-type" {
							f.resources[target] = !folder
						}
						if mode == "source-lost" {
							delete(f.resources, source)
						}
						if mode == "lost-ack" {
							return nil, errors.New("synthetic lost acknowledgement")
						}
						response := quarkResponse("", nil)
						response.StatusCode = 201
						if mode == "partial" {
							response.StatusCode = 207
						}
						return response, nil
					})}
					result, e := copyAccountEntry(context.Background(), client, a, MoveBatchEntry{Input: input, Folder: folder})
					good := mode == "success" || mode == "lost-ack"
					if (e == nil) != good {
						t.Fatal(mode, result, e)
					}
					want := 1
					if mode == "conflict" || mode == "descendant" {
						want = 0
					}
					if writes != want || f.writes != 0 {
						t.Fatal("wrong write count or MOVE used", writes, f.writes)
					}
					if mode != "source-lost" {
						if _, ok := f.resources[source]; !ok {
							t.Fatal("source removed")
						}
					}
				})
			}
		}
	}
}

func TestBaiduCopyChecksBothLocationsAndPayload(t *testing.T) {
	for _, folder := range []bool{false, true} {
		for _, mode := range []string{"success", "conflict", "lost-ack", "pending", "wrong-size", "source-lost"} {
			t.Run(map[bool]string{true: "folder/", false: "file/"}[folder]+mode, func(t *testing.T) {
				a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture"}
				input := MoveFileInput{AccountID: "a", ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/item", Name: "item"}
				copied := false
				writes := 0
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
						return quarkResponse(`{"errno":0,"result":{"bdstoken":"synthetic-csrf"}}`, nil), nil
					}
					if strings.HasSuffix(r.URL.Path, "/list") {
						rows := []any{}
						dir := r.URL.Query().Get("dir")
						size := 12
						typ := 0
						if folder {
							typ = 1
						}
						if copied && dir == "/目标" && mode == "wrong-size" {
							size = 99
						}
						if dir == "/来源" && !(copied && mode == "source-lost") || dir == "/目标" && (copied || mode == "conflict") {
							rows = append(rows, map[string]any{"fs_id": 123, "path": dir + "/item", "server_filename": "item", "isdir": typ, "size": size})
						}
						return jsonResponse(map[string]any{"errno": 0, "list": rows}), nil
					}
					if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/filemanager") || r.URL.Query().Get("opera") != "copy" {
						t.Fatal("unexpected mutation", r.Method, r.URL)
					}
					r.ParseForm()
					if r.PostForm.Get("ondup") != "fail" || r.PostForm.Get("async") != "0" || !strings.Contains(r.PostForm.Get("filelist"), `"ondup":"fail"`) {
						t.Error("unsafe copy payload", r.PostForm)
					}
					writes++
					copied = mode != "pending"
					if mode == "lost-ack" {
						return nil, errors.New("synthetic lost acknowledgement")
					}
					return quarkResponse(`{"errno":0}`, nil), nil
				})}
				_, e := copyAccountEntry(context.Background(), client, a, MoveBatchEntry{Input: input, Folder: folder})
				good := mode == "success" || mode == "lost-ack" || mode == "wrong-size" && folder
				if (e == nil) != good {
					t.Fatal(mode, e)
				}
				want := 1
				if mode == "conflict" {
					want = 0
				}
				if writes != want {
					t.Fatal("replayed copy", writes)
				}
			})
		}
	}
}

func TestCopyRejectsUnsupportedProvider(t *testing.T) {
	if _, e := copyAccountEntry(context.Background(), nil, Account{Kind: "quark"}, MoveBatchEntry{}); e == nil {
		t.Fatal("unsupported copy accepted")
	}
}

func TestLocateCopyDestinationBindsConfirmedReceiptToAccount(t *testing.T) {
	a := Account{ID: "a", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "u"}
	b := a
	b.ID = "b"
	op := &copyOperation{saved: diskCopy{
		Scope: downloadScope(a), Input: MoveBatchEntry{Input: MoveFileInput{DestinationID: "/target"}},
		Receipt: CopyReceipt{ID: "copy", AccountID: "a", State: "confirmed"},
	}}
	m := &Manager{accounts: []Account{a, b}, copies: map[string]*copyOperation{"copy": op}}
	ctx := context.Background()
	if location, err := m.LocateCopyDestination(ctx, a.ID, "copy"); err != nil || location.FolderID != "/target" {
		t.Fatal("verified destination failed to resolve", location, err)
	}
	if _, err := m.LocateCopyDestination(ctx, b.ID, "copy"); err == nil {
		t.Fatal("another account could open the receipt")
	}
	op.saved.Receipt.State = "unknown"
	if _, err := m.LocateCopyDestination(ctx, a.ID, "copy"); err == nil {
		t.Fatal("unconfirmed receipt could open a destination")
	}
}

func TestLocateCopyResultRequiresOriginalFolderID(t *testing.T) {
	a := Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com"}
	op := &copyOperation{saved: diskCopy{
		Scope: downloadScope(a), Input: MoveBatchEntry{Input: MoveFileInput{DestinationID: "50"}},
		Receipt: CopyReceipt{ID: "copy", AccountID: a.ID, State: "confirmed", OutputFolder: "Backup"},
		Lanzou:  &lanzouCopyProof{FolderID: "60"},
	}}
	m := &Manager{accounts: []Account{a}, copies: map[string]*copyOperation{"copy": op}}
	id := "60"
	c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil || r.FormValue("task") != "47" || r.FormValue("folder_id") != "50" {
			t.Fatal("unexpected copy location request", r.URL, r.Form, err)
		}
		return jsonResponse(map[string]any{
			"zt": 1, "info": []map[string]string{{"folderid": "50", "name": "Target"}},
			"text": []map[string]string{{"fol_id": id, "name": "Backup"}},
		}), nil
	})}
	ctx := context.Background()
	if rows := m.CopyHistory(a.ID); len(rows) != 1 || !rows[0].CanOpenResult {
		t.Fatal("copy result is missing from confirmed receipts")
	}
	result, err := m.locateCopyDestination(ctx, c, a.ID, "copy", true)
	if err != nil || result.FolderID != "60" || len(result.Breadcrumbs) != 3 {
		t.Fatal("correct result folder could not be opened", result, err)
	}
	id = "61"
	if _, err := m.locateCopyDestination(ctx, c, a.ID, "copy", true); err == nil {
		t.Fatal("different folder with the same name was accepted")
	}
	if parent, err := m.locateCopyDestination(ctx, c, a.ID, "copy", false); err != nil || parent.FolderID != "50" {
		t.Fatal("parent folder navigation changed", parent, err)
	}
}

func TestCopyManagerCloseCancelsPreflightWithoutWrite(t *testing.T) {
	testCopyCancellation(t, "", true)
}

func TestCopyManagerStopPreservesSubmissionBoundary(t *testing.T) {
	t.Run("before-write", func(t *testing.T) { testCopyCancellation(t, "", false) })
	t.Run("after-write", func(t *testing.T) { testCopyCancellation(t, "COPY", false) })
}

func testCopyCancellation(t *testing.T, blockedMethod string, shutdown bool) {
	t.Helper()
	a := Account{ID: "a", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "u", Secret: "fixture"}
	input := MoveFileInput{AccountID: a.ID, ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/item", Name: "item"}
	fixture, tr := folderMoveTransport(t, a, input.DestinationID, "success")
	fixture.resources[input.EntryID] = false
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blockedMethod == "COPY" && r.Method == "PROPFIND" {
			response, err := tr.RoundTrip(r)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer response.Body.Close()
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		if blockedMethod == "" && r.Method != "PROPFIND" || blockedMethod == "COPY" && r.Method != "COPY" {
			t.Error("unexpected write during preflight", r.Method)
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.accounts = []Account{a}
	done := make(chan error, 1)
	go func() {
		_, e := m.CopyEntry(context.Background(), MoveBatchEntry{Input: input})
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("preflight did not start")
	}
	if shutdown {
		m.Close()
	} else {
		if m.CancelCopy("other", input.EntryID, input.DestinationID) || m.CancelCopy(a.ID, input.EntryID, "/other") ||
			m.CancelCopy(a.ID, "/other", input.DestinationID) {
			t.Fatal("unrelated copy cancellation succeeded")
		}
		if !m.CancelCopy(a.ID, input.EntryID, input.DestinationID) {
			t.Fatal("matching copy could not be stopped")
		}
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled copy succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not stop")
	}
	if m.copyCancel != nil || m.activeCopy != nil || m.renaming["a"] {
		t.Fatal("copy reservation leaked")
	}
	want := "not_sent"
	if blockedMethod == "COPY" {
		want = "unknown"
	}
	if rows := m.CopyHistory(a.ID); len(rows) != 1 || rows[0].State != want {
		t.Fatal("cancel did not preserve the submission state", rows)
	}
}
