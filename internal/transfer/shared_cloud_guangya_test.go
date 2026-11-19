package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func gyManagedFixture(t *testing.T, rows any, total int, summary any) *http.Client {
	t.Helper()
	return &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.guangyapan.com" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-auth" {
			t.Fatal("wrong Guangya request")
		}
		var data any
		switch r.URL.Path {
		case "/userres/v1/get_share_list":
			var body struct{ Page, PageSize int }
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Page != 0 || body.PageSize != 50 {
				t.Fatal("wrong zero-based page")
			}
			data = map[string]any{"list": rows, "total": total}
		case "/userres/v1/get_share_summary":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["shareId"] != "public-url" {
				t.Fatal("summary used internal ID")
			}
			data = summary
		default:
			t.Fatal("read path attempted mutation", r.URL.Path)
		}
		raw, _ := json.Marshal(map[string]any{"code": 0, "data": data})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
}

func TestGuangyaManagedListReadAndOriginalSummary(t *testing.T) {
	a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-auth"}
	row := map[string]any{"id": json.RawMessage(`9007199254740993123`), "shareId": "different-public-record", "title": "fixture.zip", "shareUrl": "https://www.guangyapan.com/s/public-url", "shareType": 2, "code": "A1b2", "shareStatus": 1, "leftTime": -1}
	client := gyManagedFixture(t, []any{row}, 1, nil)
	page, e := gyManagedShares(context.Background(), client, a, 1)
	if e != nil || len(page.Items) != 1 || page.HasMore || !page.Items[0].CanRevoke {
		t.Fatal(page, e)
	}
	h, e := decodeManagedShareHandle(page.Items[0].ID)
	if e != nil || h.ProviderID != "9007199254740993123" || h.PublicID != "different-public-record" || string(h.RawID) != "9007199254740993123" {
		t.Fatal("mixed internal/public identity", h, e)
	}
	link, e := gyManagedShareLink(context.Background(), client, a, h)
	if e != nil || link.Password != "A1b2" || link.Expiry != "永久" {
		t.Fatal(link, e)
	}
	for _, mode := range []string{"random-code", "random-missing-code", "random-bad-code", "random-switch-conflict", "unsupported-mode", "public-dash", "missing-status", "missing-left", "bad-code", "blocked", "expired", "mode-conflict", "wrong-summary"} {
		t.Run(mode, func(t *testing.T) {
			bad := map[string]any{}
			for k, v := range row {
				bad[k] = v
			}
			summary := map[string]any{"shareId": "public-url", "shareStatus": 1, "leftTime": -1, "needCode": true}
			switch mode {
			case "random-code":
				bad["shareType"] = 1
			case "random-missing-code":
				bad["shareType"] = 1
				delete(bad, "code")
			case "random-bad-code":
				bad["shareType"] = 1
				bad["code"] = "-"
			case "random-switch-conflict":
				bad["shareType"] = 1
				bad["needCode"] = false
			case "unsupported-mode":
				bad["shareType"] = 3
			case "public-dash":
				bad["shareType"] = 0
				bad["code"] = "-"
			case "missing-status":
				delete(bad, "shareStatus")
			case "missing-left":
				delete(bad, "leftTime")
			case "bad-code":
				bad["code"] = "bad code"
			case "blocked":
				bad["shareStatus"] = 4
			case "expired":
				bad["leftTime"] = 0
			case "mode-conflict":
				bad["shareType"] = 0
			case "wrong-summary":
				delete(bad, "shareStatus")
				summary["shareId"] = "wrong"
			}
			got, e := gyManagedShareLink(context.Background(), gyManagedFixture(t, []any{bad}, 1, summary), a, h)
			if mode == "random-code" || mode == "public-dash" || mode == "missing-status" || mode == "missing-left" {
				if e != nil || got.URL != h.URL || mode == "public-dash" && got.Password != "" {
					t.Fatal(got, e)
				}
			} else if e == nil || got.URL != "" {
				t.Fatal("invalid share exposed", got, e)
			}
		})
	}
	for _, rows := range []any{nil, []any{row, row}, []any{map[string]any{"shareId": "public-only"}}} {
		if _, e := gyManagedShares(context.Background(), gyManagedFixture(t, rows, 1, nil), a, 1); e == nil {
			t.Fatal("invalid original record identity accepted")
		}
	}
	if _, e := gyManagedShares(context.Background(), gyManagedFixture(t, []any{row}, 2, nil), a, 1); e == nil {
		t.Fatal("incomplete total accepted")
	}
	deleted := map[string]any{}
	for k, v := range row {
		deleted[k] = v
	}
	deleted["shareStatus"] = 3
	p, e := gyManagedShares(context.Background(), gyManagedFixture(t, []any{deleted}, 1, nil), a, 1)
	if e != nil || p.Items[0].CanRevoke {
		t.Fatal("deleted share offered revoke", e)
	}
}

