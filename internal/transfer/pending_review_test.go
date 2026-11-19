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
	"sync/atomic"
	"testing"
	"time"
)

func pendingReviewFixture(t *testing.T) *Manager {
	t.Helper()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	q := Account{ID: "q", Kind: "quark", Endpoint: QuarkAPI, Secret: "__puus=private-q"}
	b := Account{ID: "b", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=private-b"}
	g := Account{ID: "g", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "private-g"}
	d := Account{ID: "d", Kind: "webdav", Endpoint: "https://dav.fixture", Folder: "/", Username: "private-user", Secret: "private-d"}
	m.accounts = []Account{q, b, g, d}
	now := time.Now()
	creation := &shareOperation{in: ShareInput{AccountID: "q", ParentID: "0", EntryID: "file", Name: "file.txt", Action: "create"}, scope: downloadScope(q), key: "creation-key", share: "provider-private", created: now, result: ShareResult{ID: "same", State: "unknown", Kind: "quark", Name: "file.txt", CanCheck: true, URL: "https://pan.quark.cn/s/private-url", Password: "private-password", Message: "provider-private-error"}}
	creation.saved = snapshotShare(creation)
	m.shares["same"] = creation
	h := managedShareHandle{Kind: "guangya", AccountID: "g", Scope: downloadScope(g), ProviderID: "original", PublicID: "public", RawID: json.RawMessage(`"original"`), Page: 1, Name: "settings.txt", URL: "https://www.guangyapan.com/s/public"}
	handle, e := sealManagedShare(h)
	if e != nil {
		t.Fatal(e)
	}
	settings := &shareOperation{in: ShareInput{AccountID: "g", Handle: handle, ParentID: "managed-share", EntryID: "original", Name: h.Name, Action: "settings", SettingsIntent: "intent"}, scope: downloadScope(g), key: "settings-key", share: "original", created: now, result: ShareResult{ID: "settings", State: "unknown", Kind: "guangya", Name: h.Name, CanCheck: true}}
	settings.saved = snapshotShare(settings)
	m.shares["settings"] = settings
	m.shareRevokes["same"] = &shareRevocation{saved: diskShareRevocation{Source: managedShareHandle{Kind: "quark", AccountID: "q", Scope: downloadScope(q), ProviderID: "another-private", Name: "revoke.txt"}, Submitted: true, Result: ShareRevocationResult{ID: "same", Name: "revoke.txt", State: "unknown", Created: now, CanCheck: true}}}
	size := int64(1)
	m.copies["same"] = &copyOperation{saved: diskCopy{Scope: downloadScope(b), Submitted: true, Input: MoveBatchEntry{Input: MoveFileInput{AccountID: "b", ParentID: "/", EntryID: "/file.txt", Name: "file.txt", DestinationID: "/target"}}, Source: CloudEntry{ID: "/file.txt", Name: "file.txt", Size: &size}, Manifest: []CopyManifestItem{{Path: "", Size: 1}}, Receipt: CopyReceipt{ID: "same", AccountID: "b", Name: "copy.txt", State: "unknown", Created: now}}}
	m.trashes["same"] = &trashOperation{saved: diskTrash{Source: trashSource{Scope: downloadScope(b), Input: TrashInput{AccountID: "b", ParentID: "/", Entry: CloudEntry{ID: "/file.txt", Name: "file.txt", ObjectID: "1", Size: &size}}, Original: RecycleEntry{ID: "1", Name: "file.txt", Kind: "file", Size: &size, OriginalPath: "/file.txt"}}, Submitted: true, Receipt: TrashReceipt{ID: "same", AccountID: "b", Name: "trash.txt", State: "unknown", Created: now}}}
	m.recycleRestores["same"] = &recycleRestore{saved: diskRecycleRestore{Source: recycleRestoreSource{Purpose: "baidu-recycle-restore-v1", AccountID: "b", Scope: downloadScope(b), Page: 1, Entry: RecycleEntry{ID: "1", Name: "file.txt", Kind: "file", Size: &size, OriginalPath: "/file.txt"}}, Submitted: true, Result: RecycleRestoreResult{ID: "same", Name: "restore.txt", State: "unknown", Created: now, CanCheck: true}}}
	m.recyclePurges["same"] = &recyclePurgeOperation{saved: diskRecyclePurge{Source: recyclePurgeSource{Purpose: "recycle-purge-v1", Kind: "baidu", AccountID: "b", Scope: downloadScope(b), Entry: RecycleEntry{ID: "1", Name: "file.txt", Kind: "file", Size: &size, OriginalPath: "/file.txt"}}, Submitted: true, Acknowledged: true, Receipt: TrashReceipt{ID: "same", AccountID: "b", Name: "purge.txt", State: "unknown", Created: now}}}
	m.relays = []diskRelay{{SourceKind: "baidu", TargetKind: "webdav", Input: RelayInput{SourceAccountID: "b", TargetAccountID: "d", ParentID: "/", EntryID: "/file.txt", Name: "file.txt", Size: 1, DestinationID: "/"}, SourceScope: downloadScope(b), TargetScope: downloadScope(d), Submitted: true, Job: RelayJob{ID: "same", Name: "relay.txt", State: "unknown", Created: now}}}
	return m
}

func TestPendingReviewLocalIsolationAndLimits(t *testing.T) {
	m := pendingReviewFixture(t)
	s := m.PendingReview()
	if len(s.Rows) != 8 || s.Total != 8 || s.Limited {
		t.Fatal(s)
	}
	seen := map[string]bool{}
	for _, r := range s.Rows {
		if seen[r.Key] {
			t.Fatal("cross-kind collision")
		}
		seen[r.Key] = true
		if r.Kind == "relay" {
			if r.CanCheck {
				t.Fatal("relay check exposed")
			}
		} else if !r.CanCheck {
			t.Fatal("pending receipt cannot check", r)
		}
	}
	raw, _ := json.Marshal(s)
	for _, secret := range []string{"private", "provider-private", "https://", "/target", "creation-key"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private field in DTO", secret)
		}
	}
	s.Rows[0].Name = "changed"
	if m.PendingReview().Rows[0].Name == "changed" {
		t.Fatal("snapshot aliases storage")
	}
	m.mu.Lock()
	m.shares["same"].running = true
	m.mu.Unlock()
	for _, r := range m.PendingReview().Rows {
		if r.Kind == "share" && r.CanCheck {
			t.Fatal("active check exposed")
		}
	}
	m.mu.Lock()
	m.shares["same"].running = false
	m.accounts = nil
	m.copies["same"].saved.Receipt.State = "confirmed"
	m.mu.Unlock()
	s = m.PendingReview()
	if len(s.Rows) != 7 {
		t.Fatal("removed-account pending lost or finalized receipt resurrected", s)
	}
	for _, r := range s.Rows {
		if r.CanCheck {
			t.Fatal("removed source can check", r)
		}
	}
	if _, e := m.CheckPendingReview(context.Background(), "share", "same"); e == nil {
		t.Fatal("old source accepted")
	}
	m.mu.Lock()
	m.shares = map[string]*shareOperation{}
	m.shareRevokes = nil
	m.copies = nil
	m.trashes = nil
	m.recycleRestores = nil
	m.recyclePurges = nil
	m.relays = nil
	for i := 0; i < 2001; i++ {
		id := fmt.Sprintf("%04d", i)
		m.shares[id] = &shareOperation{in: ShareInput{AccountID: "removed"}, created: time.Unix(int64(i), 0), result: ShareResult{ID: id, Name: id, State: "unknown"}}
	}
	m.mu.Unlock()
	s = m.PendingReview()
	if !s.Limited || s.Total != 2001 || len(s.Rows) != 2000 || s.Rows[0].ID != "2000" {
		t.Fatal("limit/order not explicit", s.Total, len(s.Rows))
	}
}

