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
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

type relay123TreeNode struct {
	parent, name string
	folder       bool
	data         []byte
}

func TestRelay123DirectoryPrivateIdentity(t *testing.T) {
	parent := diskRelay{Folder: true, CreatedAck: true, Submitted: true, TargetFolderID: "9007199254740993001", TargetKind: "pan123", SourceKind: "webdav", ETag: "directory-v1", Batch: "batch", SourceScope: "s", TargetScope: "t", Input: RelayInput{SourceAccountID: "s", TargetAccountID: "t", EntryID: "/tree", DestinationID: "77", Name: "tree"}, Job: RelayJob{ID: "parent", Folder: true, State: "verified"}}
	if !validRelayTreeRecord(parent) {
		t.Fatal("valid native folder rejected")
	}
	for _, id := range []string{"", "0", "01", "-1", "1e2"} {
		bad := parent
		bad.TargetFolderID = id
		if validRelayTreeRecord(bad) {
			t.Fatal("bad identity accepted", id)
		}
	}
	for _, mode := range []string{"file", "no-ack", "dav"} {
		bad := parent
		switch mode {
		case "file":
			bad.Folder = false
			bad.Job.Folder = false
		case "no-ack":
			bad.CreatedAck = false
		case "dav":
			bad.TargetKind = "webdav"
		}
		if validRelayTreeRecord(bad) {
			t.Fatal("invalid identity attachment", mode)
		}
	}
	child := diskRelay{ParentJobID: "parent", TargetKind: "pan123", SourceKind: "webdav", Batch: "batch", SourceScope: "s", TargetScope: "t", Input: RelayInput{SourceAccountID: "s", TargetAccountID: "t", ParentID: "/tree", EntryID: "/tree/file", DestinationID: "/77/tree", Name: "file"}, Job: RelayJob{ID: "child"}}
	if err := validateRelayTreeParents([]diskRelay{parent, child}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{relays: []diskRelay{parent, child}}
	d, err := m.resolveRelayTarget(child)
	if err != nil || relayTargetParent(d) != parent.TargetFolderID {
		t.Fatal(d, err)
	}
	if child.Input.DestinationID != "/77/tree" || m.relays[1].targetParent != "" {
		t.Fatal("planned destination mutated")
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "targetParent") {
		t.Fatal("runtime parent persisted")
	}
	b, _ = json.Marshal(parent.Job)
	if strings.Contains(string(b), "targetFolderId") {
		t.Fatal("private ID exposed")
	}
	for _, mode := range []string{"wrong-path", "wrong-kind", "missing-parent", "parent-unknown"} {
		p, c := parent, child
		switch mode {
		case "wrong-path":
			c.Input.DestinationID = "/77/other"
		case "wrong-kind":
			c.TargetKind = "baidu"
		case "missing-parent":
			c.ParentJobID = "other"
		case "parent-unknown":
			p.Job.State = "unknown"
		}
		if mode != "parent-unknown" {
			if validateRelayTreeParents([]diskRelay{p, c}) == nil {
				t.Fatal("invalid dependency accepted", mode)
			}
		}
		m.relays = []diskRelay{p, c}
		if mode != "wrong-kind" {
			if _, err := m.resolveRelayTarget(c); err == nil {
				t.Fatal("unconfirmed parent resolved", mode)
			}
		}
	}
}

func TestRelay123DirectoryTarget(t *testing.T) {
	for _, mode := range []string{"rapid", "signed", "from-123", "from-baidu", "123-overlap", "empty-file", "empty-tree", "info-response", "missing-id", "wrong-info", "ack-lost", "ack-save-failed", "id-changed", "after-download", "after-readback", "read-failed", "target-conflict", "conflict-race", "stop-preflight", "checkpoint-failed", "final-save-failed"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newDirectoryFixture(t, "success")
			statePath := m.path
			if mode == "empty-tree" {
				f.src = map[string]treeEntry{"/": {folder: true}, "/tree": {folder: true}}
			}
			if mode == "empty-file" {
				f.src["/tree/zero"] = treeEntry{data: []byte{}}
			}
			m.accounts[1] = Account{ID: "t", Name: "123 target", Kind: "pan123", Endpoint: Pan123API, Secret: "target-private"}
			in.DestinationID = "77"
			sourceKind := "webdav"
			ids, paths := map[string]string{"/": "0"}, map[string]string{"0": "/"}
			ps := []string{}
			for p := range f.src {
				if p != "/" {
					ps = append(ps, p)
				}
			}
			sort.Strings(ps)
			for i, p := range ps {
				id := fmt.Sprint(1000 + i)
				ids[p] = id
				paths[id] = p
			}
			if mode == "from-123" || mode == "123-overlap" {
				sourceKind = "pan123"
				m.accounts[0] = Account{ID: "s", Kind: sourceKind, Endpoint: Pan123API, Secret: "source-private"}
				in.ParentID = "0"
				in.EntryID = ids["/tree"]
			}
			if mode == "from-baidu" {
				sourceKind = "baidu"
				m.accounts[0] = Account{ID: "s", Kind: sourceKind, Endpoint: BaiduAPI, Secret: "BDUSS=source-private"}
			}
			var mu sync.Mutex
			nodes := map[string]relay123TreeNode{"77": {name: "destination", folder: true}}
			if mode == "123-overlap" {
				in.DestinationID = ids["/tree"]
				nodes[in.DestinationID] = relay123TreeNode{name: "overlap", folder: true}
			}
			if mode == "target-conflict" {
				nodes["88"] = relay123TreeNode{parent: "77", name: "tree", folder: true}
			}
			creates, uploads, sourceGets, reads := 0, 0, 0, 0
			targetRead := false
			pendingID := ""
			var uploaded []byte
			var nodePath func(string) string
			nodePath = func(id string) string {
				if id == "77" {
					return "/"
				}
				n := nodes[id]
				return path.Join(nodePath(n.parent), n.name)
			}
			info := func(id string, n relay123TreeNode) map[string]any {
				typ := 0
				if n.folder {
					typ = 1
				}
				sum := md5.Sum(n.data)
				if n.parent == "77" && n.name == "tree" && (mode == "id-changed" && creates > 0 || mode == "after-download" && sourceGets > 0 || mode == "after-readback" && targetRead) {
					id = "999999"
				}
				return map[string]any{"FileId": json.Number(id), "ParentFileId": json.Number(n.parent), "FileName": n.name, "Type": typ, "Size": len(n.data), "Etag": hex.EncodeToString(sum[:]), "S3KeyFlag": "fixture"}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/src/") {
					mu.Lock()
					if r.Method == "GET" {
						sourceGets++
					}
					mu.Unlock()
					f.serve(w, r)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "source-content.example" || r.Host == "d.pcs.baidu.com" {
					sourceGets++
					w.Write(f.src[r.URL.Query().Get("source")].data)
					return
				}
				if r.Header.Get("Authorization") == "Bearer source-private" || strings.Contains(r.Header.Get("Cookie"), "BDUSS=source-private") {
					parent := r.URL.Query().Get("dir")
					listRoute := "/api/list"
					if sourceKind == "pan123" {
						parent = paths[r.URL.Query().Get("parentFileId")]
						listRoute = "/b/api/file/list/new"
					}
					if r.Method == "GET" && r.URL.Path == listRoute {
						rows := []any{}
						for _, p := range ps {
							if path.Dir(p) != parent {
								continue
							}
							src := f.src[p]
							typ := 0
							if src.folder {
								typ = 1
							}
							sum := md5.Sum(src.data)
							digest := hex.EncodeToString(sum[:])
							var row any
							if sourceKind == "pan123" {
								row = map[string]any{"FileId": json.Number(ids[p]), "ParentFileId": json.Number(ids[parent]), "FileName": path.Base(p), "Type": typ, "Size": len(src.data), "Etag": digest, "S3KeyFlag": "fixture"}
							} else {
								row = map[string]any{"fs_id": json.Number(ids[p]), "path": p, "server_filename": path.Base(p), "isdir": typ, "size": len(src.data), "md5": digest}
							}
							rows = append(rows, row)
						}
						if sourceKind == "pan123" {
							json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": len(rows), "Next": -1}})
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
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": "https://source-content.example/file?source=" + url.QueryEscape(paths[string(body.ID)])}})
						return
					}
					if sourceKind == "baidu" && r.Method == "GET" && r.URL.Path == "/api/filemetas" {
						var names []string
						json.Unmarshal([]byte(r.URL.Query().Get("target")), &names)
						if len(names) != 1 {
							t.Error("wrong source metadata request")
							w.WriteHeader(400)
							return
						}
						p := names[0]
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": p, "fs_id": json.Number(ids[p]), "size": len(f.src[p].data), "dlink": "https://d.pcs.baidu.com/file?source=" + url.QueryEscape(p)}}})
						return
					}
					t.Error("unexpected source API", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				if r.Host == "relay-read.example" {
					if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("CDN credential/method")
					}
					targetRead = true
					w.Write(nodes[r.URL.Query().Get("id")].data)
					return
				}
				if r.Host == "relay-storage.example" {
					uploaded, _ = io.ReadAll(r.Body)
					if r.Method != "PUT" || r.Header.Get("Authorization") != "" {
						t.Error("storage request")
					}
					w.Header().Set("ETag", `"part"`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer target-private" {
					t.Error("target auth")
				}
				if r.Method == "GET" && r.URL.Path == "/b/api/file/list/new" {
					reads++
					if mode == "stop-preflight" && reads == 1 {
						_ = m.PauseRelays()
					}
					if mode == "checkpoint-failed" && reads == 2 {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "read-failed" && creates > 0 {
						w.WriteHeader(503)
						return
					}
					if mode == "final-save-failed" && creates > 0 {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					parent := r.URL.Query().Get("parentFileId")
					if !valid123Folder(parent) {
						t.Error("logical path used as wire parent", parent)
					}
					rows := []any{}
					ids := []string{}
					for id := range nodes {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					for _, id := range ids {
						n := nodes[id]
						if n.parent == parent {
							rows = append(rows, info(id, n))
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": len(rows), "Next": -1}})
					return
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var out any = map[string]any{}
				switch r.URL.Path {
				case "/b/api/file/upload_request":
					var name string
					json.Unmarshal(body["fileName"], &name)
					parent := strings.Trim(string(body["parentFileId"]), "\"")
					folder := string(body["type"]) == "1"
					if !valid123Folder(parent) || string(body["duplicate"]) != "1" || !nodes[parent].folder {
						t.Error("wrong parent or overwrite policy", parent, string(body["duplicate"]))
					}
					id := fmt.Sprint(int64(9007199254740993000) + int64(len(nodes)))
					if folder {
						creates++
						m.mu.Lock()
						checkpoint := false
						for _, d := range m.relays {
							if d.Folder && d.Input.Name == name && d.Submitted {
								checkpoint = true
							}
						}
						m.mu.Unlock()
						if !checkpoint {
							t.Error("create without saved checkpoint")
						}
						if mode == "conflict-race" {
							name += " (1)"
						}
						n := relay123TreeNode{parent: parent, name: name, folder: true}
						nodes[id] = n
						out = map[string]any{"FileId": json.Number(id)}
						if mode == "info-response" || mode == "wrong-info" {
							r := info(id, n)
							if mode == "wrong-info" {
								r["ParentFileId"] = json.Number("999")
							}
							out = map[string]any{"Info": r}
						}
						if mode == "missing-id" {
							out = map[string]any{}
						}
						if mode == "ack-save-failed" {
							m.mu.Lock()
							m.path = t.TempDir()
							m.mu.Unlock()
						}
						if mode == "ack-lost" {
							w.WriteHeader(503)
							return
						}
					} else {
						uploads++
						data := f.src[path.Join(nodePath(parent), name)].data
						n := relay123TreeNode{parent: parent, name: name, data: data}
						nodes[id] = n
						if mode == "signed" {
							pendingID = id
							out = map[string]any{"FileId": json.Number(id), "Reuse": false, "Bucket": "bucket", "Key": "key", "UploadId": "upload", "StorageNode": "node", "SliceSize": 1048576}
						} else {
							out = map[string]any{"FileId": json.Number(id), "Reuse": true, "Info": info(id, n)}
						}
					}
				case "/b/api/file/s3_repare_upload_parts_batch", "/b/api/file/s3_upload_object/auth":
					out = map[string]any{"presignedUrls": map[string]string{"1": "https://relay-storage.example/part?signature=fixture"}}
				case "/b/api/file/upload_complete", "/b/api/file/upload_complete/v2":
					n := nodes[pendingID]
					n.data = uploaded
					nodes[pendingID] = n
					out = map[string]any{"file_info": info(pendingID, n)}
				case "/b/api/file/download_info":
					id := strings.Trim(string(body["fileId"]), "\"")
					out = map[string]any{"DownloadUrl": "https://relay-read.example/file?id=" + id}
				default:
					t.Error("unexpected target API", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": out})
			}))
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			old := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { m.Close(); http.DefaultTransport = old; tr.CloseIdleConnections(); server.Close() }()
			n, err := m.QueueRelayDirectory(context.Background(), in)
			if mode == "target-conflict" || mode == "stop-preflight" || mode == "123-overlap" {
				if err == nil || n != 0 || creates != 0 || uploads != 0 {
					t.Fatal("preflight wrote", n, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			jobs := awaitRelay(t, m)
			m.mu.Lock()
			m.path = statePath
			m.mu.Unlock()
			success := mode == "rapid" || mode == "signed" || mode == "from-123" || mode == "from-baidu" || mode == "empty-file" || mode == "empty-tree" || mode == "info-response"
			if success {
				for _, j := range jobs {
					if j.State != "verified" {
						t.Fatal(j.Name, j.State, j.Message)
					}
				}
				mu.Lock()
				for p, src := range f.src {
					if p == "/" {
						continue
					}
					found := false
					for id, dst := range nodes {
						if id != "77" && nodePath(id) == p && src.folder == dst.folder && string(src.data) == string(dst.data) {
							found = true
						}
					}
					if !found {
						t.Error("missing tree content", p)
					}
				}
				mu.Unlock()
			} else {
				if jobs[0].State == "verified" && mode != "after-download" && mode != "after-readback" {
					t.Fatal("unconfirmed directory accepted", jobs[0])
				}
				if mode != "after-readback" && mode != "after-download" && uploads != 0 {
					t.Fatal("child bypassed failed parent")
				}
			}
			if f.sourceWrites != 0 {
				t.Fatal("source mutated")
			}
			m.Close()
			restored, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal("restore tree", err)
			}
			defer restored.Close()
			if success {
				for _, j := range restored.RelayJobs() {
					if j.State != "verified" {
						t.Fatal("lost confirmed directory")
					}
				}
			}
			if jobs[0].State == "unknown" {
				beforeCreates, beforeUploads := creates, uploads
				if err := restored.CheckRelay(jobs[0].ID); err != nil {
					t.Fatal(err)
				}
				awaitRelay(t, restored)
				if creates != beforeCreates || uploads != beforeUploads {
					t.Fatal("unknown replayed")
				}
			}
		})
	}
}
