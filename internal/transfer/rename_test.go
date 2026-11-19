package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func renameListing(kind, parent, id, name string, folder bool) *http.Response {
	var item any
	switch kind {
	case "quark":
		item = map[string]any{"fid": id, "file_name": name, "dir": folder}
		return jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": []any{item}}})
	case "pan123":
		typ := 0
		if folder {
			typ = 1
		}
		item = map[string]any{"FileId": json.Number(id), "FileName": name, "Type": typ}
		return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{item}, "Next": -1}})
	case "guangya":
		typ := 1
		if folder {
			typ = 2
		}
		item = map[string]any{"fileId": id, "fileName": name, "resType": typ}
		return jsonResponse(map[string]any{"data": map[string]any{"list": []any{item}}})
	case "baidu":
		typ := 0
		if folder {
			typ = 1
		}
		item = map[string]any{"fs_id": 43, "server_filename": name, "path": path.Join(parent, name), "isdir": typ}
		return jsonResponse(map[string]any{"errno": 0, "list": []any{item}})
	case "ctdav", "webdav":
		prefix := "/dav" + strings.TrimRight(parent, "/") + "/"
		child := prefix + name
		resource := ""
		if folder {
			child += "/"
			resource = "<d:collection/>"
		}
		self := `<d:response><d:href>` + html.EscapeString(prefix) + `</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
		entry := `<d:response><d:href>` + html.EscapeString(child) + `</d:href><d:propstat><d:prop><d:resourcetype>` + resource + `</d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
		return quarkResponse(`<d:multistatus xmlns:d="DAV:">`+self+entry+`</d:multistatus>`, nil)
	}
	panic("unexpected listing kind")
}

func TestRenameProviderFilesAndFolders(t *testing.T) {
	for _, kind := range []string{"lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav"} {
		for _, folder := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-file", true: "-folder"}[folder], func(t *testing.T) {
				parent := "42"
				if kind == "quark" || kind == "guangya" {
					parent = "parent-id"
				}
				if kind == "baidu" || kind == "ctdav" || kind == "webdav" {
					parent = "/父目录"
				}
				oldName, newName := "原文件.zip", "改后&文件.zip"
				if folder {
					oldName, newName = "原目录", "改后&目录"
				}
				id := "43"
				if kind == "quark" || kind == "guangya" {
					id = "stable-id"
				}
				if strings.HasPrefix(parent, "/") {
					id = path.Join(parent, oldName)
				}
				a := Account{Kind: kind, Secret: "synthetic-secret", Username: "dav-user"}
				a.Endpoint = map[string]string{"lanzou": "https://lanzou.example", "quark": QuarkAPI, "pan123": Pan123API, "baidu": BaiduAPI, "guangya": GuangyaAPI, "ctdav": "https://dav.example/dav", "webdav": "https://dav.example/dav"}[kind]
				writes := 0
				currentName := oldName
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if kind == "lanzou" {
						r.ParseForm()
						switch r.Form.Get("task") {
						case "47":
							items := []any{}
							if folder {
								items = append(items, map[string]any{"fol_id": id, "name": currentName})
							}
							return jsonResponse(map[string]any{"zt": 1, "text": items}), nil
						case "5":
							items := []any{}
							if !folder {
								items = append(items, map[string]any{"id": id, "name_all": currentName, "size": "12 M"})
							}
							return jsonResponse(map[string]any{"zt": 1, "text": items}), nil
						case "18":
							return jsonResponse(map[string]any{"zt": 1, "info": map[string]string{"name": oldName, "des": "原描述<&必须保留>"}}), nil
						case "46":
							if r.Form.Get("type") == "1" {
								return jsonResponse(map[string]any{"zt": 1, "info": strings.TrimSuffix(oldName, ".zip")}), nil
							}
							if folder || r.Form.Get("file_id") != id || r.Form.Get("file_name") != strings.TrimSuffix(newName, ".zip") {
								t.Fatal("Lanzou editable basename incorrect")
							}
						case "4":
							if !folder || r.Form.Get("folder_id") != id || r.Form.Get("folder_name") != newName || r.Form.Get("folder_description") != "原描述<&必须保留>" {
								t.Fatal("Lanzou description lost or wrong rename")
							}
						default:
							t.Fatal("unknown task")
						}
						writes++
						currentName = newName
						return quarkResponse(`{"zt":1}`, nil), nil
					}
					if strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
						return quarkResponse(`{"errno":0,"result":{"bdstoken":"synthetic-csrf"}}`, nil), nil
					}
					if r.Method == "GET" || r.Method == "PROPFIND" || strings.HasSuffix(r.URL.Path, "get_file_list") {
						return renameListing(kind, parent, id, currentName, folder), nil
					}
					writes++
					currentName = newName
					if kind == "ctdav" || kind == "webdav" {
						dest, e := url.Parse(r.Header.Get("Destination"))
						user, secret, ok := r.BasicAuth()
						suffix := ""
						if folder {
							suffix = "/"
						}
						if e != nil || dest.Host != "dav.example" || dest.Path != "/dav"+path.Join(parent, newName)+suffix || r.URL.Path != "/dav"+id+suffix || r.Method != "MOVE" || r.Header.Get("Overwrite") != "F" || !ok || user != a.Username || secret != a.Secret {
							t.Fatal("unsafe DAV rename")
						}
						resp := quarkResponse("", nil)
						resp.StatusCode = 201
						return resp, nil
					}
					if kind == "baidu" {
						r.ParseForm()
						var items []map[string]string
						json.Unmarshal([]byte(r.Form.Get("filelist")), &items)
						if r.URL.Query().Get("opera") != "rename" || r.Form.Get("ondup") != "fail" || r.Form.Get("async") != "0" || len(items) != 1 || items[0]["path"] != id || items[0]["newname"] != newName {
							t.Fatal("unsafe Baidu rename")
						}
						return quarkResponse(`{"errno":0}`, nil), nil
					}
					var data map[string]any
					json.NewDecoder(r.Body).Decode(&data)
					switch kind {
					case "quark":
						if !strings.HasSuffix(r.URL.Path, "/file/rename") || data["fid"] != id || data["file_name"] != newName {
							t.Fatal("Quark rename fields")
						}
						return quarkResponse(`{"status":200,"code":0}`, nil), nil
					case "pan123":
						if !strings.HasSuffix(r.URL.Path, "/file/rename") || data["fileId"] != id || data["fileName"] != newName {
							t.Fatal("123 rename fields")
						}
						return quarkResponse(`{"code":0}`, nil), nil
					case "guangya":
						if r.URL.Path != "/userres/v1/file/rename" || data["fileId"] != id || data["newName"] != newName {
							t.Fatal("Guangya rename fields")
						}
						return quarkResponse(`{"data":{}}`, nil), nil
					}
					panic("unexpected request")
				})}
				entry, e := RenameAccountEntry(context.Background(), client, a, RenameInput{ParentID: parent, EntryID: id, OldName: oldName, NewName: newName, Folder: folder})
				if e != nil || entry.Name != newName || entry.Folder != folder || writes != 1 {
					t.Fatalf("rename failed %+v %v writes=%d", entry, e, writes)
				}
			})
		}
	}
}

