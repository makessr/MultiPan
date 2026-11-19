package transfer

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func Test123RecycleRestoreToDestination(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		writes int32
		folder bool
	}{
		{"success-200", "confirmed", 1, false},
		{"case-conflict", "not_sent", 0, false},
		{"stale-path", "not_sent", 0, false},
		{"incomplete-target", "not_sent", 0, false},
		{"lost-ack", "unknown", 1, false},
		{"still-trashed", "unknown", 1, false},
		{"folder-success", "confirmed", 1, true},
		{"folder-descendant", "not_sent", 0, true},
		{"folder-wrong-type", "unknown", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-auth"}
			m.accounts = []Account{a}
			const id = "9007199254740993123"
			size := int64(7)
			entry := RecycleEntry{ID: id, Name: "demo.txt", Kind: "file", Size: &size, OriginalPath: "原目录 ID 77 / demo.txt", ParentID: "77"}
			entryType := 0
			if tc.folder {
				entry.Kind = "folder"
				entry.Size = nil
				entryType = 1
			}
			handle, err := sealRecycleRestore(a, 1, entry)
			if err != nil {
				t.Fatal(err)
			}
			var writes atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "api.123278.com" || r.Header.Get("Authorization") != "Bearer fixture-auth" {
					t.Error("unexpected host or authorization")
				}
				if r.URL.Path == "/a/api/file/recover/by_path" && r.Method == http.MethodPost {
					var payload struct {
						Parent json.Number   `json:"parentFileId"`
						Files  []json.Number `json:"fileIds"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Parent != "88" ||
						len(payload.Files) != 1 || payload.Files[0] != id {
						t.Errorf("unexpected restore body: %+v", payload)
					}
					writes.Add(1)
					if tc.name == "lost-ack" {
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]int{"code": 200})
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				rows := []map[string]any{}
				row := func(fileID, parent, name string, kind int) map[string]any {
					data := map[string]any{"FileId": json.Number(fileID), "ParentFileId": json.Number(parent), "FileName": name, "Type": kind}
					if kind == 0 {
						data["Size"] = 7
					}
					return data
				}
				q := r.URL.Query()
				switch {
				case q.Get("trashed") == "true":
					if writes.Load() == 0 || tc.name == "still-trashed" {
						rows = append(rows, row(id, "77", entry.Name, entryType))
					}
				case q.Get("parentFileId") == "0":
					name := "目标"
					if tc.name == "stale-path" {
						name = "已改名"
					}
					rows = append(rows, row("88", "0", name, 1))
				case q.Get("parentFileId") == "88":
					if tc.name == "case-conflict" {
						rows = append(rows, row("99", "88", "Demo.txt", 0))
					} else if writes.Load() != 0 {
						kind := entryType
						if tc.name == "folder-wrong-type" {
							kind = 0
						}
						rows = append(rows, row(id, "88", entry.Name, kind))
					}
				default:
					t.Error("direct restore must not use original parent")
				}
				total := len(rows)
				if q.Get("parentFileId") == "88" && tc.name == "incomplete-target" {
					total++
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": total, "Next": -1}})
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			old := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = old; transport.CloseIdleConnections() }()

			ctx := context.Background()
			trail := []CloudFolder{{ID: "0", Name: "根目录"}, {ID: "88", Name: "目标"}}
			if tc.name == "folder-descendant" {
				trail = []CloudFolder{trail[0], {ID: id, Name: entry.Name}, trail[1]}
			}
			result, err := m.RestoreRecycleTo(ctx, handle, trail)
			if err != nil || result.State != tc.state || writes.Load() != tc.writes {
				t.Fatalf("result=%+v err=%v writes=%d", result, err, writes.Load())
			}
			if _, err := m.RestoreRecycle(ctx, handle); err == nil {
				t.Fatal("same item accepted another restore destination")
			}
			m.Close()
			m, err = NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			repeated, err := m.RestoreRecycleTo(ctx, handle, trail)
			if err != nil || repeated.ID != result.ID || repeated.State != tc.state || writes.Load() != tc.writes {
				t.Fatalf("repeated result=%+v err=%v writes=%d", repeated, err, writes.Load())
			}
			if result.CanCheck {
				checked, err := m.CheckRecycleRestore(ctx, result.ID)
				if err != nil || checked.State != tc.state || writes.Load() != tc.writes {
					t.Fatalf("read-only check result=%+v err=%v writes=%d", checked, err, writes.Load())
				}
			}
			if tc.state == "confirmed" {
				location, err := m.LocateRecycleRestore(ctx, a.ID, result.ID)
				if err != nil || location.ParentID != "88" || location.MoveFile != nil || location.MoveFolderID != "" {
					t.Fatalf("expected destination location: %+v, %v", location, err)
				}
			}
		})
	}
}
