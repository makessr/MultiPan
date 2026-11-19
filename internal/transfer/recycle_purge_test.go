package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecyclePurgeAcrossPagesPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "page-changed", "total-changed", "duplicate-page-row"} {
		t.Run(mode, func(t *testing.T) {
			a := recycleFixtureAccount("quark")
			m := &Manager{accounts: []Account{a}}
			pages := make(chan string, 8)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/1/clouddrive/file/recycle/list" {
					t.Error("unexpected request during read-only preflight", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				page := r.URL.Query().Get("_page")
				pages <- page
				rows := []map[string]any{}
				total := 101
				if page == "1" {
					for i := 1; i <= recyclePageSize; i++ {
						rows = append(rows, map[string]any{"record_id": fmt.Sprint(i), "file_name": "同名.txt", "dir": false})
					}
				} else {
					id, name := "101", "同名.txt"
					if mode == "page-changed" {
						name = "已改名.txt"
					}
					if mode == "duplicate-page-row" {
						id = "1"
					}
					rows = append(rows, map[string]any{"record_id": id, "file_name": name, "dir": false})
					if mode == "total-changed" {
						total++
						rows = append(rows, map[string]any{"record_id": "102", "file_name": name, "dir": false})
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": total}})
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			previous := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = previous; tr.CloseIdleConnections() }()
			selections := []RecyclePurgeSelection{
				{Page: 1, Entry: RecycleEntry{ID: "1", Kind: "file", Name: "同名.txt"}},
				{Page: 2, Entry: RecycleEntry{ID: "101", Kind: "file", Name: "同名.txt"}},
				{Page: 1, Entry: RecycleEntry{ID: "2", Kind: "file", Name: "同名.txt"}},
			}
			prepared, err := m.PrepareRecyclePurgeAcrossPages(context.Background(), a.ID, selections)
			if len(pages) != 2 || <-pages != "1" || <-pages != "2" || len(m.recyclePurges) != 0 {
				t.Fatal("preflight reread a page or created a receipt")
			}
			if mode != "valid" {
				if err == nil || len(prepared) != 0 {
					t.Fatal("inconsistent batch produced handles", err)
				}
				return
			}
			if err != nil || len(prepared) != len(selections) {
				t.Fatalf("valid batch yielded %d handles: %v", len(prepared), err)
			}
			for i, item := range prepared {
				source, err := decodeRecyclePurge(item.Handle)
				if err != nil || source.AccountID != a.ID || source.Entry.ID != selections[i].Entry.ID {
					t.Fatal("prepared handle lost the original identity", err)
				}
			}
		})
	}
}

func TestRecyclePurgeThreeProvidersReceiptsAndNoReplay(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu"} {
		for _, mode := range []string{"success", "folder", "silent-success", "ack-lost", "rejected", "post-read-failed", "incomplete-after", "source-changed", "initial-save-failed", "checkpoint-failed", "ack-save-failed", "final-save-failed"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				statePath := filepath.Join(t.TempDir(), "state.enc")
				m, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				a := recycleFixtureAccount(kind)
				m.accounts = []Account{a}
				size := int64(0)
				entry := RecycleEntry{ID: "9007199254740993123", Name: "disposable.txt", Kind: "file", Size: &size, OriginalPath: "/old/disposable.txt"}
				if kind == "quark" {
					entry.ID = "record_1"
					entry.FileID = "file_1"
					entry.ParentID = "parent_1"
					entry.OriginalPath = "原目录 ID parent_1 / disposable.txt"
				}
				if kind == "pan123" {
					entry.ParentID = "77"
					entry.OriginalPath = "原目录 ID 77 / disposable.txt"
				}
				if mode == "folder" {
					entry.Kind = "folder"
				}
				writes := 0
				removed := false
				changed := false
				preparedPhase := true
				postReadFail := false
				incomplete := false
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" {
						writes++
						switch kind {
						case "quark":
							var body map[string]json.RawMessage
							_ = json.NewDecoder(r.Body).Decode(&body)
							if r.URL.Path != "/1/clouddrive/file/recycle/remove" || len(body) != 2 || string(body["select_mode"]) != "2" || string(body["record_list"]) != `["record_1"]` {
								t.Error("wrong quark purge", r.URL, body)
							}
						case "pan123":
							var body map[string]json.RawMessage
							_ = json.NewDecoder(r.Body).Decode(&body)
							if r.URL.Path != "/b/api/file/delete" || string(body["fileIdList"]) != `[{"fileId":9007199254740993123}]` || string(body["event"]) != `"recycleDelete"` || string(body["operatePlace"]) != "1" || string(body["RequestSource"]) != "null" {
								t.Error("wrong 123 purge", r.URL, body)
							}
						case "baidu":
							_ = r.ParseForm()
							if r.URL.Path != "/api/recycle/delete" || r.Form.Get("fidlist") != "[9007199254740993123]" {
								t.Error("wrong baidu purge", r.URL, r.Form)
							}
						}
						if mode != "silent-success" && mode != "rejected" {
							removed = true
						}
						if mode == "post-read-failed" {
							postReadFail = true
						}
						if mode == "incomplete-after" {
							incomplete = true
						}
						if mode == "ack-save-failed" {
							m.mu.Lock()
							m.path = t.TempDir()
							m.mu.Unlock()
						}
						if mode == "ack-lost" || mode == "rejected" {
							w.WriteHeader(503)
							return
						}
						switch kind {
						case "quark":
							fmt.Fprint(w, `{"status":200,"code":0,"data":{}}`)
						case "pan123":
							fmt.Fprint(w, `{"code":0,"data":{}}`)
						case "baidu":
							fmt.Fprint(w, `{"errno":0}`)
						}
						return
					}
					if r.Method != "GET" {
						t.Error("unexpected method", r.Method)
						w.WriteHeader(400)
						return
					}
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					if mode == "final-save-failed" && writes > 0 {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "checkpoint-failed" && !preparedPhase {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if postReadFail {
						w.WriteHeader(403)
						return
					}
					rows := []any{}
					if !removed {
						name := entry.Name
						if changed {
							name = "changed.txt"
						}
						folder := entry.Kind == "folder"
						typ := 0
						if folder {
							typ = 1
						}
						switch kind {
						case "quark":
							rows = append(rows, map[string]any{"record_id": entry.ID, "fid": entry.FileID, "pdir_fid": entry.ParentID, "file_name": name, "dir": folder, "size": 0})
						case "pan123":
							rows = append(rows, map[string]any{"FileId": json.Number(entry.ID), "ParentFileId": 77, "FileName": name, "Type": typ, "Size": 0})
						case "baidu":
							rows = append(rows, map[string]any{"fs_id": json.Number(entry.ID), "server_filename": name, "path": entry.OriginalPath, "isdir": typ, "size": 0})
						}
					}
					total := len(rows)
					if incomplete {
						total = 101
					}
					switch kind {
					case "quark":
						_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": total}})
					case "pan123":
						next := 0
						if !incomplete {
							next = -1
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": total, "Next": next}})
					case "baidu":
						_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows, "total": total})
					}
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
				prepared, e := m.PrepareRecyclePurge(context.Background(), a.ID, 1, []RecycleEntry{entry})
				if e != nil || len(prepared) != 1 {
					t.Fatal(prepared, e)
				}
				bad := entry
				bad.ID = "missing_2"
				if kind != "quark" {
					bad.ID = "999"
				}
				if _, e := m.PrepareRecyclePurge(context.Background(), a.ID, 1, []RecycleEntry{entry, bad}); e == nil || writes != 0 {
					t.Fatal("partial preflight wrote", e, writes)
				}
				for _, confirmation := range []string{"", "删除", " 永久删除"} {
					if _, e := m.PurgeRecycle(context.Background(), prepared[0].Handle, confirmation); e == nil || writes != 0 || len(m.RecyclePurgeHistory(a.ID)) != 0 {
						t.Fatal("confirmation bypass", e, writes)
					}
				}
				preparedPhase = false
				if mode == "source-changed" {
					changed = true
				}
				if mode == "initial-save-failed" {
					m.path = t.TempDir()
				}
				r, e := m.PurgeRecycle(context.Background(), prepared[0].Handle, "永久删除")
				if mode == "initial-save-failed" {
					if e == nil || writes != 0 || len(m.RecyclePurgeHistory(a.ID)) != 0 {
						t.Fatal(r, e, writes)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				want := "unknown"
				if mode == "success" || mode == "folder" {
					want = "confirmed"
				}
				if mode == "source-changed" || mode == "checkpoint-failed" {
					want = "not_sent"
				}
				expectedWrites := 1
				if want == "not_sent" {
					expectedWrites = 0
				}
				if r.State != want || writes != expectedWrites {
					t.Fatal(mode, want, r, writes)
				}
				if _, e := m.PurgeRecycle(context.Background(), prepared[0].Handle, "永久删除"); e == nil || writes != expectedWrites {
					t.Fatal("replayed", e, writes)
				}
				reopened, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				if len(reopened.RecyclePurgeHistory(a.ID)) != 1 {
					t.Fatal("missing persisted record")
				}
				if want == "unknown" {
					if n, e := reopened.ClearRecyclePurgeHistory(a.ID); n != 0 || e != nil {
						t.Fatal("cleared unknown", n, e)
					}
					removed = true
					postReadFail = false
					incomplete = false
					checked, e := reopened.CheckRecyclePurge(context.Background(), r.ID)
					if e != nil || writes != expectedWrites {
						t.Fatal("check replay", checked, e, writes)
					}
					wantCheck := "confirmed"
					if mode == "ack-lost" || mode == "rejected" || mode == "ack-save-failed" {
						wantCheck = "unknown"
					}
					if checked.State != wantCheck {
						t.Fatal("absence alone confirmed", mode, checked)
					}
				}
				history := reopened.RecyclePurgeHistory(a.ID)
				if history[0].State == "confirmed" {
					before := reopened.path
					reopened.path = t.TempDir()
					if n, e := reopened.ClearRecyclePurgeHistory(a.ID); n != 0 || e == nil || len(reopened.RecyclePurgeHistory(a.ID)) != 1 {
						t.Fatal("clear rollback", n, e)
					}
					reopened.path = before
				}
				reopened.mu.Lock()
				reopened.accounts[0].Secret = "changed-fixture"
				reopened.mu.Unlock()
				if _, e := reopened.CheckRecyclePurge(context.Background(), r.ID); e == nil {
					t.Fatal("cross scope check")
				}
				if reopened.RecyclePurgeHistory(a.ID)[0].CanCheck {
					t.Fatal("cross scope history")
				}
			})
		}
	}
}

