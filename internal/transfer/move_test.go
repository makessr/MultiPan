package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"
)

func TestMoveRestored123FileRejectsMissingSize(t *testing.T) {
	reads := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
			t.Fatalf("unexpected request before size verification: %s %s", r.Method, r.URL.Path)
		}
		reads++
		if r.URL.Query().Get("parentFileId") == "44" {
			return quarkResponse(`{"code":0,"data":{"InfoList":[],"Next":-1}}`, nil), nil
		}
		return quarkResponse(`{"code":0,"data":{"InfoList":[{"FileId":43,"FileName":"空文件","Type":0,"ParentFileId":42}],"Next":-1}}`, nil), nil
	})}
	zero := int64(0)
	_, err := MoveAccountFile(context.Background(), client, Account{Kind: "pan123", Endpoint: Pan123API}, MoveFileInput{
		ParentID: "42", DestinationID: "44", EntryID: "43", Name: "空文件", ExpectedSize: &zero,
	})
	if err == nil || !strings.Contains(err.Error(), "无法在原目录重新核对") || reads != 3 {
		t.Fatalf("expected strict verification to refuse the missing size after three reads; got %d reads, %v", reads, err)
	}
}

func TestMoveFilesAcrossProviderDirectories(t *testing.T) {
	for _, kind := range []string{"lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav"} {
		for _, toRoot := range []bool{true, false} {
			t.Run(kind+map[bool]string{true: "-to-root", false: "-to-child"}[toRoot], func(t *testing.T) {
				parent, destination, id := "42", "44", "43"
				if kind == "quark" || kind == "guangya" {
					parent, destination, id = "source-id", "destination-id", "file-id"
				}
				if kind == "baidu" || kind == "ctdav" || kind == "webdav" {
					parent, destination = "/来源", "/目标"
				}
				if toRoot {
					destination = cloudRoot(kind)
				}
				name := "测试 & 文件.zip"
				if strings.HasPrefix(parent, "/") {
					id = path.Join(parent, name)
				}
				a := Account{Kind: kind, Secret: "synthetic-secret", Username: "dav-user"}
				a.Endpoint = map[string]string{"lanzou": "https://lanzou.example", "quark": QuarkAPI, "pan123": Pan123API, "baidu": BaiduAPI, "guangya": GuangyaAPI, "ctdav": "https://dav.example/dav", "webdav": "https://dav.example/dav"}[kind]
				writes, reads := 0, 0
				moved := false
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
						return quarkResponse(`{"errno":0,"result":{"bdstoken":"synthetic-csrf"}}`, nil), nil
					}
					listing := r.Method == "GET" || r.Method == "PROPFIND" || strings.HasSuffix(r.URL.Path, "get_file_list")
					if kind == "lanzou" {
						r.ParseForm()
						if r.Form.Get("task") == "47" {
							return quarkResponse(`{"zt":1,"text":[]}`, nil), nil
						}
						listing = r.Form.Get("task") == "5"
					}
					if listing {
						reads++
						dir := ""
						switch kind {
						case "lanzou":
							dir = r.Form.Get("folder_id")
						case "quark":
							dir = r.URL.Query().Get("pdir_fid")
						case "pan123":
							dir = r.URL.Query().Get("parentFileId")
						case "baidu":
							dir = r.URL.Query().Get("dir")
						case "guangya":
							var body map[string]string
							json.NewDecoder(r.Body).Decode(&body)
							dir = body["parentId"]
							if dir == "" {
								dir = "root"
							}
						case "ctdav", "webdav":
							dir = strings.TrimRight(strings.TrimPrefix(r.URL.Path, "/dav"), "/")
							if dir == "" {
								dir = "/"
							}
						}
						if dir != parent && dir != destination {
							t.Fatalf("unexpected directory %q", dir)
						}
						entryID, entryName := "99", "其他文件"
						if (!moved && dir == parent) || (moved && dir == destination) {
							entryID, entryName = id, name
						}
						if kind == "lanzou" {
							return jsonResponse(map[string]any{"zt": 1, "text": []any{map[string]any{"id": entryID, "name_all": entryName, "size": "1 M"}}}), nil
						}
						return renameListing(kind, dir, entryID, entryName, false), nil
					}
					writes++
					moved = true
					if kind == "lanzou" {
						if r.Form.Get("task") != "20" || r.Form.Get("folder_id") != destination || r.Form.Get("file_id") != id {
							t.Fatal("wrong Lanzou move")
						}
						return quarkResponse(`{"zt":1}`, nil), nil
					}
					if kind == "ctdav" || kind == "webdav" {
						dst, e := url.Parse(r.Header.Get("Destination"))
						user, secret, ok := r.BasicAuth()
						if e != nil || dst.Host != "dav.example" || dst.Path != "/dav"+path.Join(destination, name) || r.URL.Path != "/dav"+id || r.Method != "MOVE" || r.Header.Get("Overwrite") != "F" || !ok || user != a.Username || secret != a.Secret {
							t.Fatal("unsafe DAV move")
						}
						res := quarkResponse("", nil)
						res.StatusCode = 201
						return res, nil
					}
					if kind == "baidu" {
						r.ParseForm()
						var items []map[string]string
						json.Unmarshal([]byte(r.Form.Get("filelist")), &items)
						if r.URL.Query().Get("opera") != "move" || r.Form.Get("ondup") != "fail" || r.Form.Get("async") != "0" || len(items) != 1 || items[0]["path"] != id || items[0]["dest"] != destination || items[0]["newname"] != name || items[0]["ondup"] != "fail" {
							t.Fatal("unsafe Baidu move")
						}
						return quarkResponse(`{"errno":0}`, nil), nil
					}
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					switch kind {
					case "quark":
						if !strings.HasSuffix(r.URL.Path, "/file/move") || body["to_pdir_fid"] != destination || body["filelist"].([]any)[0] != id {
							t.Fatal("wrong Quark move")
						}
						return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"task"}}`, nil), nil
					case "pan123":
						if !strings.HasSuffix(r.URL.Path, "/file/mod_pid") || body["parentFileId"] != destination || body["fileIdList"].([]any)[0].(map[string]any)["FileId"] != id {
							t.Fatal("wrong 123 move")
						}
						return quarkResponse(`{"code":0}`, nil), nil
					case "guangya":
						pid := destination
						if pid == "root" {
							pid = ""
						}
						if r.URL.Path != "/userres/v1/file/move_file" || body["parentId"] != pid || body["fileIds"].([]any)[0] != id {
							t.Fatal("wrong Guangya move")
						}
						if !toRoot {
							return quarkResponse(`{}`, nil), nil
						}
						return quarkResponse(`{"data":{"taskId":"task"}}`, nil), nil
					}
					panic("unexpected endpoint")
				})}
				entry, e := MoveAccountFile(context.Background(), client, a, MoveFileInput{ParentID: parent, DestinationID: destination, EntryID: id, Name: name})
				if e != nil || entry.Name != name || entry.Folder || writes != 1 || reads != 4 {
					t.Fatalf("move failed %+v %v writes=%d reads=%d", entry, e, writes, reads)
				}
			})
		}
	}
}

func TestMoveFileConflictsAndLostAcknowledgements(t *testing.T) {
	for _, mode := range []string{"collision", "stale", "folder", "lost-response", "async", "copy-only", "wrong-id", "wrong-size", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, reads := 0, 0
			moved := false
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					writes++
					moved = true
					if mode == "lost-response" {
						return nil, errors.New("private synthetic-secret")
					}
					if mode == "rejected" {
						moved = false
						return quarkResponse(`{"status":403,"code":101}`, nil), nil
					}
					if mode == "async" {
						moved = false
					}
					return quarkResponse(`{"status":200,"code":0}`, nil), nil
				}
				reads++
				dir := r.URL.Query().Get("pdir_fid")
				id, name, folder := "other", "其他", false
				if (dir == "0" && (!moved || mode == "copy-only")) || (dir == "dest" && (moved || mode == "collision")) {
					id, name = "file", "原文件"
				}
				if mode == "stale" && dir == "0" {
					name = "已改名"
				}
				if mode == "folder" && dir == "0" {
					folder = true
				}
				if mode == "wrong-id" && moved && dir == "dest" {
					id = "unrelated"
				}
				if writes > 0 && dir == "dest" && (mode == "copy-only" || mode == "wrong-id" || mode == "wrong-size") {
					cancel()
				}
				if mode == "async" && reads == 4 {
					moved = true
				}
				if mode == "wrong-size" {
					size := 12
					if moved && dir == "dest" {
						size = 13
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": []any{map[string]any{"fid": id, "file_name": name, "dir": false, "size": size}}}}), nil
				}
				return renameListing("quark", dir, id, name, folder), nil
			})}
			entry, e := MoveAccountFile(ctx, client, Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "synthetic-secret"}, MoveFileInput{ParentID: "0", DestinationID: "dest", EntryID: "file", Name: "原文件"})
			if mode == "lost-response" || mode == "async" {
				expectedReads := 4
				if mode == "async" {
					expectedReads = 6
				}
				if e != nil || entry.ID != "file" || writes != 1 || reads != expectedReads {
					t.Fatalf("committed move falsely rejected/replayed %v", e)
				}
				return
			}
			if e == nil || writes > 1 || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatalf("unsafe result/replay/leak: %v writes=%d", e, writes)
			}
			if (mode == "collision" || mode == "stale" || mode == "folder") && writes != 0 {
				t.Fatal("unsafe mutation")
			}
		})
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid input reached network")
		return nil, nil
	})}
	if _, e := MoveAccountFile(context.Background(), client, Account{Kind: "quark"}, MoveFileInput{ParentID: "0", DestinationID: "0", EntryID: "file", Name: "文件"}); e == nil {
		t.Fatal("same directory accepted")
	}
}
