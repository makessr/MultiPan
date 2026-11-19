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
	"testing"
	"time"
)

func TestSessionCandidateRecovery(t *testing.T) {
	for _, mode := range []string{"recover", "read-failed", "mismatch", "save-failed", "reordered", "cancelled", "source-changed", "candidate-save-failed"} {
		t.Run(mode, func(t *testing.T) {
			const old = "synthetic-old-token"
			const candidate = "synthetic-received-token"
			const uid = "9007199254740993123"
			statePath := filepath.Join(t.TempDir(), "state.enc")
			badPath := t.TempDir()
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { m.Close() }()
			a := Account{ID: "a", Name: "test", Kind: "pan123", Endpoint: Pan123API, Folder: "0", Secret: old}
			scope := downloadScope(a)
			m.accounts = []Account{{ID: "other", Kind: "webdav", Name: "other", Secret: "unrelated"}, a}
			m.fingerprints = []UploadFingerprint{{AccountID: "a", EntryID: "1", Name: "f", Size: 1, SHA256: strings.Repeat("a", 64)}}
			if e = m.SetSessionAutoRenew("a", true); e != nil {
				t.Fatal(e)
			}
			refreshes, reads := 0, 0
			recovery := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Host != "api.123278.com" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected credential origin/method")
				}
				if r.URL.Path == "/api/user/refresh_token" {
					refreshes++
					if recovery {
						t.Error("recovery requested refresh")
					}
					if mode == "candidate-save-failed" {
						m.mu.Lock()
						m.path = badPath
						m.mu.Unlock()
					}
					fmt.Fprintf(w, `{"code":0,"data":{"token":%q}}`, candidate)
					return
				}
				if r.URL.Path != "/b/api/user/info" {
					t.Error("unexpected request")
					w.WriteHeader(405)
					return
				}
				reads++
				if reads == 1 {
					if r.Header.Get("Authorization") != "Bearer "+old {
						t.Error("original identity")
					}
					fmt.Fprintf(w, `{"code":0,"data":{"UID":%s}}`, uid)
					return
				}
				if r.Header.Get("Authorization") != "Bearer "+candidate {
					t.Error("recovery read old credential")
				}
				saved, err := readState(statePath)
				if err != nil {
					t.Error(err)
				} else {
					found := false
					for _, acc := range saved.Accounts {
						if acc.Account.ID == "a" {
							found = true
							if acc.Secret != old || acc.Renewal == nil || acc.Renewal.Candidate != candidate || !acc.Renewal.Pending {
								t.Error("candidate not durable before identity read")
							}
						}
					}
					if !found {
						t.Error("original account missing")
					}
				}
				if !recovery || mode == "read-failed" {
					w.WriteHeader(503)
					return
				}
				switch mode {
				case "mismatch":
					fmt.Fprint(w, `{"code":0,"data":{"UID":2}}`)
					return
				case "save-failed":
					m.mu.Lock()
					m.path = badPath
					m.mu.Unlock()
				case "reordered":
					if err := m.DeleteAccount("other"); err != nil {
						t.Error(err)
					}
				case "cancelled":
					cancel()
					w.WriteHeader(503)
					return
				case "source-changed":
					m.mu.Lock()
					m.accounts[1].Folder = "99"
					m.mu.Unlock()
				}
				fmt.Fprintf(w, `{"code":0,"data":{"UID":%s}}`, uid)
			}))
			tr := srv.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = srv.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}
			original := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = original; tr.CloseIdleConnections(); srv.Close() }()
			view, e := m.RenewAccountSession(context.Background(), "a")
			if e == nil || !view.Pending || view.Enabled || refreshes != 1 {
				t.Fatal("initial uncertainty not retained")
			}
			m.mu.Lock()
			m.path = statePath
			m.mu.Unlock()
			if mode == "candidate-save-failed" {
				if view.CanCheck || reads != 1 {
					t.Fatal("unsaved candidate exposed or read")
				}
				return
			}
			if !view.CanCheck {
				t.Fatal("received candidate unavailable")
			}
			raw, _ := os.ReadFile(statePath)
			if strings.Contains(string(raw), candidate) {
				t.Fatal("plaintext candidate")
			}
			public, _ := json.Marshal(m.SessionRenewals())
			for _, value := range []string{candidate, old, uid, scope} {
				if strings.Contains(string(public), value) {
					t.Fatal("private recovery proof leaked")
				}
			}
			m.Close()
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			if !m.SessionRenewals()[0].CanCheck || m.SessionRenewals()[0].Enabled {
				t.Fatal("restart lost recoverable candidate or enabled auto")
			}
			m.renewDueSessions(context.Background(), time.Now().Add(48*time.Hour))
			if _, e = m.RenewAccountSession(context.Background(), "a"); e == nil {
				t.Fatal("pending refreshed again")
			}
			recovery = true
			view, e = m.CheckSessionRenewal(ctx, "a")
			success := mode == "recover" || mode == "reordered"
			if (e == nil) != success || refreshes != 1 {
				t.Fatal("recovery result or refresh replay", mode, e)
			}
			m.mu.Lock()
			m.path = statePath
			current := m.downloadAccountLocked("a")
			m.mu.Unlock()
			if success {
				if current.Secret != candidate || view.Pending || view.CanCheck || view.Enabled || view.CheckedAt.IsZero() || current.renewal.Candidate != "" || downloadScope(current) != scope || len(m.fingerprints) != 1 {
					t.Fatal("recovery lost identity, proof or default off")
				}
				m.Close()
				m, e = NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				if m.downloadAccountLocked("a").Secret != candidate || m.SessionRenewals()[0].Pending {
					t.Fatal("resolved recovery not durable")
				}
			} else {
				if current.Secret != old || !current.renewal.Pending || current.renewal.Enabled {
					t.Fatal("failed recovery adopted or removed original")
				}
				if mode == "mismatch" {
					if view.CanCheck || current.renewal.Candidate != "" || view.Outcome != "candidate_mismatch" {
						t.Fatal("mismatched candidate retained")
					}
					m.Close()
					m, e = NewManager(statePath, nil)
					if e != nil {
						t.Fatal(e)
					}
					if m.SessionRenewals()[0].Outcome != "candidate_mismatch" {
						t.Fatal("mismatch cause lost")
					}
				}
				if mode == "read-failed" {
					_, _ = m.CheckSessionRenewal(context.Background(), "a")
					if refreshes != 1 || !m.SessionRenewals()[0].CanCheck {
						t.Fatal("read retry refreshed or lost candidate")
					}
				}
				if mode == "save-failed" {
					m.Close()
					m, e = NewManager(statePath, nil)
					if e != nil {
						t.Fatal(e)
					}
					if !m.SessionRenewals()[0].CanCheck {
						t.Fatal("save failure lost durable candidate")
					}
				}
			}
		})
	}
}