func TestPendingReviewClassifiesLanzouPasswordChangeAsSettings(t *testing.T) {
	a := Account{ID: "blue", Kind: "lanzou", Endpoint: "https://up.woozooo.com"}
	op := &shareOperation{
		in: ShareInput{AccountID: a.ID, Action: "password"}, scope: downloadScope(a),
		result: ShareResult{ID: "code-change", Kind: a.Kind, State: "unknown", CanCheck: true}, created: time.Now(),
	}
	m := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{op.result.ID: op}}
	if rows := m.ShareHistory(); len(rows) != 1 || rows[0].Action != "password" {
		t.Fatal("missing original action", rows)
	}
	if rows := m.PendingReview().Rows; len(rows) != 1 || rows[0].Key != "share_settings:code-change" || !rows[0].CanCheck {
		t.Fatal("password change not grouped with share settings", rows)
	}
}

func TestPendingReviewDispatchOnlyReads(t *testing.T) {
	for _, kind := range []string{"share", "share_settings", "share_revoke", "copy", "trash", "restore", "purge"} {
		t.Run(kind, func(t *testing.T) {
			m := pendingReviewFixture(t)
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" && !(r.Method == "POST" && r.Host == "api.guangyapan.com" && r.URL.Path == "/userres/v1/get_share_list") {
					t.Error("mutation through check endpoint", r.Method, r.Host, r.URL.Path)
				}
				w.WriteHeader(503)
				fmt.Fprint(w, "private-remote-error")
			}))
			old := http.DefaultTransport
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = old; tr.CloseIdleConnections(); server.Close() }()
			m.PendingReview()
			if requests.Load() != 0 {
				t.Fatal("listing contacted cloud")
			}
			for _, bad := range []string{"relay", "resume", "trash_submit", ""} {
				if _, e := m.CheckPendingReview(context.Background(), bad, "same"); e == nil {
					t.Fatal("non-read dispatch accepted", bad)
				}
			}
			if requests.Load() != 0 {
				t.Fatal("invalid dispatch contacted cloud")
			}
			id := "same"
			if kind == "share_settings" {
				id = "settings"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, e := m.CheckPendingReview(ctx, kind, id)
			if e != nil {
				t.Fatal(e)
			}
			if result.State != "unknown" || requests.Load() == 0 {
				t.Fatal("failed read not retained", result, requests.Load())
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "private-") {
				t.Fatal("provider data in outcome")
			}
		})
	}
}
