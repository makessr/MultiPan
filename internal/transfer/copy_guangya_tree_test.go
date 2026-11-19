package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestGuangyaMixedCopyBatchPreflight(t *testing.T) {
	for _, scenario := range []string{"complete", "at-capacity", "over-capacity", "incomplete-second-folder", "recursive-second-folder",
		"direct-folders", "direct-conflict", "direct-mixed"} {
		t.Run(scenario, func(t *testing.T) {
			direct := strings.HasPrefix(scenario, "direct-")
			a := Account{ID: "fixture", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
			manager := &Manager{accounts: []Account{a}}
			target := "destination"
			destination := []CloudFolder{{ID: guangyaRoot}, {ID: target}}
			if scenario == "recursive-second-folder" {
				target = "nested"
				destination = []CloudFolder{{ID: guangyaRoot}, {ID: "second"}, {ID: target}}
			}
			makeRow := func(id, name string, folder bool) MoveBatchEntry {
				return MoveBatchEntry{Folder: folder, Input: MoveFileInput{
					AccountID: a.ID, ParentID: guangyaRoot, EntryID: id, Name: name, DestinationID: target,
					SourceTrail: []CloudFolder{{ID: guangyaRoot}}, DestinationTrail: destination,
				}}
			}
			rows := []MoveBatchEntry{makeRow("first", "A", true), makeRow("file", "文件", false), makeRow("second", "B", true)}
			if direct && scenario != "direct-mixed" {
				rows = []MoveBatchEntry{rows[0], rows[2]}
			}
			entry := func(id, parent, name string, kind int) map[string]any {
				return map[string]any{"fileId": id, "parentId": parent, "fileName": name, "resType": kind, "fileSize": 1}
			}
			reads := map[string]int{}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/userres/v1/file/get_file_list" {
					t.Fatal("preflight attempted an action other than listing", r.URL)
				}
				var input struct {
					Parent string `json:"parentId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				reads[input.Parent]++
				entries := []map[string]any{}
				switch input.Parent {
				case "":
					entries = append(entries, entry("first", "", "A", 2), entry("file", "", "文件", 1),
						entry("second", "", "B", 2), entry("destination", "", "目标", 2))
				case "first":
					if scenario == "at-capacity" || scenario == "over-capacity" {
						first, second := entry("part-1", "first", "f1", 1), entry("part-2", "first", "f2", 1)
						first["fileSize"] = int64(1 << 30)
						second["fileSize"] = int64(1 << 30)
						if scenario == "over-capacity" {
							second["fileSize"] = int64(1<<30) + 1
						}
						entries = append(entries, first, second)
					}
				case "second":
					entries = append(entries, entry("nested", "second", "子目录", 2))
				case "destination":
					if scenario == "direct-conflict" {
						entries = append(entries, entry("existing", "destination", "b", 1))
					}
				}
				total := len(entries)
				if scenario == "incomplete-second-folder" && input.Parent == "second" {
					total++
				}
				return quarkResponse(quarkData(map[string]any{"list": entries, "total": total}), nil), nil
			})}
			err := manager.validateNativeCopyBatch(context.Background(), client, rows, "guangya", direct)
			if (err == nil) != (scenario == "complete" || scenario == "at-capacity" || scenario == "direct-folders") {
				t.Fatalf("unexpected batch preflight result: %v", err)
			}
			if scenario == "direct-mixed" {
				if len(reads) != 0 {
					t.Fatal("unsupported mixed batch issued a request")
				}
				return
			}
			if len(manager.copies) != 0 || reads["first"] == 0 ||
				(scenario == "over-capacity") != (reads["second"] == 0) ||
				scenario == "complete" && reads["nested"] == 0 {
				t.Fatal("preflight failed to scan the selected trees without creating receipts")
			}
		})
	}
}

func TestGuangyaCopyTreeReceiptSizeLimit(t *testing.T) {
	tree := []CopyManifestItem{
		{NativeID: "source", Folder: true},
		{Path: "f1", NativeID: "file-1", Size: 1 << 30},
		{Path: "f2", NativeID: "file-2", Size: 1 << 30},
	}
	if !validGuangyaCopyTree(tree, "source") {
		t.Fatal("a 2 GiB directory manifest could not be read back")
	}
	tree[2].Size++
	if validGuangyaCopyTree(tree, "source") {
		t.Fatal("a directory manifest one byte over 2 GiB was accepted")
	}
}

func TestGuangyaFolderCopyVerifiesTreeAndDoesNotReplay(t *testing.T) {
	for _, scenario := range []string{"success", "changed-content", "partial-source", "recursive-target",
		"direct-folder", "direct-empty", "direct-conflict", "direct-changed-content"} {
		t.Run(scenario, func(t *testing.T) {
			direct := strings.HasPrefix(scenario, "direct-")
			a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
			target := "target"
			targetTrail := []CloudFolder{{ID: guangyaRoot}, {ID: target}}
			if scenario == "recursive-target" {
				target = "empty"
				targetTrail = []CloudFolder{{ID: guangyaRoot}, {ID: "docs"}, {ID: target}}
			}
			d := diskCopy{Input: MoveBatchEntry{Folder: true, Input: MoveFileInput{
				AccountID: a.ID, ParentID: guangyaRoot, EntryID: "docs", DestinationID: target, Name: "资料",
				SourceTrail: []CloudFolder{{ID: guangyaRoot}}, DestinationTrail: targetTrail,
			}}, Receipt: CopyReceipt{ID: "receipt", OutputFolder: guangyaCopyFolderPrefix + "receipt"}}
			d.DirectGuangya = direct
			if direct {
				d.Receipt.OutputFolder = "资料"
			}
			entry := func(id, parent, name string, typ int) map[string]any {
				return map[string]any{"fileId": id, "parentId": parent, "fileName": name, "resType": typ, "fileSize": 3}
			}
			creates, copies := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "fixture.example" {
					content := "abc"
					if (scenario == "changed-content" || scenario == "direct-changed-content") && r.URL.Path == "/copy-file" {
						content = "abd"
					}
					resp := quarkResponse(content, nil)
					resp.ContentLength = 3
					return resp, nil
				}
				var req struct {
					Parent       string   `json:"parentId"`
					IDs          []string `json:"fileIds"`
					ID           string   `json:"fileId"`
					DirName      string   `json:"dirName"`
					FailIfExists bool     `json:"failIfNameExist"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					rows := []map[string]any{}
					switch req.Parent {
					case "":
						rows = append(rows, entry("docs", "", "资料", 2), entry("target", "", "目标", 2))
					case "docs":
						if scenario != "direct-empty" {
							rows = append(rows, entry("empty", "docs", "空目录", 2), entry("source-file", "docs", "内容.txt", 1))
						}
					case "target":
						if creates > 0 {
							rows = append(rows, entry("out", "target", d.Receipt.OutputFolder, 2))
						}
						if scenario == "direct-conflict" {
							rows = append(rows, entry("old", "target", d.Receipt.OutputFolder, 2))
						}
					case "out":
						if copies > 0 && direct {
							rows = append(rows, entry("copy-empty", "out", "空目录", 2), entry("copy-file", "out", "内容.txt", 1))
						} else if copies > 0 {
							rows = append(rows, entry("copy", "out", "资料", 2))
						}
					case "copy":
						rows = append(rows, entry("copy-empty", "copy", "空目录", 2), entry("copy-file", "copy", "内容.txt", 1))
					}
					total := len(rows)
					if scenario == "partial-source" && req.Parent == "docs" {
						total++
					}
					return quarkResponse(quarkData(map[string]any{"list": rows, "total": total}), nil), nil
				case "/userres/v1/get_res_download_url":
					return quarkResponse(quarkData(map[string]any{"signedURL": "https://fixture.example/" + req.ID}), nil), nil
				case "/userres/v1/file/create_dir":
					if req.DirName != d.Receipt.OutputFolder || !req.FailIfExists {
						t.Fatal("directory creation missing name or conflict protection", req)
					}
					creates++
					return quarkResponse(quarkData(map[string]any{"fileId": "out"}), nil), nil
				case "/userres/v1/file/copy_file":
					copies++
					expected := []string{"docs"}
					if direct {
						expected = []string{"source-file", "empty"}
					}
					if req.Parent != "out" || !slices.Equal(req.IDs, expected) {
						t.Fatal("unexpected folder copy", req)
					}
					return quarkResponse(quarkData(map[string]any{"taskId": "copy-task"}), nil), nil
				case "/userres/v1/get_task_status":
					return quarkResponse(quarkData(map[string]any{"status": 2}), nil), nil
				default:
					t.Fatal("unexpected request", r.URL)
					return nil, nil
				}
			})}
			var receipt diskCopy
			result, err := startGuangyaCopy(context.Background(), client, a, d, func(saved diskCopy) error {
				if !validSavedCopy(saved) || !validCopyManifest(saved) {
					t.Fatal("invalid saved folder proof")
				}
				receipt = saved
				return nil
			})
			if scenario == "partial-source" || scenario == "recursive-target" || scenario == "direct-conflict" {
				if err == nil || creates != 0 || copies != 0 {
					t.Fatalf("unsafe copy: %v, %d creates, %d copies", err, creates, copies)
				}
				return
			}
			success := scenario == "success" || scenario == "direct-folder" || scenario == "direct-empty"
			expectedID, expectedCopies := "copy", 1
			if direct {
				expectedID = "out"
			}
			if scenario == "direct-empty" {
				expectedCopies = 0
			}
			if (err == nil) != success || success && result.ID != expectedID || creates != 1 || copies != expectedCopies {
				t.Fatalf("unexpected result: %v, %d creates, %d copies", err, creates, copies)
			}
			_, err = checkGuangyaCopy(context.Background(), client, a, receipt)
			if (err == nil) != success || creates != 1 || copies != expectedCopies {
				t.Fatalf("read-only check did not preserve the original result: %v", err)
			}
		})
	}
}
