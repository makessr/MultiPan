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

func TestBaiduRecycleRestoreVerifiedPersistentAndNeverReplayed(t *testing.T) {
	for _, mode := range []string{"success", "folder", "missing-ack", "pending", "wrong-id", "wrong-size", "changed-source", "conflict", "parent-unreadable", "initial-save-failed", "checkpoint-failed"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture-auth"}
			m.accounts = []Account{a}
			size := int64(7)
			entry := RecycleEntry{ID: "9007199254740993123", Name: "file.txt", Kind: "file", Size: &size, OriginalPath: "/docs/file.txt"}
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
			correct := mode != "wrong-id" && mode != "wrong-size"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Host != "pan.baidu.com" || r.Header.Get("Cookie") != "BDUSS=fixture-auth" {
					t.Error("wrong auth/origin")
				}
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					if r.Method != "GET" {
						t.Error("session wrote")
					}
					write(map[string]any{"errno": 0, "result": map[string]string{"bdstoken": "fixture-token"}})
				case "/api/recycle/list":
					if r.Method != "GET" || r.URL.Query().Get("num") != "100" || r.URL.Query().Get("page") != "1" {
						t.Error("wrong recycle preflight")
					}
					name := entry.Name
					if mode == "changed-source" {
						name = "changed.txt"
					}
					dir := 0
					if entry.Kind == "folder" {
						dir = 1
					}
					write(map[string]any{"errno": 0, "list": []any{map[string]any{"fs_id": json.RawMessage(entry.ID), "server_filename": name, "path": entry.OriginalPath, "isdir": dir, "size": 7}}})
				case "/api/list":
					if r.Method != "GET" || r.URL.Query().Get("dir") != "/docs" {
						t.Error("wrong original parent")
					}
					if mode == "parent-unreadable" {
						write(map[string]any{"errno": -9})
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
						fileSize := 7
						if mode == "conflict" || !correct && mode == "wrong-id" {
							id = "22"
						}
						if !correct && mode == "wrong-size" {
							fileSize = 99
						}
						dir := 0
						if entry.Kind == "folder" {
							dir = 1
						}
						rows = append(rows, map[string]any{"fs_id": json.RawMessage(id), "server_filename": entry.Name, "path": entry.OriginalPath, "isdir": dir, "size": fileSize})
					}
					write(map[string]any{"errno": 0, "list": rows})
				case "/api/recycle/restore":
					if r.Method != "POST" || r.URL.Query().Get("async") != "1" {
						t.Error("invalid restore request")
					}
					_ = r.ParseForm()
					if r.PostForm.Get("fidlist") != "[9007199254740993123]" || len(r.PostForm) != 1 {
						t.Error("wrong restore identity/payload", r.PostForm)
					}
					posts++
					completed = mode != "pending"
					if mode == "missing-ack" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"errno":0}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
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
			case "pending", "wrong-id", "wrong-size":
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
			if want == "unknown" {
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
func TestRecycleRestoreHandlePurposeAndMissingOrigin(t *testing.T) {
	a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "fixture"}
	entry := RecycleEntry{ID: "1", Name: "file", Kind: "file", OriginalPath: "/file"}
	h, e := sealRecycleRestore(a, 1, entry)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeRecycleRestore(h)
	if e != nil || decoded.Entry.ID != "1" {
		t.Fatal(decoded, e)
	}
	for _, bad := range []string{"", h + "changed"} {
		if _, e := decodeRecycleRestore(bad); e == nil {
			t.Fatal("invalid handle accepted")
		}
	}
	for _, entry := range []RecycleEntry{{ID: "1", Name: "file", Kind: "file"}, {ID: "1", Name: "other", Kind: "file", OriginalPath: "/file"}, {ID: "1", Name: "file", Kind: "unknown", OriginalPath: "/file"}} {
		if _, e := sealRecycleRestore(a, 1, entry); e == nil {
			t.Fatal("incomplete source accepted")
		}
	}
	decoded.Purpose = "other"
	if validRecycleRestoreSource(decoded) {
		t.Fatal("cross-purpose source accepted")
	}
}
