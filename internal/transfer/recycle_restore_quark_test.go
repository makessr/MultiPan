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
)

func TestQuarkRecycleRestoreVerifiedPersistentAndNeverReplayed(t *testing.T) {
	for _, mode := range []string{"success", "folder", "missing-ack", "pending", "wrong-fid", "wrong-size", "wrong-parent", "wrong-type", "missing-size", "record-id-as-fid", "changed-source", "conflict", "parent-unreadable", "incomplete-parent", "initial-save-failed", "checkpoint-failed", "final-save-failed"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "session=fixture-auth"}
			m.accounts = []Account{a}
			posts := 0
			completed := false
			displayed := false
			folder := mode == "folder"
			kind := "file"
			if folder {
				kind = "folder"
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "drive.quark.cn" || r.Header.Get("Cookie") != "session=fixture-auth" {
					t.Error("wrong origin/auth", r.Host)
				}
				write := func(rows []any, total int) {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": total}})
				}
				switch r.URL.Path {
				case "/1/clouddrive/file/recycle/list":
					if r.Method != "GET" {
						t.Error("recycle list wrote")
					}
					name := "资料 &amp; 备份.txt"
					if mode == "changed-source" && displayed {
						name = "changed.txt"
					}
					rows := []any{}
					if !completed {
						rows = append(rows, map[string]any{"record_id": "rec_A17", "fid": "fid_A17", "pdir_fid": "parent_77", "file_name": name, "dir": folder, "size": 7})
					}
					write(rows, len(rows))
				case "/1/clouddrive/file/sort":
					if r.Method != "GET" || r.URL.Query().Get("pdir_fid") != "parent_77" || r.URL.Query().Get("_size") != "100" {
						t.Error("wrong live read", r.URL)
					}
					if mode == "parent-unreadable" {
						w.WriteHeader(403)
						return
					}
					if mode == "checkpoint-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					rows := []any{}
					if completed || mode == "conflict" || mode == "incomplete-parent" {
						fid := "fid_A17"
						parent := "parent_77"
						size := any(7)
						dir := folder
						if mode == "conflict" || mode == "wrong-fid" {
							fid = "different_fid"
						}
						if mode == "record-id-as-fid" {
							fid = "rec_A17"
						}
						if mode == "wrong-parent" {
							parent = "other_parent"
						}
						if mode == "wrong-size" {
							size = 8
						}
						if mode == "missing-size" {
							size = nil
						}
						if mode == "wrong-type" {
							dir = !folder
						}
						rows = append(rows, map[string]any{"fid": fid, "pdir_fid": parent, "file_name": "资料 &amp; 备份.txt", "dir": dir, "size": size})
					}
					total := len(rows)
					if mode == "incomplete-parent" {
						total = 2
					}
					write(rows, total)
				case "/1/clouddrive/file/recycle/recover":
					if r.Method != "POST" {
						t.Error("recover method", r.Method)
					}
					var body map[string]json.RawMessage
					_ = json.NewDecoder(r.Body).Decode(&body)
					if len(body) != 2 || string(body["select_mode"]) != "2" || string(body["record_list"]) != `["rec_A17"]` {
						t.Error("record_id/fid confused or invalid payload", body)
					}
					posts++
					completed = mode != "pending"
					if mode == "final-save-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "missing-ack" {
						w.WriteHeader(502)
						return
					}
					fmt.Fprint(w, `{"status":200,"code":0,"data":{}}`)
				default:
					t.Error("unexpected endpoint", r.Method, r.URL)
					w.WriteHeader(400)
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
			page, e := m.ListRecycle(context.Background(), a.ID, 1)
			if e != nil || len(page.Items) != 1 {
				t.Fatal(page, e)
			}
			entry := page.Items[0]
			displayed = true
			if entry.ID != "rec_A17" || entry.FileID != "fid_A17" || entry.Kind != kind || entry.ParentID != "parent_77" || entry.RestoreHandle == "" {
				t.Fatal(entry)
			}
			if mode == "initial-save-failed" {
				m.path = t.TempDir()
			}
			result, e := m.RestoreRecycle(context.Background(), entry.RestoreHandle)
			if mode == "initial-save-failed" {
				if e == nil || posts != 0 || len(m.recycleRestores) != 0 {
					t.Fatal(result, e, posts)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			want := "confirmed"
			if strings.HasPrefix(mode, "wrong-") || mode == "missing-size" || mode == "record-id-as-fid" || mode == "pending" || mode == "final-save-failed" {
				want = "unknown"
			}
			if mode == "changed-source" || mode == "conflict" || mode == "parent-unreadable" || mode == "incomplete-parent" || mode == "checkpoint-failed" {
				want = "not_sent"
			}
			if result.State != want {
				t.Fatalf("want %s got %+v", want, result)
			}
			wantedPosts := 1
			if want == "not_sent" {
				wantedPosts = 0
			}
			if posts != wantedPosts {
				t.Fatal("writes", posts)
			}
			repeat, e := m.RestoreRecycle(context.Background(), entry.RestoreHandle)
			if e != nil || repeat.ID != result.ID || posts != wantedPosts {
				t.Fatal("replayed", repeat, e, posts)
			}
			reopened, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			history := reopened.RecycleRestoreHistory(a.ID)
			if len(history) != 1 {
				t.Fatal(history)
			}
			if want == "unknown" {
				if n, e := reopened.ClearRecycleRestores(a.ID); e != nil || n != 0 {
					t.Fatal("cleared unknown", n, e)
				}
				mode = "fixed"
				completed = true
				checked, e := reopened.CheckRecycleRestore(context.Background(), result.ID)
				if e != nil || checked.State != "confirmed" || posts != wantedPosts {
					t.Fatal("read-only check", checked, e, posts)
				}
			}
			if want != "not_sent" {
				before := reopened.path
				reopened.path = t.TempDir()
				if n, e := reopened.ClearRecycleRestores(a.ID); n != 0 || e == nil {
					t.Fatal("clear save failure", n, e)
				}
				reopened.path = before
			}
			reopened.mu.Lock()
			reopened.accounts[0].Secret = "changed-fixture"
			reopened.mu.Unlock()
			if _, e := reopened.CheckRecycleRestore(context.Background(), result.ID); e == nil {
				t.Fatal("source changed allowed")
			}
		})
	}
}

