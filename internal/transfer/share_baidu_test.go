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

func TestBaiduShareValidation(t *testing.T) {
	a := Account{Kind: "baidu", Endpoint: BaiduAPI}
	in := ShareInput{ParentID: "/备份", EntryID: "/备份/中文 +.zip", Name: "中文 +.zip", Password: "A1b2", Days: 7, Action: "create"}
	if e := validateShare(a, in); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"root", "foreign-parent", "name", "password", "days", "action", "origin"} {
		bad, account := in, a
		switch mode {
		case "root":
			bad.EntryID = "/"
		case "foreign-parent":
			bad.ParentID = "/"
		case "name":
			bad.Name = "changed"
		case "password":
			bad.Password = ""
		case "days":
			bad.Days = 30
		case "action":
			bad.Action = "get"
		case "origin":
			account.Endpoint = "https://evil.example"
		}
		if validateShare(account, bad) == nil {
			t.Fatal("invalid input accepted", mode)
		}
	}
	if _, e := shareURL("baidu", "http://pan.baidu.com/s/1fixture"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"https://pan.baidu.com.evil.example/s/1fixture", "https://pan.baidu.com/s/1fixture?pwd=secret", "https://pan.baidu.com/share/init?surl=1fixture", "https://user:secret@pan.baidu.com/s/1fixture", "https://pan.baidu.com/s/a/b"} {
		if _, e := shareURL("baidu", raw); e == nil {
			t.Fatal("unsafe URL accepted", raw)
		}
	}
}

func TestBaiduShareCreateAndCheckOnly(t *testing.T) {
	for _, mode := range []string{"normal", "folder", "permanent", "day1", "source-changed", "recreate-replaced", "lost-create", "missing-id", "missing-link", "wrong-create-password", "wrong-detail-password", "lost-detail", "foreign-link", "expired", "wrong-source", "public", "missing-settings", "missing-list", "empty-object", "pagination", "session-failed", "pwd-only", "channel-private", "channel-public"} {
		t.Run(mode, func(t *testing.T) {
			creates, recordReads, details := 0, 0, 0
			in := ShareInput{ParentID: "/备份", EntryID: "/备份/中文 +.zip", Name: "中文 +.zip", Password: "A1b2", Days: 7, Action: "create", Folder: mode == "folder"}
			if mode == "permanent" {
				in.Days = 0
			}
			if mode == "day1" {
				in.Days = 1
			}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "pan.baidu.com" || r.Header.Get("Cookie") != "BDUSS=fixture" || r.Header.Get("Referer") != "https://pan.baidu.com/disk/main" {
					t.Error("credential boundary changed")
				}
				if r.URL.Path != "/api/gettemplatevariable" && r.URL.Query().Get("bdstoken") != "fixture-bdstoken" {
					t.Error("missing session token")
				}
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					if mode == "session-failed" {
						return nil, errors.New("fixture session lost")
					}
					return baiduResponse(map[string]any{"errno": 0, "result": map[string]string{"bdstoken": "fixture-bdstoken"}}), nil
				case "/api/list":
					dir := 0
					if in.Folder {
						dir = 1
					}
					id := json.RawMessage(`9007199254740993123`)
					if mode == "source-changed" {
						id = json.RawMessage(`0`)
					}
					return baiduResponse(map[string]any{"errno": 0, "list": []any{map[string]any{"fs_id": id, "path": in.EntryID, "server_filename": in.Name, "isdir": dir}}}), nil
				case "/share/pset":
					creates++
					r.ParseForm()
					var paths []string
					json.Unmarshal([]byte(r.PostForm.Get("path_list")), &paths)
					if r.Method != "POST" || len(paths) != 1 || paths[0] != in.EntryID || r.PostForm.Get("pwd") != in.Password || r.PostForm.Get("period") != fmt.Sprint(in.Days) || r.PostForm.Get("schannel") != "4" || r.PostForm.Get("share_type") != "9" {
						t.Error("wrong share payload", r.PostForm)
					}
					if mode == "lost-create" {
						return nil, errors.New("fixture create lost")
					}
					response := map[string]any{"errno": 0, "shareid": json.RawMessage(`9007199254740993124`), "link": "https://pan.baidu.com/s/1fixture"}
					if mode == "missing-id" {
						delete(response, "shareid")
					}
					if mode == "missing-link" {
						delete(response, "link")
					}
					if mode == "wrong-create-password" {
						response["pwd"] = "Nope"
					}
					return baiduResponse(response), nil
				case "/share/record":
					recordReads++
					if r.Method != "GET" {
						t.Error("check mutated records")
					}
					record := map[string]any{"shareId": json.RawMessage(`9007199254740993124`), "fsIds": []json.RawMessage{json.RawMessage(`9007199254740993123`)}, "expiredType": 7, "expiredTime": 6000, "public": 0}
					if mode == "pwd-only" {
						record["shortlink"] = "https://pan.baidu.com/s/1fixture"
					}
					if mode == "channel-private" || mode == "channel-public" {
						delete(record, "public")
						record["channel"] = 4
						if mode == "channel-public" {
							record["channel"] = 0
						}
					}
					if mode == "expired" {
						record["expiredType"] = -1
					}
					if mode == "wrong-source" {
						record["fsIds"] = []int{123}
					}
					if mode == "public" {
						record["public"] = 1
					}
					if mode == "missing-settings" {
						delete(record, "expiredTime")
					}
					if mode == "missing-list" {
						return baiduResponse(map[string]any{"errno": 0}), nil
					}
					if mode == "empty-object" {
						return baiduResponse(map[string]any{"errno": 0, "list": map[string]any{}}), nil
					}
					if mode == "pagination" && r.URL.Query().Get("page") == "1" {
						record["shareId"] = 123
					}
					return baiduResponse(map[string]any{"errno": 0, "list": []any{record}}), nil
				case "/share/surlinfoinrecord":
					details++
					if r.Method != "GET" || r.URL.Query().Get("shareid") != "9007199254740993124" || len(r.URL.Query().Get("sign")) != 32 {
						t.Error("wrong original detail identity")
					}
					if mode == "lost-detail" {
						return nil, errors.New("fixture detail lost")
					}
					pwd, link := "A1b2", "https://pan.baidu.com/s/1fixture"
					if mode == "wrong-detail-password" {
						pwd = "Nope"
					}
					if mode == "foreign-link" {
						link = "https://evil.example/s/a"
					}
					if mode == "pwd-only" {
						return baiduResponse(map[string]any{"errno": 0, "pwd": pwd}), nil
					}
					return baiduResponse(map[string]any{"errno": 0, "pwd": pwd, "shorturl": link}), nil
				default:
					t.Error("unexpected request", r.URL)
					return nil, errors.New("unexpected request")
				}
			})}
			a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture"}
			if mode == "recreate-replaced" {
				in.expectedBaiduSources = "9007199254740993122"
			}
			op := &shareOperation{in: in}
			first := shareBaidu(context.Background(), client, a, op, false)
			if mode == "recreate-replaced" {
				if creates != 0 || first.State != "failed" || op.baiduSource != "" {
					t.Fatal("replacement accepted for original receipt", first)
				}
				return
			}
			if mode == "session-failed" || mode == "source-changed" {
				if creates != 0 || first.State == "ready" {
					t.Fatal(first)
				}
				return
			}
			if mode == "lost-create" {
				if first.State != "unknown" || first.URL != "" {
					t.Fatal(first)
				}
				again := shareBaidu(context.Background(), client, a, op, true)
				if again.State != "unknown" || creates != 1 || recordReads != 0 {
					t.Fatal("lost create replayed")
				}
				return
			}
			checked := shareBaidu(context.Background(), client, a, op, true)
			if creates != 1 {
				t.Fatal("read replayed create")
			}
			switch mode {
			case "missing-id", "lost-detail", "foreign-link", "wrong-source", "public", "missing-settings", "missing-list", "empty-object", "channel-public":
				if checked.State != "unknown" || checked.URL != "" {
					t.Fatal(checked)
				}
			case "wrong-detail-password":
				if checked.State != "ready" || checked.Password != "Nope" {
					t.Fatal(checked)
				}
			case "expired":
				if checked.State != "failed" || checked.URL != "" {
					t.Fatal(checked)
				}
			default:
				if checked.State != "ready" || checked.Password != "A1b2" {
					t.Fatal(checked)
				}
			}
			if mode == "pagination" && recordReads != 4 {
				t.Fatal("did not follow pagination")
			}
			if mode == "expired" && details != 0 {
				t.Fatal("expired share details exposed")
			}
		})
	}
}

