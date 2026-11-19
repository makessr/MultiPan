package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestPan123DirectCopyBatchPreflight(t *testing.T) {
	for _, scenario := range []string{"valid", "target-conflict", "selection-conflict", "mixed", "mixed-conflict",
		"folder-child-missing-md5", "folder-child-limit", "missing-md5"} {
		t.Run(scenario, func(t *testing.T) {
			a := Account{ID: "fixture", Kind: "pan123", Endpoint: Pan123API}
			makeEntry := func(id, name string) MoveBatchEntry {
				return MoveBatchEntry{Input: MoveFileInput{
					AccountID: a.ID, EntryID: id, Name: name, ParentID: "0", DestinationID: "50",
					SourceTrail: []CloudFolder{{ID: "0"}}, DestinationTrail: []CloudFolder{{ID: "0"}, {ID: "50"}},
				}}
			}
			batch := []MoveBatchEntry{makeEntry("12", "first"), makeEntry("13", "second")}
			if scenario == "selection-conflict" {
				batch[1].Input.Name = "FIRST"
			}
			batch[1].Folder = scenario == "mixed" || scenario == "mixed-conflict" ||
				scenario == "folder-child-missing-md5" || scenario == "folder-child-limit"
			reads := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
					t.Fatal("preflight attempted a non-listing request", r.URL)
				}
				reads++
				const hash = "d41d8cd98f00b204e9800998ecf8427e"
				row := func(id, parent, kind int, name, digest string) map[string]any {
					return map[string]any{"FileId": id, "ParentFileId": parent, "FileName": name, "Type": kind, "Size": 10, "Etag": digest}
				}
				entries := []map[string]any{}
				switch r.URL.Query().Get("parentFileId") {
				case "0":
					secondHash := hash
					if scenario == "missing-md5" {
						secondHash = ""
					}
					secondKind := 0
					if batch[1].Folder {
						secondKind, secondHash = 1, ""
					}
					entries = append(entries, row(12, 0, 0, "first", hash), row(13, 0, secondKind, "second", secondHash), row(50, 0, 1, "Target", ""))
				case "13":
					switch scenario {
					case "folder-child-missing-md5":
						entries = append(entries, row(99, 13, 0, "child", ""))
					case "folder-child-limit":
						for i := 0; i < 51; i++ {
							entries = append(entries, row(100+i, 13, 0, fmt.Sprintf("child-%d", i), hash))
						}
					default:
						entries = append(entries, row(99, 13, 0, "child", hash))
					}
				case "50":
					if scenario == "target-conflict" || scenario == "mixed-conflict" {
						entries = append(entries, row(80, 50, 1, "SECOND", ""))
					}
				}
				return quarkResponse(quarkData(map[string]any{"InfoList": entries, "Next": -1}), nil), nil
			})}
			manager := &Manager{accounts: []Account{a}}
			err := manager.validateNativeCopyBatch(context.Background(), client, batch, "pan123", true)
			if (err == nil) != (scenario == "valid" || scenario == "mixed") || len(manager.copies) != 0 {
				t.Fatalf("unexpected read-only preflight outcome: %v", err)
			}
			if (reads == 0) != (scenario == "selection-conflict") {
				t.Fatalf("unexpected directory reads: %d", reads)
			}
		})
	}
}

func TestPan123DirectCopyRequiresNewIdentifiedFile(t *testing.T) {
	const hash = "d41d8cd98f00b204e9800998ecf8427e"
	for _, scenario := range []string{"success", "same-name", "no-reuse", "no-info", "changed-copy"} {
		t.Run(scenario, func(t *testing.T) {
			a := Account{ID: "fixture", Kind: "pan123", Endpoint: Pan123API}
			d := diskCopy{Direct123: true, Input: MoveBatchEntry{Input: MoveFileInput{
				AccountID: a.ID, EntryID: "12", ParentID: "0", DestinationID: "50", Name: "file",
				SourceTrail: []CloudFolder{{ID: "0"}}, DestinationTrail: []CloudFolder{{ID: "0"}, {ID: "50"}},
			}}}
			row := func(id, parent, kind int, name, md5 string) map[string]any {
				return map[string]any{"FileId": id, "ParentFileId": parent, "FileName": name, "Type": kind, "Size": 512 << 20, "Etag": md5}
			}
			requests := 0
			var checkpoint diskCopy
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/b/api/file/list/new":
					entries := []map[string]any{}
					if r.URL.Query().Get("parentFileId") == "0" {
						entries = append(entries, row(12, 0, 0, "file", hash), row(50, 0, 1, "Target", ""))
					} else if scenario == "same-name" || requests > 0 {
						md5 := hash
						if scenario == "changed-copy" {
							md5 = "d41d8cd98f00b204e9800998ecf8427f"
						}
						entries = append(entries, row(75, 50, 0, "file", md5))
					}
					return quarkResponse(quarkData(map[string]any{"InfoList": entries, "Next": -1}), nil), nil
				case "/b/api/file/upload_request":
					requests++
					if !checkpoint.Submitted || checkpoint.Pan123.FileID != "" {
						t.Fatal("write without earlier checkpoint")
					}
					var body struct {
						Duplicate int    `json:"duplicate"`
						Parent    int    `json:"parentFileId"`
						Size      int64  `json:"size"`
						ETag      string `json:"etag"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Duplicate != 1 || body.Parent != 50 || body.ETag != hash || body.Size != 512<<20 {
						t.Fatalf("incorrect non-overwriting reuse payload: %#v, %v", body, err)
					}
					reuse := map[string]any{"Reuse": scenario != "no-reuse", "FileId": 75}
					if scenario != "no-info" {
						reuse["Info"] = row(75, 50, 0, "file", hash)
					}
					return quarkResponse(quarkData(reuse), nil), nil
				default:
					t.Fatal("unexpected write or upload", r.URL)
					return nil, nil
				}
			})}
			entry, err := startPan123DirectCopy(context.Background(), client, a, d, func(saved diskCopy) error {
				if !validSavedCopy(saved) || !validCopyManifest(saved) {
					t.Fatal("invalid checkpoint")
				}
				checkpoint = saved
				var proof = *saved.Pan123
				checkpoint.Pan123 = &proof
				return nil
			})
			wantSuccess := scenario == "success"
			if (err == nil) != wantSuccess || wantSuccess && entry.ID != "75" || (requests == 0) != (scenario == "same-name") {
				t.Fatalf("unexpected result: %v, %d requests, entry ID %q", err, requests, entry.ID)
			}
			if requests > 0 {
				_, err = checkPan123DirectCopy(context.Background(), client, a, checkpoint)
				if (err == nil) != wantSuccess || requests != 1 {
					t.Fatalf("unexpected read-only recovery result: %v, %d requests", err, requests)
				}
			}
		})
	}
}
