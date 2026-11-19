package transfer

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestGuangyaPurgeSelectedRequiresTaskAndDeletedIdentity(t *testing.T) {
	for _, mode := range []string{"confirmed", "folder", "restored-before-write", "moved-back-to-trash", "live-elsewhere", "generic-detail-error", "reappeared", "missing-task"} {
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
			size := int64(3)
			entry := RecycleEntry{ID: "fixture-file", Kind: "file", Name: "disposable.txt", Size: &size}
			resourceType := 1
			if mode == "folder" {
				entry.Kind = "folder"
				resourceType = 2
			}
			present, deleted := true, false
			writes, taskReads, detailReads := 0, 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "api.guangyapan.com" || r.Method != http.MethodPost {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				body := `{"code":0,"data":{}}`
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					var q struct {
						DirType int `json:"dirType"`
					}
					if json.NewDecoder(r.Body).Decode(&q) != nil || q.DirType != 4 {
						t.Error("expected a recycle-only read", q)
					}
					items := []map[string]any{}
					if present {
						items = append(items, map[string]any{"fileId": entry.ID, "fileName": entry.Name, "resType": resourceType, "fileSize": size})
					}
					encoded, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"total": len(items), "list": items}})
					body = string(encoded)
				case "/userres/v1/file/delete_file":
					writes++
					var selected map[string][]string
					if json.NewDecoder(r.Body).Decode(&selected) != nil || len(selected) != 1 ||
						len(selected["fileIds"]) != 1 || selected["fileIds"][0] != entry.ID {
						t.Error("unexpected permanent deletion scope", selected)
					}
					disk, err := readState(statePath)
					if err != nil || len(disk.RecyclePurges) != 1 || !disk.RecyclePurges[0].Submitted {
						t.Error("delete sent without persisted intent", err)
					}
					present = mode == "moved-back-to-trash"
					deleted = mode != "live-elsewhere" && mode != "generic-detail-error"
					if mode != "missing-task" {
						body = `{"code":0,"data":{"taskId":"fixture-task"}}`
					}
				case "/userres/v1/get_task_status":
					taskReads++
					var q map[string]string
					if json.NewDecoder(r.Body).Decode(&q) != nil || q["taskId"] != "fixture-task" {
						t.Error("readback did not use the original task", q)
					}
					body = `{"code":0,"data":{"status":2}}`
				case "/userres/v1/file/get_file_detail":
					detailReads++
					var q map[string]string
					if json.NewDecoder(r.Body).Decode(&q) != nil || q["fileId"] != entry.ID {
						t.Error("readback did not use the original ID", q)
					}
					if deleted {
						body = `{"code":149}`
					} else if mode == "generic-detail-error" {
						body = `{"code":1}`
					}
					if mode == "reappeared" {
						present = true
					}
				default:
					t.Error("unexpected operation", r.URL)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = io.WriteString(w, body)
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
			prepared, err := m.PrepareRecyclePurge(ctx, a.ID, 1, []RecycleEntry{entry})
			if err != nil || len(prepared) != 1 || writes != 0 {
				t.Fatal("invalid read-only preparation", prepared, err, writes)
			}
			if mode == "restored-before-write" {
				present = false
			}
			if _, err = m.PurgeRecycle(ctx, prepared[0].Handle, "清空回收站"); err == nil {
				t.Fatal("clear-all confirmation accepted for selected deletion")
			}
			got, err := m.PurgeRecycle(ctx, prepared[0].Handle, "永久删除")
			want, wantWrites := "unknown", 1
			switch mode {
			case "confirmed", "folder":
				want = "confirmed"
			case "restored-before-write":
				want, wantWrites = "not_sent", 0
			}
			if err != nil || got.State != want || writes != wantWrites {
				t.Fatal(got, err, writes)
			}
			if mode == "missing-task" && (taskReads != 0 || detailReads != 0) {
				t.Fatal("missing task receipt was used for verification")
			}
			if mode == "live-elsewhere" {
				reopened, err := NewManager(statePath, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				deleted = true
				if _, err := reopened.PreviewRecycleEmpty(ctx, a.ID); err == nil {
					t.Fatal("clear-all was offered before verifying selected deletion")
				}
				checked, err := reopened.CheckRecyclePurge(ctx, got.ID)
				if err != nil || checked.State != "confirmed" || writes != 1 {
					t.Fatal("read-only recovery failed", checked, err, writes)
				}
			}
		})
	}
}
