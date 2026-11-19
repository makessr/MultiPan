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

func TestBaiduActivationOriginalShare(t *testing.T) {
	for _, mode := range []string{"timed", "permanent", "stale", "moderated", "expired", "changed-password", "changed-deadline", "pending", "batch"} {
		t.Run(mode, func(t *testing.T) {
			const id = "9007199254740993123"
			const link = "https://pan.baidu.com/s/fixture"
			const secondID = "9007199254740993124"
			const secondLink = "https://pan.baidu.com/s/second"
			deadline := time.Now().Add(24 * time.Hour).Unix()
			var posts atomic.Int32
			var pageReads atomic.Int32
			var visible, stale atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
				case "/share/record":
					pageReads.Add(1)
					status, tag, expiry := 19, 0, 0
					remaining, expiryTime := int64(86400), deadline
					switch mode {
					case "permanent":
						expiry, remaining, expiryTime = 1, 0, 0
					case "moderated":
						tag = 7
					case "expired":
						expiryTime -= 2 * 86400
					case "changed-deadline":
						if posts.Load() != 0 {
							expiryTime += 86400
						}
					}
					if visible.Load() || mode != "batch" && stale.Load() {
						status = 0
					}
					row := map[string]any{"shareId": json.RawMessage(id), "fsIds": []int{123}, "status": status, "tag": tag, "typicalPath": "/fixture", "shortlink": link, "public": 0, "expiredType": expiry, "expiredTime": remaining, "lastExpireTime": expiryTime}
					rows := []any{row}
					if mode == "batch" {
						secondStatus := 19
						if stale.Load() {
							secondStatus = 0
						}
						rows = append(rows, map[string]any{"shareId": json.RawMessage(secondID), "status": secondStatus, "typicalPath": "/second", "shortlink": secondLink, "public": 0, "expiredType": expiry, "expiredTime": remaining, "lastExpireTime": expiryTime})
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows})
				case "/share/surlinfoinrecord":
					password := "Old1"
					if mode == "changed-password" && posts.Load() != 0 {
						password = "New2"
					}
					detailLink := link
					if r.URL.Query().Get("shareid") == secondID {
						detailLink = secondLink
					}
					fmt.Fprintf(w, `{"errno":0,"pwd":%q,"shorturl":%q}`, password, detailLink)
				case "/share/resetstatus":
					query := r.URL.Query()
					if r.Method != "POST" || query.Get("sid_list") != "["+id+"]" || query.Get("status") != "0" || r.ContentLength != 0 {
						t.Error("incorrect activation request")
					}
					posts.Add(1)
					if mode == "pending" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					visible.Store(true)
					fmt.Fprint(w, `{"errno":0}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
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
			m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture", Name: "fixture", Folder: "/"}
			m.accounts = []Account{a}
			ctx := context.Background()
			page, err := m.ManagedShares(ctx, a.ID, 1)
			if mode == "batch" {
				if err != nil || len(page.Items) != 2 || !page.Items[0].CanActivate || !page.Items[1].CanActivate {
					t.Fatal("missing eligible fixture shares", err)
				}
				first, second := page.Items[0].ID, page.Items[1].ID
				before := pageReads.Load()
				if err := m.ValidateBaiduShareActivationBatch(ctx, []string{first, second}); err != nil {
					t.Fatal("frozen selection failed validation", err)
				}
				if pageReads.Load() != before+1 {
					t.Fatal("preflight should read the shared source page once")
				}
				if err := m.ValidateBaiduShareActivationBatch(ctx, []string{first, first}); err == nil {
					t.Fatal("duplicate original share passed validation")
				}
				stale.Store(true)
				if err := m.ValidateBaiduShareActivationBatch(ctx, []string{first, second}); err == nil {
					t.Fatal("stale second share passed validation")
				}
				if posts.Load() != 0 || len(m.shares) != 0 {
					t.Fatal("preflight wrote or created a receipt")
				}
				return
			}
			if err != nil || len(page.Items) != 1 {
				t.Fatal("missing fixture row", err)
			}
			row := page.Items[0]
			if row.CanEdit || row.Status != "已被冻结" {
				t.Fatal("incorrect frozen row")
			}
			if mode == "moderated" || mode == "expired" {
				if row.CanActivate || posts.Load() != 0 {
					t.Fatal("ineligible share offered activation")
				}
				return
			}
			if !row.CanActivate {
				t.Fatal("missing activation")
			}
			stale.Store(mode == "stale")
			result, err := m.ActivateManagedBaiduShare(ctx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "stale" {
				if result.State != "failed" || posts.Load() != 0 {
					t.Fatal("stale row submitted activation")
				}
				return
			}
			if posts.Load() != 1 {
				t.Fatal("activation should be submitted once")
			}
			if strings.HasPrefix(mode, "changed-") || mode == "pending" {
				if result.State != "unknown" || result.URL != "" || result.Password != "" || !result.CanCheck {
					t.Fatal("unverified activation exposed a link or lost its receipt")
				}
				if mode != "pending" {
					return
				}
				visible.Store(true)
				result, err = m.CheckShare(ctx, result.ID)
				if err != nil || posts.Load() != 1 {
					t.Fatal("read-only check replayed a write or failed", err)
				}
			}
			if result.State != "ready" || result.URL != link || result.Password != "Old1" || result.Expiry != row.Expiry {
				t.Fatal("original share settings not preserved")
			}
		})
	}
}
