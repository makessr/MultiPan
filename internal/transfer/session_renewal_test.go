package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type renewalNoNetwork struct{ t *testing.T }

func (r renewalNoNetwork) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Error("unexpected network request in guard test", req.URL.Host, req.URL.Path)
	return nil, fmt.Errorf("unexpected network")
}

func Test123SessionRenewalIdentityCheckpointAndScope(t *testing.T) {
	for _, mode := range []string{"success", "scheduled", "same-token", "old-read-failed", "bad-uid", "known-uid-changed", "refresh-rejected", "empty-token", "invalid-token", "redirect", "new-read-failed", "other-uid", "checkpoint-failed", "final-save-failed", "account-reordered", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			const uid = "9007199254740993123"
			const oldToken = "private-old-token"
			const newToken = "private-new-token"
			path := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(path, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			a := Account{ID: "a", Name: "123", Kind: "pan123", Endpoint: Pan123API, Folder: "0", Secret: oldToken}
			scope := downloadScope(a)
			m.accounts = []Account{{ID: "other", Kind: "webdav", Name: "other", Secret: "other-private"}, a}
			m.fingerprints = []UploadFingerprint{{AccountID: "a", EntryID: "1", Name: "file.txt", Size: 1, SHA256: strings.Repeat("a", 64)}}
			h := managedShareHandle{AccountID: "a", Scope: scope, Kind: "pan123", ProviderID: "123", Name: "existing", URL: "https://www.123pan.com/s/existing", Page: 1, Cursor: "0", Key123: "existing"}
			m.shareRevokes["receipt"] = &shareRevocation{saved: diskShareRevocation{Source: h, Submitted: true, Result: ShareRevocationResult{ID: "receipt", Name: "existing", State: "unknown", CanCheck: true, Created: time.Now()}}}
			if mode == "known-uid-changed" {
				m.accounts[1].renewal = diskSessionRenewal{Scope: scope, UID: "99"}
			}
			if e = m.SetSessionAutoRenew("a", true); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			reads, refreshes := 0, 0
			badPath := t.TempDir()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host != "api.123278.com" || r.Method != "GET" || r.Header.Get("Cookie") != "" {
					t.Error("credential origin/method", r.Host, r.Method)
				}
				if r.URL.Path == "/api/user/refresh_token" {
					refreshes++
					if r.Header.Get("Authorization") != "Bearer "+oldToken || r.Header.Get("platform") != "pc" || r.Header.Get("app-version") != "3.2.7" || r.URL.RawQuery != "" {
						t.Error("refresh request shape")
					}
					saved, e := readState(path)
					if e != nil || len(saved.Accounts) != 2 || saved.Accounts[1].Renewal == nil || !saved.Accounts[1].Renewal.Pending || saved.Accounts[1].Secret != oldToken {
						t.Error("missing durable write-ahead", e)
					}
					if mode == "account-reordered" {
						if e = m.DeleteAccount("other"); e != nil {
							t.Error(e)
						}
					}
					if mode == "cancelled" {
						cancel()
						w.WriteHeader(503)
						return
					}
					if mode == "refresh-rejected" {
						w.WriteHeader(503)
						fmt.Fprint(w, "private-body")
						return
					}
					if mode == "redirect" {
						w.Header().Set("Location", "https://foreign.example/token")
						w.WriteHeader(302)
						return
					}
					token := newToken
					if mode == "same-token" {
						token = oldToken
					}
					if mode == "empty-token" {
						token = ""
					}
					if mode == "invalid-token" {
						token = "bad\r\ncredential"
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"token": token, "expire": "do-not-expose-private-expiry"}})
					return
				}
				if r.URL.Path != "/b/api/user/info" {
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(405)
					return
				}
				reads++
				if reads == 1 {
					if r.Header.Get("Authorization") != "Bearer "+oldToken {
						t.Error("old auth")
					}
					if mode == "old-read-failed" {
						w.WriteHeader(403)
						return
					}
					if mode == "checkpoint-failed" {
						m.mu.Lock()
						m.path = badPath
						m.mu.Unlock()
					}
				} else {
					want := newToken
					if mode == "same-token" {
						want = oldToken
					}
					if r.Header.Get("Authorization") != "Bearer "+want {
						t.Error("new auth")
					}
					if mode == "new-read-failed" {
						w.WriteHeader(503)
						return
					}
					if mode == "final-save-failed" {
						m.mu.Lock()
						m.path = badPath
						m.mu.Unlock()
					}
				}
				value := uid
				if mode == "bad-uid" {
					value = "1e2"
				}
				if mode == "other-uid" && reads > 1 {
					value = "2"
				}
				fmt.Fprintf(w, `{"code":0,"data":{"UID":%s}}`, value)
			}))
			original := http.DefaultTransport
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = original; tr.CloseIdleConnections(); server.Close() }()
			var view SessionRenewalView
			if mode == "scheduled" {
				m.renewDueSessions(ctx, time.Now())
				view = m.SessionRenewals()[0]
				m.renewDueSessions(ctx, time.Now())
				if e = m.SetSessionAutoRenew("a", false); e != nil {
					t.Fatal(e)
				}
				m.renewDueSessions(ctx, time.Now().Add(24*time.Hour))
			} else {
				view, e = m.RenewAccountSession(ctx, "a")
			}
			success := mode == "success" || mode == "scheduled" || mode == "same-token" || mode == "account-reordered"
			if (e == nil) != success {
				t.Fatal(mode, view, e)
			}
			m.mu.Lock()
			m.path = path
			current := m.downloadAccountLocked("a")
			fingerprints := len(m.fingerprints)
			m.mu.Unlock()
			mu.Lock()
			count := refreshes
			mu.Unlock()
			sent := mode != "old-read-failed" && mode != "bad-uid" && mode != "known-uid-changed" && mode != "checkpoint-failed"
			wantCount := 0
			if sent {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatal("refresh replay/early request", count)
			}
			if success {
				want := newToken
				if mode == "same-token" {
					want = oldToken
				}
				if current.Secret != want || downloadScope(current) != scope || current.renewal.UID != uid || current.renewal.Pending || fingerprints != 1 {
					t.Fatal("trusted source continuity lost")
				}
				if !m.ShareRevocations("a")[0].CanCheck {
					t.Fatal("old unknown receipt orphaned")
				}
			} else if current.Secret != oldToken {
				t.Fatal("unverified token adopted")
			}
			if mode == "known-uid-changed" && (downloadScope(current) == scope || fingerprints != 0) {
				t.Fatal("changed identity retained old sources")
			}
			raw, _ := json.Marshal(m.Snapshot())
			views, _ := json.Marshal(m.SessionRenewals())
			for _, secret := range []string{oldToken, newToken, uid, scope, "do-not-expose"} {
				if strings.Contains(string(raw), secret) || strings.Contains(string(views), secret) {
					t.Fatal("renewal evidence leaked", secret)
				}
			}
			if e != nil && (strings.Contains(e.Error(), "private-") || strings.Contains(e.Error(), uid)) {
				t.Fatal("secret in error")
			}
			if sent && !success {
				if !current.renewal.Pending || current.renewal.Enabled {
					t.Fatal("uncertain refresh not paused")
				}
				if _, e = m.RenewAccountSession(context.Background(), "a"); e == nil {
					t.Fatal("uncertain refresh repeated")
				}
				found := false
				for _, row := range m.PendingReview().Rows {
					if row.Kind == "session_renewal" {
						found = true
						if row.CanCheck {
							t.Fatal("credential update replay exposed in pending center")
						}
					}
				}
				if !found {
					t.Fatal("unconfirmed renewal absent from pending center")
				}
			}
			m.Close()
			restored, e := NewManager(path, nil)
			if e != nil {
				t.Fatal("restore", e)
			}
			defer restored.Close()
			saved := restored.downloadAccountLocked("a")
			if success && downloadScope(saved) != scope {
				t.Fatal("stable scope lost on restart")
			}
			if sent && !success {
				if !saved.renewal.Pending || saved.renewal.Enabled {
					t.Fatal("restart replay enabled")
				}
				restored.renewDueSessions(context.Background(), time.Now().Add(48*time.Hour))
				if _, e = restored.RenewAccountSession(context.Background(), "a"); e == nil {
					t.Fatal("restart replay")
				}
			}
			bytes, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(bytes), oldToken) || strings.Contains(string(bytes), newToken) {
				t.Fatal("credential stored in plaintext")
			}
			if success {
				in := AccountInput{ID: "a", Name: "renamed", Kind: "pan123", Folder: "0"}
				if _, e = restored.SaveAccount(in); e != nil {
					t.Fatal(e)
				}
				if downloadScope(restored.downloadAccountLocked("a")) != scope {
					t.Fatal("name edit reset renewal proof")
				}
				in.Secret = "manual-new-token"
				if _, e = restored.SaveAccount(in); e != nil {
					t.Fatal(e)
				}
				if downloadScope(restored.downloadAccountLocked("a")) == scope || len(restored.fingerprints) != 0 || restored.downloadAccountLocked("a").renewal.Enabled {
					t.Fatal("manual credential change reused old proof")
				}
				in.Secret = oldToken
				if _, e = restored.SaveAccount(in); e != nil {
					t.Fatal(e)
				}
				if downloadScope(restored.downloadAccountLocked("a")) == scope {
					t.Fatal("reusing an old token resurrected the old source namespace")
				}
			}
		})
	}
}

