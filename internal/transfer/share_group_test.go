package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestShareCreationBatchPreflightRejectsMissingTargetWithoutWriting(t *testing.T) {
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reads++
		fmt.Fprint(w, `{"code":0,"data":{"Next":-1,"InfoList":[{"FileId":11,"FileName":"file","Type":0,"Size":1},{"FileId":22,"FileName":"folder","Type":1}]}}`)
	}))
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig.ServerName = server.Certificate().IPAddresses[0].String()
	client.Transport = transport
	m := &Manager{accounts: []Account{{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-token"}}}
	first := ShareInput{AccountID: "a", ParentID: "0", EntryID: "11", Name: "file", Action: "create"}
	second := ShareInput{AccountID: "a", ParentID: "0", EntryID: "22", Name: "folder", Folder: true, Action: "create"}
	check := func(inputs ...ShareInput) error {
		return m.validateShareCreationBatch(context.Background(), client, inputs)
	}
	if err := check(first, second); err != nil || reads != 1 {
		t.Fatalf("valid mixed selection should use one list request, got %d: %v", reads, err)
	}
	gone := second
	gone.Name = "old name"
	if err := check(first, gone); err == nil || reads != 2 {
		t.Fatalf("stale target escaped preflight, got %d: %v", reads, err)
	}
	if err := check(first, first); err == nil || reads != 2 {
		t.Fatalf("duplicate selection requested listing, got %d: %v", reads, err)
	}
	m.shares = map[string]*shareOperation{"pending": {
		in: second, scope: downloadScope(m.accounts[0]), result: ShareResult{State: "unknown"},
	}}
	if err := check(first, second); err == nil || reads != 2 || len(m.shares) != 1 {
		t.Fatalf("pending target must block the batch without creating a receipt, got %d: %v", reads, err)
	}
}

func Test123CombinedShareScopePersistenceAndOverlap(t *testing.T) {
	for _, mode := range []string{"ready", "missing-source", "unknown", "recreate", "recreate-lost"} {
		t.Run(mode, func(t *testing.T) {
			creates := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/b/api/file/list/new":
					if mode == "missing-source" {
						fmt.Fprint(w, `{"code":0,"data":{"Next":-1,"InfoList":[{"FileId":11,"FileName":"file","Type":0,"Size":1}]}}`)
					} else {
						fmt.Fprint(w, `{"code":0,"data":{"Next":-1,"InfoList":[{"FileId":11,"FileName":"file","Type":0,"Size":1},{"FileId":22,"FileName":"folder","Type":1}]}}`)
					}
				case "/b/api/share/create":
					creates++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["fileIdList"] != "11,22" || body["fileNum"] != float64(2) || r.Method != "POST" {
						t.Error("not one exact group", body)
					}
					if mode == "unknown" || mode == "recreate-lost" && creates > 1 {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"code":0,"data":{"ShareId":456,"ShareKey":"group"}}`)
				case "/b/api/share/list":
					fmt.Fprint(w, `{"code":0,"data":{"InfoList":[{"ShareId":456,"ShareKey":"group","SharePwd":"","Expiration":"2099-12-12T08:00:00+08:00"}],"Next":-1}}`)
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
			transport.TLSClientConfig.ServerName = server.Certificate().IPAddresses[0].String()
			old := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = old }()
			m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { m.Close() }()
			m.accounts = []Account{{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-token", Name: "fixture", Folder: "0"}}
			in := ShareInput{AccountID: "a", ParentID: "0", EntryID: "11", Name: "file", Action: "create", Targets: []ShareTarget{{ID: "22", Name: "folder", Folder: true}, {ID: "11", Name: "file"}}}
			result, err := m.ShareBatchEntry(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-source" {
				if creates != 0 || result.State != "failed" {
					t.Fatal("partial selection was shared", creates, result)
				}
				return
			}
			if creates != 1 {
				t.Fatal("group split into multiple requests", creates)
			}
			if mode == "ready" && result.State != "ready" || mode == "unknown" && result.State != "unknown" {
				t.Fatal(result)
			}
			if mode == "recreate" || mode == "recreate-lost" {
				if result.State != "ready" {
					t.Fatal("original receipt must be ready", result)
				}
				source, err := m.CreatedShareSourceLocation("a", result.ID)
				if err != nil || source.Recreation == nil || creates != 1 {
					t.Fatal("preparing recreation must not create a share", source, err)
				}
				attempt := source.Recreation.AttemptID
				subset := in
				subset.Targets = nil
				if _, err := m.RecreateShare(context.Background(), result.ID, attempt, subset); err == nil || creates != 1 {
					t.Fatal("recreation accepted a partial original share", err)
				}
				newResult, err := m.RecreateShare(context.Background(), result.ID, attempt, in)
				if err != nil || newResult.ID == result.ID || creates != 2 {
					t.Fatal("recreation did not submit the original set once", newResult, err, creates)
				}
				want := "ready"
				if mode == "recreate-lost" {
					want = "unknown"
					blocked, err := m.RecreateShare(context.Background(), result.ID, newID(), in)
					if err != nil || blocked.ID != newResult.ID || blocked.State != "unknown" || creates != 2 {
						t.Fatal("another dialog replayed an uncertain creation", blocked, err, creates)
					}
				} else if newResult.URL != result.URL || newResult.Message != "网盘返回了原分享链接，请以此链接和当前显示的设置为准" {
					t.Fatal("provider reused the link without informing the user", newResult)
				}
				if newResult.State != want {
					t.Fatal(newResult)
				}
				m.Close()
				m, err = NewManager(m.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				in.Password = "AB12"
				again, err := m.RecreateShare(context.Background(), result.ID, attempt, in)
				if err != nil || again.ID != newResult.ID || again.State != want || creates != 2 {
					t.Fatal("attempt id did not prevent replay after restart", again, err, creates)
				}
				return
			}
			m.Close()
			m, err = NewManager(m.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.ShareHistory()) != 1 {
				t.Fatal("group receipt missing")
			}
			in.Targets[0], in.Targets[1] = in.Targets[1], in.Targets[0]
			if mode == "unknown" {
				in.Password = "A1b2"
			}
			again, err := m.ShareBatchEntry(context.Background(), in)
			if err != nil || again.ID != result.ID || creates != 1 {
				t.Fatal("group replayed after restart/reorder", again, err, creates)
			}
			if mode == "unknown" {
				single := in
				single.Targets = nil
				single.Password = "New2"
				again, err = m.ShareEntry(context.Background(), single)
				if err != nil || again.ID != result.ID || creates != 1 {
					t.Fatal("overlapping single bypassed unknown group", again, err)
				}
			}
		})
	}
}

func TestBaiduCombinedShareExactSetAndMissingSource(t *testing.T) {
	for _, mode := range []string{"ready", "wrong-record", "missing-source", "reused-code", "omitted-create-code", "token-detail", "query-detail", "init-detail", "relative-detail", "missing-code", "masked-code", "unconfirmed-mode", "url-conflict", "frozen", "expiry-past"} {
		t.Run(mode, func(t *testing.T) {
			creates := 0
			deadline := time.Now().Add(time.Hour).Truncate(time.Second)
			in := ShareInput{ParentID: "/", EntryID: "/file", Name: "file", Action: "create", Password: "A1b2", Targets: []ShareTarget{{ID: "/file", Name: "file"}, {ID: "/folder", Name: "folder", Folder: true}}}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					return baiduResponse(map[string]any{"errno": 0, "result": map[string]string{"bdstoken": "fixture-bdstoken"}}), nil
				case "/api/list":
					list := []any{map[string]any{"fs_id": json.RawMessage(`9007199254740993111`), "path": "/file", "server_filename": "file", "isdir": 0}}
					if mode != "missing-source" {
						list = append(list, map[string]any{"fs_id": json.RawMessage(`9007199254740993222`), "path": "/folder", "server_filename": "folder", "isdir": 1})
					}
					return baiduResponse(map[string]any{"errno": 0, "list": list}), nil
				case "/share/pset":
					creates++
					r.ParseForm()
					var paths []string
					json.Unmarshal([]byte(r.PostForm.Get("path_list")), &paths)
					if r.Method != "POST" || !reflect.DeepEqual(paths, []string{"/file", "/folder"}) {
						t.Error("wrong combined paths", paths)
					}
					code := "A1b2"
					if mode == "reused-code" {
						code = "Z9x8"
					}
					response := map[string]any{"errno": 0, "shareid": 456, "link": "https://pan.baidu.com/s/group", "pwd": code}
					if mode == "init-detail" {
						response["link"] = "https://pan.baidu.com/s/1group"
					}
					if mode == "omitted-create-code" || mode == "token-detail" || mode == "query-detail" || mode == "init-detail" || mode == "relative-detail" {
						delete(response, "pwd")
					}
					return baiduResponse(response), nil
				case "/share/record":
					ids := []json.RawMessage{json.RawMessage(`9007199254740993222`), json.RawMessage(`9007199254740993111`)}
					if mode == "wrong-record" {
						ids = ids[:1]
					}
					record := map[string]any{"shareId": 456, "fsIds": ids, "expiredType": 0, "expiredTime": 6000, "public": 0, "lastExpireTime": deadline.Unix()}
					if mode == "frozen" {
						record["status"] = baiduShareFrozen
					}
					if mode == "expiry-past" {
						record["lastExpireTime"] = deadline.Add(-2 * time.Hour).Unix()
					}
					if mode == "unconfirmed-mode" {
						delete(record, "public")
					}
					if mode == "url-conflict" {
						record["shortlink"] = "https://pan.baidu.com/s/other"
					}
					return baiduResponse(map[string]any{"errno": 0, "list": []any{record}}), nil
				case "/share/surlinfoinrecord":
					detail := map[string]any{"errno": 0, "pwd": "A1b2", "shorturl": "https://pan.baidu.com/s/group"}
					if mode == "token-detail" {
						detail["shorturl"] = "group"
					}
					if mode == "query-detail" {
						detail["shorturl"] = "https://pan.baidu.com/s/group?pwd=A1b2"
					}
					if mode == "init-detail" {
						detail["shorturl"] = "https://pan.baidu.com/share/init?surl=group&pwd=A1b2"
					}
					if mode == "relative-detail" {
						detail["shorturl"] = "/s/group"
					}
					if mode == "reused-code" || mode == "omitted-create-code" || mode == "unconfirmed-mode" {
						detail["pwd"] = "Z9x8"
					}
					if mode == "missing-code" {
						delete(detail, "pwd")
					}
					if mode == "masked-code" {
						detail["pwd"] = "****"
					}
					return baiduResponse(detail), nil
				default:
					t.Error("unexpected request", r.URL.Path)
					return baiduResponse(map[string]any{"errno": 1}), nil
				}
			})}
			op := &shareOperation{in: in}
			a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture"}
			result := shareBaidu(context.Background(), client, a, op, false)
			if mode == "missing-source" {
				if creates != 0 || result.State != "unknown" {
					t.Fatal("partial source was shared", creates, result)
				}
				return
			}
			want := "ready"
			switch mode {
			case "wrong-record", "missing-code", "masked-code", "unconfirmed-mode", "url-conflict", "frozen":
				want = "unknown"
			case "expiry-past":
				want = "failed"
			}
			if creates != 1 || result.State != want || (result.URL != "") != (want == "ready") {
				t.Fatal(result, creates)
			}
			if want == "ready" && result.Expiry != ("到期："+deadline.Format(time.RFC3339)) {
				t.Fatal("confirmed deadline missing", result)
			}
			// Persisted source identity must survive and compare a reordered exact set.
			restored := &shareOperation{in: op.in, share: op.share, baiduSource: op.baiduSource, baiduURL: op.baiduURL}
			result = shareBaidu(context.Background(), client, a, restored, true)
			if result.State != want || creates != 1 {
				t.Fatal("group check mismatch/replayed", result, creates)
			}
			if (mode == "reused-code" || mode == "omitted-create-code") && (result.Password != "Z9x8" || op.in.Password != "A1b2") {
				t.Fatal("actual code was lost or requested identity mutated", result)
			}
		})
	}
}