func TestQuarkIncompleteRecycleMetadataRemainsReadOnly(t *testing.T) {
	for _, field := range []string{"fid", "pdir_fid", "size", "type-conflict"} {
		row := map[string]any{"record_id": "record_1", "fid": "file_1", "pdir_fid": "parent_1", "file_name": "item", "dir": false, "size": 0}
		if field == "type-conflict" {
			row["file_type"] = 0
		} else {
			delete(row, field)
		}
		a := recycleFixtureAccount("quark")
		p, e := listAccountRecycle(context.Background(), recycleFixtureClient(t, "quark", []any{row}, 1, nil, 1), a, 1)
		if e != nil || len(p.Items) != 1 {
			t.Fatal(p, e)
		}
		if handle, e := sealRecycleRestore(a, 1, p.Items[0]); e == nil || handle != "" {
			t.Fatal("incomplete entry allowed restore", field)
		}
	}
	for _, pair := range []struct {
		dir  bool
		typ  int
		want string
	}{{true, 0, "folder"}, {false, 1, "file"}, {false, 0, "unknown"}, {true, 1, "unknown"}} {
		if got := quarkRecycleKind(&pair.dir, &pair.typ); got != pair.want {
			t.Fatal(pair, got)
		}
	}
}

func TestRecycleRestoreBatchPreflightThreeProviders(t *testing.T) {
	for _, kind := range []string{"quark", "baidu", "pan123"} {
		modes := []string{"valid", "cross-page", "cross-page-stale", "second-missing", "occupied", "duplicate", "same-target", "different-account", "existing-receipt"}
		if kind == "pan123" {
			modes = append(modes, "direct-cross-page", "direct-cross-page-stale", "direct-occupied", "direct-duplicate-name", "direct-descendant", "direct-parent-child")
		}
		for _, mode := range modes {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				direct := strings.HasPrefix(mode, "direct-")
				scenario := strings.TrimPrefix(mode, "direct-")
				crossPage := strings.HasPrefix(scenario, "cross-page")
				m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
				if e != nil {
					t.Fatal(e)
				}
				a := recycleFixtureAccount(kind)
				m.accounts = []Account{a}
				entries := []RecycleEntry{{ID: "101", Name: "one", Kind: "file", OriginalPath: "/old/one"}, {ID: "102", Name: "two", Kind: "folder", OriginalPath: "/old/two"}}
				size := int64(0)
				for i := range entries {
					entries[i].Size = &size
					if kind == "pan123" {
						entries[i].ParentID = "77"
						entries[i].OriginalPath = "原目录 ID 77 / " + entries[i].Name
					}
					if kind == "quark" {
						entries[i].FileID = fmt.Sprintf("file_%d", i)
						entries[i].ParentID = "parent_77"
						entries[i].OriginalPath = "原目录 ID parent_77 / " + entries[i].Name
					}
				}
				if mode == "same-target" {
					entries[1].Name = entries[0].Name
					entries[1].OriginalPath = entries[0].OriginalPath
				}
				if mode == "direct-duplicate-name" {
					entries[1].Name = "ONE"
					entries[1].OriginalPath = "原目录 ID 77 / ONE"
				}
				if mode == "direct-parent-child" {
					entries[0].ParentID = entries[1].ID
					entries[0].OriginalPath = "原目录 ID 102 / one"
				}
				handles := []string{}
				for i, r := range entries {
					page := 1
					if crossPage {
						page += i
					}
					h, e := sealRecycleRestore(a, page, r)
					if e != nil {
						t.Fatal(e)
					}
					handles = append(handles, h)
				}
				if mode == "duplicate" {
					handles[1] = handles[0]
				}
				if crossPage {
					handles[0], handles[1] = handles[1], handles[0]
				}
				if mode == "different-account" {
					other := a
					other.ID = "different-account"
					handles[1], e = sealRecycleRestore(other, 1, entries[1])
					if e != nil {
						t.Fatal(e)
					}
				}
				if mode == "existing-receipt" {
					h, e := decodeRecycleRestore(handles[1])
					if e != nil {
						t.Fatal(e)
					}
					m.recycleRestores = map[string]*recycleRestore{"prior": {saved: diskRecycleRestore{Source: h}}}
				}
				reads, writes, targetReads := 0, 0, 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						writes++
						t.Error("batch preflight wrote", r.URL)
						w.WriteHeader(400)
						return
					}
					reads++
					if strings.Contains(r.URL.Path, "gettemplatevariable") {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					recycle := strings.Contains(r.URL.Path, "recycle/list") || r.URL.Query().Get("trashed") == "true"
					list := []any{}
					appendRow := func(e RecycleEntry) {
						switch kind {
						case "quark":
							list = append(list, map[string]any{"record_id": e.ID, "fid": e.FileID, "pdir_fid": e.ParentID, "file_name": e.Name, "dir": e.Kind == "folder", "size": 0})
						case "pan123":
							typ := 0
							if e.Kind == "folder" {
								typ = 1
							}
							list = append(list, map[string]any{"FileId": json.Number(e.ID), "ParentFileId": 77, "FileName": e.Name, "Type": typ, "Size": 0})
						case "baidu":
							typ := 0
							if e.Kind == "folder" {
								typ = 1
							}
							list = append(list, map[string]any{"fs_id": json.Number(e.ID), "path": e.OriginalPath, "server_filename": e.Name, "isdir": typ, "size": 0})
						}
					}
					page := r.URL.Query().Get("_page")
					if kind == "pan123" {
						page = r.URL.Query().Get("Page")
					} else if kind == "baidu" {
						page = r.URL.Query().Get("page")
					}
					next := -1
					if recycle && crossPage && page == "1" {
						appendRow(entries[0])
						for i := 0; i < recyclePageSize-1; i++ {
							filler := entries[0]
							filler.ID = fmt.Sprint(200 + i)
							filler.FileID = "filler_" + filler.ID
							appendRow(filler)
						}
						next = 1
					} else if recycle && crossPage && page == "2" {
						entry := entries[1]
						if scenario == "cross-page-stale" {
							entry.ID = "999"
						}
						appendRow(entry)
					} else if recycle {
						appendRow(entries[0])
						if mode != "second-missing" {
							appendRow(entries[1])
						}
					} else if direct && r.URL.Query().Get("parentFileId") == "0" {
						list = append(list, map[string]any{"FileId": 88, "ParentFileId": 0, "FileName": "目标", "Type": 1})
					} else if direct {
						if r.URL.Query().Get("parentFileId") != "88" {
							t.Error("batch direct preflight read the wrong parent")
						}
						targetReads++
						if scenario == "occupied" {
							list = append(list, map[string]any{"FileId": 99, "ParentFileId": 88, "FileName": "TWO", "Type": 1})
						}
					} else if mode == "occupied" {
						if kind == "quark" {
							list = append(list, map[string]any{"fid": "another_fid", "pdir_fid": "parent_77", "file_name": "two", "dir": true, "size": 0})
						} else {
							appendRow(entries[1])
						}
					}
					total := len(list)
					if recycle && crossPage {
						total = recyclePageSize + 1
					}
					switch kind {
					case "quark":
						_ = json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": list}, "metadata": map[string]any{"_total": total}})
					case "pan123":
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": list, "Total": total, "Next": next}})
					case "baidu":
						_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": list, "total": total})
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
				if direct {
					trail := []CloudFolder{{ID: "0", Name: "根目录"}, {ID: "88", Name: "目标"}}
					if scenario == "descendant" {
						trail = []CloudFolder{trail[0], {ID: entries[1].ID, Name: entries[1].Name}, trail[1]}
					}
					e = m.ValidateRecycleRestoreBatchTo(context.Background(), handles, trail)
				} else {
					e = m.ValidateRecycleRestoreBatch(context.Background(), handles)
				}
				if (e == nil) != (scenario == "valid" || scenario == "cross-page") || writes != 0 {
					t.Fatal(mode, e, writes)
				}
				if mode == "direct-cross-page" && targetReads != 1 {
					t.Fatal("direct batch preflight should scan the target once", targetReads)
				}
				if (mode == "direct-duplicate-name" || mode == "direct-descendant" || mode == "direct-parent-child") && reads != 0 {
					t.Fatal("invalid direct batch read cloud", reads)
				}
				if (mode == "duplicate" || mode == "same-target" || mode == "different-account" || mode == "existing-receipt") && reads != 0 {
					t.Fatal("invalid batch read cloud", reads)
				}
				if len(m.recycleRestores) != 0 && mode != "existing-receipt" {
					t.Fatal("preflight created receipts")
				}
			})
		}
	}
}