func TestSessionRenewalBusyAndDueGuards(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a := Account{ID: "a", Name: "123", Kind: "pan123", Endpoint: Pan123API, Folder: "0", Secret: "private"}
	m.accounts = []Account{a}
	m.renaming = map[string]bool{}
	original := http.DefaultTransport
	http.DefaultTransport = renewalNoNetwork{t}
	defer func() { http.DefaultTransport = original }()
	for _, mode := range []string{"upload", "download", "legacy", "account-check", "search", "relay-source", "relay-target", "mutation"} {
		t.Run(mode, func(t *testing.T) {
			m.mu.Lock()
			m.tasks = nil
			m.downloads = nil
			m.downloadImmediate = false
			m.relays = nil
			m.accountCheck = nil
			m.search = nil
			delete(m.renaming, "a")
			switch mode {
			case "upload":
				m.tasks = []Task{{AccountID: "a", Status: "queued"}}
			case "download":
				m.downloads = []DownloadJob{{Input: DownloadInput{AccountID: "a"}, Status: "queued"}}
			case "legacy":
				m.downloadImmediate = true
			case "account-check":
				m.accountCheck = &accountCheck{running: true, scopes: map[string]string{"a": "scope"}, cancel: func() {}}
			case "search":
				m.search = &cloudSearch{running: true, scopes: map[string]string{"a": "scope"}, cancel: func() {}}
			case "relay-source":
				m.relays = []diskRelay{{Job: RelayJob{State: "queued"}, Input: RelayInput{SourceAccountID: "a"}}}
			case "relay-target":
				m.relays = []diskRelay{{Job: RelayJob{State: "queued"}, Input: RelayInput{TargetAccountID: "a"}}}
			case "mutation":
				m.renaming["a"] = true
			}
			m.mu.Unlock()
			if _, e := m.RenewAccountSession(context.Background(), "a"); e == nil {
				t.Fatal("busy account renewed")
			}
		})
	}
	if m.accounts[0].renewal.Enabled {
		t.Fatal("default auto enabled")
	}
	m.mu.Lock()
	m.tasks = nil
	m.downloads = nil
	m.downloadImmediate = false
	m.relays = nil
	m.accountCheck = nil
	m.search = nil
	m.renaming = map[string]bool{}
	m.mu.Unlock()
	if _, e = m.renewAccountSession(context.Background(), "a", true); e == nil {
		t.Fatal("disabled automatic attempt accepted")
	}
	m.mu.Lock()
	m.accounts[0].renewal.Enabled = true
	m.accounts[0].renewal.Next = time.Now().Add(12 * time.Hour)
	m.mu.Unlock()
	if _, e = m.renewAccountSession(context.Background(), "a", true); e == nil {
		t.Fatal("future automatic attempt accepted")
	}
	if validSessionRenewal(a, diskSessionRenewal{Scope: "bad", UID: "1"}) || validSessionRenewal(a, diskSessionRenewal{Pending: true}) {
		t.Fatal("invalid renewal evidence accepted")
	}
}