func TestRenameRejectsStaleCollisionAndAmbiguity(t *testing.T) {
	for _, mode := range []string{"stale", "collision", "network", "wrong-id", "wrong-type"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					writes++
					if mode == "network" {
						return nil, errors.New("synthetic-secret")
					}
					return quarkResponse(`{"status":200,"code":0}`, nil), nil
				}
				name, id, folder := "原目录", "id", true
				if mode == "stale" {
					name = "别人改过"
				}
				if writes > 0 {
					name = "新目录"
					if mode == "wrong-id" {
						id = "unrelated"
					}
					if mode == "wrong-type" {
						folder = false
					}
				}
				resp := renameListing("quark", "0", id, name, folder)
				if mode == "collision" {
					resp = quarkResponse(`{"status":200,"code":0,"data":{"list":[{"fid":"id","file_name":"原目录","dir":true},{"fid":"other","file_name":"新目录"}]}}`, nil)
				}
				return resp, nil
			})}
			_, e := RenameAccountEntry(context.Background(), client, Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "synthetic-secret"}, RenameInput{ParentID: "0", EntryID: "id", OldName: "原目录", NewName: "新目录", Folder: true})
			if e == nil || writes > 1 || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatalf("accepted stale/false result or replay/leak: %v writes=%d", e, writes)
			}
			if (mode == "stale" || mode == "collision") && writes != 0 {
				t.Fatal("unsafe mutation")
			}
		})
	}
}

