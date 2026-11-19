package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPan123CopiesFolderIntoNewSubdirectory(t *testing.T) {
	const md5 = "d41d8cd98f00b204e9800998ecf8427e"
	for _, mode := range []string{"success", "missing-source-md5", "wrong-target-md5",
		"direct-success", "direct-empty", "direct-conflict", "direct-reuse", "direct-wrong-md5"} {
		t.Run(mode, func(t *testing.T) {
			direct := strings.HasPrefix(mode, "direct-")
			a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API}
			d := diskCopy{
				Input: MoveBatchEntry{Folder: true, Input: MoveFileInput{
					AccountID: a.ID, EntryID: "10", ParentID: "0", DestinationID: "50", Name: "Docs",
					SourceTrail: []CloudFolder{{ID: "0"}}, DestinationTrail: []CloudFolder{{ID: "0"}, {ID: "50"}},
				}},
				Receipt:   CopyReceipt{ID: "fixture", OutputFolder: "MultiPan-复制-fixture"},
				Direct123: direct,
			}
			if direct {
				d.Receipt.OutputFolder = "Docs"
			}
			created, copied := false, false
			saves := 0
			row := func(id, parent, typ int, name, hash string) map[string]any {
				return map[string]any{"FileId": id, "ParentFileId": parent, "FileName": name, "Type": typ, "Size": 0, "Etag": hash}
			}
			c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/b/api/file/list/new":
					entries := []map[string]any{}
					switch r.URL.Query().Get("parentFileId") {
					case "0":
						entries = append(entries, row(10, 0, 1, "Docs", ""), row(50, 0, 1, "Target", ""))
					case "10":
						if mode == "direct-empty" {
							break
						}
						tag := md5
						if mode == "missing-source-md5" {
							tag = ""
						}
						entries = append(entries, row(11, 10, 1, "Empty", ""), row(12, 10, 0, "a.txt", tag))
					case "50":
						if created || mode == "direct-conflict" {
							entries = append(entries, row(70, 50, 1, d.Receipt.OutputFolder, ""))
						}
					case "70":
						if copied && direct {
							tag := md5
							if mode == "direct-wrong-md5" {
								tag = "d41d8cd98f00b204e9800998ecf8427f"
							}
							entries = append(entries, row(81, 70, 1, "Empty", ""), row(82, 70, 0, "a.txt", tag))
						} else if copied {
							entries = append(entries, row(80, 70, 1, "Docs", ""))
						}
					case "80":
						tag := md5
						if mode == "wrong-target-md5" {
							tag = "d41d8cd98f00b204e9800998ecf8427f"
						}
						entries = append(entries, row(81, 80, 1, "Empty", ""), row(82, 80, 0, "a.txt", tag))
					case "11", "81":
					default:
						t.Fatal("unexpected parent", r.URL)
					}
					payload, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"InfoList": entries, "Next": -1}})
					return quarkResponse(string(payload), nil), nil
				case "/b/api/file/upload_request":
					if saves != 1 || created {
						t.Fatal("creation without unique checkpoint")
					}
					var mkdir struct {
						Name      string `json:"fileName"`
						Duplicate int    `json:"duplicate"`
					}
					if json.NewDecoder(r.Body).Decode(&mkdir) != nil || mkdir.Name != d.Receipt.OutputFolder || mkdir.Duplicate != 1 {
						t.Fatal("unexpected directory creation payload", mkdir)
					}
					created = true
					if mode == "direct-reuse" {
						return quarkResponse(`{"code":0,"data":{"FileId":70,"Reuse":true}}`, nil), nil
					}
					return quarkResponse(`{"code":0,"data":{"FileId":70}}`, nil), nil
				case "/b/api/restful/goapi/v1/file/copy/async":
					if !created || copied || saves != 2 {
						t.Fatal("copy without folder checkpoint")
					}
					var in struct {
						Target int `json:"targetFileId"`
						Items  []struct {
							ID     int    `json:"fileId"`
							Type   int    `json:"type"`
							Parent int    `json:"parentFileId"`
							MD5    string `json:"etag"`
						} `json:"fileList"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Target != 70 {
						t.Fatal("wrong directory copy payload", in, err)
					}
					if direct {
						if len(in.Items) != 2 || in.Items[0].ID != 11 || in.Items[0].Type != 1 || in.Items[0].Parent != 10 ||
							in.Items[1].ID != 12 || in.Items[1].Type != 0 || in.Items[1].Parent != 10 || in.Items[1].MD5 != md5 {
							t.Fatal("direct copy did not submit the source children", in)
						}
					} else if len(in.Items) != 1 || in.Items[0].ID != 10 || in.Items[0].Type != 1 {
						t.Fatal("wrapped copy did not submit the source folder", in)
					}
					copied = true
					return quarkResponse(`{"code":0,"data":{"mode":1,"taskId":99}}`, nil), nil
				case "/b/api/restful/goapi/v1/file/copy/task":
					return quarkResponse(`{"code":0,"data":{"taskId":99,"status":2,"errorCode":0}}`, nil), nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL)
				}
			})}
			var receipt diskCopy
			entry, err := startPan123Copy(context.Background(), c, a, d, func(saved diskCopy) error {
				saved.Submitted = true
				if !validCopyManifest(saved) || !validSavedCopy(saved) {
					t.Fatal("folder copy did not create a valid manifest")
				}
				receipt = saved
				saves++
				return nil
			})
			success := mode == "success" || mode == "direct-success" || mode == "direct-empty"
			noCreate := mode == "missing-source-md5" || mode == "direct-conflict"
			if success != (err == nil) || created == noCreate ||
				(mode == "direct-empty" || mode == "direct-reuse" || noCreate) == copied {
				t.Fatal("unexpected folder copy outcome", entry, err, created, copied)
			}
			if mode == "success" && (!entry.Folder || entry.ID != "80") {
				t.Fatal("wrong copied folder", entry)
			}
			if direct && success {
				if !entry.Folder || entry.ID != "70" || entry.Name != "Docs" {
					t.Fatal("direct copy returned the wrong folder", entry)
				}
				if _, err := checkPan123Copy(context.Background(), c, a, receipt); err != nil {
					t.Fatal("direct folder receipt could not be checked", err)
				}
			}
		})
	}
}

func TestPan123CopyBatchSourceChecksDirectoryBeforeAnyWrite(t *testing.T) {
	account := Account{ID: "fixture", Kind: "pan123", Endpoint: Pan123API}
	source := FilePage{Entries: []CloudEntry{{ID: "10", Name: "A", Folder: true}, {ID: "20", Name: "B", Folder: true}}}
	for _, scenario := range []string{"complete", "destination-inside", "missing-md5"} {
		t.Run(scenario, func(t *testing.T) {
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
					t.Fatalf("unexpected preflight request %s %s", r.Method, r.URL)
				}
				entries := []map[string]any{}
				if r.URL.Query().Get("parentFileId") == "20" {
					if scenario == "missing-md5" {
						entries = append(entries, map[string]any{"FileId": 21, "ParentFileId": 20, "Type": 0, "FileName": "a.txt", "Size": 1})
					} else {
						entries = append(entries, map[string]any{"FileId": 21, "ParentFileId": 20, "Type": 1, "FileName": "Child", "Size": 0})
					}
				}
				payload, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"InfoList": entries, "Next": -1}})
				return quarkResponse(string(payload), nil), nil
			})}
			target := "50"
			if scenario == "destination-inside" {
				target = "21"
			}
			for i, entry := range source.Entries {
				row := MoveBatchEntry{Folder: true, Input: MoveFileInput{EntryID: entry.ID, Name: entry.Name, DestinationID: target}}
				err := validatePan123CopyBatchSource(context.Background(), client, account, source, row, false)
				if (err != nil) != (i == 1 && scenario != "complete") {
					t.Fatalf("unexpected preflight for %s: %v", entry.Name, err)
				}
			}
		})
	}
}
