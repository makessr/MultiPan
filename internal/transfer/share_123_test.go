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
	"time"
)

func Test123ShareManagerDeduplicatesAndRejectsChangedSource(t *testing.T) {
	creates := 0
	lost, stale := false, false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/b/api/file/list/new":
			name := "fixture"
			if stale {
				name = "renamed"
			}
			fmt.Fprintf(w, `{"code":0,"data":{"Next":-1,"InfoList":[{"FileId":123,"FileName":%q,"Type":0,"Size":1}]}}`, name)
		case "/b/api/share/create":
			creates++
			if lost {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"code":0,"data":{"ShareId":456,"ShareKey":"fixture"}}`)
		case "/b/api/share/list":
			fmt.Fprint(w, `{"code":0,"data":{"InfoList":[{"ShareId":456,"ShareKey":"fixture","SharePwd":"","Expiration":"2099-12-12T08:00:00+08:00"}],"Next":-1}}`)
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	cert := server.Certificate()
	transport.TLSClientConfig.ServerName = cert.IPAddresses[0].String()
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.accounts = []Account{{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-token", Folder: "0"}}
	in := ShareInput{AccountID: "a", ParentID: "0", EntryID: "123", Name: "fixture", Action: "create"}
	first, e := m.ShareEntry(context.Background(), in)
	if e != nil || first.State != "ready" || !first.CanCheck || first.Kind != "pan123" {
		t.Fatal(first, e)
	}
	second, e := m.ShareEntry(context.Background(), in)
	if e != nil || second.ID != first.ID || second.State != "ready" || creates != 1 {
		t.Fatal("original receipt not reused", second, e)
	}
	stale = true
	check, e := m.CheckShare(context.Background(), first.ID)
	if e != nil || check.State != "unknown" || check.URL != "" || creates != 1 {
		t.Fatal("stale source reused", check, e)
	}
	stale = false
	lost = true
	in.Days = 7
	uncertain, e := m.ShareEntry(context.Background(), in)
	if e != nil || uncertain.State != "unknown" || uncertain.CanCheck {
		t.Fatal(uncertain, e)
	}
	again, e := m.ShareEntry(context.Background(), in)
	if e != nil || again.ID != uncertain.ID || creates != 2 {
		t.Fatal("uncertain create replayed", again, e)
	}
	if len(m.renaming) != 0 {
		t.Fatal("reservation leaked")
	}
}

func Test123ShareValidationAndLinks(t *testing.T) {
	a := Account{Kind: "pan123", Endpoint: Pan123API}
	in := ShareInput{ParentID: "0", EntryID: "9007199254740993123", Name: "fixture", Action: "create"}
	if e := validateShare(a, in); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []ShareInput{
		{ParentID: "0", EntryID: "0", Name: "fixture", Action: "create"},
		{ParentID: "0", EntryID: "1", Name: "fixture", Action: "get"},
		{ParentID: "0", EntryID: "1", Name: "fixture", Action: "create", Password: "abc"},
		{ParentID: "0", EntryID: "1", Name: "fixture", Action: "create", Days: 2},
	} {
		if validateShare(a, bad) == nil {
			t.Fatal("invalid settings accepted", bad)
		}
	}
	for _, raw := range []string{"https://www.123pan.com/s/abc-123", "https://123.share.123pan.cn/123pan/abc-123", "https://123.mshare.123pan.cn/123pan/abc"} {
		if _, e := shareURL("pan123", raw); e != nil {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{"https://share.123pan.cn.evil.example/123pan/a", "https://www.123pan.com/s/a?pwd=secret", "https://user:secret@www.123pan.com/s/a", "https://www.123pan.com/s/a/b", "https://123.share.123pan.cn/123pan/a#secret"} {
		if _, e := shareURL("pan123", raw); e == nil {
			t.Fatal("unsafe URL accepted", raw)
		}
	}
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for _, days := range []int{1, 7, 30} {
		if got := share123Expiry(days, now); !got.Equal(now.AddDate(0, 0, days)) {
			t.Fatal("wrong expiry", got)
		}
	}
	if got := share123Expiry(0, now).Format(time.RFC3339); got != "2099-12-12T08:00:00+08:00" {
		t.Fatal(got)
	}
}

func Test123ShareCreateAndReadOriginalWithoutReplaying(t *testing.T) {
	for _, mode := range []string{"public", "password", "folder", "legacy", "missing-link", "lost-create", "lost-list", "other-id", "wrong-password", "expired", "bad-link", "missing-list", "pagination", "violation"} {
		t.Run(mode, func(t *testing.T) {
			creates, reads := 0, 0
			in := ShareInput{ParentID: "0", EntryID: "9007199254740993123", Name: "fixture", Action: "create", Days: 7, Folder: mode == "folder"}
			if mode == "password" {
				in.Password = "A1b2"
			}
			good := map[string]any{"ShareId": json.RawMessage(`9007199254740993123`), "SharePwd": in.Password, "Expiration": "2099-12-12T08:00:00+08:00", "shareLinkList": map[string]any{"list": []string{"https://123.share.123pan.cn/123pan/fixture"}}}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.123278.com" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("platform") != "web" || r.Header.Get("Origin") != "https://yun.123pan.cn" {
					t.Error("wrong authentication boundary")
				}
				if len(r.URL.Query()) == 0 {
					t.Error("unsigned request")
				}
				switch r.URL.Path {
				case "/b/api/share/create":
					creates++
					var body map[string]any
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
						t.Fatal(e)
					}
					if r.Method != "POST" || body["fileIdList"] != in.EntryID || body["sharePwd"] != in.Password || body["shareModality"] != float64(2) || body["event"] != "shareCreate" {
						t.Error("wrong create payload", body)
					}
					deadline, e := time.Parse(time.RFC3339, body["expiration"].(string))
					if e != nil || time.Until(deadline) < 6*24*time.Hour || time.Until(deadline) > 8*24*time.Hour {
						t.Error("wrong deadline")
					}
					if mode == "lost-create" {
						return nil, errors.New("fixture reply lost")
					}
					if mode == "legacy" {
						return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"ShareKey": "legacy-key"}}), nil
					}
					record := map[string]any{"ShareId": good["ShareId"]}
					if mode != "missing-link" {
						record["shareLinkList"] = good["shareLinkList"]
					}
					if mode == "bad-link" {
						record["shareLinkList"] = map[string]any{"list": []string{"https://evil.example/s/a"}}
					}
					if mode == "violation" {
						record["ViolateFileIdList"] = []string{in.EntryID}
					}
					return jsonResponse(map[string]any{"code": 0, "data": record}), nil
				case "/b/api/share/list":
					reads++
					if r.Method != "GET" || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("event") != "shareListFile" {
						t.Error("wrong read request")
					}
					if mode == "lost-list" {
						return nil, errors.New("fixture list lost")
					}
					if mode == "missing-list" {
						return jsonResponse(map[string]any{"code": 0, "data": map[string]any{}}), nil
					}
					record := map[string]any{}
					for k, v := range good {
						record[k] = v
					}
					if mode == "legacy" {
						record["ShareKey"] = "legacy-key"
					}
					if mode == "other-id" {
						record["ShareId"] = "9007199254740993124"
					}
					if mode == "wrong-password" {
						record["SharePwd"] = "Other"
					}
					if mode == "expired" {
						record["Expiration"] = "2020-01-01T00:00:00+08:00"
					}
					if mode == "pagination" && r.URL.Query().Get("next") == "0" {
						return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{map[string]any{"ShareId": "other"}}, "Next": 123}}), nil
					}
					return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{record}, "Next": -1}}), nil
				default:
					t.Error("unexpected request", r.URL)
					return nil, errors.New("unexpected")
				}
			})}
			a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-token"}
			op := &shareOperation{in: in}
			first := share123(context.Background(), client, a, op, false)
			if mode == "lost-create" {
				if first.State != "unknown" || op.share != "" {
					t.Fatal(first)
				}
				again := share123(context.Background(), client, a, op, true)
				if again.State != "unknown" || creates != 1 || reads != 0 {
					t.Fatal("lost create replayed")
				}
				return
			}
			if mode == "violation" {
				if first.State != "failed" || first.URL != "" {
					t.Fatal(first)
				}
				return
			}
			if mode == "bad-link" { // Original ID allows a subsequent authenticated read to recover a usable URL.
				if first.State != "ready" || reads != 1 {
					t.Fatal(first)
				}
			} else if first.State != "ready" {
				t.Fatal(first)
			}
			result := share123(context.Background(), client, a, op, true)
			if creates != 1 {
				t.Fatal("check replayed create")
			}
			switch mode {
			case "lost-list", "other-id", "wrong-password", "missing-list":
				if result.State != "unknown" || result.URL != "" {
					t.Fatal(result)
				}
			case "expired":
				if result.State != "failed" || result.URL != "" {
					t.Fatal(result)
				}
			default:
				if result.State != "ready" || result.Password != in.Password {
					t.Fatal(result)
				}
			}
			if mode == "pagination" && reads != 2 {
				t.Fatal("pagination not followed")
			}
		})
	}
}
