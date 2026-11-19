package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrashReceiptsAndReadOnlyRecovery(t *testing.T) {
	for _, kind := range []string{"baidu", "pan123", "quark"} {
		for _, mode := range []string{"success", "folder", "pending", "silent-success", "ack-lost", "recycle-mismatch", "missing-recycle", "replaced-before-submit", "initial-save-failed", "checkpoint-failed", "final-save-failed"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				statePath := filepath.Join(t.TempDir(), "state.enc")
				m, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				endpoint, parent, id := BaiduAPI, "/source", "/source/item.txt"
				if kind == "pan123" {
					endpoint, parent, id = Pan123API, "10", "9007199254740993123"
				}
				if kind == "quark" {
					endpoint, parent, id = QuarkAPI, "folder-10", "file-fid"
				}
				a := Account{ID: "a", Kind: kind, Endpoint: endpoint, Secret: "fixture"}
				m.accounts = []Account{a}
				size := int64(12)
				folder := mode == "folder"
				row := TrashInput{AccountID: "a", ParentID: parent, Entry: CloudEntry{ID: id, ObjectID: "9007199254740993123", Name: "item.txt", Size: &size, Folder: folder}}
				originalID := "9007199254740993123"
				if kind == "quark" {
					originalID = id
				}
				writes := 0
				removed := false
				inRecycle := false
				replacement := false
				preparing := true
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if kind == "quark" && r.Host != "drive.quark.cn" {
						t.Error("unexpected Quark host", r.Host)
					}
					if r.Method == "POST" {
						writes++
						if kind == "baidu" {
							_ = r.ParseForm()
							if r.URL.Path != "/api/filemanager" || r.URL.Query().Get("opera") != "delete" || r.URL.Query().Get("newVerify") != "1" || r.Form.Get("filelist") != `["/source/item.txt"]` {
								t.Error("unexpected write", r.URL, r.Form)
							}
						} else if kind == "quark" {
							var body struct {
								ActionType int      `json:"action_type"`
								FileList   []string `json:"filelist"`
								Exclude    []string `json:"exclude_fids"`
							}
							if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/1/clouddrive/file/delete" ||
								body.ActionType != 2 || len(body.FileList) != 1 || body.FileList[0] != originalID || body.Exclude == nil || len(body.Exclude) != 0 {
								t.Error("unexpected Quark trash payload", r.URL, body)
							}
							disk, err := readState(statePath)
							if err != nil || len(disk.Trashes) != 1 || !disk.Trashes[0].Submitted {
								t.Error("missing durable Quark trash intent", err)
							}
						} else {
							var body struct {
								Drive     int                      `json:"driveId"`
								Rows      []map[string]json.Number `json:"fileTrashInfoList"`
								Operation bool                     `json:"operation"`
							}
							_ = json.NewDecoder(r.Body).Decode(&body)
							if r.URL.Path != "/a/api/file/trash" || !body.Operation || len(body.Rows) != 1 || len(body.Rows[0]) != 1 || string(body.Rows[0]["FileId"]) != originalID {
								t.Error("bad trash payload", r.URL, body)
							}
						}
						if mode != "pending" && mode != "silent-success" {
							removed = true
							inRecycle = mode != "missing-recycle"
						}
						if mode == "final-save-failed" {
							m.mu.Lock()
							m.path = t.TempDir()
							m.mu.Unlock()
						}
						if mode == "ack-lost" {
							w.WriteHeader(503)
							fmt.Fprint(w, `{}`)
							return
						}
						if kind == "baidu" {
							fmt.Fprint(w, `{"errno":0}`)
						} else if kind == "quark" {
							fmt.Fprint(w, `{"code":0,"data":{"task_id":"fixture-trash-task"}}`)
						} else {
							fmt.Fprint(w, `{"code":0,"data":{"InfoList":[]}}`)
						}
						return
					}
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					if kind == "quark" && r.URL.Path == "/1/clouddrive/task" {
						if r.URL.Query().Get("task_id") != "fixture-trash-task" || writes != 1 {
							t.Error("unexpected Quark task check", r.URL)
						}
						fmt.Fprint(w, `{"code":0,"data":{"status":2}}`)
						return
					}
					recycle := strings.Contains(r.URL.Path, "recycle") || r.URL.Query().Get("trashed") == "true"
					rows := []map[string]any{}
					if recycle && inRecycle || !recycle && !removed {
						rid := originalID
						if !recycle && replacement {
							rid = "9007199254740993124"
						}
						name := "item.txt"
						if recycle && mode == "recycle-mismatch" {
							name = "other.txt"
						}
						dir := 0
						if folder {
							dir = 1
						}
						if kind == "baidu" {
							rows = append(rows, map[string]any{"fs_id": json.Number(rid), "path": "/source/item.txt", "server_filename": name, "isdir": dir, "size": size})
						} else if kind == "quark" {
							item := map[string]any{"fid": rid, "pdir_fid": parent, "file_name": name, "dir": folder, "size": size}
							if recycle {
								item["record_id"] = "recycle-record-id"
							}
							rows = append(rows, item)
						} else {
							rows = append(rows, map[string]any{"FileId": json.Number(rid), "ParentFileId": 10, "FileName": name, "Type": dir, "Size": size, "Trashed": recycle})
						}
					}
					if mode == "checkpoint-failed" && !preparing {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if kind == "baidu" {
						_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows, "total": len(rows)})
					} else if kind == "quark" {
						if !recycle && (r.URL.Path != "/1/clouddrive/file/sort" || r.URL.Query().Get("pdir_fid") != parent) {
							t.Error("unexpected Quark directory read", r.URL)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": len(rows)}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": len(rows), "Next": -1}})
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
				prepared, e := m.PrepareTrash(context.Background(), []TrashInput{row})
				if e != nil || len(prepared) != 1 || writes != 0 {
					t.Fatal(prepared, e, writes)
				}
				bad := row
				bad.Entry.Name = "stale.txt"
				for _, inputs := range [][]TrashInput{{row, bad}, {row, row}} {
					if _, e := m.PrepareTrash(context.Background(), inputs); e == nil || writes != 0 {
						t.Fatal("invalid batch was accepted", e, writes)
					}
				}
				if kind == "baidu" {
					bad = row
					bad.Entry.ObjectID = "1"
					if _, e := m.PrepareTrash(context.Background(), []TrashInput{bad}); e == nil {
						t.Fatal("accepted replaced object at displayed path")
					}
				}
				preparing = false
				if mode == "replaced-before-submit" {
					replacement = true
				}
				if mode == "initial-save-failed" {
					m.path = t.TempDir()
				}
				r, e := m.TrashEntry(context.Background(), prepared[0].Handle)
				if mode == "initial-save-failed" {
					if e == nil || writes != 0 || len(m.TrashHistory(a.ID)) != 0 {
						t.Fatal(r, e, writes)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				want := "unknown"
				if mode == "success" || mode == "folder" || mode == "ack-lost" {
					want = "confirmed"
				}
				if mode == "replaced-before-submit" || mode == "checkpoint-failed" {
					want = "not_sent"
				}
				if r.State != want {
					t.Fatalf("want %s got %+v", want, r)
				}
				wantWrites := 1
				if want == "not_sent" {
					wantWrites = 0
				}
				if writes != wantWrites {
					t.Fatal("writes", writes)
				}
				if _, e = m.TrashEntry(context.Background(), prepared[0].Handle); e == nil || writes != wantWrites {
					t.Fatal("duplicate replay", e, writes)
				}
				// Reopen the originally saved state; submitted checkpoints recover as unknown.
				reopened, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				history := reopened.TrashHistory(a.ID)
				if len(history) != 1 {
					t.Fatal(history)
				}
				if mode == "final-save-failed" && history[0].State != "unknown" {
					t.Fatal(history)
				}
				if want == "unknown" && mode != "checkpoint-failed" {
					if n, e := reopened.ClearTrashHistory(a.ID); e != nil || n != 0 {
						t.Fatal("cleared unknown", n, e)
					}
					removed = true
					inRecycle = true
					mode = "recovery" // complete the original operation remotely
					checked, e := reopened.CheckTrash(context.Background(), r.ID)
					if e != nil || checked.State != "confirmed" || writes != wantWrites {
						t.Fatal("read-only recovery", checked, e, writes)
					}
					before := reopened.path
					reopened.path = t.TempDir()
					if n, e := reopened.ClearTrashHistory(a.ID); e == nil || n != 0 || len(reopened.TrashHistory(a.ID)) != 1 {
						t.Fatal("clear rollback", n, e)
					}
					reopened.path = before
				}
				reopened.mu.Lock()
				reopened.accounts[0].Secret = "different-fixture"
				reopened.mu.Unlock()
				if _, e = reopened.CheckTrash(context.Background(), r.ID); e == nil {
					t.Fatal("scope changed check allowed")
				}
				if n, e := reopened.ClearTrashHistory(a.ID); e != nil || n != 0 {
					t.Fatal("scope changed clear", n, e)
				}
			})
		}
	}
}

