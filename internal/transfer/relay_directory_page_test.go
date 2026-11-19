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

func TestRelayDirectoryPageAccounting(t *testing.T) {
	for _, kind := range []string{"pan123", "baidu"} {
		for _, mode := range []string{"complete", "repeat", "missing-final", "null-final"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				limit := 100
				host := "api.123278.com"
				parent := "0"
				a := Account{Kind: kind, Endpoint: Pan123API, Secret: "synthetic-source"}
				if kind == "baidu" {
					limit = 1000
					host = "pan.baidu.com"
					parent = "/"
					a.Endpoint = BaiduAPI
					a.Secret = "BDUSS=synthetic-source"
				}
				requests := 0
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					page, _ := strconv.Atoi(r.URL.Query().Get("Page"))
					if kind == "baidu" {
						page, _ = strconv.Atoi(r.URL.Query().Get("page"))
					}
					if r.Host != host || r.Method != "GET" || page < 1 || page > 2 {
						t.Error("unexpected pagination request")
						w.WriteHeader(405)
						return
					}
					rows := []any{}
					start, end := (page-1)*limit+1, page*limit
					if end > limit+1 {
						end = limit + 1
					}
					if mode == "repeat" && page == 2 {
						start = 1
						end = limit
					}
					for i := start; i <= end; i++ {
						name := fmt.Sprintf("folder%d", i)
						if kind == "pan123" {
							rows = append(rows, map[string]any{"FileId": i, "ParentFileId": 0, "FileName": name, "Type": 1})
						} else {
							rows = append(rows, map[string]any{"fs_id": i, "path": "/" + name, "server_filename": name, "isdir": 1})
						}
					}
					if kind == "pan123" {
						next := 0
						if page == 2 {
							next = -1
						}
						data := map[string]any{"InfoList": rows, "Total": limit + 1, "Next": next}
						if page == 2 && mode == "missing-final" {
							delete(data, "InfoList")
						}
						if page == 2 && mode == "null-final" {
							data["InfoList"] = nil
						}
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
					} else {
						data := map[string]any{"errno": 0, "list": rows}
						if page == 2 && mode == "missing-final" {
							delete(data, "list")
						}
						if page == 2 && mode == "null-final" {
							data["list"] = nil
						}
						json.NewEncoder(w).Encode(data)
					}
				}))
				original := http.DefaultTransport
				tr := srv.Client().Transport.(*http.Transport).Clone()
				tr.Proxy = nil
				tr.TLSClientConfig.ServerName = srv.Certificate().DNSNames[0]
				tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
				}
				http.DefaultTransport = tr
				defer func() { http.DefaultTransport = original; tr.CloseIdleConnections(); srv.Close() }()
				p, e := relayDirectoryPage(context.Background(), NewHTTPUploader().Client, a, parent)
				if mode == "complete" {
					if e != nil || len(p.Entries) != limit+1 {
						t.Fatal("complete pages lost", len(p.Entries), e)
					}
				} else if e == nil {
					t.Fatal("incomplete pages accepted")
				}
				if requests != 2 {
					t.Fatal("unexpected pagination count", requests)
				}
			})
		}
	}
}
