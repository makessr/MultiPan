package transfer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
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

func TestRelayGuangyaPinnedOriginal(t *testing.T) {
	for _, parent := range []string{guangyaRoot, "child-id"} {
		for _, scenario := range []string{"success", "empty", "no-pin", "wrong-account", "duplicate-pins", "invalid-pin", "bad-parent", "missing-parent", "missing-size", "bad-type", "incomplete-page", "bad-pin-bytes", "bad-content-md5", "changed-after-target", "target-unknown", "unsafe-url", "cache-recheck", "snapshot-pass", "snapshot-changed"} {
			t.Run(parent+"/"+scenario, func(t *testing.T) {
				mode := scenario
				data := []byte("光鸭本机可信原件\x00")
				if mode == "empty" {
					data = []byte{}
				}
				sha := sha256.Sum256(data)
				pin := hex.EncodeToString(sha[:])
				var mu sync.Mutex
				gets, puts, targetLists := 0, 0, 0
				var target []byte
				exists := false
				var m *Manager
				badPath := t.TempDir()
				pid := parent
				if pid == guangyaRoot {
					pid = ""
				}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.Host == "api.guangyapan.com" {
						if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer source-private" || r.Header.Get("Did") != "fixture-device" || r.Header.Get("Cookie") != "" {
							t.Error("source authentication")
						}
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						var out any
						switch r.URL.Path {
						case "/userres/v1/file/get_file_list":
							if body["parentId"] != pid {
								t.Error("root mapping", body)
							}
							p := pid
							if mode == "bad-parent" {
								p = "other-parent"
							}
							row := map[string]any{"fileId": "gy-file", "parentId": p, "fileName": "原件.txt", "fileSize": len(data), "resType": 1}
							if mode == "missing-parent" {
								delete(row, "parentId")
							}
							if mode == "missing-size" {
								delete(row, "fileSize")
							}
							if mode == "bad-type" {
								row["resType"] = 7
							}
							total := 1
							if mode == "incomplete-page" {
								total = 2
							}
							out = map[string]any{"list": []any{row}, "total": total}
						case "/userres/v1/get_res_download_url":
							if body["fileId"] != "gy-file" {
								t.Error("source ID")
							}
							u := "https://public-node.example/original?signature=ephemeral"
							if mode == "unsafe-url" {
								u = "https://127.0.0.1/private"
							}
							out = map[string]any{"signedURL": u}
						default:
							t.Error("unexpected source mutation", r.URL.Path)
							w.WriteHeader(405)
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": out})
						return
					}
					if r.Host == "public-node.example" {
						gets++
						if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Did") != "" || r.Header.Get("Cookie") != "" {
							t.Error("source credentials leaked to node")
						}
						body := data
						if mode == "bad-pin-bytes" || mode == "changed-after-target" && exists || mode == "cache-changed" || mode == "snapshot-changed" && gets > 1 {
							body = []byte(strings.Repeat("x", len(data)))
						}
						sum := md5.Sum(body)
						w.Header().Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
						if mode == "bad-content-md5" {
							w.Header().Set("Content-MD5", "invalid")
						}
						w.Write(body)
						return
					}
					if r.Host != "dav.fixture" {
						t.Error("foreign host", r.Host)
						w.WriteHeader(400)
						return
					}
					u, p, _ := r.BasicAuth()
					if u != "target" || p != "target-private" || r.Header.Get("Did") != "" || r.Header.Get("Cookie") != "" {
						t.Error("target credentials mixed")
					}
					switch r.Method {
					case "PROPFIND":
						targetLists++
						if mode == "cache-recheck" && targetLists == 2 {
							m.mu.Lock()
							m.path = badPath
							m.mu.Unlock()
						}
						w.WriteHeader(207)
						fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/base/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>`)
						if exists {
							fmt.Fprintf(w, `<d:response><d:href>/base/原件.txt</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:getcontentlength>%d</d:getcontentlength></d:prop></d:propstat></d:response>`, len(target))
						}
						fmt.Fprint(w, `</d:multistatus>`)
					case "PUT":
						puts++
						if r.Header.Get("If-None-Match") != "*" || exists {
							t.Error("unsafe target")
						}
						target, _ = io.ReadAll(r.Body)
						exists = true
						w.WriteHeader(201)
					case "GET":
						if mode == "target-unknown" {
							w.WriteHeader(503)
							return
						}
						w.Write(target)
					default:
						t.Error("unexpected target mutation", r.Method)
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
				statePath := filepath.Join(t.TempDir(), "state.enc")
				var e error
				m, e = NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer m.Close()
				m.accounts = []Account{{ID: "g", Name: "光鸭源", Kind: "guangya", Endpoint: GuangyaAPI, Username: "fixture-device", Secret: "source-private"}, {ID: "d", Name: "DAV目标", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "target", Secret: "target-private"}}
				fp := UploadFingerprint{AccountID: "g", EntryID: "gy-file", Name: "原件.txt", Size: int64(len(data)), SHA256: pin}
				if mode == "wrong-account" {
					fp.AccountID = "other"
				}
				if mode == "invalid-pin" {
					fp.SHA256 = "invalid"
				}
				m.fingerprints = []UploadFingerprint{fp}
				snapshot := strings.HasPrefix(mode, "snapshot-")
				if mode == "no-pin" || snapshot {
					m.fingerprints = nil
				}
				if mode == "duplicate-pins" {
					m.fingerprints = append(m.fingerprints, fp)
				}
				page, e := m.RelaySourceFiles(context.Background(), "g", parent)
				badPage := mode == "bad-parent" || mode == "missing-parent" || mode == "missing-size" || mode == "bad-type" || mode == "incomplete-page"
				noPin := snapshot || mode == "no-pin" || mode == "wrong-account" || mode == "duplicate-pins" || mode == "invalid-pin"
				if badPage {
					if e == nil {
						t.Fatal("incomplete metadata listed")
					}
				} else if e != nil {
					t.Fatal(e)
				}
				if noPin && len(page.Entries) != 0 {
					t.Fatal("untrusted original listed")
				}
				if !badPage && !noPin && len(page.Entries) != 1 {
					t.Fatal("original absent")
				}
				in := RelayInput{SourceAccountID: "g", ParentID: parent, EntryID: "gy-file", Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "d", DestinationID: "/"}
				n, e := m.QueueRelays(context.Background(), []RelayInput{in})
				if badPage || noPin {
					if e == nil || n != 0 || gets != 0 || puts != 0 {
						t.Fatal("invalid source queued", n, e)
					}
					if !snapshot {
						return
					}
					listed, err := m.RelaySnapshotSourceFiles(context.Background(), "g", parent)
					if err != nil || len(listed.Entries) != 1 {
						t.Fatal("explicit snapshot listing did not include the eligible file", err)
					}
					n, e = m.QueueRelaySnapshots(context.Background(), []RelayInput{in})
				}
				if e != nil || n != 1 {
					t.Fatal(n, e)
				}
				j := awaitRelay(t, m)[0]
				expected := "verified"
				wantPuts := 1
				switch mode {
				case "bad-pin-bytes", "bad-content-md5", "unsafe-url", "cache-recheck", "snapshot-changed":
					expected = "failed"
					wantPuts = 0
				case "changed-after-target", "target-unknown":
					expected = "unknown"
				}
				if j.State != expected {
					t.Fatalf("%s expected %s: %s", j.State, expected, j.Message)
				}
				mu.Lock()
				if puts != wantPuts {
					t.Error("wrong target writes", puts)
				}
				if expected == "verified" && gets < 2 {
					t.Error("source was not fully re-read")
				}
				if mode == "snapshot-pass" && gets < 3 {
					t.Error("missing initial snapshot or later source re-read")
				}
				mu.Unlock()
				if mode == "cache-recheck" {
					m.mu.Lock()
					m.path = statePath
					m.mu.Unlock()
					mu.Lock()
					mode = "cache-changed"
					mu.Unlock()
					if e = m.ResumeRelay(j.ID); e != nil {
						t.Fatal(e)
					}
					if got := awaitRelay(t, m)[0]; got.State != "failed" {
						t.Fatal(got)
					}
					mu.Lock()
					if puts != 0 || gets < 2 {
						t.Error("complete cache was not checked against current source")
					}
					mode = "success"
					mu.Unlock()
					if e = m.ResumeRelay(j.ID); e != nil {
						t.Fatal(e)
					}
					if got := awaitRelay(t, m)[0]; got.State != "verified" {
						t.Fatal(got)
					}
				}
				m.Close()
				again, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer again.Close()
				if len(again.RelayJobs()) != 1 {
					t.Fatal("lost pin receipt")
				}
				if expected == "unknown" {
					j = again.RelayJobs()[0]
					if e = again.ResumeRelay(j.ID); e == nil {
						t.Fatal("unknown replay")
					}
					mu.Lock()
					mode = "success"
					before := puts
					mu.Unlock()
					if e = again.CheckRelay(j.ID); e != nil {
						t.Fatal(e)
					}
					if got := awaitRelay(t, again)[0]; got.State != "verified" {
						t.Fatal(got)
					}
					mu.Lock()
					if puts != before {
						t.Error("read-only check uploaded")
					}
					mu.Unlock()
				}
				saved, e := readState(statePath)
				if e != nil {
					t.Fatal(e)
				}
				raw, _ := json.Marshal(saved.Relays)
				for _, value := range []string{"source-private", "target-private", "signature=", "ephemeral", "fixture-device"} {
					if strings.Contains(string(raw), value) {
						t.Fatal("credential/dlink in relay", value)
					}
				}
			})
		}
	}
}
