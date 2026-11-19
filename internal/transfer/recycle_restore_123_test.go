package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func Test123RecycleRestoreVerifiedPersistentAndNeverReplayed(t *testing.T) {
	for _, mode := range []string{"success", "folder", "missing-ack", "pending", "wrong-id", "wrong-size", "wrong-parent", "wrong-type", "missing-size", "changed-source", "conflict", "parent-unreadable", "initial-save-failed", "checkpoint-failed"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-auth"}
			m.accounts = []Account{a}
			size := int64(7)
			entry := RecycleEntry{ID: "9007199254740993123", Name: "file.txt", Kind: "file", Size: &size, OriginalPath: "原目录 ID 77 / file.txt", ParentID: "77"}
			if mode == "folder" {
				entry.Kind = "folder"
			}
			handle, e := sealRecycleRestore(a, 1, entry)
			if e != nil {
				t.Fatal(e)
			}
			var mu sync.Mutex
			posts := 0
			completed := false
			correct := mode != "wrong-id" && mode != "wrong-size" && mode != "wrong-parent" && mode != "wrong-type" && mode != "missing-size"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				if r.Host != "api.123278.com" || r.Header.Get("Authorization") != "Bearer fixture-auth" {
					t.Error("wrong auth/origin")
				}
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				switch r.URL.Path {
				case "/b/api/file/list/new":
					if r.Method != "GET" {
						t.Error("list wrote")
					}
					if r.URL.Query().Get("trashed") == "true" {
						name := entry.Name
						if mode == "changed-source" {
							name = "changed.txt"
						}
						typ := 0
						if entry.Kind == "folder" {
							typ = 1
						}
						write(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{map[string]any{"FileId": json.RawMessage(entry.ID), "FileName": name, "Type": typ, "Size": 7, "ParentFileId": 77}}, "Next": -1, "Total": 1}})
						return
					}
					if r.URL.Query().Get("parentFileId") != "77" || r.URL.Query().Get("trashed") != "false" {
						t.Error("wrong parent/filter")
					}
					if mode == "parent-unreadable" {
						write(map[string]any{"code": 404})
						return
					}
					if mode == "checkpoint-failed" && posts == 0 {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					rows := []any{}
					if completed || mode == "conflict" {
						id := entry.ID
						sz := any(7)
						parent := 77
						typ := 0
						if entry.Kind == "folder" {
							typ = 1
						}
						if mode == "conflict" || !correct && mode == "wrong-id" {
							id = "22"
						}
						if !correct && mode == "wrong-size" {
							sz = 99
						}
						if !correct && mode == "wrong-parent" {
							parent = 88
						}
						if !correct && mode == "wrong-type" {
							typ = 1
						}
						if !correct && mode == "missing-size" {
							sz = nil
						}
						rows = append(rows, map[string]any{"FileId": json.RawMessage(id), "FileName": entry.Name, "Type": typ, "Size": sz, "ParentFileId": parent})
					}
					write(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Next": -1, "Total": len(rows)}})
				case "/a/api/file/trash":
					if r.Method != "POST" {
						t.Error("wrong method")
					}
					var body struct {
						Drive     int   `json:"driveId"`
						Operation *bool `json:"operation"`
						Rows      []struct {
							ID json.Number `json:"FileId"`
						} `json:"fileTrashInfoList"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Drive != 0 || body.Operation == nil || *body.Operation || len(body.Rows) != 1 || string(body.Rows[0].ID) != entry.ID {
						t.Error("wrong restore payload")
					}
					posts++
					completed = mode != "pending"
					if mode == "missing-ack" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"code":0,"data":{}}`)
				default:
					t.Error("unexpected route", r.URL.Path)
				}

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
			defer func() { http.DefaultTransport = old }()
			if mode == "initial-save-failed" {
				m.path = t.TempDir()
			}
			r, e := m.RestoreRecycle(context.Background(), handle)
			if mode == "initial-save-failed" {
				if e == nil || posts != 0 || len(m.recycleRestores) != 0 {
					t.Fatal("save failure sent request", r, e, posts)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			want := "confirmed"
			switch mode {
			case "pending", "wrong-id", "wrong-size", "wrong-parent", "wrong-type", "missing-size":
				want = "unknown"
			case "changed-source", "conflict", "parent-unreadable", "checkpoint-failed":
				want = "not_sent"
			}
			if r.State != want {
				t.Fatal(mode, r)
			}
			wantedPosts := 1
			if want == "not_sent" {
				wantedPosts = 0
			}
			if posts != wantedPosts {
				t.Fatal("unexpected post count", posts)
			}
			repeated, e := m.RestoreRecycle(context.Background(), handle)
			if e != nil || repeated.ID != r.ID || posts != wantedPosts {
				t.Fatal("restore replayed", repeated, e, posts)
			}
			m.path = statePath
			m.Close()
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			hist := m.RecycleRestoreHistory(a.ID)
			if len(hist) != 1 || hist[0].EntryID != entry.ID || hist[0].State != want {
				t.Fatal("receipt lost", hist)
			}
			if mode == "success" {
				location, err := m.LocateRecycleRestore(context.Background(), a.ID, r.ID)
				if err != nil || location.AccountID != a.ID || location.ParentID != entry.ParentID ||
					location.MoveFile == nil || location.MoveFile.ID != entry.ID || location.MoveFile.Name != entry.Name ||
					location.MoveFile.Size != size || !hist[0].CanMove || posts != wantedPosts {
					t.Fatal("confirmed receipt did not locate its parent", location, err, posts)
				}
				mu.Lock()
				completed = false
				mu.Unlock()
				location, err = m.LocateRecycleRestore(context.Background(), a.ID, r.ID)
				if err == nil || location.ParentID != "" || posts != wantedPosts || m.RecycleRestoreHistory(a.ID)[0].State != "confirmed" {
					t.Fatal("missing item produced a navigation target or changed its receipt", location, err, posts)
				}
			}
			if want == "unknown" {
				if _, e := m.LocateRecycleRestore(context.Background(), a.ID, r.ID); e == nil {
					t.Fatal("uncertain receipt allowed navigation")
				}
				if n, e := m.ClearRecycleRestores(a.ID); e != nil || n != 0 {
					t.Fatal("unknown receipt cleared", n, e)
				}
				_, e = m.CheckRecycleRestore(context.Background(), r.ID)
				if e != nil || posts != wantedPosts {
					t.Fatal("check wrote", e, posts)
				}
				mu.Lock()
				completed = true
				correct = true
				mu.Unlock()
				checked, e := m.CheckRecycleRestore(context.Background(), r.ID)
				if e != nil || checked.State != "confirmed" || posts != wantedPosts {
					t.Fatal("readonly confirmation failed", checked, e, posts)
				}
			}
			m.accounts[0].Secret = "changed"
			if _, e := m.LocateRecycleRestore(context.Background(), a.ID, r.ID); e == nil {
				t.Fatal("changed source exposed a navigation target")
			}
			if _, e = m.CheckRecycleRestore(context.Background(), r.ID); e == nil {
				t.Fatal("changed source reused receipt")
			}
			if m.RecycleRestoreHistory(a.ID)[0].CanCheck {
				t.Fatal("changed source exposed check")
			}
			m.accounts[0] = a
			m.path = t.TempDir()
			if n, e := m.ClearRecycleRestores(a.ID); e == nil || n != 0 || len(m.recycleRestores) != 1 {
				t.Fatal("failed clear lost receipt")
			}
			m.path = statePath
			if n, e := m.ClearRecycleRestores(a.ID); e != nil || n != 1 {
				t.Fatal("clear failed", n, e)
			}
			m.Close()
		})
	}
}

