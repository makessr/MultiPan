package transfer

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestRelayNativeBaiduTarget(t *testing.T) {
	for _, scenario := range []string{"single", "multipart", "rapid", "opaque-md5", "uppercase-md5", "from-baidu", "from-123", "conflict", "empty-rejected", "empty-mixed", "create-ack-lost", "pending", "read-failed", "corrupt-body", "target-replaced", "bad-part", "final-save-failed"} {
		t.Run(scenario, func(t *testing.T) {
			const fsid = "9007199254740993123"
			const sourceID = "9007199254740993124"
			const targetPath = "/目标/原件.txt"
			mode := scenario
			data := []byte("relay-baidu-target\x00原件")
			if mode == "multipart" {
				data = bytes.Repeat([]byte("m"), int(baiduChunk)+7)
			}
			if mode == "empty-rejected" {
				data = []byte{}
			}
			digest := md5.Sum(data)
			hash := hex.EncodeToString(digest[:])
			var mu sync.Mutex
			preCreates, partWrites, creates, targetLists := 0, 0, 0, 0
			exists := mode == "conflict"
			var sent []byte
			badPath := t.TempDir()
			var m *Manager
			targetInfo := func() map[string]any {
				v := hash
				if mode == "opaque-md5" {
					v = "opaque-provider-version"
				}
				if mode == "uppercase-md5" {
					v = strings.ToUpper(v)
				}
				id := fsid
				if mode == "target-replaced" && targetLists >= 6 {
					id = "999"
				}
				return map[string]any{"path": targetPath, "server_filename": "原件.txt", "fs_id": json.Number(id), "isdir": 0, "size": len(data), "md5": v}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "source.fixture" {
					user, pass, _ := r.BasicAuth()
					if user != "source" || pass != "source-secret" {
						t.Error("source auth")
					}
					if r.Method == "PROPFIND" {
						w.WriteHeader(207)
						fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/base/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response><d:response><d:href>/base/原件.txt</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:getcontentlength>%d</d:getcontentlength><d:getetag>&quot;v1&quot;</d:getetag></d:prop></d:propstat></d:response></d:multistatus>`, len(data))
						return
					}
					if r.Method != "GET" || r.Header.Get("If-Match") != `"v1"` {
						t.Error("source mutation/condition")
					}
					w.Header().Set("ETag", `"v1"`)
					w.Write(data)
					return
				}
				if r.Host == "api.123278.com" {
					if r.Header.Get("Authorization") != "Bearer source-private" || r.Header.Get("Cookie") != "" {
						t.Error("123 source credentials")
					}
					var out any
					switch r.URL.Path {
					case "/b/api/file/list/new":
						out = map[string]any{"Next": -1, "Total": 1, "InfoList": []any{map[string]any{"FileId": json.Number(sourceID), "ParentFileId": 0, "FileName": "原件.txt", "Type": 0, "Size": len(data), "Etag": hash, "S3KeyFlag": "fixture"}}}
					case "/b/api/file/download_info":
						out = map[string]any{"DownloadUrl": "https://source-content.example/original"}
					default:
						t.Error("unexpected 123 source mutation")
						w.WriteHeader(405)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": out})
					return
				}
				if r.Host == "source-content.example" {
					if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("source credentials leaked")
					}
					w.Write(data)
					return
				}
				if r.Host == "d.pcs.baidu.com" {
					if r.Method == "POST" {
						partWrites++
						if r.Header.Get("Cookie") != "BDUSS=target-private" || r.URL.Query().Get("bdstoken") != "" || r.URL.Query().Get("path") != targetPath {
							t.Error("part credentials/path")
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
						body, _ := io.ReadAll(part)
						if part.FormName() != "file" {
							t.Error("bad multipart")
						}
						sent = append(sent, body...)
						sum := md5.Sum(body)
						value := hex.EncodeToString(sum[:])
						if mode == "bad-part" {
							value = "wrong"
						}
						json.NewEncoder(w).Encode(map[string]any{"md5": value})
						return
					}
					if r.Method != "GET" || !strings.Contains(r.Header.Get("Cookie"), "BDUSS=target-private") {
						t.Error("readback authentication")
					}
					if mode == "read-failed" {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"error_code":9999}`)
						return
					}
					body := data
					if mode == "corrupt-body" {
						body = bytes.Repeat([]byte("x"), len(data))
					}
					w.Write(body)
					return
				}
				if r.Host != "pan.baidu.com" {
					t.Error("foreign host", r.Host)
					w.WriteHeader(400)
					return
				}
				source := strings.Contains(r.Header.Get("Cookie"), "BDUSS=source-private")
				if source {
					if r.Method != "GET" {
						t.Error("Baidu source write")
					}
					switch r.URL.Path {
					case "/api/list":
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/原件.txt", "server_filename": "原件.txt", "fs_id": json.Number(sourceID), "isdir": 0, "size": len(data), "md5": hash}}})
					case "/api/filemetas":
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": "/原件.txt", "fs_id": json.Number(sourceID), "size": len(data), "dlink": "https://source-content.example/original"}}})
					default:
						t.Error("source API", r.URL.Path)
						w.WriteHeader(405)
					}
					return
				}
				if r.Header.Get("Cookie") != "BDUSS=target-private; STOKEN=pan-only" || r.Header.Get("Authorization") != "" {
					t.Error("target API credentials")
				}
				switch r.URL.Path {
				case "/api/list":
					targetLists++
					if r.URL.Query().Get("dir") != "/目标" {
						t.Error("target folder")
					}
					rows := []any{}
					if exists {
						rows = append(rows, targetInfo())
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": rows})
				case "/api/gettemplatevariable":
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "fixture-bdstoken"}})
				case "/api/precreate":
					preCreates++
					r.ParseForm()
					if r.Method != "POST" || r.PostForm.Get("rtype") != "0" || r.PostForm.Get("path") != targetPath || r.PostForm.Get("size") != strconv.Itoa(len(data)) {
						t.Error("unsafe precreate")
					}
					var blocks []string
					json.Unmarshal([]byte(r.PostForm.Get("block_list")), &blocks)
					need := []int{}
					for i := range blocks {
						need = append(need, i)
					}
					returnType := 1
					if mode == "rapid" {
						returnType = 2
						exists = true
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "return_type": returnType, "uploadid": "private-allocation", "block_list": need})
				case "/api/create":
					creates++
					r.ParseForm()
					if r.Method != "POST" || r.PostForm.Get("rtype") != "0" || r.PostForm.Get("path") != targetPath || !bytes.Equal(sent, data) {
						t.Error("create before correct bytes")
					}
					if mode != "pending" {
						exists = true
					}
					if mode == "final-save-failed" {
						m.mu.Lock()
						m.path = badPath
						m.mu.Unlock()
					}
					if mode == "create-ack-lost" || mode == "pending" {
						w.WriteHeader(503)
						return
					}
					out := targetInfo()
					out["errno"] = 0
					json.NewEncoder(w).Encode(out)
				case "/api/filemetas":
					if !exists {
						json.NewEncoder(w).Encode(map[string]any{"errno": -9})
						return
					}
					info := targetInfo()
					if r.URL.Query().Get("dlink") == "1" {
						info["dlink"] = "https://d.pcs.baidu.com/target?signature=ephemeral"
					}
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{info}})
				default:
					t.Error("unexpected target mutation", r.Method, r.URL.Path)
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
			var e error
			m, e = NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			source := Account{ID: "s", Name: "DAV源", Kind: "webdav", Endpoint: "https://source.fixture/base", Username: "source", Secret: "source-secret"}
			in := RelayInput{SourceAccountID: "s", ParentID: "/", EntryID: "/原件.txt", Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "t", DestinationID: "/目标"}
			if mode == "from-baidu" {
				source = Account{ID: "s", Name: "百度源", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=source-private"}
			}
			if mode == "from-123" {
				source = Account{ID: "s", Name: "123源", Kind: "pan123", Endpoint: Pan123API, Secret: "source-private"}
				in.ParentID = "0"
				in.EntryID = sourceID
			}
			m.accounts = []Account{source, {ID: "t", Name: "百度目标", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=target-private; STOKEN=pan-only"}}
			inputs := []RelayInput{in}
			if mode == "empty-mixed" {
				zero := in
				zero.Name = "empty.txt"
				zero.EntryID = "/empty.txt"
				zero.Size = 0
				inputs = append(inputs, zero)
			}
			n, e := m.QueueRelays(context.Background(), inputs)
			if mode == "conflict" || mode == "empty-rejected" || mode == "empty-mixed" {
				if e == nil || n != 0 || preCreates != 0 || partWrites != 0 || creates != 0 {
					t.Fatal("preflight created target", n, e)
				}
				if strings.HasPrefix(mode, "empty-") && !strings.Contains(e.Error(), "空文件") {
					t.Fatal("missing empty-file reason", e)
				}
				return
			}
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			j := awaitRelay(t, m)[0]
			want := "verified"
			switch mode {
			case "pending", "read-failed", "corrupt-body", "target-replaced", "bad-part", "final-save-failed":
				want = "unknown"
			}
			if j.State != want {
				t.Fatalf("%s expected %s: %s", j.State, want, j.Message)
			}
			mu.Lock()
			beforePre, beforeParts, beforeCreate := preCreates, partWrites, creates
			mu.Unlock()
			if beforePre != 1 {
				t.Fatal("precreate repeated", beforePre)
			}
			if mode == "multipart" && beforeParts != 2 {
				t.Fatal("multipart", beforeParts)
			}
			if mode == "rapid" && (beforeParts != 0 || beforeCreate != 0) {
				t.Fatal("rapid uploaded parts")
			}
			m.mu.Lock()
			m.path = statePath
			m.mu.Unlock()
			m.Close()
			restored, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			if len(restored.RelayJobs()) != 1 {
				t.Fatal("lost target receipt")
			}
			if want == "unknown" {
				j = restored.RelayJobs()[0]
				if e = restored.ResumeRelay(j.ID); e == nil {
					t.Fatal("unknown replay")
				}
				if e = restored.ClearRelay(j.ID); e == nil {
					t.Fatal("unknown cleared")
				}
				mu.Lock()
				mode = "single"
				exists = true
				mu.Unlock()
				if e = restored.CheckRelay(j.ID); e != nil {
					t.Fatal(e)
				}
				if got := awaitRelay(t, restored)[0]; got.State != "verified" {
					t.Fatal(got)
				}
				mu.Lock()
				if preCreates != beforePre || partWrites != beforeParts || creates != beforeCreate {
					t.Error("read-only check wrote")
				}
				mu.Unlock()
			}
			saved, e := readState(statePath)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(saved.Relays)
			for _, value := range []string{"private-allocation", "signature=", "ephemeral", "BDUSS=", "pan-only", "fixture-bdstoken"} {
				if strings.Contains(string(raw), value) {
					t.Error("temporary credentials in receipt", value)
				}
			}
		})
	}
}
