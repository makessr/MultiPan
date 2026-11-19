package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBaiduOriginalExpiryOnceAndRestart(t *testing.T) {
	for _, mode := range []string{"timed", "permanent", "public", "unknown", "missing-deadline", "changed-password", "legacy-pending", "stale", "disk"} {
		t.Run(mode, func(t *testing.T) {
			updates := 0
			expiry := 0
			deadline := time.Now().Add(24 * time.Hour).Unix()
			visible := mode != "unknown" && mode != "missing-deadline" && mode != "legacy-pending"
			password := "Old1"
			public := 0
			if mode == "public" {
				password = "0"
				public = 1
			}
			var m *Manager
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
				case "/share/record":
					title := "/fixture"
					if mode == "stale" {
						title = "/changed"
					}
					typ, stamp := expiry, deadline
					if !visible {
						typ = 0
						stamp = time.Now().Add(time.Hour).Unix()
					}
					duration := int64(3 * 86400)
					if typ == 1 {
						duration = 0
					}
					row := map[string]any{"shareId": json.RawMessage(`9007199254740993123`), "fsIds": []int{123}, "typicalPath": title, "shortlink": "https://pan.baidu.com/s/fixture", "public": public, "expiredType": typ, "expiredTime": duration, "lastExpireTime": stamp}
					if mode == "missing-deadline" && !visible {
						delete(row, "lastExpireTime")
						row["expiredTime"] = deadline // A plausible timestamp in the duration field is still insufficient.
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{row}})
				case "/share/surlinfoinrecord":
					fmt.Fprintf(w, `{"errno":0,"pwd":%q,"shorturl":"https://pan.baidu.com/s/fixture"}`, password)
				case "/share/mexpire":
					updates++
					if mode == "changed-password" {
						password = "New2"
					}
					r.ParseForm()
					if r.Method != "POST" || r.Form.Get("share_id") != "9007199254740993123" || r.URL.Query().Get("bdstoken") != "" || r.Form.Has("pwd") {
						t.Error("wrong expiry-only write", r.Form)
					}
					d, _ := strconv.Atoi(r.Form.Get("op_expire_days"))
					x, _ := strconv.ParseInt(r.Form.Get("expire_time"), 10, 64)
					if mode == "permanent" {
						if d != -1 || x != -1 {
							t.Error("wrong permanent encoding")
						}
						expiry = 1
						deadline = 0
					} else {
						if d != 3 || x < time.Now().Unix()+3*86400-120 || x > time.Now().Unix()+3*86400+120 {
							t.Error("wrong deadline")
						}
						expiry = 0
						deadline = x
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
			transport.TLSClientConfig.ServerName = server.Certificate().IPAddresses[0].String()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			old := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = old }()
			var err error
			m, err = NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { m.Close() }()
			a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture", Name: "fixture", Folder: "/"}
			m.accounts = []Account{a}
			h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: "baidu", ProviderID: "9007199254740993123", Name: "fixture", URL: "https://pan.baidu.com/s/fixture", Page: 1}
			handle, _ := sealManagedShare(h)
			days := 3
			if mode == "permanent" {
				days = 0
			}
			if mode == "disk" {
				m.path = t.TempDir()
			}
			result, err := m.UpdateManagedShare(context.Background(), handle, "", days)
			if mode == "disk" {
				if err == nil || updates != 0 {
					t.Fatal("disk failure wrote")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "stale" {
				if result.State != "failed" || updates != 0 {
					t.Fatal("stale source modified", result)
				}
				return
			}
			pending := mode == "unknown" || mode == "missing-deadline" || mode == "changed-password" || mode == "legacy-pending"
			if updates != 1 || pending && result.State != "unknown" || !pending && result.State != "ready" {
				t.Fatal("unexpected receipt state", result.State, updates)
			}
			if pending && (result.Password != "" || result.URL != "") {
				t.Fatal("pending receipt exposed a link or password")
			}
			if mode == "legacy-pending" {
				op := m.shares[result.ID]
				op.baiduExpiryPassword = nil
				if err := op.saveCheckpoint(); err != nil {
					t.Fatal(err)
				}
			}
			m.Close()
			m, err = NewManager(m.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if pending {
				again, e := m.UpdateManagedShare(context.Background(), handle, "", 7)
				if e != nil || again.ID != result.ID || updates != 1 {
					t.Fatal("unknown replay", again, e)
				}
				visible = true
			}
			checked, err := m.CheckShare(context.Background(), result.ID)
			if mode == "changed-password" || mode == "legacy-pending" {
				if err != nil || checked.State != "unknown" || checked.Password != "" || checked.URL != "" || updates != 1 {
					t.Fatal("unverified access was confirmed after restart", checked.State, err, updates)
				}
				return
			}
			expectedPassword := "Old1"
			if mode == "public" {
				expectedPassword = ""
			}
			if err != nil || checked.State != "ready" || checked.Password != expectedPassword || checked.URL != h.URL || updates != 1 {
				t.Fatal("check changed share", checked, err, updates)
			}
			shares, err := m.ManagedShares(context.Background(), a.ID, 1)
			wantExpiry := "到期：" + time.Unix(deadline, 0).Format(time.RFC3339)
			if mode == "permanent" {
				wantExpiry = "永久"
			}
			if err != nil || len(shares.Items) != 1 || shares.Items[0].Expiry != wantExpiry {
				t.Fatal("share list did not show the verified expiry", shares, err)
			}
			link, err := m.ManagedShareLink(context.Background(), shares.Items[0].ID)
			if err != nil || link.Expiry != wantExpiry || updates != 1 {
				t.Fatal("share detail did not show the verified expiry", link, err, updates)
			}
			if mode == "timed" {
				again, e := m.UpdateManagedShare(context.Background(), handle, "", days)
				if e != nil || again.State != "ready" || again.ID == result.ID || updates != 2 {
					t.Fatal("explicit new edit reused old intent", again, e, updates)
				}
			}
		})
	}
}
