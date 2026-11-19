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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRelay123SourceToDAV(t *testing.T) {
	for _, scenario := range []string{"success", "empty", "missing-md5", "wrong-parent", "tampered-bytes", "download-version-changed", "source-changed-after-download", "target-unknown", "target-response-lost", "unsafe-url", "redirect", "target-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			const id = "9007199254740993123"
			mode := scenario
			data := []byte("原件123 relay\x00")
			if mode == "empty" {
				data = []byte{}
			}
			sum := md5.Sum(data)
			version := hex.EncodeToString(sum[:])
			other := strings.Repeat("a", 32)
			var mu sync.Mutex
			lists, puts, downloads, infos := 0, 0, 0, 0
			var target []byte
			exists := false
			if mode == "target-conflict" {
				target = []byte("foreign")
				exists = true
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "api.123278.com" {
					if r.Header.Get("Authorization") != "Bearer source-private" || r.Header.Get("Cookie") != "" {
						t.Error("API credentials wrong")
					}
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" && r.URL.Path == "/b/api/file/list/new" {
						lists++
						v := version
						parent := "0"
						if mode == "missing-md5" {
							v = ""
						}
						if mode == "wrong-parent" {
							parent = "999"
						}
						if mode == "download-version-changed" && lists >= 3 || mode == "source-changed-after-download" && lists >= 4 {
							v = other
						}
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"Next": -1, "Total": 1, "InfoList": []any{map[string]any{"FileId": json.Number(id), "ParentFileId": json.Number(parent), "FileName": "原件.txt", "Type": 0, "Size": len(data), "Etag": v, "S3KeyFlag": "fixture-key"}}}})
						return
					}
					if r.Method == "POST" && r.URL.Path == "/b/api/file/download_info" {
						infos++
						var body map[string]any
						decoder := json.NewDecoder(r.Body)
						decoder.UseNumber()
						decoder.Decode(&body)
						if body["fileId"] != json.Number(id) || body["s3keyFlag"] != "fixture-key" {
							t.Error("lost large identity", body)
						}
						u := "https://relay-cdn.example/file?sig=ephemeral"
						if mode == "unsafe-url" {
							u = "https://127.0.0.1/private"
						}
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": u}})
						return
					}
					t.Error("unexpected source write", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				if r.Host == "relay-cdn.example" {
					downloads++
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("source credentials leaked to CDN")
					}
					if mode == "redirect" && r.URL.Path == "/file" {
						w.Header().Set("Location", "/final")
						w.WriteHeader(302)
						return
					}
					body := data
					if mode == "tampered-bytes" {
						body = []byte(strings.Repeat("x", len(data)))
					}
					w.Write(body)
					return
				}
				if r.Host != "dav.fixture" {
					t.Error("foreign host", r.Host)
					w.WriteHeader(400)
					return
				}
				user, pass, _ := r.BasicAuth()
				if user != "target-user" || pass != "target-private" || r.Header.Get("Cookie") != "" {
					t.Error("target credentials crossed")
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
					if r.URL.Path != "/base/原件.txt" || r.Header.Get("If-None-Match") != "*" {
						t.Error("unsafe target PUT", r.URL.Path)
					}
					if exists {
						w.WriteHeader(412)
						return
					}
					target, _ = io.ReadAll(r.Body)
					exists = true
					if mode == "target-response-lost" {
						w.WriteHeader(503)
					} else {
						w.WriteHeader(201)
					}
				case "GET":
					if mode == "target-unknown" {
						w.WriteHeader(503)
						return
					}
					if !exists {
						w.WriteHeader(404)
						return
					}
					w.Write(target)
				default:
					t.Error("unexpected DAV mutation", r.Method)
					w.WriteHeader(405)
				}
			}))
			original := http.DefaultTransport
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections(); server.Close() }()
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.accounts = []Account{{ID: "s", Name: "123源", Kind: "pan123", Endpoint: Pan123API, Secret: "source-private"}, {ID: "t", Name: "DAV目标", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "target-user", Secret: "target-private"}}
			in := RelayInput{SourceAccountID: "s", ParentID: "0", EntryID: id, Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "t", DestinationID: "/"}
			n, e := m.QueueRelays(context.Background(), []RelayInput{in})
			if mode == "missing-md5" || mode == "wrong-parent" || mode == "target-conflict" {
				if e == nil || n != 0 {
					t.Fatal("preflight accepted", e)
				}
				if puts != 0 || downloads != 0 || infos != 0 {
					t.Fatal("preflight transferred data")
				}
				return
			}
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			j := awaitRelay(t, m)[0]
			want := "verified"
			wantPuts := 1
			switch mode {
			case "tampered-bytes", "download-version-changed", "source-changed-after-download", "unsafe-url":
				want = "failed"
				wantPuts = 0
			case "target-unknown":
				want = "unknown"
			}
			if j.State != want {
				t.Fatalf("%s expected %s: %s", j.State, want, j.Message)
			}
			mu.Lock()
			if puts != wantPuts {
				t.Error("wrong PUT count", puts)
			}
			oldLists := lists
			mu.Unlock()
			m.Close()
			restored, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			if len(restored.RelayJobs()) != 1 {
				t.Fatal("lost receipt")
			}
			mu.Lock()
			if lists != oldLists {
				t.Error("restart sent a request")
			}
			mu.Unlock()
			if want == "unknown" {
				got := restored.RelayJobs()[0]
				if e = restored.ResumeRelay(got.ID); e == nil {
					t.Fatal("unknown upload replayed")
				}
				mu.Lock()
				mode = "success"
				mu.Unlock()
				if e = restored.CheckRelay(got.ID); e != nil {
					t.Fatal(e)
				}
				if checked := awaitRelay(t, restored)[0]; checked.State != "verified" {
					t.Fatal(checked)
				}
				mu.Lock()
				if puts != 1 {
					t.Error("check repeated PUT")
				}
				mu.Unlock()
			}
			raw, e := os.ReadFile(statePath)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(raw), "ephemeral") || strings.Contains(string(raw), "source-private") {
				t.Fatal("plaintext secret in state")
			}
			saved, e := readState(statePath)
			if e != nil {
				t.Fatal(e)
			}
			encoded, _ := json.Marshal(saved.Relays)
			if strings.Contains(string(encoded), "ephemeral") || strings.Contains(string(encoded), "source-private") {
				t.Fatal("ephemeral URL/credentials persisted in relay")
			}
		})
	}
}

func TestRelayLegacySourceCompatibility(t *testing.T) {
	f, m, in := newRelayFixture(t, "success")
	if _, e := m.QueueRelays(context.Background(), []RelayInput{in}); e != nil {
		t.Fatal(e)
	}
	awaitRelay(t, m)
	m.mu.Lock()
	m.relays[0].SourceKind = ""
	if e := m.saveLocked(); e != nil {
		t.Fatal(e)
	}
	m.mu.Unlock()
	m.Close()
	restored, e := NewManager(m.path, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if len(restored.RelayJobs()) != 1 || restored.RelayJobs()[0].State != "verified" {
		t.Fatal("legacy DAV record lost")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 1 {
		t.Fatal("legacy replayed")
	}
	d := m.relays[0]
	d.SourceKind = "pan123"
	if validRelaySavedSource(d) {
		t.Fatal("cross-provider record accepted")
	}
}
