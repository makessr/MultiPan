package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestGuangyaOriginalSettingsOnceAndRestart(t *testing.T) {
	for _, mode := range []string{"ready", "unknown", "changed-source", "disk-failure", "public", "pending-revoke", "random-source"} {
		t.Run(mode, func(t *testing.T) {
			updates := 0
			visible := mode != "unknown"
			code := "Old1"
			duration := int64(0)
			left := int64(-1)
			shareType := 2
			if mode == "random-source" {
				shareType = 1
			}
			var m *Manager
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/userres/v1/get_share_list":
					title := "fixture"
					if mode == "changed-source" {
						title = "changed"
					}
					c := code
					kind := shareType
					if !visible {
						c = "Old1"
						kind = 2
					}
					row := map[string]any{"id": json.RawMessage(`9007199254740993123`), "shareId": "public-id", "title": title, "shareUrl": "https://www.guangyapan.com/s/public-id", "shareType": kind, "code": c, "shareStatus": 1, "leftTime": left, "validateDuration": duration, "downloadType": 1, "trafficLimit": "123", "maxRestoreCount": 3, "enableShareCode": true, "shareCode": "fixture-code", "autoFillCode": true}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": []any{row}, "total": 1}})
				case "/userres/v1/update_share":
					updates++
					var body map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("bad body")
					}
					if string(body["id"]) != "9007199254740993123" || string(body["downloadType"]) != "1" || string(body["trafficLimit"]) != `"123"` || string(body["maxRestoreCount"]) != "3" || string(body["enableShareCode"]) != "true" || string(body["shareCode"]) != `"fixture-code"` {
						t.Error("original settings or raw ID lost", body)
					}
					// The durable intent and obsolete receipt boundary precede the only POST.
					disk, e := NewManager(m.path, nil)
					if e != nil {
						t.Error(e)
					} else {
						if disk.shares["old"].result.URL != "" || !disk.shares["old"].superseded || len(disk.shares) != 2 {
							t.Error("write sent before durable intent")
						}
						disk.Close()
					}
					json.Unmarshal(body["code"], &code)
					json.Unmarshal(body["shareType"], &shareType)
					json.Unmarshal(body["validateDuration"], &duration)
					left = duration
					if duration == 0 {
						left = -1
					}
					fmt.Fprint(w, `{}`) // Acknowledgement without data must still be verified.
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig.ServerName = server.Certificate().IPAddresses[0].String()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = previous }()
			var e error
			m, e = NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { m.Close() }()
			a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Name: "fixture", Secret: "fixture-auth", Folder: "root"}
			m.accounts = []Account{a}
			h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: "guangya", ProviderID: "9007199254740993123", RawID: json.RawMessage(`9007199254740993123`), PublicID: "public-id", Name: "fixture", URL: "https://www.guangyapan.com/s/public-id", Page: 1}
			handle, e := sealManagedShare(h)
			if e != nil {
				t.Fatal(e)
			}
			old := &shareOperation{in: ShareInput{AccountID: "a", Action: "create", ParentID: "root", EntryID: "file", Name: "fixture"}, scope: h.Scope, key: "old-key", share: h.PublicID, created: time.Now().Add(-time.Hour), result: ShareResult{ID: "old", Kind: "guangya", Name: "fixture", State: "ready", URL: h.URL, Password: "Old1", CanCheck: true}}
			old.saved = snapshotShare(old)
			m.shares = map[string]*shareOperation{"old": old}
			if mode == "disk-failure" {
				m.path = t.TempDir()
			}
			if mode == "pending-revoke" {
				m.shareRevokes = map[string]*shareRevocation{"fixture-revoke": {saved: diskShareRevocation{Source: h, Submitted: true, Result: ShareRevocationResult{ID: "fixture-revoke", State: "unknown", Created: time.Now()}}}}
			}
			wantCode := "New2"
			days := 7
			if mode == "public" {
				wantCode = ""
				days = 0
			}
			if mode == "ready" {
				missing := h
				missing.ProviderID = "42"
				missing.RawID = json.RawMessage("42")
				missingHandle, err := sealManagedShare(missing)
				if err != nil {
					t.Fatal(err)
				}
				if err := m.ValidateGuangyaShareSettingsBatch(context.Background(), []string{handle, missingHandle}, wantCode, days); err == nil {
					t.Fatal("preflight accepted a batch with a missing second share")
				}
				if err := m.ValidateGuangyaShareSettingsBatch(context.Background(), []string{handle}, "Invalid", days); err == nil {
					t.Fatal("preflight accepted an invalid code")
				}
			}
			err := m.ValidateGuangyaShareSettingsBatch(context.Background(), []string{handle}, wantCode, days)
			wantPreflightFailure := mode == "changed-source" || mode == "pending-revoke"
			if (err != nil) != wantPreflightFailure || updates != 0 || len(m.shares) != 1 {
				t.Fatal("unexpected preflight outcome or mutation", err, updates)
			}
			result, e := m.UpdateManagedShare(context.Background(), handle, wantCode, days)
			if mode == "disk-failure" || mode == "pending-revoke" {
				if e == nil || updates != 0 {
					t.Fatal("disk failure wrote remote", e, updates)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "changed-source" {
				if updates != 0 || result.State != "failed" {
					t.Fatal("changed target was modified", updates, result)
				}
				return
			}
			if updates != 1 {
				t.Fatal("wrong write count", updates)
			}
			if mode == "unknown" && result.State != "unknown" || mode != "unknown" && result.State != "ready" {
				t.Fatal(result)
			}
			m.Close()
			m, e = NewManager(m.path, nil)
			if e != nil {
				t.Fatal(e)
			}
			if !m.shares["old"].superseded || m.shares["old"].result.CanCheck || m.shares["old"].result.URL != "" {
				t.Fatal("old link revived")
			}
			if mode == "unknown" {
				again, e := m.UpdateManagedShare(context.Background(), handle, "Diff", 0)
				if e != nil || again.ID != result.ID || updates != 1 {
					t.Fatal("unknown update replayed", again, e)
				}
				visible = true
			}
			checked, e := m.CheckShare(context.Background(), result.ID)
			if e != nil || checked.State != "ready" || checked.URL != h.URL || checked.Password != wantCode || updates != 1 {
				t.Fatal("readonly original check failed", checked, e, updates)
			}
			m.accounts[0].Secret = "changed-auth"
			if _, e = m.CheckShare(context.Background(), result.ID); e == nil {
				t.Fatal("changed login reused original handle")
			}
		})
	}
}
