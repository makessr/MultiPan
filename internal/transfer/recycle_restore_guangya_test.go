package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestGuangyaRecycleRestoreRequiresExplicitRootAndVerifiedReturn(t *testing.T) {
	for _, mode := range []string{"root-file", "root-folder", "nested", "missing-parent", "stale-record", "absent-origin", "null-origin", "root-alias", "conflict", "wrong-live-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
			m.accounts = []Account{a}
			wireParent, restoreParent, displayPath := "", guangyaRoot, "根目录 / fixture.txt"
			if mode == "nested" || mode == "missing-parent" {
				wireParent, restoreParent, displayPath = "parent-12", "parent-12", "原目录 ID parent-12 / fixture.txt"
			}
			resourceType := 1
			if mode == "root-folder" {
				resourceType = 2
			}
			var writes atomic.Int32
			var sourceChanged atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					DirType  int      `json:"dirType"`
					ParentID *string  `json:"parentId"`
					FileIDs  []string `json:"fileIds"`
					FileID   string   `json:"fileId"`
					TaskID   string   `json:"taskId"`
				}
				if r.Host != "api.guangyapan.com" || r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&q) != nil {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					rows := []map[string]any{}
					row := map[string]any{"fileId": "fixture-file", "fileName": "fixture.txt", "parentId": wireParent, "resType": resourceType, "fileSize": 7}
					if q.DirType == 4 {
						if q.ParentID == nil || *q.ParentID != "" {
							t.Error("wrong recycle parent", q.ParentID)
						}
						switch mode {
						case "absent-origin":
							delete(row, "parentId")
						case "null-origin":
							row["parentId"] = nil
						case "root-alias":
							row["parentId"] = guangyaRoot
						}
						if writes.Load() == 0 && !sourceChanged.Load() {
							rows = append(rows, row)
						}
					} else {
						if q.ParentID == nil || *q.ParentID != wireParent {
							t.Error("wrong live restore directory", q.ParentID)
						}
						if mode == "conflict" {
							row["fileId"] = "another-file"
							rows = append(rows, row)
						} else if writes.Load() != 0 {
							if mode == "wrong-live-parent" {
								delete(row, "parentId")
							}
							rows = append(rows, row)
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"total": len(rows), "list": rows}})
				case "/userres/v1/file/get_file_detail":
					if (mode != "nested" && mode != "missing-parent") || q.FileID != wireParent {
						t.Error("unexpected directory detail request", q.FileID)
					}
					folderID := wireParent
					if mode == "missing-parent" {
						folderID = "other-folder"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"fileInfo": map[string]any{"fileId": folderID, "resType": 2}}})
				case "/userres/v1/file/recycle_file":
					writes.Add(1)
					if len(q.FileIDs) != 1 || q.FileIDs[0] != "fixture-file" {
						t.Error("wrong restore identity", q.FileIDs)
					}
					disk, err := readState(statePath)
					if err != nil || len(disk.RecycleRestores) != 1 || !disk.RecycleRestores[0].Submitted {
						t.Error("restore sent without a durable checkpoint", err)
					}
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"taskId":"fixture-restore"}}`)
				case "/userres/v1/get_task_status":
					if q.TaskID != "fixture-restore" {
						t.Error("unexpected task read", q.TaskID)
					}
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"status":2}}`)
				default:
					t.Error("unexpected operation", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			before := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() {
				http.DefaultTransport = before
				tr.CloseIdleConnections()
			}()

			page, err := m.ListRecycle(ctx, a.ID, 1)
			if err != nil || len(page.Items) != 1 {
				t.Fatal(page, err)
			}
			entry := page.Items[0]
			if mode == "absent-origin" || mode == "null-origin" || mode == "root-alias" {
				if entry.RestoreHandle != "" || entry.OriginalPath != "" || writes.Load() != 0 {
					t.Fatal("ambiguous original directory was accepted", entry)
				}
				return
			}
			if entry.RestoreHandle == "" || entry.ParentID != restoreParent || entry.OriginalPath != displayPath {
				t.Fatal("unexpected restore location", entry)
			}
			sourceChanged.Store(mode == "stale-record")
			preview, err := m.PreviewGuangyaRestore(ctx, entry.RestoreHandle)
			previewShouldFail := mode == "missing-parent" || mode == "stale-record"
			if (err != nil) != previewShouldFail || writes.Load() != 0 || len(m.RecycleRestoreHistory(a.ID)) != 0 {
				t.Fatal("read-only preview returned an unexpected result", preview, err)
			}
			if !previewShouldFail && (preview.Location.AccountID != a.ID || preview.Location.ParentID != restoreParent || preview.Occupied != (mode == "conflict")) {
				t.Fatal("wrong restore preview", preview)
			}
			err = m.ValidateRecycleRestoreBatch(ctx, []string{entry.RestoreHandle})
			if (err != nil) != (mode == "conflict" || previewShouldFail) || writes.Load() != 0 {
				t.Fatal("unexpected batch preflight result", err)
			}
			got, err := m.RestoreRecycle(ctx, entry.RestoreHandle)
			want, wantWrites := "confirmed", int32(1)
			if mode == "conflict" || previewShouldFail {
				want, wantWrites = "not_sent", 0
			} else if mode == "wrong-live-parent" {
				want = "unknown"
			}
			if err != nil || got.State != want || writes.Load() != wantWrites {
				t.Fatal(got, err, writes.Load())
			}
			if _, err = m.CheckRecycleRestore(ctx, got.ID); err != nil || writes.Load() != wantWrites {
				t.Fatal("check replayed restore", err, writes.Load())
			}
		})
	}
}
