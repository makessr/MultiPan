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
	"sync"
	"testing"
	"time"
)

func TestOtherShareRevocationProtocolsAndPersistentRecovery(t *testing.T) {
	for _, provider := range []string{"pan123", "pan123-paid", "baidu"} {
		for _, mode := range []string{"success", "lost-response", "pending", "renamed", "second-page", "key-only", "initial-save-failed", "link-changed"} {
			paid := provider == "pan123-paid"
			if mode == "link-changed" && !paid {
				continue
			}
			t.Run(provider+"/"+mode, func(t *testing.T) {
				kind, listPath, paidFlag := provider, "/b/api/share/list", 0
				if paid {
					kind, listPath, paidFlag = "pan123", "/b/api"+pan123PaidShareList, 1
				}
				statePath := filepath.Join(t.TempDir(), "state.enc")
				m, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer func() { m.Close() }()
				a := Account{ID: "a", Kind: kind, Name: "fixture", Secret: "fixture-auth", Endpoint: Pan123API}
				link := "https://www.123pan.com/s/public"
				if kind == "baidu" {
					a.Secret = "BDUSS=fixture-auth"
					a.Endpoint = BaiduAPI
					link = "https://pan.baidu.com/s/public"
				}
				m.accounts = []Account{a}
				id := "9007199254740993123"
				h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: id, Name: "fixture.zip", URL: link, Kind: kind, Page: 1, Cursor: "0", Key123: "public", Paid123: paid}
				handle, e := sealManagedShare(h)
				if e != nil {
					t.Fatal(e)
				}
				creation := &shareOperation{in: ShareInput{AccountID: a.ID}, scope: downloadScope(a), key: "fixture", share: id, created: time.Now(), result: ShareResult{ID: "creation", Kind: kind, Name: h.Name, State: "ready", URL: link, CanCheck: true}}
				if kind == "pan123" && mode == "key-only" {
					creation.share = ""
					creation.key123 = "public"
				}
				creation.saved = snapshotShare(creation)
				m.shares["creation"] = creation
				var mu sync.Mutex
				posts := 0
				present := true
				failFollow := false
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
						return
					}
					if r.URL.Path == listPath || r.URL.Path == "/share/record" {
						if r.Method != "GET" {
							t.Error("observation mutated shares")
						}
						if failFollow {
							w.WriteHeader(503)
							return
						}
						rows := []any{}
						next := any(-1)
						page := r.URL.Query().Get("page")
						if kind == "pan123" {
							page = r.URL.Query().Get("next")
							if r.URL.Query().Get("limit") != "50" {
								t.Error("123 page not bounded")
							}
						} else if r.URL.Query().Get("num") != "100" {
							t.Error("Baidu page not bounded")
						}
						if mode == "second-page" && present && (page == "0" || page == "1") {
							pageSize := 50
							if kind == "baidu" {
								pageSize = 100
							}
							for i := 0; i < pageSize; i++ {
								if kind == "pan123" {
									rows = append(rows, map[string]any{"ShareId": i + 1})
								} else {
									rows = append(rows, map[string]any{"shareId": i + 1})
								}
							}
							next = 17
						} else if present {
							name := "fixture.zip"
							if mode == "renamed" {
								name = "changed.zip"
							}
							if kind == "pan123" {
								row := map[string]any{"ShareId": json.RawMessage(id), "ShareName": name, "ShareKey": "public"}
								if paid {
									currentLink := link
									if mode == "link-changed" {
										currentLink += "-changed"
									}
									row["shareLinkList"] = map[string]any{"list": []string{currentLink}}
									row["isPayShare"] = 1
								}
								rows = append(rows, row)
							} else {
								rows = append(rows, map[string]any{"shareId": json.RawMessage(id), "typicalPath": "/folder/" + name, "shortlink": link})
							}
						}
						if kind == "pan123" {
							_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Next": next}})
						} else {
							_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows})
						}
						return
					}
					if kind == "pan123" && r.URL.Path == "/b/api/share/delete" || kind == "baidu" && r.URL.Path == "/share/cancel" {
						posts++
						if r.Method != "POST" {
							t.Error("revoke was not explicit POST")
						}
						disk, e := readState(statePath)
						if e != nil || len(disk.ShareRevokes) != 1 || !disk.ShareRevokes[0].Submitted {
							t.Error("missing durable submission boundary")
						}
						if kind == "pan123" {
							var body struct {
								IDs []struct {
									ID json.RawMessage `json:"shareId"`
								} `json:"shareInfoList"`
								Event string `json:"event"`
								Place int    `json:"operatePlace"`
								Drive int    `json:"driveId"`
								Paid  int    `json:"isPayShare"`
							}
							if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IDs) != 1 || string(body.IDs[0].ID) != id || body.Event != "shareCancel" || body.Place != 2 || body.Drive != 0 || body.Paid != paidFlag {
								t.Error("wrong 123 request or integer precision loss")
							}
						} else {
							if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil || r.PostForm.Get("shareid_list") != "["+id+"]" {
								t.Error("wrong Baidu form or integer precision loss")
							}
						}
						if mode == "pending" {
							failFollow = true
						} else {
							present = false
						}
						if mode == "lost-response" {
							connection, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
							} else {
								_ = connection.Close()
							}
							return
						}
						if kind == "pan123" {
							fmt.Fprint(w, `{"code":0,"data":{}}`)
						} else {
							fmt.Fprint(w, `{"errno":0}`)
						}
						return
					}
					t.Error("unexpected remote endpoint", r.URL.Path)
					w.WriteHeader(404)
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
				if mode == "initial-save-failed" {
					m.path = t.TempDir()
				}
				if paid && mode == "success" {
					if e := m.ValidateShareRevocationBatch(context.Background(), []string{handle}); e != nil {
						t.Fatal("paid batch preflight failed", e)
					}
					free := h
					free.Paid123, free.ProviderID = false, "7"
					freeHandle, e := sealManagedShare(free)
					if e != nil {
						t.Fatal(e)
					}
					if e := m.ValidateShareRevocationBatch(context.Background(), []string{handle, freeHandle}); e == nil {
						t.Fatal("mixed paid and free batch accepted")
					}
				}
				r, e := m.RevokeManagedShare(context.Background(), handle)
				mu.Lock()
				count := posts
				mu.Unlock()
				if mode == "initial-save-failed" {
					if e == nil || count != 0 {
						t.Fatal("write despite save failure")
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				want := "confirmed"
				if mode == "pending" {
					want = "unknown"
				}
				if mode == "renamed" || mode == "link-changed" {
					want = "not_sent"
				}
				if r.State != want || r.Paid123 != paid {
					t.Fatal(kind, mode, r)
				}
				wantedPosts := 1
				if want == "not_sent" {
					wantedPosts = 0
				}
				if count != wantedPosts {
					t.Fatal("wrong post count", count)
				}
				m.Close()
				m, e = NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				duplicate, e := m.RevokeManagedShare(context.Background(), handle)
				if e != nil || duplicate.ID != r.ID || duplicate.Paid123 != paid {
					t.Fatal("original receipt not reused", duplicate, e)
				}
				if want == "unknown" {
					if !paid && m.ShareHistory()[0].Result.URL != "" {
						t.Fatal("pending revoke exposed cached link")
					}
					if _, e = m.ManagedShareLink(context.Background(), handle); e == nil {
						t.Fatal("pending revoke reused handle")
					}
					if n, e := m.ClearShareRevocations(a.ID); e != nil || n != 0 {
						t.Fatal("pending receipt cleared")
					}
					mu.Lock()
					present = false
					failFollow = false
					mu.Unlock()
					resolved, e := m.CheckShareRevocation(context.Background(), r.ID)
					if e != nil || resolved.State != "confirmed" {
						t.Fatal("read-only recovery failed", resolved, e)
					}
					want = "confirmed"
				}
				if want == "confirmed" {
					got := m.ShareHistory()[0].Result.URL
					if !paid && got != "" || paid && got != link {
						t.Fatal("wrong free receipt link after revocation", got)
					}
				}
				m.accounts[0].Secret = "changed"
				if _, e = m.CheckShareRevocation(context.Background(), r.ID); e == nil {
					t.Fatal("changed account reused receipt")
				}
				m.accounts[0] = a
				if n, e := m.ClearShareRevocations(a.ID); e != nil || n != 1 {
					t.Fatal("final clear failed", n, e)
				}
				m.Close()
				m, e = NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				if want == "confirmed" && !paid {
					old, e := m.CheckShare(context.Background(), "creation")
					if e != nil || old.URL != "" || old.CanCheck || old.State != "failed" {
						t.Fatal("restart resurrected cleared revoke", old, e)
					}
				}
				mu.Lock()
				after := posts
				mu.Unlock()
				if after != wantedPosts {
					t.Fatal("replayed remote revoke", after)
				}
			})
		}
	}
}

