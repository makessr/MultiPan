package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"testing"
)

func TestBaiduFolderMoveNativePaths(t *testing.T) {
	for _, dest := range []string{"/", "/目标"} {
		for _, mode := range []string{"success", "lost", "html", "copy", "wrong-type", "conflict", "source-file"} {
			t.Run(dest+mode, func(t *testing.T) {
				const source = "/来源/资料 & 备份"
				moved := false
				writes := 0
				a := Account{Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=synthetic-cookie"}
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "gettemplatevariable") {
						return quarkResponse(`{"errno":0,"result":{"bdstoken":"synthetic-csrf"}}`, nil), nil
					}
					if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/list") {
						dir := r.URL.Query().Get("dir")
						list := []any{}
						if (dir == "/来源" && (!moved || mode == "copy")) || (dir == dest && moved) {
							id := source
							if moved && dir == dest {
								id = path.Join(dest, path.Base(source))
							}
							typ := 1
							if mode == "source-file" || (moved && mode == "wrong-type") {
								typ = 0
							}
							list = append(list, map[string]any{"fs_id": 123, "path": id, "server_filename": path.Base(source), "isdir": typ})
						}
						if mode == "conflict" && dir == dest {
							list = append(list, map[string]any{"fs_id": 99, "path": path.Join(dest, path.Base(source)), "server_filename": path.Base(source), "isdir": 1})
						}
						return jsonResponse(map[string]any{"errno": 0, "list": list}), nil
					}
					if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/filemanager") {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						return nil, errors.New("unexpected request")
					}
					writes++
					r.ParseForm()
					var items []map[string]string
					json.Unmarshal([]byte(r.Form.Get("filelist")), &items)
					if r.URL.Query().Get("opera") != "move" || r.Form.Get("async") != "0" || r.Form.Get("ondup") != "fail" || len(items) != 1 || items[0]["path"] != source || items[0]["dest"] != dest || items[0]["newname"] != path.Base(source) || items[0]["ondup"] != "fail" {
						t.Error("unsafe Baidu folder move request")
					}
					moved = true
					if mode == "lost" || mode == "copy" || mode == "wrong-type" {
						return nil, errors.New("synthetic lost response")
					}
					if mode == "html" {
						return quarkResponse("<html>unexpected acknowledgement</html>", nil), nil
					}
					return quarkResponse(`{"errno":0}`, nil), nil
				})}
				if mode == "success" || mode == "conflict" || mode == "source-file" {
					a.ID = "a"
					err := validateAccountMoveBatch(context.Background(), client, a, []MoveBatchEntry{{Folder: true, Input: MoveFileInput{AccountID: "a", ParentID: "/来源", EntryID: source, Name: path.Base(source), DestinationID: dest}}})
					if (err == nil) != (mode == "success") || writes != 0 {
						t.Fatal("batch preflight", mode, err, writes)
					}
				}
				entry, e := MoveAccountFolder(context.Background(), client, a, MoveFileInput{AccountID: "a", ParentID: "/来源", EntryID: source, Name: path.Base(source), DestinationID: dest})
				if mode == "success" || mode == "lost" || mode == "html" {
					if e != nil || !entry.Folder || entry.ID != path.Join(dest, path.Base(source)) || writes != 1 {
						t.Fatalf("folder move %+v %v writes=%d", entry, e, writes)
					}
				} else {
					if e == nil {
						t.Fatal("unconfirmed/conflicting folder move accepted")
					}
					if (mode == "conflict" || mode == "source-file") && writes != 0 {
						t.Fatal("invalid source/conflict sent mutation")
					}
				}
				if writes > 1 {
					t.Fatal("mutation replayed")
				}
			})
		}
	}
}
