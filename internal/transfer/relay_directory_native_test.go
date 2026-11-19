package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestRelayNativeDirectory(t *testing.T) {
	for _, kind := range []string{"pan123", "baidu"} {
		for _, scenario := range []string{"success", "empty", "missing-md5", "missing-type", "missing-size", "missing-id", "missing-list", "duplicate", "wrong-parent", "partial-page", "source-id-changed", "after-download", "after-readback", "ack-lost", "read-failed", "stop-preflight", "wrong-bytes", "duplicate-object"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				f, m, in := newDirectoryFixture(t, "success")
				if scenario == "empty" {
					f.src = map[string]treeEntry{"/": {folder: true}, "/tree": {folder: true}}
				}
				sourcePaths := []string{}
				for p := range f.src {
					sourcePaths = append(sourcePaths, p)
				}
				sort.Strings(sourcePaths)
				ids, paths := map[string]string{}, map[string]string{}
				for i, p := range sourcePaths {
					id := fmt.Sprintf("90071992547409931%02d", i)
					ids[p] = id
					paths[id] = p
				}
				ids["/"] = "0"
				paths["0"] = "/"
				a := Account{ID: "s", Name: "native source", Kind: kind, Endpoint: Pan123API, Secret: "source-private-token"}
				if kind == "baidu" {
					a.Endpoint = BaiduAPI
					a.Secret = "BDUSS=source-private-cookie"
				}
				m.accounts[0] = a
				m.accounts[1].Endpoint = "https://dav.fixture/dst"
				if kind == "pan123" {
					in.ParentID = "0"
					in.EntryID = ids["/tree"]
				}
				var mu sync.Mutex
				mode := scenario
				gets := 0
				targetRead := false
				sourceWrites := 0
				stopSent := false
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Host == "dav.fixture" {
						if r.Method == "GET" {
							mu.Lock()
							targetRead = true
							mu.Unlock()
						}
						f.serve(w, r)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					host := "api.123278.com"
					if kind == "baidu" {
						host = "pan.baidu.com"
					}
					if r.Host == "relay-cdn.example" || r.Host == "d.pcs.baidu.com" {
						gets++
						if r.Header.Get("Authorization") != "" || kind == "pan123" && r.Header.Get("Cookie") != "" {
							t.Error("credentials leaked to CDN")
						}
						if kind == "baidu" && !strings.Contains(r.Header.Get("Cookie"), "source-private-cookie") {
							t.Error("trusted Baidu cookie absent")
						}
						p := r.URL.Query().Get("path")
						if mode == "wrong-bytes" {
							w.Write([]byte(strings.Repeat("x", len(f.src[p].data))))
							return
						}
						w.Write(f.src[p].data)
						return
					}
					if r.Host != host {
						t.Error("unexpected origin", r.Host)
						w.WriteHeader(400)
						return
					}
					if kind == "pan123" {
						if r.Header.Get("Authorization") != "Bearer "+a.Secret || r.Header.Get("Cookie") != "" {
							t.Error("123 source credentials")
						}
					} else {
						if !strings.Contains(r.Header.Get("Cookie"), a.Secret) || r.Header.Get("Authorization") != "" {
							t.Error("Baidu source credentials")
						}
					}
					listRoute := "/b/api/file/list/new"
					if kind == "baidu" {
						listRoute = "/api/list"
					}
					if r.URL.Path == listRoute && r.Method == "GET" {
						parent := r.URL.Query().Get("dir")
						if kind == "pan123" {
							parent = paths[r.URL.Query().Get("parentFileId")]
						}
						if parent == "" {
							t.Error("lost native parent identity")
							w.WriteHeader(400)
							return
						}
						if mode == "stop-preflight" && parent == "/tree" && !stopSent {
							stopSent = true
							_ = m.PauseRelays()
						}
						f.mu.Lock()
						creates := f.creates
						f.mu.Unlock()
						replaced := mode == "source-id-changed" && creates > 0 || mode == "after-download" && gets > 0 || mode == "after-readback" && targetRead
						rows := []any{}
						for _, p := range sourcePaths {
							if p == "/" || path.Dir(p) != parent {
								continue
							}
							entry := f.src[p]
							dir := 0
							if entry.folder {
								dir = 1
							}
							id := ids[p]
							if mode == "duplicate-object" && parent == "/tree" {
								id = "123"
							}
							if replaced && p == "/tree" {
								id = "9007199254740993199"
							}
							sum := md5.Sum(entry.data)
							hash := hex.EncodeToString(sum[:])
							if mode == "missing-md5" && !entry.folder {
								hash = ""
							}
							var row map[string]any
							if kind == "pan123" {
								pid := ids[parent]
								if mode == "wrong-parent" {
									pid = "999"
								}
								row = map[string]any{"FileId": json.Number(id), "ParentFileId": json.Number(pid), "FileName": path.Base(p), "Type": dir, "Size": len(entry.data), "Etag": hash, "S3KeyFlag": "fixture"}
							} else {
								row = map[string]any{"path": p, "fs_id": json.Number(id), "server_filename": path.Base(p), "isdir": dir, "size": len(entry.data), "md5": hash}
								if mode == "wrong-parent" {
									row["path"] = "/elsewhere/" + path.Base(p)
								}
							}
							if mode == "missing-type" {
								delete(row, "Type")
								delete(row, "isdir")
							}
							if mode == "missing-size" && !entry.folder {
								delete(row, "Size")
								delete(row, "size")
							}
							if mode == "missing-id" {
								delete(row, "FileId")
								delete(row, "fs_id")
							}
							rows = append(rows, row)
						}
						if mode == "duplicate" && len(rows) > 0 {
							rows = append(rows, rows[0])
						}
						if kind == "pan123" {
							data := map[string]any{"InfoList": rows, "Total": len(rows), "Next": -1}
							if mode == "partial-page" {
								data["Total"] = len(rows) + 1
							}
							if mode == "missing-list" {
								delete(data, "InfoList")
							}
							json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
						} else {
							if mode == "partial-page" {
								for len(rows) < 1000 {
									rows = append(rows, map[string]any{"path": fmt.Sprintf("%s/p%d", strings.TrimSuffix(parent, "/"), len(rows)), "fs_id": json.Number(fmt.Sprint(1000 + len(rows))), "server_filename": fmt.Sprint("p", len(rows)), "isdir": 0, "size": 1, "md5": strings.Repeat("a", 32)})
								}
							}
							data := map[string]any{"errno": 0, "list": rows}
							if mode == "missing-list" {
								delete(data, "list")
							}
							json.NewEncoder(w).Encode(data)
						}
						return
					}
					if kind == "pan123" && r.URL.Path == "/b/api/file/download_info" && r.Method == "POST" {
						var body struct {
							ID json.Number `json:"fileId"`
						}
						json.NewDecoder(r.Body).Decode(&body)
						p := paths[string(body.ID)]
						if p == "" {
							t.Error("native download identity lost")
						}
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": "https://relay-cdn.example/content?path=" + url.QueryEscape(p)}})
						return
					}
					if kind == "baidu" && r.URL.Path == "/api/filemetas" && r.Method == "GET" {
						var ps []string
						json.Unmarshal([]byte(r.URL.Query().Get("target")), &ps)
						if len(ps) != 1 {
							t.Error("download target shape")
							w.WriteHeader(400)
							return
						}
						p := ps[0]
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": p, "fs_id": json.Number(ids[p]), "size": len(f.src[p].data), "dlink": "https://d.pcs.baidu.com/content?path=" + url.QueryEscape(p)}}})
						return
					}
					sourceWrites++
					t.Error("unexpected source operation", r.Method, r.URL.Path)
					w.WriteHeader(405)
				}))
				original := http.DefaultTransport
				tr := srv.Client().Transport.(*http.Transport).Clone()
				tr.Proxy = nil
				tr.TLSClientConfig.ServerName = srv.Certificate().DNSNames[0]
				tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
				}
				http.DefaultTransport = tr
				defer func() { m.Close(); http.DefaultTransport = original; tr.CloseIdleConnections(); srv.Close() }()
				if scenario == "ack-lost" || scenario == "read-failed" {
					f.mode = scenario
				}
				n, e := m.QueueRelayDirectory(context.Background(), in)
				rejected := scenario == "missing-md5" || scenario == "missing-type" || scenario == "missing-size" || scenario == "missing-id" || scenario == "missing-list" || scenario == "duplicate" || scenario == "wrong-parent" || scenario == "partial-page" || scenario == "stop-preflight" || scenario == "duplicate-object"
				if rejected {
					if e == nil || n != 0 || len(m.RelayJobs()) != 0 {
						t.Fatal("incomplete native manifest accepted", e)
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.creates != 0 || f.puts != 0 {
						t.Fatal("preflight wrote target")
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				jobs := awaitRelay(t, m)
				success := scenario == "success" || scenario == "empty"
				if success {
					for _, j := range jobs {
						if j.State != "verified" {
							t.Fatal(j.State, j.Message)
						}
					}
					f.mu.Lock()
					for p, src := range f.src {
						if p == "/" {
							continue
						}
						dst, ok := f.dst[p]
						if !ok || dst.folder != src.folder || string(dst.data) != string(src.data) {
							t.Error("native tree mismatch", p)
						}
					}
					f.mu.Unlock()
				} else {
					all := true
					for _, j := range jobs {
						all = all && j.State == "verified"
					}
					if all {
						t.Fatal("changed or unknown native tree confirmed")
					}
					if scenario != "after-readback" {
						f.mu.Lock()
						if f.puts != 0 {
							t.Error("file uploaded after unconfirmed ancestor")
						}
						f.mu.Unlock()
					}
				}
				if sourceWrites != 0 {
					t.Fatal("source mutated")
				}
				statePath := m.path
				m.Close()
				restored, e := NewManager(statePath, nil)
				if e != nil {
					t.Fatal("native manifest restore", e)
				}
				defer restored.Close()
				if success {
					for _, j := range restored.RelayJobs() {
						if j.State != "verified" {
							t.Fatal("confirmed native receipt lost")
						}
					}
				}
				if scenario == "ack-lost" || scenario == "read-failed" {
					f.mu.Lock()
					writes := f.creates
					f.mode = "success"
					f.mu.Unlock()
					if e = restored.CheckRelay(jobs[0].ID); e != nil {
						t.Fatal(e)
					}
					checked := awaitRelay(t, restored)
					want := "unknown"
					if scenario == "read-failed" {
						want = "verified"
					}
					if checked[0].State != want {
						t.Fatal("native directory ack ignored", checked[0].State)
					}
					f.mu.Lock()
					if f.creates != writes || f.puts != 0 {
						t.Error("read-only native check wrote target")
					}
					f.mu.Unlock()
				}
				raw, _ := os.ReadFile(statePath)
				for _, secret := range []string{a.Secret, "target-private"} {
					if strings.Contains(string(raw), secret) {
						t.Fatal("plaintext native manifest")
					}
				}
			})
		}
	}
}
