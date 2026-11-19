package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestGuangyaShareValidation(t *testing.T) {
	a := Account{Kind: "guangya", Endpoint: GuangyaAPI}
	in := ShareInput{ParentID: "root", EntryID: "fixture-id", Name: "fixture", Action: "create"}
	if e := validateShare(a, in); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"root", "code", "days", "action", "origin"} {
		bad, account := in, a
		switch mode {
		case "root":
			bad.EntryID = "root"
		case "code":
			bad.Password = "abc"
		case "days":
			bad.Days = 2
		case "action":
			bad.Action = "get"
		case "origin":
			account.Endpoint = "https://evil.example"
		}
		if validateShare(account, bad) == nil {
			t.Fatal("invalid request accepted", mode)
		}
	}
	for _, raw := range []string{"https://www.guangyapan.com/s/fixture", "http://www.guangyapan.com/s/fixture"} {
		if _, e := shareURL("guangya", raw); e != nil {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{"https://www.guangyapan.com.evil.example/s/a", "https://www.guangyapan.com/s/a?code=secret", "https://www.guangyapan.com/s/a/b", "https://user:secret@www.guangyapan.com/s/a", "https://www.guangyapan.com:8443/s/a"} {
		if _, e := shareURL("guangya", raw); e == nil {
			t.Fatal("unsafe share link accepted", raw)
		}
	}
}

func TestGuangyaShareCreateAndReadWithoutReplay(t *testing.T) {
	for _, mode := range []string{"public", "protected", "random-protected", "random-conflict", "day1", "day7", "day30", "folder", "missing-code", "wrong-code", "wrong-mode", "expired", "deleted", "blocked", "pending", "wrong-expiry", "missing-state", "lost-create", "lost-list", "missing-list", "missing-link", "url-id", "large-id", "foreign-link", "pagination", "not-found", "omitted-duration-permanent", "omitted-duration-timed", "fallback-status", "fallback-left", "fallback-protected", "fallback-expired", "fallback-code-expired", "fallback-wrong-id", "fallback-offline", "fallback-empty", "fallback-mode", "fallback-conflict", "fallback-record-id"} {
		t.Run(mode, func(t *testing.T) {
			creates, reads, summaries := 0, 0, 0
			in := ShareInput{ParentID: "root", EntryID: "fixture", Name: "测试 +.zip", Action: "create", Folder: mode == "folder"}
			if mode == "random-protected" || mode == "random-conflict" || mode == "protected" || mode == "missing-code" || mode == "wrong-code" || mode == "fallback-protected" {
				in.Password = "G1y2"
			}
			switch mode {
			case "day1":
				in.Days = 1
			case "day7", "omitted-duration-timed":
				in.Days = 7
			case "day30":
				in.Days = 30
			}
			id := any("share-fixture")
			if mode == "large-id" || mode == "fallback-record-id" {
				id = json.RawMessage(`9007199254740993123`)
			}
			publicURL := "https://www.guangyapan.com/s/share-fixture"
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.guangyapan.com" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("Did") != "fixture-device" || r.Header.Get("Dt") != "4" || r.Method != "POST" {
					t.Error("wrong authentication boundary")
				}
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				switch r.URL.Path {
				case "/userres/v1/share_file":
					creates++
					wanted := float64(0)
					if in.Password != "" {
						wanted = 2
					}
					ids, _ := body["fileIds"].([]any)
					if len(ids) != 1 || ids[0] != in.EntryID || body["validateDuration"] != float64(in.Days*86400) || body["shareType"] != wanted || body["code"] != in.Password || body["autoFillCode"] != false || body["title"] != in.Name {
						t.Error("incorrect create payload", body)
					}
					if mode == "lost-create" {
						return nil, errors.New("fixture create lost")
					}
					out := map[string]any{"shareId": id, "shareUrl": publicURL}
					if mode == "missing-link" {
						delete(out, "shareUrl")
					}
					if mode == "url-id" {
						delete(out, "shareId")
					}
					if mode == "foreign-link" {
						out["shareUrl"] = "https://evil.example/s/a"
					}
					// The official protocol also accepts a missing top-level business code.
					return baiduResponse(map[string]any{"data": out}), nil
				case "/userres/v1/get_share_list":
					reads++
					if body["pageSize"] != float64(100) || body["orderType"] != float64(1) {
						t.Error("wrong list request")
					}
					if mode == "lost-list" {
						return nil, errors.New("fixture list lost")
					}
					if mode == "missing-list" {
						return baiduResponse(map[string]any{"code": 0, "data": map[string]any{}}), nil
					}
					shareType := 0
					if in.Password != "" {
						shareType = 2
					}
					left := int64(-1)
					if in.Days > 0 {
						left = int64(in.Days*86400 - 1)
					}
					record := map[string]any{"id": id, "shareUrl": publicURL, "code": in.Password, "shareType": shareType, "validateDuration": in.Days * 86400, "shareStatus": 1, "leftTime": left}
					switch mode {
					case "random-protected":
						record["shareType"] = 1
					case "random-conflict":
						record["shareType"] = 1
						record["needCode"] = false
					case "missing-code":
						delete(record, "code")
					case "wrong-code":
						record["code"] = "Nope"
					case "wrong-mode":
						record["shareType"] = 1
					case "expired":
						record["shareStatus"] = 2
						record["leftTime"] = 0
					case "deleted":
						record["shareStatus"] = 3
					case "blocked":
						record["shareStatus"] = 4
					case "pending":
						record["shareStatus"] = 0
					case "wrong-expiry":
						record["validateDuration"] = 86400
					case "omitted-duration-permanent", "omitted-duration-timed":
						delete(record, "validateDuration")
					case "fallback-status", "fallback-protected", "fallback-record-id":
						delete(record, "shareStatus")
					case "fallback-left", "fallback-conflict":
						delete(record, "leftTime")
					case "fallback-expired", "fallback-code-expired", "fallback-wrong-id", "fallback-offline", "fallback-empty", "fallback-mode":
						delete(record, "shareStatus")
						delete(record, "leftTime")
					case "missing-state":
						delete(record, "leftTime")
					case "foreign-link":
						record["shareUrl"] = "https://evil.example/s/a"
					}
					var records []any
					if mode == "not-found" {
						records = []any{map[string]any{"id": "other"}}
					} else if mode == "pagination" && body["page"] == float64(0) {
						for i := 0; i < 100; i++ {
							records = append(records, map[string]any{"id": fmt.Sprint(i)})
						}
					} else {
						records = []any{record}
					}
					return baiduResponse(map[string]any{"code": 0, "data": map[string]any{"list": records, "total": 101}}), nil
				case "/userres/v1/get_share_summary":
					summaries++
					if body["shareId"] != "share-fixture" {
						t.Error("summary queried a different share")
					}
					if mode == "fallback-offline" {
						return nil, errors.New("fixture summary lost")
					}
					if mode == "fallback-code-expired" {
						return baiduResponse(map[string]any{"code": 202, "data": map[string]any{}}), nil
					}
					summary := map[string]any{"shareId": "share-fixture", "shareStatus": 1, "leftTime": -1}
					if mode == "missing-state" || mode == "fallback-empty" {
						delete(summary, "leftTime")
						delete(summary, "shareStatus")
					}
					if mode == "fallback-wrong-id" {
						summary["shareId"] = "other"
					}
					if mode == "fallback-mode" {
						summary["needCode"] = true
					}
					if mode == "fallback-protected" {
						summary["needCode"] = true
					}
					if mode == "fallback-expired" || mode == "fallback-conflict" {
						summary["shareStatus"] = 2
						summary["leftTime"] = 0
					}
					return baiduResponse(map[string]any{"data": summary}), nil
				default:
					t.Error("unexpected path", r.URL.Path)
					return nil, errors.New("unexpected")
				}
			})}
			a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-token", Username: "fixture-device"}
			op := &shareOperation{in: in}
			first := shareGuangya(context.Background(), client, a, op, false)
			if mode == "lost-create" {
				if first.State != "unknown" || op.share != "" {
					t.Fatal(first)
				}
				again := shareGuangya(context.Background(), client, a, op, true)
				if again.State != "unknown" || creates != 1 || reads != 0 {
					t.Fatal("lost creation replayed")
				}
				return
			}
			if mode == "foreign-link" {
				if first.State != "unknown" {
					t.Fatal(first)
				}
			} else if first.State != "ready" {
				t.Fatal(first)
			}
			checked := shareGuangya(context.Background(), client, a, op, true)
			if creates != 1 {
				t.Fatal("recheck created a share")
			}
			switch mode {
			case "expired", "deleted", "blocked", "fallback-expired", "fallback-code-expired":
				if checked.State != "failed" || checked.URL != "" {
					t.Fatal(checked)
				}
			case "random-conflict", "missing-code", "wrong-code", "wrong-mode", "pending", "wrong-expiry", "missing-state", "lost-list", "missing-list", "foreign-link", "not-found", "fallback-wrong-id", "fallback-offline", "fallback-empty", "fallback-mode", "fallback-conflict":
				if checked.State != "unknown" || checked.URL != "" {
					t.Fatal(checked)
				}
			default:
				if checked.State != "ready" || checked.Password != in.Password {
					t.Fatal(checked)
				}
			}
			if (mode == "fallback-status" || mode == "fallback-left" || mode == "fallback-protected") && summaries != 1 {
				t.Fatal("summary read not used exactly once")
			}
			if mode == "pagination" && reads != 2 {
				t.Fatal("pagination not followed")
			}
		})
	}
}