func TestBaiduShareManagerReusesOriginalReceipt(t *testing.T) {
	creates, details := 0, 0
	lost := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gettemplatevariable":
			fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
		case "/api/list":
			fmt.Fprint(w, `{"errno":0,"list":[{"fs_id":123,"path":"/fixture","server_filename":"fixture","isdir":0}]}`)
		case "/share/pset":
			creates++
			if lost {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"errno":0,"shareid":456,"link":"https://pan.baidu.com/s/1fixture"}`)
		case "/share/record":
			fmt.Fprint(w, `{"errno":0,"list":[{"shareId":456,"fsIds":[123],"public":0,"expiredType":7,"expiredTime":6000}]}`)
		case "/share/surlinfoinrecord":
			details++
			fmt.Fprint(w, `{"errno":0,"pwd":"A1b2","shorturl":"https://pan.baidu.com/s/1fixture"}`)
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
	m.accounts = []Account{{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture", Folder: "/"}}
	in := ShareInput{AccountID: "a", ParentID: "/", EntryID: "/fixture", Name: "fixture", Action: "create", Password: "A1b2", Days: 7}
	first, e := m.ShareEntry(context.Background(), in)
	if e != nil || first.State != "ready" || !first.CanCheck {
		t.Fatal(first, e)
	}
	again, e := m.ShareEntry(context.Background(), in)
	if e != nil || again.ID != first.ID || again.State != "ready" || creates != 1 || details != 2 {
		t.Fatal("ready receipt not rechecked", again, e)
	}
	checked, e := m.CheckShare(context.Background(), first.ID)
	if e != nil || checked.State != "ready" || creates != 1 || details != 3 {
		t.Fatal("check replayed mutation", checked, e)
	}
	lost = true
	in.Days = 1
	uncertain, e := m.ShareEntry(context.Background(), in)
	if e != nil || uncertain.State != "unknown" || uncertain.CanCheck {
		t.Fatal(uncertain, e)
	}
	again, e = m.ShareEntry(context.Background(), in)
	if e != nil || again.ID != uncertain.ID || creates != 2 {
		t.Fatal("unknown create replayed", again, e)
	}
	m.accounts[0].Secret = "BDUSS=changed"
	if _, e = m.CheckShare(context.Background(), first.ID); e == nil {
		t.Fatal("changed account reused receipt")
	}
	if len(m.renaming) != 0 {
		t.Fatal("reservation leaked")
	}
}