func TestGuangyaRevokeUsesOriginalIDTypeAndPersistentOutcome(t *testing.T) {
	for _, mode := range []string{"removed", "deleted-record", "lost-response", "pending", "string-id", "renamed", "already-deleted"} {
		t.Run(mode, func(t *testing.T) {
			a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-auth", Name: "fixture"}
			id := json.RawMessage(`9007199254740993123`)
			if mode == "string-id" {
				id = json.RawMessage(`"9007199254740993123"`)
			}
			h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: "9007199254740993123", Name: "fixture.zip", URL: "https://www.guangyapan.com/s/public-url", Kind: a.Kind, Page: 1, RawID: id, PublicID: "public-create-id"}
			handle, e := sealManagedShare(h)
			if e != nil {
				t.Fatal(e)
			}
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { m.Close() }()
			m.accounts = []Account{a}
			creation := &shareOperation{in: ShareInput{AccountID: a.ID}, scope: h.Scope, key: "fixture", share: "public-create-id", created: time.Now(), result: ShareResult{ID: "creation", Kind: a.Kind, Name: h.Name, State: "ready", URL: h.URL, CanCheck: true}}
			creation.saved = snapshotShare(creation)
			m.shares["creation"] = creation
			var mu sync.Mutex
			posts := 0
			present := true
			status := 1
			failFollow := false
			if mode == "already-deleted" {
				status = 3
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "POST" {
					t.Error("wrong method")
				}
				switch r.URL.Path {
				case "/userres/v1/get_share_list":
					if failFollow {
						w.WriteHeader(503)
						return
					}
					rows := []any{}
					if present {
						name, url := h.Name, h.URL
						if mode == "renamed" {
							name = "changed"
						}
						if status == 3 {
							name = ""
							url = ""
						}
						rows = append(rows, map[string]any{"id": id, "title": name, "shareUrl": url, "shareStatus": status})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": rows, "total": len(rows)}})
				case "/userres/v1/delete_share":
					posts++
					var body struct {
						IDs []json.RawMessage `json:"ids"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IDs) != 1 || string(body.IDs[0]) != string(id) {
						t.Error("wrong ID type or precision")
					}
					disk, e := readState(statePath)
					if e != nil || len(disk.ShareRevokes) != 1 || !disk.ShareRevokes[0].Submitted {
						t.Error("revoke preceded durable marker")
					}
					if mode == "pending" {
						failFollow = true
					} else if mode == "deleted-record" {
						status = 3
					} else {
						present = false
					}
					if mode == "lost-response" {
						c, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
						} else {
							_ = c.Close()
						}
						return
					}
					fmt.Fprint(w, `{"code":0}`)
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
			r, e := m.RevokeManagedShare(context.Background(), handle)
			if e != nil {
				t.Fatal(e)
			}
			want := "confirmed"
			if mode == "pending" {
				want = "unknown"
			}
			if mode == "renamed" || mode == "already-deleted" {
				want = "not_sent"
			}
			if r.State != want {
				t.Fatal(mode, r)
			}
			if mode == "deleted-record" && strings.Contains(r.Message, "列表已无") {
				t.Fatal("deleted status misreported as absent record")
			}
			m.Close()
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			again, e := m.RevokeManagedShare(context.Background(), handle)
			if e != nil || again.ID != r.ID {
				t.Fatal("replayed original operation", e)
			}
			if want == "unknown" {
				if m.ShareHistory()[0].Result.URL != "" {
					t.Fatal("public-ID creation not hidden")
				}
				if n, e := m.ClearShareRevocations(a.ID); n != 0 || e != nil {
					t.Fatal("unknown cleared")
				}
				mu.Lock()
				status = 3
				failFollow = false
				mu.Unlock()
				resolved, e := m.CheckShareRevocation(context.Background(), r.ID)
				if e != nil || resolved.State != "confirmed" {
					t.Fatal("original deleted record not confirmed", resolved, e)
				}
				want = "confirmed"
			}
			if want == "confirmed" && m.ShareHistory()[0].Result.URL != "" {
				t.Fatal("revoked public ID cache exposed")
			}
			if n, e := m.ClearShareRevocations(a.ID); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			m.Close()
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			if want == "confirmed" {
				old, e := m.CheckShare(context.Background(), "creation")
				if e != nil || old.State != "failed" || old.CanCheck || old.URL != "" {
					t.Fatal("restart revived old creation", old, e)
				}
			}
			mu.Lock()
			count := posts
			mu.Unlock()
			expected := 1
			if mode == "renamed" || mode == "already-deleted" {
				expected = 0
			}
			if count != expected {
				t.Fatal("wrong cancel count", count)
			}
		})
	}
}

func TestGuangyaRevokeScanLimitDoesNotConfirmAbsence(t *testing.T) {
	a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-auth"}
	pages := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		var body struct{ Page, PageSize int }
		if r.URL.Path != "/userres/v1/get_share_list" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Page != pages || body.PageSize != 50 {
			t.Fatal("unbounded or mutating scan")
		}
		pages++
		rows := []any{}
		for i := 0; i < 50; i++ {
			rows = append(rows, map[string]any{"id": fmt.Sprintf("other-%d-%d", pages, i)})
		}
		raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows, "total": 501}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	if present, e := gyObserveShare(context.Background(), client, a, managedShareHandle{ProviderID: "missing"}); present || e == nil || pages != 10 {
		t.Fatal("truncated scan treated as absent", pages, e)
	}
}