func Test123RevokeCursorCycleIsNotAbsence(t *testing.T) {
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	h := managedShareHandle{AccountID: a.ID, Kind: a.Kind, Scope: downloadScope(a), ProviderID: "9999", Name: "fixture"}
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		cursor := r.URL.Query().Get("next")
		next := "17"
		if cursor == "17" {
			next = "23"
		}
		raw := `{"code":0,"data":{"InfoList":[{"ShareId":` + strconv.Itoa(calls) + `}],"Next":"` + next + `"}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}
	if present, e := observeShareRevocation(context.Background(), client, a, h); e == nil || present || calls != 3 {
		t.Fatal("cursor loop accepted as absence", calls, e)
	}
}

func TestOtherRevocationScanLimitNeverConfirmsAbsence(t *testing.T) {
	for _, kind := range []string{"pan123", "baidu"} {
		t.Run(kind, func(t *testing.T) {
			a := Account{ID: "a", Kind: kind, Endpoint: Pan123API, Secret: "fixture"}
			if kind == "baidu" {
				a.Endpoint = BaiduAPI
				a.Secret = "BDUSS=fixture"
			}
			h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: kind, ProviderID: "999999", Name: "fixture"}
			pages := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("scan performed a write")
				}
				var data any
				if r.URL.Path == "/api/gettemplatevariable" {
					data = map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "fixture-token"}}
				} else {
					pages++
					rows := []any{}
					pageSize := 50
					if kind == "baidu" {
						pageSize = 100
					}
					for i := 0; i < pageSize; i++ {
						if kind == "pan123" {
							rows = append(rows, map[string]any{"ShareId": pages*pageSize + i})
						} else {
							rows = append(rows, map[string]any{"shareId": pages*pageSize + i})
						}
					}
					if kind == "pan123" {
						data = map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Next": pages + 100}}
					} else {
						data = map[string]any{"errno": 0, "list": rows}
					}
				}
				raw, _ := json.Marshal(data)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			expectedPages := 10
			if kind == "baidu" {
				expectedPages = 5
			}
			if present, e := observeShareRevocation(context.Background(), client, a, h); present || e == nil || pages != expectedPages {
				t.Fatal("truncated scan accepted as completed", pages, e)
			}
		})
	}
}
