package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"path"
	"strings"
	"testing"
)

func TestCloudFolderCreationProviders(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu", "guangya", "ctdav", "webdav"} {
		for _, nested := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-root", true: "-child"}[nested], func(t *testing.T) {
				parent := cloudRoot(kind)
				if nested {
					parent = map[string]string{"quark": "parent-id", "pan123": "42", "baidu": "/父目录", "guangya": "parent-id", "ctdav": "/父目录", "webdav": "/父目录"}[kind]
				}
				name := "资料 & (备份)"
				created := false
				writes, lists := 0, 0
				a := Account{Kind: kind, Secret: "synthetic-secret", Username: "device-user"}
				a.Endpoint = map[string]string{"quark": QuarkAPI, "pan123": Pan123API, "baidu": BaiduAPI, "guangya": GuangyaAPI, "ctdav": "https://dav.example/dav", "webdav": "https://dav.example/dav"}[kind]
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if kind == "baidu" && strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
						return quarkResponse(`{"errno":0,"result":{"bdstoken":"synthetic-csrf"}}`, nil), nil
					}
					listing := r.Method == "PROPFIND" || strings.HasSuffix(r.URL.Path, "get_file_list") || strings.HasSuffix(r.URL.Path, "/file/sort") || strings.HasSuffix(r.URL.Path, "/file/list/new") || strings.HasSuffix(r.URL.Path, "/api/list")
					if listing {
						lists++
						items := []any{}
						if kind == "ctdav" || kind == "webdav" {
							if r.URL.Path != "/dav"+strings.TrimRight(parent, "/")+"/" {
								t.Fatal("DAV listed wrong parent")
							}
							self := "/dav" + strings.TrimRight(parent, "/") + "/"
							body := `<d:response><d:href>` + html.EscapeString(self) + `</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
							if created {
								body += `<d:response><d:href>` + html.EscapeString(self+name+"/") + `</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
							}
							return quarkResponse(`<d:multistatus xmlns:d="DAV:">`+body+`</d:multistatus>`, nil), nil
						}
						if created {
							switch kind {
							case "quark":
								items = append(items, map[string]any{"fid": "new-id", "file_name": name, "dir": true})
							case "pan123":
								items = append(items, map[string]any{"FileId": 43, "FileName": name, "Type": 1})
							case "baidu":
								items = append(items, map[string]any{"fs_id": 43, "server_filename": name, "path": path.Join(parent, name), "isdir": 1})
							case "guangya":
								items = append(items, map[string]any{"fileId": "new-id", "fileName": name, "resType": 2})
							}
						}
						switch kind {
						case "quark":
							return jsonResponse(map[string]any{"code": 0, "status": 200, "data": map[string]any{"list": items}}), nil
						case "pan123":
							return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"InfoList": items, "Next": -1}}), nil
						case "baidu":
							return jsonResponse(map[string]any{"errno": 0, "list": items}), nil
						case "guangya":
							return jsonResponse(map[string]any{"data": map[string]any{"list": items}}), nil
						}
					}
					writes++
					if writes > 1 {
						t.Fatal("mutation replayed")
					}
					created = true
					if kind == "ctdav" || kind == "webdav" {
						user, secret, ok := r.BasicAuth()
						if !ok || user != a.Username || secret != a.Secret || r.Method != "MKCOL" || r.URL.Path != "/dav"+path.Join(parent, name)+"/" || r.Header.Get("Cookie") != "" {
							t.Fatal("incorrect DAV mutation/auth/path")
						}
						resp := quarkResponse("", nil)
						resp.StatusCode = 201
						return resp, nil
					}
					if kind == "baidu" {
						r.ParseForm()
						if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/api/create") || r.URL.Query().Get("bdstoken") != "synthetic-csrf" || r.Form.Get("rtype") != "0" || r.Form.Get("isdir") != "1" || r.Form.Get("path") != path.Join(parent, name) {
							t.Fatal("wrong Baidu create fields")
						}
						return quarkResponse(`{"errno":0}`, nil), nil
					}
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					switch kind {
					case "quark":
						if r.URL.Path != "/1/clouddrive/file" || body["pdir_fid"] != parent || body["file_name"] != name {
							t.Fatal("wrong Quark fields")
						}
						return quarkResponse(`{"status":200,"code":0}`, nil), nil
					case "pan123":
						if !strings.HasSuffix(r.URL.Path, "/file/upload_request") || body["type"] != float64(1) || body["parentFileId"] != parent || body["fileName"] != name || body["duplicate"] != float64(1) {
							t.Fatal("wrong 123 fields")
						}
						return quarkResponse(`{"code":0}`, nil), nil
					case "guangya":
						pid := parent
						if pid == "root" {
							pid = ""
						}
						if r.URL.Path != "/userres/v1/file/create_dir" || body["parentId"] != pid || body["dirName"] != name || body["failIfNameExist"] != true {
							t.Fatal("wrong Guangya fields")
						}
						return quarkResponse(`{"data":{"fileId":"new-id"}}`, nil), nil
					}
					panic("unexpected endpoint")
				})}
				folder, e := CreateAccountFolder(context.Background(), client, a, parent, name)
				if e != nil || folder.Name != name || folder.ID == "" || writes != 1 || lists != 2 {
					t.Fatalf("create failed %v %+v writes=%d lists=%d", e, folder, writes, lists)
				}
			})
		}
	}
}

func jsonResponse(data any) *http.Response {
	b, _ := json.Marshal(data)
	return quarkResponse(string(b), nil)
}

func TestCloudCreateNoReplayAndNoFalseSuccess(t *testing.T) {
	for _, mode := range []string{"duplicate-file", "lost-response", "bad-response", "wrong-type", "refused"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					items := []any{}
					if mode == "duplicate-file" || writes > 0 {
						items = append(items, map[string]any{"fid": "new-id", "file_name": "目录", "dir": false})
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": items}}), nil
				}
				writes++
				switch mode {
				case "lost-response":
					return nil, errors.New("private synthetic-secret")
				case "bad-response":
					return quarkResponse(`<html>synthetic-secret</html>`, nil), nil
				case "refused":
					return quarkResponse(`{"status":400,"code":100}`, nil), nil
				}
				return quarkResponse(`{"status":200,"code":0}`, nil), nil
			})}
			_, e := CreateAccountFolder(ctx, client, Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "synthetic-secret"}, "0", "目录")
			if e == nil || strings.Contains(e.Error(), "synthetic-secret") || writes > 1 {
				t.Fatalf("invalid mutation result %v writes=%d", e, writes)
			}
			if mode == "duplicate-file" && writes != 0 {
				t.Fatal("same-name file overwritten")
			}
		})
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid path/name reached network")
		return nil, nil
	})}
	for _, name := range []string{"", ".", "..", " a", "a ", "a/b", "a\\b", "a\n"} {
		if _, e := CreateAccountFolder(context.Background(), client, Account{Kind: "webdav", Endpoint: "https://dav.example"}, "/", name); e == nil {
			t.Fatal("invalid name accepted")
		}
	}
	if _, e := CreateAccountFolder(context.Background(), client, Account{Kind: "webdav", Endpoint: "https://dav.example"}, "/../escape", "有效"); e == nil {
		t.Fatal("invalid parent accepted")
	}
}