func TestRecyclePurgeValidationAndOppositeOperationGuards(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, handle := range []string{"", "bad", strings.Repeat("a", 32769)} {
		if _, e := m.PurgeRecycle(context.Background(), handle, "永久删除"); e == nil {
			t.Fatal("invalid handle")
		}
	}
	for _, entries := range [][]RecycleEntry{nil, make([]RecycleEntry, 51)} {
		if _, e := m.PrepareRecyclePurge(context.Background(), "a", 1, entries); e == nil {
			t.Fatal("invalid count")
		}
	}
	if e := m.restoreRecyclePurges([]diskRecyclePurge{{}}); e == nil {
		t.Fatal("corrupt record accepted")
	}
	a := recycleFixtureAccount("baidu")
	m.accounts = []Account{a}
	scope := downloadScope(a)
	m.recyclePurges = map[string]*recyclePurgeOperation{"p": {saved: diskRecyclePurge{Source: recyclePurgeSource{Scope: scope, Entry: RecycleEntry{ID: "12"}}, Submitted: true, Receipt: TrashReceipt{State: "unknown"}}}}
	size := int64(0)
	entry := RecycleEntry{ID: "12", Name: "item", Kind: "file", Size: &size, OriginalPath: "/item"}
	token, e := sealRecycleRestore(a, 1, entry)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := m.RestoreRecycle(context.Background(), token); e == nil {
		t.Fatal("restored pending purge")
	}
	if e := m.ValidateRecycleRestoreBatch(context.Background(), []string{token}); e == nil {
		t.Fatal("batch restored pending purge")
	}
	m.recycleRestores = map[string]*recycleRestore{"r": {saved: diskRecycleRestore{Source: recycleRestoreSource{Scope: scope, Entry: entry}, Submitted: true, Result: RecycleRestoreResult{State: "unknown"}}}}
	if !m.pendingRestoreLocked(scope, "12") || m.pendingRestoreLocked(scope, "other") || m.pendingPurgeLocked("other", "12") {
		t.Fatal("opposite scope/id guard")
	}
}

