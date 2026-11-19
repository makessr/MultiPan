package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
)

func TestRelayBaiduBoundOriginal(t *testing.T) {
	for _, scenario := range []string{"success", "empty", "json-file", "missing-md5", "opaque-md5", "not-raw-md5", "missing-fsid", "same-path-replaced", "changed-after-download", "name-mismatch", "metadata-id-mismatch", "unsafe-link", "foreign-redirect", "forbidden", "target-read-failed"} {
		t.Run(scenario, func(t *testing.T) {
			const fsid = "9007199254740993123"
			mode := scenario
			data := []byte("百度relay原件\x00")
			if mode == "empty" {
				data = []byte{}
			}
			if mode == "json-file" {
				data = []byte(`{"redirect_url":"https://must-not-follow.example/"}`)
			}
			sum := md5.Sum(data)
			digest := hex.EncodeToString(sum[:])
			var mu sync.Mutex
			lists, puts, sourceGets := 0, 0, 0
			var target []byte
			exists := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "pan.baidu.com" {
					if r.Method != "GET" || !strings.Contains(r.Header.Get("Cookie"), "BDUSS=") || r.Header.Get("Authorization") != "" {
						t.Error("unexpected source mutation/auth", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/list":
						lists++
						name, id, hash := "原件.txt", fsid, digest
						if mode == "missing-md5" {
							hash = ""
						}
						if mode == "opaque-md5" {
							hash = "opaque"
						}
						if mode == "not-raw-md5" {
							hash = strings.Repeat("f", 32)
						}
						if mode == "missing-fsid" {
							id = "0"
						}
						if mode == "same-path-replaced" && lists >= 3 {
							id = "9007199254740993124"
						}
						if mode == "changed-after-download" && lists >= 4 {
							hash = strings.Repeat("e", 32)
						}
						if mode == "name-mismatch" {
							name = "other.txt"
						}
						w.Header().Add("Set-Cookie", "BDUSS=rotated-fixture; Secure; Path=/")
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/原件.txt", "server_filename": name, "fs_id": json.Number(id), "isdir": 0, "size": len(data), "md5": hash}}})
						return
					case "/api/filemetas":
						id := fsid
						if mode == "metadata-id-mismatch" {
							id = "999"
						}
						u := "https://d.pcs.baidu.com/original?signature=ephemeral"
						if mode == "unsafe-link" {
							u = "https://127.0.0.1/private"
						}
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": "/原件.txt", "fs_id": json.Number(id), "size": len(data), "dlink": u}}})
						return
					default:
						t.Error("unexpected source API", r.URL.Path)
						w.WriteHeader(405)
						return
					}
				}
				if r.Host == "d.pcs.baidu.com" || r.Host == "baidu.com.foreign.example" {
					sourceGets++
					if r.Method != "GET" || r.Header.Get("Authorization") != "" {
						t.Error("source content mutation/auth")
					}
					if r.Host == "d.pcs.baidu.com" && !strings.Contains(r.Header.Get("Cookie"), "rotated-fixture") {
						t.Error("trusted cookie rotation missing")
					}
					if r.Host == "baidu.com.foreign.example" && r.Header.Get("Cookie") != "" {
						t.Error("Cookie leaked to lookalike")
					}
					if mode == "foreign-redirect" && r.Host == "d.pcs.baidu.com" {
						w.Header().Set("Location", "https://baidu.com.foreign.example/original")
						w.WriteHeader(302)
						return
					}
					if mode == "forbidden" {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"error_code":9999,"error_msg":"do-not-leak-cookie"}`)
						return
					}
					w.Write(data)
					return
				}
				if r.Host != "dav.fixture" {
					t.Error("unexpected host", r.Host)
					w.WriteHeader(400)
					return
				}
				user, pass, _ := r.BasicAuth()
				if user != "target" || pass != "target-private" || r.Header.Get("Cookie") != "" {
					t.Error("target credentials mixed")
				}
				switch r.Method {
				case "PROPFIND":
					w.WriteHeader(207)
					fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/base/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>`)
					if exists {
						fmt.Fprintf(w, `<d:response><d:href>/base/原件.txt</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:getcontentlength>%d</d:getcontentlength></d:prop></d:propstat></d:response>`, len(target))
					}
					fmt.Fprint(w, `</d:multistatus>`)
				case "PUT":
					puts++
					if r.Header.Get("If-None-Match") != "*" || exists {
						t.Error("unsafe target upload")
					}
					target, _ = io.ReadAll(r.Body)
					exists = true
					w.WriteHeader(201)
				case "GET":
					if mode == "target-read-failed" {
						w.WriteHeader(503)
						return
					}
					w.Write(target)
				default:
					t.Error("unexpected DAV operation", r.Method)
					w.WriteHeader(405)
				}
			}))
			original := http.DefaultTransport
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = original; tr.CloseIdleConnections(); server.Close() }()
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.accounts = []Account{{ID: "b", Name: "百度来源", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=source-private", UserAgent: "fixture-browser"}, {ID: "d", Name: "DAV目标", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "target", Secret: "target-private"}}
			in := RelayInput{SourceAccountID: "b", ParentID: "/", EntryID: "/原件.txt", Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "d", DestinationID: "/"}
			n, e := m.QueueRelays(context.Background(), []RelayInput{in})
			if mode == "missing-md5" || mode == "opaque-md5" || mode == "missing-fsid" || mode == "name-mismatch" {
				if e == nil || n != 0 || puts != 0 || sourceGets != 0 {
					t.Fatal("invalid preflight allowed", n, e)
				}
				return
			}
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			j := awaitRelay(t, m)[0]
			want := "verified"
			expectedPuts := 1
			switch mode {
			case "not-raw-md5", "same-path-replaced", "changed-after-download", "metadata-id-mismatch", "unsafe-link", "forbidden":
				want = "failed"
				expectedPuts = 0
			case "target-read-failed":
				want = "unknown"
			}
			if j.State != want {
				t.Fatalf("%s expected %s: %s", j.State, want, j.Message)
			}
			if strings.Contains(j.Message, "do-not-leak-cookie") {
				t.Fatal("raw denial leaked")
			}
			mu.Lock()
			if puts != expectedPuts {
				t.Error("wrong target writes", puts)
			}
			mu.Unlock()
			m.Close()
			restored, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			if len(restored.RelayJobs()) != 1 {
				t.Fatal("source receipt lost")
			}
			if want == "unknown" {
				j = restored.RelayJobs()[0]
				if e = restored.ResumeRelay(j.ID); e == nil {
					t.Fatal("unknown replay")
				}
				mu.Lock()
				mode = "success"
				mu.Unlock()
				if e = restored.CheckRelay(j.ID); e != nil {
					t.Fatal(e)
				}
				if got := awaitRelay(t, restored)[0]; got.State != "verified" {
					t.Fatal(got)
				}
				mu.Lock()
				if puts != 1 {
					t.Fatal("check wrote target")
				}
				mu.Unlock()
			}
			saved, e := readState(statePath)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(saved.Relays)
			for _, value := range []string{"signature=", "ephemeral", "BDUSS=", "source-private", "target-private"} {
				if strings.Contains(string(raw), value) {
					t.Fatal("secret or dlink in relay", value)
				}
			}
		})
	}
}