func TestGuangyaShareManagerDeduplicatesReceipt(t *testing.T) {
	creates, reads := 0, 0
	lost := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/userres/v1/file/get_file_list":
			fmt.Fprint(w, `{"data":{"list":[{"fileId":"source","fileName":"fixture","resType":2}],"total":1}}`)
		case "/userres/v1/share_file":
			creates++
			if lost {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"data":{"shareId":"original","shareUrl":"https://www.guangyapan.com/s/original"}}`)
		case "/userres/v1/get_share_list":
			reads++
			fmt.Fprint(w, `{"data":{"list":[{"id":"original","shareUrl":"https://www.guangyapan.com/s/original","code":"","shareType":0,"validateDuration":0,"shareStatus":1,"leftTime":-1}],"total":1}}`)
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig.ServerName = server.Certificate().IPAddresses[0].String()
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.accounts = []Account{{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-token", Folder: "root"}}
	in := ShareInput{AccountID: "a", ParentID: "root", EntryID: "source", Name: "fixture", Folder: true, Action: "create"}
	first, e := m.ShareEntry(context.Background(), in)
	if e != nil || first.State != "ready" || !first.CanCheck {
		t.Fatal(first, e)
	}
	again, e := m.ShareEntry(context.Background(), in)
	if e != nil || again.State != "ready" || again.ID != first.ID || creates != 1 || reads != 1 {
		t.Fatal("ready receipt replayed", again, e)
	}
	lost = true
	in.Days = 7
	uncertain, e := m.ShareEntry(context.Background(), in)
	if e != nil || uncertain.State != "unknown" || uncertain.CanCheck {
		t.Fatal(uncertain, e)
	}
	again, e = m.ShareEntry(context.Background(), in)
	if e != nil || again.ID != uncertain.ID || creates != 2 {
		t.Fatal("uncertain create replayed", again, e)
	}
	m.accounts[0].Secret = "changed"
	if _, e = m.CheckShare(context.Background(), first.ID); e == nil {
		t.Fatal("changed login reused receipt")
	}
	if len(m.renaming) != 0 {
		t.Fatal("account reservation leaked")
	}
}