func TestRecyclePurgeBoundedAbsenceChecks(t *testing.T) {
	for _, mode := range []string{"complete", "duplicate", "limit", "changed-total", "early-end"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				reads++
				rows := []any{}
				n, total := 100, 101
				if mode == "limit" {
					total = 5001
				}
				if reads == 2 && mode == "complete" {
					n = 1
				}
				if reads == 2 && mode == "changed-total" {
					total = 102
				}
				if mode == "early-end" {
					n = 0
				}
				for i := 0; i < n; i++ {
					id := (reads-1)*100 + i + 1
					if mode == "duplicate" {
						id = i + 1
					}
					rows = append(rows, map[string]any{"record_id": fmt.Sprintf("record_%d", id), "file_name": "item", "dir": false, "size": 0})
				}
				b, _ := json.Marshal(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": total}})
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
			})}
			present, e := findRecyclePurge(context.Background(), client, recycleFixtureAccount("quark"), RecycleEntry{ID: "not_present"})
			if present || (e == nil) != (mode == "complete") || reads > 50 {
				t.Fatal(mode, present, e, reads)
			}
		})
	}
}

func TestRecyclePurgeCloseCancelsSubmittedRequest(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state")
	m, e := NewManager(statePath, nil)
	if e != nil {
		t.Fatal(e)
	}
	a := recycleFixtureAccount("quark")
	m.accounts = []Account{a}
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		fmt.Fprint(w, `{"status":200,"code":0,"metadata":{"_total":1},"data":{"list":[{"record_id":"record_1","file_name":"item","dir":false,"size":0}]}}`)
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
	size := int64(0)
	entries := []RecycleEntry{{ID: "record_1", Name: "item", Kind: "file", Size: &size}}
	p, e := m.PrepareRecyclePurge(context.Background(), a.ID, 1, entries)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan TrashReceipt, 1)
	go func() { r, _ := m.PurgeRecycle(context.Background(), p[0].Handle, "永久删除"); done <- r }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel")
	}
	if r := <-done; r.State != "unknown" {
		t.Fatal(r)
	}
	reopened, e := NewManager(statePath, nil)
	if e != nil || len(reopened.RecyclePurgeHistory(a.ID)) != 1 || reopened.RecyclePurgeHistory(a.ID)[0].State != "unknown" {
		t.Fatal("submitted checkpoint lost", e)
	}
}
