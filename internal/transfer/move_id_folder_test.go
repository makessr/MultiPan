package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
)

func TestIDFolderNativeMoveAndVerifiedTrails(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "guangya"} {
		for _, toRoot := range []bool{false, true} {
			for _, mode := range []string{"success", "lost", "copy", "wrong-type", "conflict", "stale-name", "missing-trail", "wrong-edge", "descendant", "cycle"} {
				t.Run(kind+mode+map[bool]string{true: "-root", false: "-child"}[toRoot], func(t *testing.T) {
					const source = "9007199254740993"
					in := MoveFileInput{AccountID: "a", ParentID: "10", EntryID: source, Name: "中文 空格资料", DestinationID: "40", SourceTrail: []CloudFolder{{ID: "0", Name: "forged root"}, {ID: "10", Name: "forged parent"}}, DestinationTrail: []CloudFolder{{ID: "0"}, {ID: "40", Name: "forged target"}}}
					if toRoot {
						in.DestinationID = "0"
						in.DestinationTrail = []CloudFolder{{ID: "0"}}
					}
					if mode == "missing-trail" {
						in.SourceTrail = nil
					}
					if mode == "wrong-edge" {
						in.DestinationID = "30"
						in.DestinationTrail = []CloudFolder{{ID: "0"}, {ID: "30"}}
					}
					if mode == "descendant" {
						in.DestinationID = "30"
						in.DestinationTrail = []CloudFolder{{ID: "0"}, {ID: "10"}, {ID: source}, {ID: "30"}}
					}
					if mode == "cycle" {
						in.DestinationTrail = []CloudFolder{{ID: "0"}, {ID: "0"}, {ID: in.DestinationID}}
					}
					if kind == "guangya" {
						for i := range in.SourceTrail {
							if in.SourceTrail[i].ID == "0" {
								in.SourceTrail[i].ID = guangyaRoot
							}
						}
						for i := range in.DestinationTrail {
							if in.DestinationTrail[i].ID == "0" {
								in.DestinationTrail[i].ID = guangyaRoot
							}
						}
						if in.DestinationID == "0" {
							in.DestinationID = guangyaRoot
						}
					}
					a := Account{Kind: kind, Endpoint: QuarkAPI, Secret: "synthetic-secret"}
					if kind == "pan123" {
						a.Endpoint = Pan123API
					}
					if kind == "guangya" {
						a.Endpoint = GuangyaAPI
					}
					moved := false
					writes := 0
					client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method == "GET" || (kind == "guangya" && r.URL.Path == "/userres/v1/file/get_file_list") {
							id := r.URL.Query().Get("pdir_fid")
							if kind == "pan123" {
								id = r.URL.Query().Get("parentFileId")
							}
							if kind == "guangya" {
								var body map[string]any
								json.NewDecoder(r.Body).Decode(&body)
								id, _ = body["parentId"].(string)
								if id == "" {
									id = guangyaRoot
								}
							}
							items := []CloudEntry{}
							if id == cloudRoot(kind) {
								items = append(items, CloudEntry{ID: "10", Name: "来源", Folder: true}, CloudEntry{ID: "40", Name: "目标", Folder: true})
							}
							if (id == "10" && (!moved || mode == "copy")) || (id == in.DestinationID && moved) {
								name := in.Name
								if mode == "stale-name" {
									name = "changed"
								}
								items = append(items, CloudEntry{ID: source, Name: name, Folder: !(moved && mode == "wrong-type")})
							}
							if id == in.DestinationID && mode == "conflict" {
								items = append(items, CloudEntry{ID: "99", Name: in.Name, Folder: false})
							}
							if id == source {
								items = append(items, CloudEntry{ID: "30", Name: "子目录", Folder: true})
							}
							if kind == "quark" {
								list := []any{}
								for _, f := range items {
									list = append(list, map[string]any{"fid": f.ID, "file_name": f.Name, "dir": f.Folder})
								}
								return jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": list}}), nil
							}
							if kind == "guangya" {
								list := []any{}
								for _, f := range items {
									typ := 1
									if f.Folder {
										typ = 2
									}
									list = append(list, map[string]any{"fileId": f.ID, "fileName": f.Name, "resType": typ, "fileSize": 0})
								}
								return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"list": list, "total": len(list)}}), nil
							}
							list := []any{}
							for _, f := range items {
								typ := 0
								if f.Folder {
									typ = 1
								}
								list = append(list, map[string]any{"FileId": json.Number(f.ID), "FileName": f.Name, "Type": typ, "Size": 0})
							}
							return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"InfoList": list, "Next": -1}}), nil
						}
						writes++
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						if kind == "quark" {
							if r.URL.Path != "/1/clouddrive/file/move" || body["filelist"].([]any)[0] != source || body["to_pdir_fid"] != in.DestinationID {
								t.Error("wrong Quark folder payload")
							}
						} else if kind == "guangya" {
							parent := in.DestinationID
							if parent == guangyaRoot {
								parent = ""
							}
							if r.URL.Path != "/userres/v1/file/move_file" || body["fileIds"].([]any)[0] != source || body["parentId"] != parent {
								t.Error("wrong Guangya folder payload")
							}
						} else {
							if r.URL.Path != "/b/api/file/mod_pid" || body["fileIdList"].([]any)[0].(map[string]any)["FileId"] != source || body["parentFileId"] != in.DestinationID {
								t.Error("wrong 123 folder payload")
							}
						}
						moved = true
						if mode == "lost" || mode == "copy" || mode == "wrong-type" {
							return nil, errors.New("synthetic lost acknowledgement")
						}
						if kind == "quark" {
							return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"fixture"}}`, nil), nil
						}
						return quarkResponse(`{"code":0,"data":{"taskId":"fixture"}}`, nil), nil
					})}
					if mode == "success" || mode == "conflict" || mode == "descendant" || mode == "missing-trail" || mode == "wrong-edge" || mode == "cycle" {
						a.ID = "a"
						err := validateAccountMoveBatch(context.Background(), client, a, []MoveBatchEntry{{Input: in, Folder: true}})
						if (err == nil) != (mode == "success") || writes != 0 {
							t.Fatal("batch preflight", mode, err, writes)
						}
					}
					var labels folderMoveLabels
					entry, e := moveAccountEntry(context.Background(), client, a, in, true, &labels)
					if mode == "success" || mode == "lost" {
						want := "根目录 / 目标 / " + in.Name
						if toRoot {
							want = "根目录 / " + in.Name
						}
						if e != nil || !entry.Folder || entry.ID != source || writes != 1 || labels.Old != "根目录 / 来源 / "+in.Name || labels.New != want {
							t.Fatalf("result %+v %v writes %d labels %+v", entry, e, writes, labels)
						}
					} else {
						if e == nil {
							t.Fatal("invalid or unconfirmed folder move accepted")
						}
						if mode != "copy" && mode != "wrong-type" && writes != 0 {
							t.Fatal("preflight error sent mutation")
						}
					}
					if writes > 1 {
						t.Fatal("move replayed")
					}
				})
			}
		}
	}
}

func TestMovedIDFolderLabelsPersistWithoutChangingIDs(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	labels := folderMoveLabels{Old: "根目录 / 来源 / 资料", New: "根目录 / 目标 / 资料"}
	in := MoveFileInput{AccountID: "a", EntryID: "42"}
	m.accounts = []Account{{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Folder: "42", FolderDisplay: labels.Old, Secret: "synthetic"}}
	m.tasks = []Task{{ID: "retry", AccountID: "a", Status: "failed", Folder: "43", FolderDisplay: labels.Old + " / 子"}, {ID: "history", AccountID: "a", Status: "success", Folder: "43", FolderDisplay: labels.Old + " / 子"}, {ID: "sibling", AccountID: "a", Status: "paused", Folder: "44", FolderDisplay: labels.Old + "2"}, {ID: "other", AccountID: "other", Status: "paused", Folder: "42", FolderDisplay: labels.Old}}
	if !m.updateMovedIDFolderLocked(in, labels) {
		m.Close()
		t.Fatal("labels unchanged")
	}
	if e = m.saveLocked(); e != nil {
		m.Close()
		t.Fatal(e)
	}
	m.Close()
	loaded, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer loaded.Close()
	s := loaded.Snapshot()
	if s.Accounts[0].Folder != "42" || s.Accounts[0].FolderDisplay != labels.New || s.Tasks[0].Folder != "43" || s.Tasks[0].FolderDisplay != labels.New+" / 子" || s.Tasks[1].FolderDisplay != labels.Old+" / 子" || s.Tasks[2].FolderDisplay != labels.Old+"2" || s.Tasks[3].FolderDisplay != labels.Old {
		t.Fatal("ID/label/history/prefix persistence mismatch")
	}
}
