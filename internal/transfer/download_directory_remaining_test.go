package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type remainingNode struct {
	id, name string
	folder   bool
}

func TestRemainingDirectoryProtocols(t *testing.T) {
	for _, kind := range []string{"guangya", "baidu", "lanzou"} {
		for _, mode := range []string{"normal", "empty-tree", "optional-parent", "cycle", "duplicate", "collision", "stale-root", "expired", "scan-fail", "changed-file", "corrupt", "cancel-scan", "cancel-transfer", "provider-special", "password"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				parentID, rootID, subID, emptyID, fileA, fileB, extra := "root", "R", "S", "E", "a", "b", "extra"
				endpoint := GuangyaAPI
				if kind == "baidu" {
					parentID, rootID, subID, emptyID, fileA, fileB, extra = "/", "/Root", "/Root/Sub", "/Root/Empty", "/Root/alpha.txt", "/Root/Sub/beta.txt", "/Root/ALPHA.TXT"
					endpoint = BaiduAPI
				}
				if kind == "lanzou" {
					parentID, rootID, subID, emptyID, fileA, fileB, extra = "-1", "1", "2", "3", "1", "2", "9"
					endpoint = "https://up.woozooo.com"
				}
				tree := map[string][]remainingNode{parentID: {{rootID, "Root", true}}, rootID: {{fileA, "alpha.txt", false}, {subID, "Sub", true}, {emptyID, "Empty", true}}, subID: {{fileB, "beta.txt", false}}, emptyID: {}}
				switch mode {
				case "empty-tree":
					tree[rootID] = nil
				case "cycle":
					tree[subID] = append(tree[subID], remainingNode{rootID, "Loop", true})
				case "duplicate":
					tree[subID] = append(tree[subID], remainingNode{fileA, "again.txt", false})
				case "collision":
					tree[rootID] = append(tree[rootID], remainingNode{extra, "ALPHA.TXT", false})
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				scanDone := false
				transfers := 0
				a := Account{ID: "account", Kind: kind, Endpoint: endpoint, Secret: "fixture-session", Username: "device"}
				nodes := func(id string) []remainingNode {
					rows := append([]remainingNode(nil), tree[id]...)
					if scanDone && mode == "changed-file" {
						for i := range rows {
							if rows[i].id == fileA && !rows[i].folder {
								rows[i].name = "changed.txt"
							}
						}
					}
					return rows
				}
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if e := r.Context().Err(); e != nil {
						return nil, e
					}
					official := r.URL.Host == "api.guangyapan.com" || r.URL.Host == "pan.baidu.com" || r.URL.Host == "up.woozooo.com"
					if official {
						if kind == "guangya" {
							if r.Header.Get("Authorization") != "Bearer fixture-session" || r.Header.Get("Did") != "device" {
								t.Error("wrong GY session")
							}
						} else if r.Header.Get("Cookie") == "" {
							t.Error("missing management session")
						}
						if mode == "expired" {
							res := quarkResponse("", nil)
							res.StatusCode = 401
							return res, nil
						}
						if kind == "guangya" {
							var body map[string]any
							json.NewDecoder(r.Body).Decode(&body)
							if r.URL.Path == "/userres/v1/file/get_file_list" {
								id, _ := body["parentId"].(string)
								if id == "" {
									id = parentID
								}
								if mode == "scan-fail" && id == subID {
									return quarkResponse(`{"code":500}`, nil), nil
								}
								rows := []any{}
								for _, n := range nodes(id) {
									typ := 1
									if n.folder {
										typ = 2
									}
									m := map[string]any{"fileId": n.id, "fileName": n.name, "resType": typ, "fileSize": 3}
									if mode != "optional-parent" {
										m["parentId"] = id
									}
									if mode == "provider-special" && n.id == fileA {
										m["parentId"] = "outside"
									}
									rows = append(rows, m)
								}
								return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"list": rows, "total": len(rows)}}), nil
							}
							part := "b"
							if body["fileId"] == fileA {
								part = "a"
							}
							return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"signedURL": "https://cdn.example/" + part}}), nil
						}
						if kind == "baidu" {
							switch r.URL.Path {
							case "/api/list":
								id := r.URL.Query().Get("dir")
								if mode == "scan-fail" && id == subID {
									res := quarkResponse("", nil)
									res.StatusCode = 500
									return res, nil
								}
								rows := []any{}
								for _, n := range nodes(id) {
									dir := 0
									if n.folder {
										dir = 1
									}
									fid := 1002
									if n.id == fileA {
										fid = 1001
									}
									rows = append(rows, map[string]any{"path": n.id, "server_filename": n.name, "isdir": dir, "fs_id": fid, "size": 3, "md5": "opaque"})
								}
								return jsonResponse(map[string]any{"errno": 0, "list": rows}), nil
							case "/api/filemetas":
								var targets []string
								json.Unmarshal([]byte(r.URL.Query().Get("target")), &targets)
								part := "b"
								fid := 1002
								if targets[0] == fileA {
									part = "a"
									fid = 1001
								}
								host := "https://cdn.example/"
								if mode == "provider-special" {
									host = "https://d.pcs.baidu.com/denied/"
								}
								return jsonResponse(map[string]any{"errno": 0, "info": []any{map[string]any{"path": targets[0], "fs_id": fid, "size": 3, "dlink": host + part}}}), nil
							case "/api/gettemplatevariable":
								return quarkResponse(`{"errno":0,"result":{"bdstoken":"fixture-token","sign1":"Plaintext","sign3":"Key","timestamp":12345}}`, nil), nil
							case "/api/download":
								part := "b"
								fid := 1002
								if r.URL.Query().Get("fidlist") == "[1001]" {
									part = "a"
									fid = 1001
								}
								return jsonResponse(map[string]any{"errno": 0, "dlink": []any{map[string]any{"fs_id": fid, "dlink": "https://cdn.example/" + part}}}), nil
							}
							t.Error("unexpected baidu route")
						}
						if kind == "lanzou" {
							r.ParseForm()
							task := r.Form.Get("task")
							id := r.Form.Get("folder_id")
							if mode == "scan-fail" && id == subID {
								res := quarkResponse("", nil)
								res.StatusCode = 500
								return res, nil
							}
							rows := []any{}
							switch task {
							case "47":
								for _, n := range nodes(id) {
									if n.folder {
										rows = append(rows, map[string]any{"fol_id": n.id, "name": n.name})
									}
								}
								return jsonResponse(map[string]any{"zt": 1, "text": rows}), nil
							case "5":
								for _, n := range nodes(id) {
									if !n.folder {
										rows = append(rows, map[string]any{"id": n.id, "name_all": n.name, "size": "3 B"})
									}
								}
								return jsonResponse(map[string]any{"zt": 1, "text": rows}), nil
							case "22":
								part := "b"
								if r.Form.Get("file_id") == fileA {
									part = "a"
								}
								fid := "i-" + part
								if mode == "provider-special" && part == "a" {
									fid = "i"
								}
								on := "0"
								if mode == "password" {
									on = "1"
								}
								return jsonResponse(map[string]any{"zt": 1, "info": map[string]any{"is_newd": "https://share.example", "f_id": fid, "onof": on, "pwd": "fixture-pass"}}), nil
							}
							t.Error("unexpected LZ task")
						}
					}
					if r.URL.Host == "d.pcs.baidu.com" {
						res := quarkResponse(`{"error_code":31329,"error_msg":"hit black userlist"}`, nil)
						res.StatusCode = 403
						return res, nil
					}
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Did") != "" {
						t.Error("credentials leaked to public node")
					}
					if r.URL.Host == "share.example" {
						if strings.HasPrefix(r.URL.Path, "/i-") {
							part := strings.TrimPrefix(r.URL.Path, "/i-")
							if mode == "password" {
								return quarkResponse(`<script>data:'action=downprocess&sign=abc&p='</script>`, nil), nil
							}
							return quarkResponse(`<iframe src="/fn?which=`+part+`"></iframe>`, nil), nil
						}
						if r.URL.Path == "/fn" {
							return quarkResponse(`data:{'sign':'abc',ves:1}`, nil), nil
						}
						if r.URL.Path == "/ajaxm.php" {
							r.ParseForm()
							if mode == "password" && r.Form.Get("p") != "fixture-pass" {
								t.Error("password not forwarded to public share")
							}
							ref, _ := url.Parse(r.Header.Get("Referer"))
							part := ref.Query().Get("which")
							if part == "" {
								part = strings.TrimPrefix(ref.Path, "/i-")
							}
							return jsonResponse(map[string]any{"zt": 1, "dom": "https://cdn.example", "url": part}), nil
						}
					}
					data := "bbb"
					if strings.HasSuffix(r.URL.Path, "/a") {
						data = "aaa"
						if mode == "corrupt" {
							data = "AAA"
						}
					}
					res := quarkResponse(data, nil)
					res.ContentLength = 3
					return res, nil
				})}
				sha := sha256.Sum256([]byte("aaa"))
				revision := ""
				if kind == "baidu" {
					revision = "1001:opaque"
				}
				originals := []UploadFingerprint{{AccountID: a.ID, EntryID: fileA, Name: "alpha.txt", Size: 3, Revision: revision, SHA256: hex.EncodeToString(sha[:])}}
				parent := t.TempDir()
				name := "Root"
				if mode == "stale-root" {
					name = "Changed"
				}
				in := DownloadInput{AccountID: a.ID, ParentID: parentID, EntryID: rootID, Name: name}
				download := func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
					scanDone = true
					transfers++
					if kind == "lanzou" && (strings.Contains(in.EntryID, ":") || strings.Contains(in.ParentID, ":")) {
						t.Error("LZ namespace prefix escaped planning")
					}
					return DownloadAccountFile(ctx, client, a, in, dest, func(progress DownloadProgress) {
						p(progress)
						if mode == "cancel-transfer" && progress.Received > 0 {
							cancel()
						}
					}, originals...)
				}
				update := func(event BatchDownloadEvent) {
					if mode == "cancel-scan" && strings.Contains(event.Name, "Sub") {
						cancel()
					}
				}
				var result BatchDownloadResult
				var e error
				switch kind {
				case "guangya":
					result, e = downloadGuangyaDirectory(ctx, client, a, in, parent, download, update)
				case "baidu":
					result, e = downloadBaiduDirectory(ctx, client, a, in, parent, download, update)
				case "lanzou":
					result, e = downloadLanzouDirectory(ctx, client, a, in, parent, download, update)
				}
				invalid := mode == "cycle" || mode == "duplicate" || mode == "collision" || mode == "stale-root" || mode == "expired" || mode == "scan-fail" || mode == "cancel-scan" || (mode == "provider-special" && kind == "guangya")
				if invalid {
					if e == nil || transfers != 0 {
						t.Fatal("invalid scan saved files", e)
					}
					rows, _ := os.ReadDir(parent)
					if len(rows) > 0 {
						t.Fatal("failed scan created tree")
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				if mode == "empty-tree" {
					if len(result.Items) != 0 {
						t.Fatal("empty root requested files")
					}
					if _, e = os.Stat(filepath.Join(parent, "Root")); e != nil {
						t.Fatal(e)
					}
					return
				}
				if len(result.Items) != 2 {
					t.Fatal("missing per-file results")
				}
				if _, e = os.Stat(filepath.Join(parent, "Root", "Empty")); e != nil {
					t.Fatal("empty subdirectory lost")
				}
				if mode == "cancel-transfer" {
					if !result.Cancelled || transfers != 1 || result.Items[1].Status != "skipped" {
						t.Fatal("cancelled directory continued")
					}
					return
				}
				byName := map[string]BatchDownloadItem{}
				for _, item := range result.Items {
					byName[item.Name] = item
				}
				alpha, beta := byName["alpha.txt"], byName[filepath.Join("Sub", "beta.txt")]
				partial := mode == "changed-file" || mode == "corrupt" || (mode == "provider-special" && kind == "lanzou")
				if partial {
					if alpha.Status != "failed" || beta.Status != "saved" {
						t.Fatal("per-file failure not isolated", result)
					}
					mode = "normal"
					before := transfers
					state := result.retry
					recovered, err := runDirectoryFiles(context.Background(), state.plan, state.root, state.owned, result.Items, download, nil)
					if err != nil || transfers-before != 1 || hasDirectoryPending(recovered.Items) {
						t.Fatalf("protocol retry failed: %+v %v", recovered, err)
					}
					for _, item := range recovered.Items {
						if item.Name == beta.Name && item != beta {
							t.Fatal("saved beta changed during retry")
						}
					}
					data, err := os.ReadFile(filepath.Join(result.Directory, "alpha.txt"))
					if err != nil || string(data) != "aaa" {
						t.Fatal("recovered content incorrect", err)
					}
					return
				}
				if alpha.Result.Verification != "verified" || beta.Result.Verification != "unverified" {
					t.Fatal("wrong verification", result)
				}
				for rel, want := range map[string]string{"alpha.txt": "aaa", filepath.Join("Sub", "beta.txt"): "bbb"} {
					b, e := os.ReadFile(filepath.Join(parent, "Root", rel))
					if e != nil || string(b) != want {
						t.Fatal("incorrect bytes or hierarchy", e)
					}
				}
			})
		}
	}
}
