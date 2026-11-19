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
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecycleEmptyFullScanBoundaries(t *testing.T) {
	for _, mode := range []string{"1000", "101", "empty", "duplicate", "changed-total", "read-failed", "short-page"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				reads++
				if r.Method != "GET" {
					t.Fatal("scan wrote")
				}
				if mode == "read-failed" && reads == 2 {
					return nil, fmt.Errorf("fixture read error")
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
				total := 101
				if mode == "1000" {
					total = 1000
				}
				if mode == "empty" {
					total = 0
				}
				if mode == "changed-total" && page == 2 {
					total++
				}
				rows := []any{}
				end := min(page*100, total)
				if mode == "short-page" && page == 1 {
					end = 99
				}
				for id := (page-1)*100 + 1; id <= end; id++ {
					record := id
					if mode == "duplicate" && page == 2 {
						record = 1
					}
					rows = append(rows, map[string]any{"record_id": fmt.Sprintf("r%d", record), "file_name": "item", "dir": false, "size": 0})
				}
				b, _ := json.Marshal(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": total}})
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
			})}
			rows, err := scanRecycleEmpty(context.Background(), client, recycleFixtureAccount("quark"))
			valid := mode == "1000" || mode == "101" || mode == "empty"
			if (err == nil) != valid || reads > 10 {
				t.Fatal(mode, len(rows), err, reads)
			}
			if valid {
				expected := 101
				if mode == "1000" {
					expected = 1000
				}
				if mode == "empty" {
					expected = 0
				}
				if len(rows) != expected {
					t.Fatal("truncated", len(rows))
				}
			}
		})
	}
}