func Test123RecycleRestoreHandleOriginAndProvider(t *testing.T) {
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	size := int64(0)
	entry := RecycleEntry{ID: "9007199254740993123", Name: "zero.txt", Kind: "file", Size: &size, ParentID: "0", OriginalPath: "原目录 ID 0 / zero.txt"}
	token, e := sealRecycleRestore(a, 1, entry)
	if e != nil {
		t.Fatal(e)
	}
	h, e := decodeRecycleRestore(token)
	if e != nil || h.Entry.ParentID != "0" || !recycleRestoreAccount(a, h) {
		t.Fatal(h, e)
	}
	b := a
	b.Kind = "baidu"
	b.Endpoint = BaiduAPI
	if recycleRestoreAccount(b, h) {
		t.Fatal("cross-provider handle accepted")
	}
	for _, mode := range []string{"missing-parent", "wrong-display", "self-parent", "missing-size", "unknown-type"} {
		bad := entry
		switch mode {
		case "missing-parent":
			bad.ParentID = ""
		case "wrong-display":
			bad.OriginalPath = "/zero.txt"
		case "self-parent":
			bad.ParentID = bad.ID
		case "missing-size":
			bad.Size = nil
		case "unknown-type":
			bad.Kind = "unknown"
		}
		if _, e := sealRecycleRestore(a, 1, bad); e == nil {
			t.Fatal("invalid origin sealed", mode)
		}
	}
}