func TestTrashInputAndCorruptState(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state"), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"", "garbage", strings.Repeat("a", 32769)} {
		if _, e = m.TrashEntry(context.Background(), s); e == nil {
			t.Fatal("accepted bad handle")
		}
	}
	if _, e = m.PrepareTrash(context.Background(), nil); e == nil {
		t.Fatal("accepted empty batch")
	}
	if _, e = m.PrepareTrash(context.Background(), make([]TrashInput, 51)); e == nil {
		t.Fatal("accepted large batch")
	}
	if e = m.restoreTrash([]diskTrash{{}}); e == nil {
		t.Fatal("accepted invalid state")
	}
	if _, e = m.CheckTrash(context.Background(), "missing"); e == nil {
		t.Fatal("missing check")
	}
}

func TestTrashCloseCancelsSubmittedRequest(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state")
	m, e := NewManager(statePath, nil)
	if e != nil {
		t.Fatal(e)
	}
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	m.accounts = []Account{a}
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"InfoList":[{"FileId":12,"ParentFileId":0,"FileName":"item","Type":0,"Size":0}],"Total":1,"Next":-1}}`)
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
	prepared, e := m.PrepareTrash(context.Background(), []TrashInput{{AccountID: "a", ParentID: "0", Entry: CloudEntry{ID: "12", Name: "item", Size: &size}}})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan TrashReceipt, 1)
	go func() { r, _ := m.TrashEntry(context.Background(), prepared[0].Handle); done <- r }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never started")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel")
	}
	r := <-done
	if r.State != "unknown" {
		t.Fatal(r)
	}
	reopened, e := NewManager(statePath, nil)
	if e != nil || len(reopened.TrashHistory("a")) != 1 || reopened.TrashHistory("a")[0].State != "unknown" {
		t.Fatal("lost submitted checkpoint", e)
	}
}

func TestTrashIncompleteDirectoryNeverConfirms(t *testing.T) {
	for _, mode := range []string{"limit", "duplicate", "malformed", "wrong-parent"} {
		t.Run(mode, func(t *testing.T) {
			visits := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				visits++
				rows := []map[string]any{}
				first := 0
				if mode == "limit" {
					first = (visits - 1) * 100
				}
				if mode != "malformed" {
					for i := 0; i < 100; i++ {
						parent := 0
						if mode == "wrong-parent" {
							parent = 99
						}
						rows = append(rows, map[string]any{"FileId": first + i + 1, "ParentFileId": parent, "FileName": fmt.Sprintf("item%d", first+i), "Type": 0, "Size": 0})
					}
				}
				var list any = rows
				if mode == "malformed" {
					list = nil
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": list, "Total": 9000, "Next": 0}})
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			if _, e := trashLive(context.Background(), &http.Client{Transport: tr}, Account{Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}, "0"); e == nil {
				t.Fatal("incomplete list was accepted")
			}
			if visits > 50 {
				t.Fatal("unbounded traversal", visits)
			}
		})
	}
}
