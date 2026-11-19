package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
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

func TestRelayNative123Target(t *testing.T) {
	for _, scenario := range []string{"rapid", "signed", "empty", "multipart", "from-123", "from-baidu", "from-quark", "from-guangya", "lost-completion", "target-read-failed", "corrupt-readback", "conflict", "race-rename", "unknown-pending", "checkpoint-save-failed", "final-save-failed"} {
		t.Run(scenario, func(t *testing.T) {
			const targetParent = "9007199254740993124"
			const targetID = "9223372036854775000"
			const sourceID = "9007199254740993123"
			mode := scenario
			data := []byte("relay-to-123\x00原件")
			if mode == "empty" {
				data = []byte{}
			}
			if mode == "multipart" {
				data = bytes.Repeat([]byte("p"), (1<<20)+9)
			}
			sum := md5.Sum(data)
			digest := hex.EncodeToString(sum[:])
			var mu sync.Mutex
			sourceLists, uploads, parts, completes := 0, 0, 0, 0
			var sent []byte
			exists := mode == "conflict"
			renamed := false
			badPath := t.TempDir()
			var m *Manager
			info := func(name string, parent string, id string) map[string]any {
				return map[string]any{"FileId": json.Number(id), "ParentFileId": json.Number(parent), "FileName": name, "Type": 0, "Size": len(data), "Etag": digest, "S3KeyFlag": "fixture"}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == "api.guangyapan.com" {
					if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer source-private" || r.Header.Get("Did") != "fixture-device" || r.Header.Get("Cookie") != "" {
						t.Error("Guangya source auth/method")
					}
					var result any
					switch r.URL.Path {
					case "/userres/v1/file/get_file_list":
						result = map[string]any{"total": 1, "list": []any{map[string]any{"fileId": "gy-original", "parentId": "", "fileName": "原件.txt", "fileSize": len(data), "resType": 1}}}
					case "/userres/v1/get_res_download_url":
						result = map[string]any{"signedURL": "https://relay-read.example/source"}
					default:
						t.Error("Guangya source mutation", r.URL.Path)
						w.WriteHeader(405)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": result})
					return
				}
				if r.Host == "drive.quark.cn" {
					if !strings.Contains(r.Header.Get("Cookie"), "__puus=source-private") || r.Header.Get("Authorization") != "" {
						t.Error("Quark source auth")
					}
					var out any
					switch r.URL.Path {
					case "/1/clouddrive/file/sort":
						out = map[string]any{"list": []any{map[string]any{"fid": "q-original", "pdir_fid": "0", "file_name": "原件.txt", "file_type": 1, "size": len(data), "updated_at": 123}}}
					case "/1/clouddrive/file/download":
						out = []any{map[string]any{"fid": "q-original", "file_name": "原件.txt", "size": len(data), "download_url": "https://relay-read.example/source", "md5": "advisory-wrong"}}
					default:
						t.Error("Quark source mutation", r.URL.Path)
						w.WriteHeader(405)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": out})
					return
				}
				if r.Host == "pan.baidu.com" {
					if r.Method != "GET" || !strings.Contains(r.Header.Get("Cookie"), "BDUSS=source-private") {
						t.Error("Baidu source mutation/auth")
					}
					if r.URL.Path == "/api/list" {
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{map[string]any{"path": "/原件.txt", "server_filename": "原件.txt", "fs_id": json.Number(sourceID), "isdir": 0, "size": len(data), "md5": digest}}})
						return
					}
					if r.URL.Path == "/api/filemetas" {
						json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"path": "/原件.txt", "fs_id": json.Number(sourceID), "size": len(data), "dlink": "https://relay-read.example/source"}}})
						return
					}
					t.Error("Baidu source unexpected API", r.URL.Path)
					w.WriteHeader(405)
					return
				}
				if r.Host == "source.fixture" {
					user, pass, _ := r.BasicAuth()
					if user != "source-user" || pass != "source-secret" {
						t.Error("source auth")
					}
					if r.Method == "PROPFIND" {
						sourceLists++
						if mode == "checkpoint-save-failed" && sourceLists == 3 {
							m.mu.Lock()
							m.path = badPath
							m.mu.Unlock()
						}
						w.WriteHeader(207)
						fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/base/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response><d:response><d:href>/base/原件.txt</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:getcontentlength>%d</d:getcontentlength><d:getetag>&quot;v1&quot;</d:getetag></d:prop></d:propstat></d:response></d:multistatus>`, len(data))
						return
					}
					if r.Method != "GET" || r.Header.Get("If-Match") != `"v1"` {
						t.Error("source mutation or missing condition", r.Method)
						w.WriteHeader(405)
						return
					}
					w.Header().Set("ETag", `"v1"`)
					w.Write(data)
					return
				}
				if r.Host == "relay-storage.example" {
					if r.Method != "PUT" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "signature=keep%2Bvalue" {
						t.Error("storage leaked auth/changed signed URL")
					}
					body, _ := io.ReadAll(r.Body)
					sent = append(sent, body...)
					parts++
					w.Header().Set("ETag", `"part"`)
					w.WriteHeader(200)
					return
				}
				if r.Host == "relay-read.example" {
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("credentials leaked to readback")
					}
					if r.URL.Path == "/target" && mode == "target-read-failed" {
						w.WriteHeader(503)
						return
					}
					body := data
					if r.URL.Path == "/target" && mode == "corrupt-readback" {
						body = bytes.Repeat([]byte("x"), len(data))
					}
					w.Write(body)
					return
				}
				if r.Host != "api.123278.com" {
					t.Error("foreign host", r.Host)
					w.WriteHeader(400)
					return
				}
				source := r.Header.Get("Authorization") == "Bearer source-private"
				if !source && r.Header.Get("Authorization") != "Bearer target-private" {
					t.Error("API credentials mixed")
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("API cookie leaked")
				}
				var payload map[string]any
				if r.Body != nil {
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					decoder.Decode(&payload)
				}
				var result any = map[string]any{}
				if source {
					switch r.URL.Path {
					case "/b/api/file/list/new":
						result = map[string]any{"Next": -1, "Total": 1, "InfoList": []any{info("原件.txt", "0", sourceID)}}
					case "/b/api/file/download_info":
						result = map[string]any{"DownloadUrl": "https://relay-read.example/source"}
					default:
						t.Error("source API mutation", r.URL.Path)
						w.WriteHeader(405)
						return
					}
				} else {
					switch r.URL.Path {
					case "/b/api/file/list/new":
						if r.URL.Query().Get("parentFileId") != targetParent {
							t.Error("target parent")
						}
						rows := []any{}
						if exists {
							rows = append(rows, info("原件.txt", targetParent, targetID))
						}
						if renamed {
							rows = append(rows, info("原件 (1).txt", targetParent, "77"))
						}
						result = map[string]any{"Next": -1, "Total": len(rows), "InfoList": rows}
					case "/b/api/file/upload_request":
						uploads++
						if payload["duplicate"] != json.Number("1") || payload["parentFileId"] != json.Number(targetParent) || payload["etag"] != digest || payload["fileName"] != "原件.txt" {
							t.Error("unsafe native preflight", payload)
						}
						rapid := mode == "rapid" || mode == "race-rename"
						pre := map[string]any{"FileId": json.Number(targetID), "Reuse": rapid, "Bucket": "fixture-bucket", "Key": "object", "UploadId": "allocation", "StorageNode": "fixture", "SliceSize": 1 << 20}
						if rapid {
							if mode == "race-rename" {
								renamed = true
								pre["Info"] = info("原件 (1).txt", targetParent, "77")
							} else {
								exists = true
								pre["Info"] = info("原件.txt", targetParent, targetID)
							}
						}
						result = pre
					case "/b/api/file/s3_upload_object/auth", "/b/api/file/s3_repare_upload_parts_batch":
						part, _ := strconv.Atoi(string(payload["partNumberStart"].(json.Number)))
						result = map[string]any{"presignedUrls": map[string]string{strconv.Itoa(part): "https://relay-storage.example/part/" + strconv.Itoa(part) + "?signature=keep%2Bvalue"}}
					case "/b/api/file/upload_complete/v2":
						completes++
						if payload["fileId"] != json.Number(targetID) || !bytes.Equal(sent, data) {
							t.Error("completed wrong bytes/identity")
						}
						if mode != "unknown-pending" {
							exists = true
						}
						if mode == "final-save-failed" {
							m.mu.Lock()
							m.path = badPath
							m.mu.Unlock()
						}
						if mode == "lost-completion" || mode == "unknown-pending" {
							w.WriteHeader(503)
							return
						}
						result = map[string]any{"file_info": info("原件.txt", targetParent, targetID)}
					case "/b/api/file/download_info":
						if payload["fileId"] != json.Number(targetID) {
							t.Error("wrong target readback ID")
						}
						result = map[string]any{"DownloadUrl": "https://relay-read.example/target"}
					default:
						t.Error("unexpected target endpoint", r.Method, r.URL.Path)
						w.WriteHeader(405)
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": result})
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
			source := Account{ID: "s", Name: "DAV来源", Kind: "webdav", Endpoint: "https://source.fixture/base", Username: "source-user", Secret: "source-secret"}
			in := RelayInput{SourceAccountID: "s", ParentID: "/", EntryID: "/原件.txt", Name: "原件.txt", Size: int64(len(data)), TargetAccountID: "t", DestinationID: targetParent}
			if mode == "from-123" {
				source = Account{ID: "s", Name: "123来源", Kind: "pan123", Endpoint: Pan123API, Secret: "source-private"}
				in.ParentID = "0"
				in.EntryID = sourceID
			}
			if mode == "from-baidu" {
				source = Account{ID: "s", Name: "百度来源", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=source-private"}
			}
			if mode == "from-quark" {
				source = Account{ID: "s", Name: "夸克来源", Kind: "quark", Endpoint: QuarkAPI, Secret: "__puus=source-private"}
				in.ParentID = "0"
				in.EntryID = "q-original"
				sum := sha256.Sum256(data)
				m.fingerprints = []UploadFingerprint{{AccountID: "s", EntryID: "q-original", Name: in.Name, Size: in.Size, Revision: "123", SHA256: hex.EncodeToString(sum[:])}}
			}
			if mode == "from-guangya" {
				source = Account{ID: "s", Name: "光鸭来源", Kind: "guangya", Endpoint: GuangyaAPI, Username: "fixture-device", Secret: "source-private"}
				in.ParentID, in.EntryID = guangyaRoot, "gy-original"
				sum := sha256.Sum256(data)
				m.fingerprints = []UploadFingerprint{{AccountID: "s", EntryID: in.EntryID, Name: in.Name, Size: in.Size, SHA256: hex.EncodeToString(sum[:])}}
			}
			m.accounts = []Account{source, {ID: "t", Name: "123目标", Kind: "pan123", Endpoint: Pan123API, Secret: "target-private"}}
			n, e := m.QueueRelays(context.Background(), []RelayInput{in})
			if mode == "conflict" {
				if e == nil || n != 0 || uploads != 0 || parts != 0 {
					t.Fatal("conflict wrote target", n, e)
				}
				return
			}
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			j := awaitRelay(t, m)[0]
			want := "verified"
			switch mode {
			case "target-read-failed", "corrupt-readback", "race-rename", "unknown-pending", "final-save-failed":
				want = "unknown"
			case "checkpoint-save-failed":
				want = "failed"
			}
			if j.State != want {
				t.Fatalf("%s expected %s: %s", j.State, want, j.Message)
			}
			mu.Lock()
			beforeUploads, beforeParts, beforeComplete := uploads, parts, completes
			mu.Unlock()
			if want == "failed" {
				if beforeUploads != 0 || beforeParts != 0 {
					t.Fatal("checkpoint failure uploaded")
				}
				m.mu.Lock()
				m.path = statePath
				m.mu.Unlock()
				return
			}
			if beforeUploads != 1 {
				t.Fatal("upload request repeated", beforeUploads)
			}
			if mode == "multipart" && beforeParts != 2 {
				t.Fatal("multipart", beforeParts)
			}
			if mode == "rapid" && (beforeParts != 0 || beforeComplete != 0) {
				t.Fatal("rapid sent file bytes")
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
				t.Fatal("lost native target record")
			}
			if want == "unknown" {
				got := restored.RelayJobs()[0]
				if e = restored.ResumeRelay(got.ID); e == nil {
					t.Fatal("replayed unknown")
				}
				if e = restored.ClearRelay(got.ID); e == nil {
					t.Fatal("cleared unknown")
				}
				mu.Lock()
				mode = "signed"
				exists = true
				renamed = false
				mu.Unlock()
				if e = restored.CheckRelay(got.ID); e != nil {
					t.Fatal(e)
				}
				if checked := awaitRelay(t, restored)[0]; checked.State != "verified" {
					t.Fatal(checked)
				}
				mu.Lock()
				if uploads != beforeUploads || parts != beforeParts || completes != beforeComplete {
					t.Error("read-only check wrote", uploads, parts, completes)
				}
				mu.Unlock()
			}
			saved, e := readState(statePath)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(saved.Relays)
			for _, forbidden := range []string{"target-private", "source-private", "signature=", "allocation", "fixture-bucket"} {
				if strings.Contains(string(raw), forbidden) {
					t.Error("upload session persisted in relay", forbidden)
				}
			}
		})
	}
}
