package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Contradictory termination metadata must never prove an item's absence,
// including when a previously acknowledged purge is checked read-only.
func TestRecyclePaginationAbsenceProof(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu"} {
		for _, mode := range []string{"complete", "complete-full", "unknown-total", "early-full-end", "late-full-end", "over-total", "total-appears", "total-disappears"} {
			if (mode == "early-full-end" || mode == "late-full-end") && kind != "pan123" {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				writes := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						writes++
						w.WriteHeader(400)
						return
					}
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					pageKey := "page"
					if kind == "quark" {
						pageKey = "_page"
					}
					if kind == "pan123" {
						pageKey = "Page"
					}
					page, _ := strconv.Atoi(r.URL.Query().Get(pageKey))
					n, total, next := 100, 101, 0
					if page == 2 {
						n, next = 1, -1
					}
					includeTotal := true
					switch mode {
					case "complete-full":
						total, next = 100, -1
					case "unknown-total":
						includeTotal = false
						if page == 1 {
							next = -1
						}
					case "early-full-end":
						next = -1
					case "late-full-end":
						total = 201
						if page == 2 {
							n = 100
						}
					case "over-total":
						if page == 2 {
							n = 2
						}
					case "total-appears":
						includeTotal = page != 1
					case "total-disappears":
						includeTotal = page == 1
					}
					// Providers without Next require an empty page after a full
					// unknown-total page to establish bounded completion.
					if mode == "unknown-total" && page == 2 {
						n = 0
					}
					rows := []any{}
					for i := 0; i < n; i++ {
						id := (page-1)*100 + i + 1
						switch kind {
						case "quark":
							rows = append(rows, map[string]any{"record_id": fmt.Sprintf("r%d", id), "file_name": "item", "dir": false, "size": 0})
						case "pan123":
							rows = append(rows, map[string]any{"FileId": id, "FileName": "item", "Type": 0, "Size": 0})
						case "baidu":
							rows = append(rows, map[string]any{"fs_id": id, "server_filename": "item", "isdir": 0, "size": 0})
						}
					}
					data := map[string]any{}
					switch kind {
					case "quark":
						meta := map[string]any{}
						if includeTotal {
							meta["_total"] = total
						}
						data = map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": meta}
					case "pan123":
						body := map[string]any{"InfoList": rows, "Next": next}
						if includeTotal {
							body["Total"] = total
						}
						data = map[string]any{"code": 0, "data": body}
					case "baidu":
						data = map[string]any{"errno": 0, "list": rows}
						if includeTotal {
							data["total"] = total
						}
					}
					_ = json.NewEncoder(w).Encode(data)
				}))
				defer server.Close()
				tr := server.Client().Transport.(*http.Transport).Clone()
				tr.Proxy = nil
				tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
				tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				defer tr.CloseIdleConnections()
				client := &http.Client{Transport: tr}
				a := recycleFixtureAccount(kind)
				entry := RecycleEntry{ID: "999999", Name: "missing", Kind: "file"}
				present, err := findRecyclePurge(context.Background(), client, a, entry)
				valid := mode == "complete" || mode == "complete-full" || mode == "unknown-total"
				if present || (err == nil) != valid {
					t.Errorf("absence validity: present=%v err=%v valid=%v", present, err, valid)
				}
				state, _ := checkRecyclePurge(context.Background(), client, a, diskRecyclePurge{Source: recyclePurgeSource{Entry: entry}, Acknowledged: true, Submitted: true})
				if (state == "confirmed") != valid {
					t.Errorf("receipt state=%s valid=%v", state, valid)
				}
				if writes != 0 {
					t.Fatal("read-only check wrote", writes)
				}
			})
		}
	}
}