func TestSessionCandidateGuards(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a := Account{ID: "a", Name: "test", Kind: "pan123", Endpoint: Pan123API, Folder: "0", Secret: "synthetic-old-token"}
	proof := diskSessionRenewal{Scope: downloadScope(a), UID: "123", Pending: true, Candidate: "received-token", Outcome: "unconfirmed"}
	a.renewal = proof
	m.accounts = []Account{a}
	m.renaming = map[string]bool{}
	original := http.DefaultTransport
	http.DefaultTransport = renewalNoNetwork{t}
	defer func() { http.DefaultTransport = original }()
	for _, mode := range []string{"no-candidate", "invalid-token", "no-uid", "no-scope", "not-pending", "wrong-provider", "busy", "cancelled", "missing"} {
		m.accounts[0] = a
		ctx := context.Background()
		id := "a"
		switch mode {
		case "no-candidate":
			m.accounts[0].renewal.Candidate = ""
		case "invalid-token":
			m.accounts[0].renewal.Candidate = "bad\r\nvalue"
		case "no-uid":
			m.accounts[0].renewal.UID = ""
		case "no-scope":
			m.accounts[0].renewal.Scope = ""
		case "not-pending":
			m.accounts[0].renewal.Pending = false
		case "wrong-provider":
			m.accounts[0].Kind = "webdav"
		case "busy":
			m.renaming["a"] = true
		case "cancelled":
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		case "missing":
			id = "missing"
		}
		if _, e = m.CheckSessionRenewal(ctx, id); e == nil {
			t.Fatal("guard accepted", mode)
		}
		delete(m.renaming, "a")
	}
	m.accounts[0] = a
	if _, e = m.SaveAccount(AccountInput{ID: "a", Name: "renamed", Kind: "pan123", Folder: "0"}); e != nil {
		t.Fatal(e)
	}
	if m.accounts[0].renewal.Candidate != proof.Candidate {
		t.Fatal("name edit lost candidate")
	}
	if _, e = m.SaveAccount(AccountInput{ID: "a", Name: "renamed", Kind: "pan123", Folder: "0", Secret: "synthetic-manual-login"}); e != nil {
		t.Fatal(e)
	}
	if m.accounts[0].renewal.Candidate != "" || m.accounts[0].renewal.Pending || downloadScope(m.accounts[0]) == proof.Scope {
		t.Fatal("manual login reused candidate proof")
	}
}
