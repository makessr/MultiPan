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
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

type baiduTreeNode struct {
	folder bool
	id     string
	data   []byte
}

func TestBaiduDirectoryIdentityRecordValidation(t *testing.T) {
	d := diskRelay{Folder: true, CreatedAck: true, Submitted: true, TargetFolderID: "1001", SourceKind: "webdav", TargetKind: "baidu", ETag: "directory-v1", Job: RelayJob{Folder: true}}
	if !validRelayTreeRecord(d) {
		t.Fatal("valid target folder proof rejected")
	}
	for _, value := range []string{"", "0", "01", "1e2", "-1"} {
		bad := d
		bad.TargetFolderID = value
		if validRelayTreeRecord(bad) {
			t.Fatal("invalid target directory identity accepted")
		}
	}
	bad := d
	bad.TargetKind = "webdav"
	if validRelayTreeRecord(bad) {
		t.Fatal("native identity attached to DAV")
	}
	bad = d
	bad.CreatedAck = false
	if validRelayTreeRecord(bad) {
		t.Fatal("target identity accepted without ack")
	}
	public, _ := json.Marshal(d.Job)
	if strings.Contains(string(public), "targetFolderId") {
		t.Fatal("private directory identity exposed")
	}
}

func TestRelayBaiduDirectoryTarget(t *testing.T) {
	for _, scenario := range []string{"upload", "rapid", "empty-tree", "from-123", "from-baidu", "empty-file", "invalid-name", "target-conflict", "conflict-race", "missing-id", "wrong-path", "wrong-type", "ack-lost", "session-failed", "checkpoint-failed", "ack-save-failed", "final-save-failed", "read-failed", "id-changed", "after-download", "after-readback", "stop-preflight", "baidu-overlap"} {
		t.Run(scenario, func(t *testing.T) {
			f, m, in := newDirectoryFixture(t, "success")
			statePath := m.path
			if scenario == "empty-tree" {
				f.src = map[string]treeEntry{"/": {folder: true}, "/tree": {folder: true}}
			}
			if scenario == "empty-file" {
				f.src["/tree/zero"] = treeEntry{data: []byte{}}
			}
			if scenario == "invalid-name" {
				f.src["/tree/bad:name"] = treeEntry{folder: true}
			}
			sourceKind := "webdav"
			if scenario == "from-123" {
				sourceKind = "pan123"
				m.accounts[0] = Account{ID: "s", Name: "123 source", Kind: sourceKind, Endpoint: Pan123API, Secret: "source-private"}
			}
			if scenario == "from-baidu" || scenario == "baidu-overlap" {
				sourceKind = "baidu"
				m.accounts[0] = Account{ID: "s", Name: "Baidu source", Kind: sourceKind, Endpoint: BaiduAPI, Secret: "BDUSS=source-private"}
			}
			m.accounts[1] = Account{ID: "t", Name: "Baidu target", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=target-private; STOKEN=pan-only"}
			in.DestinationID = "/dest"
			if scenario == "baidu-overlap" {
				in.DestinationID = "/"
			}
			ps := []string{}
			for p := range f.src {
				ps = append(ps, p)
			}
			sort.Strings(ps)
			ids, paths := map[string]string{}, map[string]string{}
			for i, p := range ps {
				ids[p] = fmt.Sprint(int64(9007199254740993000) + int64(i))
				paths[ids[p]] = p
			}
			ids["/"] = "0"
			paths["0"] = "/"
			if sourceKind == "pan123" {
				in.ParentID = "0"
				in.EntryID = ids["/tree"]
			}
			var mu sync.Mutex
			mode := scenario
			creates, precreates, parts, sourceGets := 0, 0, 0, 0
			targetRead := false
			sourceWrites := 0
			nodes := map[string]baiduTreeNode{"/dest": {folder: true, id: "800"}}
			uploads := map[string][]byte{}
			if scenario == "target-conflict" {
				nodes["/dest/tree"] = baiduTreeNode{folder: true, id: "801"}
			}
			info := func(p string, node baiduTreeNode) map[string]any {
				dir := 0
				if node.folder {
					dir = 1
				}
				id := node.id
				if p == "/dest/tree" && (mode == "id-changed" && creates > 0 || mode == "after-download" && sourceGets > 0 || mode == "after-readback" && targetRead) {
					id = "999"
				}
				sum := md5.Sum(node.data)
				return map[string]any{"path": p, "fs_id": json.Number(id), "server_filename": path.Base(p), "isdir": dir, "size": len(node.data), "md5": hex.EncodeToString(sum[:])}
			}
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/src/") {
					if r.Method == "GET" {
						mu.Lock()
						sourceGets++
						mu.Unlock()
					}
					f.serve(w, r)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "source-content.example" || r.Host == "d.pcs.baidu.com" && r.URL.Query().Get("source") != "" {
					sourceGets++
					if r.Header.Get("Authorization") != "" || sourceKind == "pan123" && r.Header.Get("Cookie") != "" {
						t.Error("source CDN credentials")
					}
					w.Write(f.src[r.URL.Query().Get("source")].data)
					return
				}
				isNativeSource := r.Host == "api.123278.com" || r.Host == "pan.baidu.com" && strings.Contains(r.Header.Get("Cookie"), "BDUSS=source-private")
				if isNativeSource {
					if sourceKind == "pan123" && r.Header.Get("Authorization") != "Bearer source-private" {
						t.Error("123 source auth")
					}
					listRoute := "/api/list"
					parent := r.URL.Query().Get("dir")
					if sourceKind == "pan123" {
						listRoute = "/b/api/file/list/new"
						parent = paths[r.URL.Query().Get("parentFileId")]
					}
					if r.Method == "GET" && r.URL.Path == listRoute {
						rows := []any{}
						for _, p := range ps {
							if p == "/" || path.Dir(p) != parent {
								continue
							}
							entry := f.src[p]
							dir := 0
							if entry.folder {
								dir = 1
							}
							sum := md5.Sum(entry.data)
							hash := hex.EncodeToString(sum[:])
							var row map[string]any
							if sourceKind == "pan123" {
								row = map[string]any{"FileId": json.Number(ids[p]), "ParentFileId": json.Number(ids[parent]), "FileName": path.Base(p), "Type": dir, "Size": len(entry.data), "Etag": hash, "S3KeyFlag": "fixture"}
							} else {
								row = map[string]any{"path": p, "fs_id": json.Number(ids[p]), "server_filename": path.Base(p), "isdir": dir, "size": len(entry.data), "md5": hash}
							}
							rows = append(rows, row)
						}
						if sourceKind == "pan123" {
							json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"Next": -1, "Total": len(rows), "InfoList": rows}})
						} else {
							json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows})
						}
						return
					}
					if sourceKind == "pan123" && r.Method == "POST" && r.URL.Path == "/b/api/file/download_info" {
						var body struct {
							ID json.Number `json:"fileId"`
						}
						json.NewDecoder(r.Body).Decode(&body)
						p := paths[string(body.ID)]
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": "https://source-content.example/file?source=" + url.QueryEscape(p)}})
						return
					}
					if sourceKind == "baidu" && r.Method == "GET" && r.URL.Path == "/api/filemetas" {
						var files []string
						json.Unmarshal([]byte(r.URL.Query().Get("target")), &files)
						if len(files) != 1 {
							w.WriteHeader(400)
							return
						}
						p := files[0]
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": p, "fs_id": json.Number(ids[p]), "size": len(f.src[p].data), "dlink": "https://d.pcs.baidu.com/file?source=" + url.QueryEscape(p)}}})
						return
					}
					sourceWrites++
					t.Error("unexpected source operation", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				if r.Host == "d.pcs.baidu.com" {
					if r.URL.Path == "/rest/2.0/pcs/superfile2" {
						parts++
						if r.Header.Get("Cookie") != "BDUSS=target-private" || r.Header.Get("Authorization") != "" {
							t.Error("part credentials")
						}
						reader, e := r.MultipartReader()
						if e != nil {
							t.Error(e)
							w.WriteHeader(400)
							return
						}
						part, e := reader.NextPart()
						if e != nil {
							t.Error(e)
							w.WriteHeader(400)
							return
						}
						data, _ := io.ReadAll(part)
						uploads[r.URL.Query().Get("path")] = append(uploads[r.URL.Query().Get("path")], data...)
						sum := md5.Sum(data)
						json.NewEncoder(w).Encode(map[string]any{"md5": hex.EncodeToString(sum[:])})
						return
					}
					if r.Header.Get("Cookie") != "BDUSS=target-private; STOKEN=pan-only" {
						t.Error("target readback credentials")
					}
					targetRead = true
					w.Write(nodes[r.URL.Query().Get("target")].data)
					return
				}
				if r.Host != "pan.baidu.com" || r.Header.Get("Cookie") != "BDUSS=target-private; STOKEN=pan-only" || r.Header.Get("Authorization") != "" {
					t.Error("target API origin/auth")
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/api/list":
					if mode == "stop-preflight" && creates == 0 {
						_ = m.PauseRelays()
					}
					if creates > 0 && mode == "read-failed" {
						w.WriteHeader(503)
						return
					}
					if creates > 0 && mode == "final-save-failed" {
						m.mu.Lock()
						m.path = f.badPath
						m.mu.Unlock()
					}
					parent := r.URL.Query().Get("dir")
					rows := []any{}
					for p, node := range nodes {
						if p != parent && path.Dir(p) == parent {
							rows = append(rows, info(p, node))
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows})
				case "/api/gettemplatevariable":
					if mode == "session-failed" {
						w.WriteHeader(503)
						return
					}
					if mode == "checkpoint-failed" {
						m.mu.Lock()
						m.path = f.badPath
						m.mu.Unlock()
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "fixture-bdstoken"}})
				case "/api/precreate":
					precreates++
					r.ParseForm()
					p := r.PostForm.Get("path")
					if r.Method != "POST" || r.PostForm.Get("rtype") != "0" {
						t.Error("unsafe precreate")
					}
					if mode == "rapid" {
						data := f.src[strings.TrimPrefix(p, "/dest")].data
						nodes[p] = baiduTreeNode{id: fmt.Sprint(2000 + precreates), data: data}
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "return_type": 2})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "return_type": 1, "uploadid": "private-upload-session", "block_list": []int{0}})
				case "/api/create":
					r.ParseForm()
					p := r.PostForm.Get("path")
					isDir := r.PostForm.Get("isdir") == "1"
					if r.Method != "POST" || r.PostForm.Get("rtype") != "0" || r.URL.Query().Get("bdstoken") != "fixture-bdstoken" {
						t.Error("unsafe create")
					}
					if isDir {
						creates++
						saved, e := readState(statePath)
						if e != nil {
							t.Error(e)
						}
						found := false
						for _, d := range saved.Relays {
							if d.Folder && path.Join(d.Input.DestinationID, d.Input.Name) == p && d.Submitted && !d.CreatedAck {
								found = true
							}
						}
						if !found {
							t.Error("directory write preceded durable checkpoint")
						}
						if mode == "conflict-race" {
							nodes[p] = baiduTreeNode{folder: true, id: "external"}
							json.NewEncoder(w).Encode(map[string]any{"errno": -8})
							return
						}
						if _, ok := nodes[p]; ok {
							t.Error("directory merged")
							json.NewEncoder(w).Encode(map[string]any{"errno": -8})
							return
						}
						nodes[p] = baiduTreeNode{folder: true, id: fmt.Sprint(1000 + creates)}
						if mode == "ack-save-failed" {
							m.mu.Lock()
							m.path = f.badPath
							m.mu.Unlock()
						}
						if mode == "ack-lost" {
							w.WriteHeader(503)
							return
						}
						out := info(p, nodes[p])
						// Replacement happens in subsequent listings, not in the
						// original creation acknowledgement itself.
						out["fs_id"] = json.Number(nodes[p].id)
						out["errno"] = 0
						if mode == "missing-id" {
							delete(out, "fs_id")
						}
						if mode == "wrong-path" {
							out["path"] = p + "-renamed"
						}
						if mode == "wrong-type" {
							out["isdir"] = 0
						}
						json.NewEncoder(w).Encode(out)
						return
					}
					nodes[p] = baiduTreeNode{id: fmt.Sprint(2000 + precreates), data: uploads[p]}
					out := info(p, nodes[p])
					out["errno"] = 0
					json.NewEncoder(w).Encode(out)
				case "/api/filemetas":
					var files []string
					json.Unmarshal([]byte(r.URL.Query().Get("target")), &files)
					if len(files) != 1 {
						t.Error("metadata target")
						w.WriteHeader(400)
						return
					}
					p := files[0]
					out := info(p, nodes[p])
					out["dlink"] = "https://d.pcs.baidu.com/file?target=" + url.QueryEscape(p)
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{out}})
				default:
					t.Error("unexpected target API", r.Method, r.URL.Path)
					w.WriteHeader(405)
				}
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
			n, e := m.QueueRelayDirectory(context.Background(), in)
			rejected := scenario == "empty-file" || scenario == "invalid-name" || scenario == "target-conflict" || scenario == "stop-preflight" || scenario == "baidu-overlap"
			if rejected {
				if e == nil || n != 0 || len(m.RelayJobs()) != 0 || creates != 0 || precreates != 0 {
					t.Fatal("preflight did not reject whole tree", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			jobs := awaitRelay(t, m)
			m.mu.Lock()
			m.path = statePath
			m.mu.Unlock()
			success := scenario == "upload" || scenario == "rapid" || scenario == "empty-tree" || scenario == "from-123" || scenario == "from-baidu"
			if success {
				for _, j := range jobs {
					if j.State != "verified" {
						t.Fatal(j.State, j.Message)
					}
				}
				mu.Lock()
				for p, src := range f.src {
					if p == "/" {
						continue
					}
					dst, ok := nodes["/dest"+p]
					if !ok || src.folder != dst.folder || string(src.data) != string(dst.data) {
						t.Error("Baidu tree mismatch", p)
					}
				}
				mu.Unlock()
			} else {
				all := true
				for _, j := range jobs {
					all = all && j.State == "verified"
				}
				if all {
					t.Fatal("unconfirmed tree accepted")
				}
				if scenario != "after-readback" && precreates != 0 {
					t.Fatal("child bypassed failed directory proof")
				}
			}
			if sourceWrites != 0 {
				t.Fatal("source mutated")
			}
			m.Close()
			restored, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal("restore Baidu tree", e)
			}
			defer restored.Close()
			if success {
				for _, j := range restored.RelayJobs() {
					if j.State != "verified" {
						t.Fatal("confirmed tree lost")
					}
				}
			}
			unknown := jobs[0].State == "unknown"
			if unknown {
				if e = restored.ResumeRelay(jobs[0].ID); e == nil {
					t.Fatal("unknown directory replayed")
				}
				mu.Lock()
				before := creates
				mode = "success"
				mu.Unlock()
				if e = restored.CheckRelay(jobs[0].ID); e != nil {
					t.Fatal(e)
				}
				checked := awaitRelay(t, restored)
				hasAck := scenario == "read-failed" || scenario == "final-save-failed" || scenario == "id-changed"
				want := "unknown"
				if hasAck {
					want = "verified"
				}
				if checked[0].State != want {
					t.Fatal("directory creation ID proof ignored", scenario, checked[0].State)
				}
				if creates != before || precreates != 0 {
					t.Fatal("directory read-only check wrote")
				}
				if hasAck {
					for step := 0; step < len(checked); step++ {
						progress := false
						for _, j := range restored.RelayJobs()[1:] {
							if j.CanResume {
								if e = restored.ResumeRelay(j.ID); e != nil {
									t.Fatal(e)
								}
								awaitRelay(t, restored)
								progress = true
							}
						}
						if !progress {
							break
						}
					}
					for _, j := range restored.RelayJobs() {
						if j.State != "verified" {
							t.Fatal("recovered Baidu parent did not release child safely", j.State, j.Message)
						}
					}
				}
			}
			raw, _ := os.ReadFile(statePath)
			for _, secret := range []string{"target-private", "pan-only", "fixture-bdstoken", "private-upload-session"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("private target credentials plaintext")
				}
			}
		})
	}
}