func TestRecycleEmptyPreviewAndGuards(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu"} {
		for _, mode := range []string{"success", "changed", "source-changed", "expired", "refresh", "unknown-total", "over-limit", "unknown-kind", "prior-purge", "pending-restore", "receipt-cap", "cancelled", "ack-lost", "order-changed"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				a := recycleFixtureAccount(kind)
				m.accounts = []Account{a}
				writes := 0
				phase := false
				remaining := []int{1, 2}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" {
						writes++
						if len(remaining) > 0 {
							remaining = remaining[1:]
						}
						if mode == "ack-lost" {
							w.WriteHeader(503)
							return
						}
						switch kind {
						case "quark":
							fmt.Fprint(w, `{"status":200,"code":0,"data":{}}`)
						case "pan123":
							fmt.Fprint(w, `{"code":0,"data":{}}`)
						case "baidu":
							fmt.Fprint(w, `{"errno":0}`)
						}
						return
					}
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					rows := []any{}
					order := append([]int{}, remaining...)
					if mode == "order-changed" && phase && len(order) == 2 {
						order[0], order[1] = order[1], order[0]
					}
					for _, i := range order {
						name := fmt.Sprintf("item%d", i)
						if mode == "changed" && phase && i == 2 {
							name = "changed"
						}
						typ := 0
						if mode == "unknown-kind" {
							typ = 2
						}
						switch kind {
						case "quark":
							row := map[string]any{"record_id": fmt.Sprintf("r%d", i), "file_name": name, "dir": false, "size": 1}
							if mode == "unknown-kind" {
								delete(row, "dir")
							}
							rows = append(rows, row)
						case "pan123":
							rows = append(rows, map[string]any{"FileId": i, "FileName": name, "Type": typ, "Size": 1})
						case "baidu":
							rows = append(rows, map[string]any{"fs_id": i, "server_filename": name, "isdir": typ, "size": 1})
						}
					}
					total := len(rows)
					if mode == "over-limit" {
						total = 1001
					}
					var out any
					switch kind {
					case "quark":
						meta := map[string]any{}
						if mode != "unknown-total" {
							meta["_total"] = total
						}
						out = map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": meta}
					case "pan123":
						body := map[string]any{"InfoList": rows, "Next": -1}
						if mode != "unknown-total" {
							body["Total"] = total
						}
						out = map[string]any{"code": 0, "data": body}
					case "baidu":
						body := map[string]any{"errno": 0, "list": rows}
						if mode != "unknown-total" {
							body["total"] = total
						}
						out = body
					}
					_ = json.NewEncoder(w).Encode(out)
				}))
				defer server.Close()
				tr := server.Client().Transport.(*http.Transport).Clone()
				tr.Proxy = nil
				tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
				tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				old := http.DefaultTransport
				http.DefaultTransport = tr
				defer func() { http.DefaultTransport = old; tr.CloseIdleConnections() }()
				ctx := context.Background()
				if mode == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				p, err := m.PreviewRecycleEmpty(ctx, a.ID)
				if mode == "unknown-total" || mode == "over-limit" || mode == "unknown-kind" || mode == "cancelled" {
					if err == nil || m.recycleEmpty != nil || writes != 0 {
						t.Fatal("invalid preview accepted", p, err, writes)
					}
					return
				}
				if err != nil || p.Total != 2 || len(p.Items) != 2 || p.Token == "" || writes != 0 {
					t.Fatal(p, err, writes)
				}
				// Public metadata is not the private snapshot or deletion authority.
				*p.Items[0].Size = 99
				p.Items[0].Name = "tampered"
				if *m.recycleEmpty.items[0].Size != 1 || m.recycleEmpty.items[0].Name != "item1" {
					t.Fatal("preview aliases private snapshot")
				}
				if _, err = m.PrepareRecycleEmpty(ctx, p.Token, "永久删除"); err == nil || m.recycleEmpty.used || writes != 0 {
					t.Fatal("confirmation bypass", err)
				}
				phase = true
				scope := downloadScope(a)
				id := "1"
				if kind == "quark" {
					id = "r1"
				}
				switch mode {
				case "source-changed":
					m.accounts[0].Secret += "changed"
					m.accounts[0].UserAgent += "changed"
				case "expired":
					m.recycleEmpty.expires = time.Now().Add(-time.Second)
				case "refresh":
					if _, err = m.PreviewRecycleEmpty(ctx, a.ID); err != nil {
						t.Fatal(err)
					}
				case "prior-purge":
					m.recyclePurges = map[string]*recyclePurgeOperation{"old": {saved: diskRecyclePurge{Source: recyclePurgeSource{Scope: scope, Entry: RecycleEntry{ID: id}}}}}
				case "pending-restore":
					m.recycleRestores = map[string]*recycleRestore{"r": {saved: diskRecycleRestore{Source: recycleRestoreSource{Scope: scope, Entry: RecycleEntry{ID: id}}, Submitted: true, Result: RecycleRestoreResult{State: "unknown"}}}}
				case "receipt-cap":
					for i := 0; i < 999; i++ {
						m.recyclePurges[fmt.Sprint(i)] = &recyclePurgeOperation{}
					}
				}
				prepared, err := m.PrepareRecycleEmpty(ctx, p.Token, "清空回收站")
				valid := mode == "success" || mode == "ack-lost" || mode == "order-changed"
				if (err == nil) != valid || writes != 0 {
					t.Fatal("prepare validity", mode, err, writes)
				}
				if !valid {
					return
				}
				if len(prepared) != 2 || prepared[0].Name != "item1" {
					t.Fatal("snapshot order changed", prepared)
				}
				if _, err = m.PrepareRecycleEmpty(ctx, p.Token, "清空回收站"); err == nil {
					t.Fatal("preview reused")
				}
				for i, row := range prepared {
					r, err := m.PurgeRecycle(ctx, row.Handle, "永久删除")
					if err != nil {
						t.Fatal(err)
					}
					if mode == "ack-lost" {
						if r.State != "unknown" || writes != 1 {
							t.Fatal(r, writes)
						}
						if _, err = m.PurgeRecycle(ctx, row.Handle, "永久删除"); err == nil || writes != 1 {
							t.Fatal("unknown replay")
						}
						return
					}
					if r.State != "confirmed" || writes != i+1 {
						t.Fatal(r, writes)
					}
				}
				final, err := m.PreviewRecycleEmpty(ctx, a.ID)
				if err != nil || final.Total != 0 || writes != 2 || len(m.RecyclePurgeHistory(a.ID)) != 2 {
					t.Fatal(final, err, writes)
				}
			})
		}
	}
}