func TestRenameDirectoryStateAndReservations(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a, e := m.SaveAccount(AccountInput{Name: "DAV", Kind: "webdav", Endpoint: "https://dav.example/dav", Username: "u", Secret: "s", Folder: "/旧/子", FolderDisplay: "根目录 / 旧 / 子"})
	if e != nil {
		t.Fatal(e)
	}
	m.tasks = []Task{{ID: "retry", AccountID: a.ID, Folder: "/旧/子", FolderDisplay: "根目录 / 旧 / 子", Status: "failed"}, {ID: "history", AccountID: a.ID, Folder: "/旧/子", Status: "success"}, {ID: "other", AccountID: a.ID, Folder: "/旧目录", Status: "paused"}}
	if !m.updateRenamedFolderLocked(RenameInput{AccountID: a.ID, EntryID: "/旧", OldName: "旧", NewName: "新", ParentDisplay: "根目录"}, "webdav", "/新") || m.accounts[0].Folder != "/新/子" || m.tasks[0].Folder != "/新/子" || m.tasks[1].Folder != "/旧/子" || m.tasks[2].Folder != "/旧目录" {
		t.Fatal("wrong default/retry/history path rewrite")
	}
	m.renaming = map[string]bool{a.ID: true}
	if e = m.Retry("retry"); e == nil {
		t.Fatal("retry allowed during rename")
	}
	if e = m.SetDefaultFolder(a.ID, "/", "根目录"); e == nil {
		t.Fatal("default changed during rename")
	}
	if e = m.DeleteAccount(a.ID); e == nil {
		t.Fatal("account removed during rename")
	}
	f := source(t)
	if _, e = m.Enqueue([]File{f}, []Target{{AccountID: a.ID, Folder: "/"}}); e == nil {
		t.Fatal("upload enqueued during rename")
	}
	delete(m.renaming, a.ID)
	m.tasks[0].Status = "queued"
	if _, e = m.RenameEntry(context.Background(), RenameInput{AccountID: a.ID}); e == nil {
		t.Fatal("rename allowed while uploads queued")
	}
}

func TestGuangyaRenameAcknowledgementWithoutData(t *testing.T) {
	for _, body := range []string{`{}`, `{"msg":"success"}`, `{"data":null}`, `{"code":0}`} {
		for _, folder := range []bool{false, true} {
			t.Run(body+map[bool]string{false: "-file", true: "-folder"}[folder], func(t *testing.T) {
				writes, reads := 0, 0
				name := "旧名称"
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/userres/v1/file/get_file_list" {
						reads++
						return renameListing("guangya", "root", "stable-id", name, folder), nil
					}
					if r.URL.Path != "/userres/v1/file/rename" {
						t.Fatal("unexpected endpoint")
					}
					writes++
					name = "新名称"
					return quarkResponse(body, nil), nil
				})}
				entry, e := RenameAccountEntry(context.Background(), client, Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-secret"}, RenameInput{ParentID: "root", EntryID: "stable-id", OldName: "旧名称", NewName: "新名称", Folder: folder})
				if e != nil || entry.Name != "新名称" || entry.ID != "stable-id" || entry.Folder != folder || writes != 1 || reads != 2 {
					t.Fatalf("committed rename falsely rejected or replayed: %v writes=%d reads=%d", e, writes, reads)
				}
			})
		}
	}
	for _, body := range []string{`null`, `[]`, `<html>synthetic-secret</html>`, `{"code":117}`, `{"error":"invalid_token"}`, `{"error_code":117}`, `{"code":0,"error":"invalid_token"}`} {
		t.Run("invalid-"+body, func(t *testing.T) {
			writes, reads := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/userres/v1/file/get_file_list" {
					reads++
					return renameListing("guangya", "root", "id", "旧名称", true), nil
				}
				writes++
				return quarkResponse(body, nil), nil
			})}
			_, e := RenameAccountEntry(context.Background(), client, Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-secret"}, RenameInput{ParentID: "root", EntryID: "id", OldName: "旧名称", NewName: "新名称", Folder: true})
			if e == nil || writes != 1 || reads != 1 || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatalf("error envelope accepted/replayed/leaked: %v writes=%d reads=%d", e, writes, reads)
			}
		})
	}
}

func TestGuangyaRenameAcknowledgementStillRequiresConfirmation(t *testing.T) {
	writes := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/userres/v1/file/get_file_list" {
			return renameListing("guangya", "root", "id", "旧名称", true), nil
		}
		writes++
		return quarkResponse(`{}`, nil), nil
	})}
	if _, e := RenameAccountEntry(context.Background(), client, Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-secret"}, RenameInput{ParentID: "root", EntryID: "id", OldName: "旧名称", NewName: "新名称", Folder: true}); e == nil || writes != 1 {
		t.Fatalf("uncommitted rename accepted or replayed: %v writes=%d", e, writes)
	}
}
