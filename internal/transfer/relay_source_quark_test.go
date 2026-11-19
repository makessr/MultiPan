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

func TestRelayQuarkPinnedOriginal(t *testing.T) {
	for _, scenario := range []string{"success", "empty", "no-pin", "wrong-account", "duplicate-pins", "stale-revision", "missing-stamp", "bad-parent", "missing-type", "bad-pin-bytes", "json-md5-advisory", "bad-content-md5", "changed-after-target", "target-unknown", "cache-recheck", "snapshot-pass", "snapshot-changed"} {
		t.Run(scenario, func(t *testing.T) {
			mode := scenario
			data := []byte("夸克可信原件\x00")
			if mode == "empty" {
				data = []byte{}
			}
			sha := sha256.Sum256(data)
			pin := hex.EncodeToString(sha[:])
			var mu sync.Mutex
			gets, puts, targetLists := 0, 0, 0
			var target []byte
			exists := false
			badPath := t.TempDir()
			var m *Manager
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "drive.quark.cn" {
					if (!strings.Contains(r.Header.Get("Cookie"), "__puus=source-private") && !strings.Contains(r.Header.Get("Cookie"), "__puus=rotated-fixture")) || r.Header.Get("Authorization") != "" {
						t.Error("source auth")
					}
					w.Header().Add("Set-Cookie", "__puus=rotated-fixture; Secure; Path=/")
					var out any
					switch r.URL.Path {
					case "/1/clouddrive/file/sort":
						stamp := "123"
						parent := "0"
						if mode == "stale-revision" {
							stamp = "124"
						}
						if mode == "missing-stamp" {
							stamp = "0"
						}
						if mode == "bad-parent" {
							parent = "other"
						}
						row := map[string]any{"fid": "q-original", "pdir_fid": parent, "file_name": "原件.txt", "file_type": 1, "dir": false, "size": len(data), "updated_at": json.Number(stamp)}
						if mode == "missing-type" {
							delete(row, "file_type")
						}
						out = map[string]any{"list": []any{row}}
					case "/1/clouddrive/file/download":
						if r.Method != "POST" {
							t.Error("download method")
						}
						var input struct {
							IDs []string `json:"fids"`
						}
						json.NewDecoder(r.Body).Decode(&input)
						if len(input.IDs) != 1 || input.IDs[0] != "q-original" {
							t.Error("source ID")
						}
						md5Hint := "wrong-advisory"
						out = []any{map[string]any{"fid": "q-original", "file_name": "原件.txt", "size": len(data), "download_url": "https://content.quark.cn/original?signature=ephemeral", "md5": md5Hint}}
					default:
						t.Error("unexpected source mutation", r.Method, r.URL.Path)
						w.WriteHeader(405)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "status": 200, "data": out})
					return
				}
				if r.Host == "content.quark.cn" {
					gets++
					if !strings.Contains(r.Header.Get("Cookie"), "rotated-fixture") || r.Header.Get("Authorization") != "" || r.Method != "GET" {
						t.Error("source content auth")
					}
					body := data
					if mode == "bad-pin-bytes" || mode == "changed-after-target" && exists || mode == "cache-changed" || mode == "snapshot-changed" && gets > 1 {
						body = []byte(strings.Repeat("x", len(data)))
					}
					digest := md5.Sum(body)
					w.Header().Set("Content-MD5", base64.StdEncoding.EncodeToString(digest[:]))
					if mode == "bad-content-md5" {
						w.Header().Set("Content-MD5", "bad-base64")
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
				if u != "target" || p != "target-private" || r.Header.Get("Cookie") != "" {
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
					t.Error("unexpected target write", r.Method)
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
			m.accounts = []Account{{ID: "q", Name: "夸克源", Kind: "quark", Endpoint: QuarkAPI, Secret: "__puus=source-private"}, {ID: "d", Name: "DAV目标", Kind: "webdav", Endpoint: "https://dav.fixture/base", Username: "target", Secret: "target-private"}}
			fp := UploadFingerprint{AccountID: "q", EntryID: "q-original", Name: "原件.txt", Size: int64(len(data)), Revision: "123", SHA256: pin}
			if mode == "wrong-account" {
				fp.AccountID = "other"
			}
			m.fingerprints = []UploadFingerprint{fp}
			snapshot := strings.HasPrefix(mode, "snapshot-")
			if mode == "no-pin" || snapshot {
				m.fingerprints = nil
			}
			if mode == "duplicate-pins" {
				m.fingerprints = append(m.fingerprints, fp)
			}
			page, e := m.RelaySourceFiles(context.Background(), "q", "0")
			invalidPage := mode == "bad-parent" || mode == "missing-type"
			if invalidPage {
				if e == nil {
					t.Fatal("invalid metadata listed")
				}
			} else if e != nil {
				t.Fatal(e)
			}
			noPin := snapshot || mode == "no-pin" || mode == "wrong-account" || mode == "duplicate-pins" || mode == "stale-revision" || mode == "missing-stamp"
			if noPin && len(page.Entries) != 0 {
				t.Fatal("ineligible pin exposed")
			}
			if !noPin && !invalidPage && len(page.Entries) != 1 {
				t.Fatal("eligible original absent", page)
			}
			in := RelayInput{SourceAccountID: "q", ParentID: "0", EntryID: "q-original", Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "d", DestinationID: "/"}
			n, e := m.QueueRelays(context.Background(), []RelayInput{in})
			if noPin || invalidPage {
				if e == nil || n != 0 || puts != 0 || gets != 0 {
					t.Fatal("untrusted source queued", n, e)
				}
				if !snapshot {
					return
				}
				listed, err := m.RelaySnapshotSourceFiles(context.Background(), "q", "0")
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
			case "bad-pin-bytes", "bad-content-md5", "cache-recheck", "snapshot-changed":
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
				t.Error("wrong PUT count", puts)
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
				if changed := awaitRelay(t, m)[0]; changed.State != "failed" {
					t.Fatal(changed)
				}
				mu.Lock()
				if puts != 0 {
					t.Error("cached source was not re-read before upload")
				}
				mode = "success"
				mu.Unlock()
				if e = m.ResumeRelay(j.ID); e != nil {
					t.Fatal(e)
				}
				if resumed := awaitRelay(t, m)[0]; resumed.State != "verified" {
					t.Fatal(resumed)
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
				if checked := awaitRelay(t, again)[0]; checked.State != "verified" {
					t.Fatal(checked)
				}
				mu.Lock()
				if puts != before {
					t.Error("read-only pin check uploaded")
				}
				mu.Unlock()
			}
			saved, e := readState(statePath)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(saved.Relays)
			for _, value := range []string{"signature=", "ephemeral", "__puus=", "source-private", "target-private"} {
				if strings.Contains(string(raw), value) {
					t.Fatal("secret in receipt", value)
				}
			}
		})
	}
}
