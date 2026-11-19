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
	"sync"
	"testing"
	"time"
)

func awaitAccountCheck(t *testing.T, m *Manager) AccountCheckStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, e := m.AccountCheckStatus("")
		if e != nil {
			t.Fatal(e)
		}
		if s.State != "running" {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("account check did not finish")
	return AccountCheckStatus{}
}
func TestAccountCheckProviders(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "rejected" {
					w.WriteHeader(403)
					fmt.Fprint(w, "secret-from-provider")
					return
				}
				if mode == "invalid" {
					fmt.Fprint(w, "<html>secret-from-provider</html>")
					return
				}
				switch r.Host {
				case "up.woozooo.com":
					r.ParseForm()
					if r.Method != "POST" || r.URL.Path != "/doupload.php" || r.Form.Get("task") != "47" || r.Header.Get("Cookie") != "phpdisk_info=lanzou-private" {
						t.Error("Lanzou request")
					}
					fmt.Fprint(w, `{"zt":1}`)
				case "drive.quark.cn":
					if r.Method != "GET" || r.URL.Path != "/1/clouddrive/file/sort" || !strings.Contains(r.Header.Get("Cookie"), "__puus=quark-private") {
						t.Error("Quark request")
					}
					http.SetCookie(w, &http.Cookie{Name: "__puus", Value: "rotated-private"})
					fmt.Fprint(w, `{"status":200,"code":0,"data":{"list":[]}}`)
				case "api.123278.com":
					if r.Method != "GET" || r.URL.Path != "/b/api/user/info" || r.Header.Get("Authorization") != "Bearer pan123-private" {
						t.Error("123 request")
					}
					fmt.Fprint(w, `{"code":0,"data":{"UID":123}}`)
				case "pan.baidu.com":
					if r.Method != "GET" || !strings.Contains(r.Header.Get("Cookie"), "BDUSS=baidu-private") {
						t.Error("Baidu request")
					}
					if r.URL.Path == "/api/gettemplatevariable" {
						fmt.Fprint(w, `{"errno":0,"result":{"bdstoken":"fixture-token"}}`)
					} else if r.URL.Path == "/api/list" {
						fmt.Fprint(w, `{"errno":0,"list":[]}`)
					} else {
						t.Error("Baidu mutation", r.URL.Path)
						w.WriteHeader(405)
					}
				case "api.guangyapan.com":
					if r.Method != "POST" || r.URL.Path != "/userres/v1/file/get_file_list" || r.Header.Get("Authorization") != "Bearer guangya-private" || r.Header.Get("Did") != "device-fixture" {
						t.Error("Guangya request")
					}
					fmt.Fprint(w, `{"code":0,"data":{"list":[],"total":0}}`)
				case "rest.ctfile.com":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if r.Method != "POST" || r.URL.Path != "/v1/user/info/profile" || body["session"] != "ctfile-private" {
						t.Error("Ct request")
					}
					fmt.Fprint(w, `{"code":200,"userid":123}`)
				case "dav.fixture":
					u, p, _ := r.BasicAuth()
					if r.Method != "PROPFIND" || r.URL.Path != "/base/allowed/" || u != "dav-user" || p != "dav-private" || r.Header.Get("Depth") != "1" {
						t.Error("DAV request")
					}
					w.WriteHeader(207)
					fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/base/allowed/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`)
				default:
					t.Error("foreign host", r.Host)
					w.WriteHeader(405)
				}
			}))
			old := http.DefaultTransport
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = old; tr.CloseIdleConnections(); server.Close() }()
			m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.accounts = []Account{
				{ID: "l", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "phpdisk_info=lanzou-private"},
				{ID: "q", Kind: "quark", Endpoint: QuarkAPI, Secret: "__puus=quark-private"},
				{ID: "p", Kind: "pan123", Endpoint: Pan123API, Secret: "pan123-private"},
				{ID: "b", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=baidu-private"},
				{ID: "g", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "guangya-private", Username: "device-fixture"},
				{ID: "c", Kind: "ctfile", Endpoint: CtFileAPI, Secret: "ctfile-private"},
				{ID: "d", Kind: "webdav", Endpoint: "https://dav.fixture/base", Folder: "/allowed", Username: "dav-user", Secret: "dav-private"},
				{ID: "t", Kind: "ctdav", Endpoint: "https://dav.fixture/base", Folder: "/allowed", Username: "dav-user", Secret: "dav-private"},
			}
			before, _ := json.Marshal(m.accounts)
			if _, e = m.StartAccountCheck([]string{"l", "q", "p", "b", "g", "c", "d", "t"}); e != nil {
				t.Fatal(e)
			}
			s := awaitAccountCheck(t, m)
			if s.State != "complete" || len(s.Rows) != 8 {
				t.Fatal(s)
			}
			for _, r := range s.Rows {
				want := "available"
				if mode != "success" {
					want = "unconfirmed"
				}
				if r.State != want || r.CheckedAt.IsZero() {
					t.Fatalf("%s: %+v", mode, r)
				}
			}
			raw, _ := json.Marshal(s)
			for _, secret := range []string{"private", "fixture-token", "device-fixture", "https://"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("status contains provider data")
				}
			}
			after, _ := json.Marshal(m.accounts)
			if string(before) != string(after) {
				t.Fatal("read check replaced account credentials")
			}
			s.Rows[0].Message = "mutated"
			again, _ := m.AccountCheckStatus(s.ID)
			if again.Rows[0].Message == "mutated" {
				t.Fatal("snapshot aliases manager")
			}
			m.mu.Lock()
			m.accounts[6].Folder = "/other"
			m.accounts[7].UserAgent = "new-agent"
			m.mu.Unlock()
			again, _ = m.AccountCheckStatus(s.ID)
			if again.Rows[6].State != "changed" || again.Rows[7].State != "changed" {
				t.Fatal("stale permissions after scope change")
			}
			if mode != "success" {
				return
			}
			second, err := m.StartAccountCheck([]string{"p"})
			if err != nil {
				t.Fatal(err)
			}
			assertPrevious := func(next AccountCheckStatus) {
				t.Helper()
				if len(next.Rows) != 1 || next.Rows[0].AccountID != "p" || len(next.PreviousRows) != 7 {
					t.Fatalf("progress and retained results must remain separate: %+v", next)
				}
				for _, row := range next.PreviousRows {
					switch row.AccountID {
					case "l":
						if row.State != "available" || !row.CheckedAt.Equal(s.Rows[0].CheckedAt) {
							t.Fatalf("first account lost its result: %+v", row)
						}
					case "d", "t":
						if row.State != "changed" {
							t.Fatalf("retained result ignored scope change: %+v", row)
						}
					}
				}
			}
			assertPrevious(second)
			assertPrevious(awaitAccountCheck(t, m))
		})
	}
}
func TestAccountCheckCancellationAndScope(t *testing.T) {
	for _, mode := range []string{"cancel", "changed", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				once.Do(func() { close(started) })
				select {
				case <-r.Context().Done():
					return
				case <-release:
				}
				w.WriteHeader(403)
			}))
			old := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			defer func() { http.DefaultTransport = old; server.Close() }()
			m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.accounts = []Account{{ID: "d", Kind: "webdav", Endpoint: server.URL, Folder: "/", Secret: "private"}, {ID: "t", Kind: "webdav", Endpoint: server.URL, Folder: "/", Secret: "other"}}
			for _, ids := range [][]string{nil, {"missing"}, {"d", "d"}, make([]string, 21)} {
				if _, e = m.StartAccountCheck(ids); e == nil {
					t.Fatal("invalid batch accepted")
				}
			}
			s, e := m.StartAccountCheck([]string{"d", "t"})
			if e != nil {
				t.Fatal(e)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("request not started")
			}
			if _, e = m.StartAccountCheck([]string{"d"}); e == nil {
				t.Fatal("concurrent checker")
			}
			if _, e = m.AccountCheckStatus("stale"); e == nil {
				t.Fatal("stale ID accepted")
			}
			if mode == "changed" {
				m.mu.Lock()
				m.accounts[0].Secret = "new-secret"
				m.accounts[1].Secret = "new-other"
				m.mu.Unlock()
				close(release)
			} else if mode == "close" {
				m.Close()
				close(release)
			} else {
				m.CancelAccountCheck(s.ID)
				close(release)
			}
			s = awaitAccountCheck(t, m)
			want := "cancelled"
			if mode == "changed" {
				want = "changed"
			}
			if s.Rows[0].State != want || s.Rows[1].State != want {
				t.Fatal(s)
			}
			if requests != 1 {
				t.Fatal("checker continued after cancel/scope change", requests)
			}
		})
	}
}

func TestAccountCheckPartialResultsAndDeadline(t *testing.T) {
	blocked := make(chan struct{})
	shutdown := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first/" {
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/first/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`)
			return
		}
		select {
		case <-blocked:
		default:
			close(blocked)
		}
		select {
		case <-r.Context().Done():
		case <-shutdown:
		}
	}))
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old; close(shutdown); server.Close() }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.accounts = []Account{{ID: "a", Kind: "webdav", Endpoint: server.URL, Folder: "/first"}, {ID: "b", Kind: "webdav", Endpoint: server.URL, Folder: "/blocked"}, {ID: "c", Kind: "webdav", Endpoint: server.URL, Folder: "/never"}}
	s, e := m.StartAccountCheck([]string{"a", "b", "c"})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("second read not started")
	}
	m.CancelAccountCheck(s.ID)
	s = awaitAccountCheck(t, m)
	if s.State != "stopped" || s.Rows[0].State != "available" || s.Rows[1].State != "cancelled" || s.Rows[2].State != "cancelled" || !s.Rows[2].CheckedAt.IsZero() {
		t.Fatal(s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := checkAccountRead(ctx, NewHTTPUploader().Client, m.accounts[1]); e == nil || ctx.Err() == nil {
		t.Fatal("request ignored context deadline", e)
	}
}
