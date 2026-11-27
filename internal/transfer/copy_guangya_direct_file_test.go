package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestGuangyaDirectFileCopyVerifiesHashWithoutSubfolder(t *testing.T) {
	for _, scenario := range []string{"success", "conflict", "changed-content"} {
		t.Run(scenario, func(t *testing.T) {
			a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
			d := diskCopy{Input: MoveBatchEntry{Input: MoveFileInput{
				AccountID: a.ID, ParentID: guangyaRoot, EntryID: "file", DestinationID: "target", Name: "a.txt",
				SourceTrail:      []CloudFolder{{ID: guangyaRoot}},
				DestinationTrail: []CloudFolder{{ID: guangyaRoot}, {ID: "target"}},
			}}, Receipt: CopyReceipt{ID: "receipt"}}
			d.DirectGuangya = true
			if !validSavedGuangyaCopy(d) {
				t.Fatal("a direct single-file copy was rejected before it started")
			}
			entry := func(id, parent, name string, typ int) map[string]any {
				return map[string]any{"fileId": id, "parentId": parent, "fileName": name, "resType": typ, "fileSize": 3}
			}
			creates, copies := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "fixture.example" {
					content := "abc"
					if scenario == "changed-content" && r.URL.Path == "/copy-file" {
						content = "abd"
					}
					resp := quarkResponse(content, nil)
					resp.ContentLength = 3
					return resp, nil
				}
				var req struct {
					Parent string   `json:"parentId"`
					IDs    []string `json:"fileIds"`
					ID     string   `json:"fileId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					rows := []map[string]any{}
					switch req.Parent {
					case "":
						rows = append(rows, entry("file", "", "a.txt", 1), entry("target", "", "目标", 2))
					case "target":
						if scenario == "conflict" {
							rows = append(rows, entry("existing", "target", "a.txt", 1))
						}
						if copies > 0 {
							rows = append(rows, entry("copy-file", "target", "a.txt", 1))
						}
					default:
						t.Fatal("unexpected listing", req.Parent)
					}
					return quarkResponse(quarkData(map[string]any{"list": rows, "total": len(rows)}), nil), nil
				case "/userres/v1/get_res_download_url":
					return quarkResponse(quarkData(map[string]any{"signedURL": "https://fixture.example/" + req.ID}), nil), nil
				case "/userres/v1/file/create_dir":
					creates++
					t.Fatal("a direct single-file copy created a subfolder", req)
					return nil, nil
				case "/userres/v1/file/copy_file":
					copies++
					if req.Parent != "target" || !slices.Equal(req.IDs, []string{"file"}) {
						t.Fatal("unexpected single-file copy request", req)
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
				if !validSavedGuangyaCopy(saved) {
					t.Fatal("invalid saved direct single-file proof")
				}
				receipt = saved
				return nil
			})
			if scenario == "conflict" {
				if err == nil || creates != 0 || copies != 0 {
					t.Fatalf("unsafe copy over a same-name file: %v, %d creates, %d copies", err, creates, copies)
				}
				return
			}
			success := scenario == "success"
			if (err == nil) != success || creates != 0 || copies != 1 {
				t.Fatalf("unexpected result: %v, %d creates, %d copies", err, creates, copies)
			}
			if !success {
				return
			}
			if result.ID != "copy-file" || result.Folder || receipt.Guangya.FolderID != "" {
				t.Fatalf("unexpected copy result: %+v", result)
			}
			if _, err := checkGuangyaCopy(context.Background(), client, a, receipt); err != nil || creates != 0 || copies != 1 {
				t.Fatalf("read-only check did not preserve the original result: %v", err)
			}
		})
	}
}
