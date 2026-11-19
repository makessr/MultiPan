package transfer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLanzouCopyUsesIsolatedFolderAndChecksOriginal(t *testing.T) {
	for _, mode := range []string{"success", "different-content", "no-length", "at-limit", "over-limit"} {
		t.Run(mode, func(t *testing.T) {
			const filename, content = "file.zip", "fixture bytes"
			a := Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
			d := diskCopy{Input: MoveBatchEntry{Input: MoveFileInput{
				AccountID: a.ID, ParentID: "-1", EntryID: "123", Name: filename, DestinationID: "50",
			}}, Receipt: CopyReceipt{ID: "test", OutputFolder: lanzouCopyFolderPrefix + "test"}}
			creates, uploads := 0, 0
			var saved diskCopy
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "up.woozooo.com" {
					switch r.URL.Path {
					case "/mydisk.php":
						return quarkResponse("", nil), nil
					case "/html5up.php":
						if saved.Lanzou == nil || saved.Lanzou.FolderID != "70" || saved.Lanzou.Acknowledged {
							t.Fatal("upload checkpoint absent")
						}
						req := r.Clone(r.Context())
						req.Method = http.MethodPost
						if err := req.ParseMultipartForm(1 << 20); err != nil {
							t.Fatal(err)
						}
						part, _, err := req.FormFile("upload_file")
						if err != nil {
							t.Fatal(err)
						}
						defer part.Close()
						raw, _ := io.ReadAll(part)
						if req.FormValue("folder_id_bb_n") != "70" || string(raw) != content {
							t.Fatal("wrong copy upload")
						}
						uploads++
						return quarkResponse(`{"zt":1}`, nil), nil
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					switch r.FormValue("task") {
					case "2":
						if !saved.Submitted || r.FormValue("parent_id") != "50" {
							t.Fatal("unexpected directory write")
						}
						creates++
						return quarkResponse(`{"zt":1}`, nil), nil
					case "47":
						rows, crumbs := []any{}, []any{map[string]any{"folderid": "50", "name": "目标"}}
						if r.FormValue("folder_id") == "50" && creates > 0 {
							rows = append(rows, map[string]any{"fol_id": "70", "name": d.Receipt.OutputFolder})
						}
						if r.FormValue("folder_id") == "70" {
							crumbs = append(crumbs, map[string]any{"folderid": "70", "name": d.Receipt.OutputFolder})
						}
						return jsonResponse(map[string]any{"zt": 1, "text": rows, "info": crumbs}), nil
					case "5":
						var id string
						switch r.FormValue("folder_id") {
						case "-1":
							id = "123"
						case "70":
							if uploads > 0 {
								id = "124"
							}
						}
						if id != "" {
							entries := []any{map[string]any{"id": id, "name_all": filename}}
							if id == "123" {
								entries = append(entries, map[string]any{"id": "125", "name_all": "second.zip"})
							}
							return jsonResponse(map[string]any{"zt": 1, "text": entries}), nil
						}
						return quarkResponse(`{"zt":2,"text":[]}`, nil), nil
					case "22":
						return jsonResponse(map[string]any{"zt": 1, "info": map[string]any{
							"is_newd": "https://share.example", "f_id": "i" + r.FormValue("file_id"), "onof": "0",
						}}), nil
					default:
						t.Fatal("unexpected account request", r.URL.Path, r.FormValue("task"))
					}
				}
				switch r.URL.Host {
				case "share.example":
					switch r.URL.Path {
					case "/i123", "/i124", "/i125":
						return quarkResponse(`<iframe src="/fn?token=`+r.URL.Path[1:]+`"></iframe>`, nil), nil
					case "/fn":
						return quarkResponse(`data:{'sign':'abc123',ves:1}`, nil), nil
					case "/ajaxm.php":
						return jsonResponse(map[string]any{"zt": 1, "dom": "https://cdn.example", "url": "token"}), nil
					}
				case "cdn.example":
					body := content
					if uploads > 0 && mode == "different-content" && strings.Contains(r.Header.Get("Referer"), "i124") {
						body = "changed bytes"
					}
					resp := quarkResponse(body, nil)
					resp.ContentLength = int64(len(body))
					if strings.Contains(r.Header.Get("Referer"), "i125") {
						switch mode {
						case "at-limit":
							resp.ContentLength = 200_000_000
						case "over-limit":
							resp.ContentLength = 200_000_001
						}
					}
					if mode == "no-length" || mode == "different-content" && strings.Contains(r.Header.Get("Referer"), "i125") {
						resp.ContentLength = -1
					}
					return resp, nil
				}
				t.Fatal("unexpected fixture request", r.URL)
				return nil, nil
			})}
			second := d.Input
			second.Input.EntryID, second.Input.Name = "125", "second.zip"
			m := &Manager{accounts: []Account{a}}
			err := m.validateNativeCopyBatch(context.Background(), client, []MoveBatchEntry{d.Input, second}, "lanzou", false)
			if (err == nil) != (mode == "success" || mode == "at-limit") || creates != 0 || uploads != 0 || len(m.copies) != 0 {
				t.Fatal("batch preflight issued writes or accepted an unreadable size", err)
			}
			if mode == "at-limit" || mode == "over-limit" {
				return
			}
			result, err := startLanzouCopy(context.Background(), client, a, d, func(next diskCopy) error {
				if !validSavedCopy(next) {
					t.Fatal("invalid checkpoint")
				}
				saved = next
				return nil
			})
			if mode == "no-length" {
				if err == nil || creates != 0 || uploads != 0 {
					t.Fatal("unknown size reached cloud writes", err)
				}
				return
			}
			if (err == nil) != (mode == "success") || creates != 1 || uploads != 1 || mode == "success" && result.ID != "124" {
				t.Fatal("unexpected copy outcome", result, err, creates, uploads)
			}
			_, err = checkLanzouCopy(context.Background(), client, a, saved)
			if (err == nil) != (mode == "success") || uploads != 1 {
				t.Fatal("check changed the copy", err, uploads)
			}
		})
	}
}

func TestLanzouFolderCopyPreservesNestedEmptyFolder(t *testing.T) {
	a := Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com"}
	d := diskCopy{Input: MoveBatchEntry{Folder: true, Input: MoveFileInput{
		AccountID: a.ID, ParentID: "-1", EntryID: "10", Name: "Source", DestinationID: "50",
	}}, Receipt: CopyReceipt{ID: "test", OutputFolder: lanzouCopyFolderPrefix + "test"}}
	folders := map[string][]CloudFolder{
		"-1": {{ID: "10", Name: "Source"}, {ID: "50", Name: "Target"}},
		"10": {{ID: "11", Name: "Empty"}},
	}
	trails := map[string][]CloudFolder{
		"-1": {}, "10": {{ID: "10", Name: "Source"}}, "50": {{ID: "50", Name: "Target"}},
		"11": {{ID: "10", Name: "Source"}, {ID: "11", Name: "Empty"}},
	}
	created := 0
	var receipt diskCopy
	c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.FormValue("task") {
		case "47":
			id := r.FormValue("folder_id")
			entries, crumbs := []any{}, []any{}
			for _, item := range folders[id] {
				entries = append(entries, map[string]string{"fol_id": item.ID, "name": item.Name})
			}
			for _, item := range trails[id] {
				crumbs = append(crumbs, map[string]string{"folderid": item.ID, "name": item.Name})
			}
			return jsonResponse(map[string]any{"zt": 1, "text": entries, "info": crumbs}), nil
		case "5":
			return quarkResponse(`{"zt":2,"text":[]}`, nil), nil
		case "2":
			parent, name := r.FormValue("parent_id"), r.FormValue("folder_name")
			if !receipt.Submitted || !validSavedCopy(receipt) {
				t.Fatal("write without receipt checkpoint")
			}
			var folder CloudFolder
			switch {
			case created == 0 && parent == "50" && name == d.Receipt.OutputFolder:
				folder = CloudFolder{ID: "70", Name: name}
			case created == 1 && parent == "70" && name == "Empty" && receipt.Lanzou.FolderID == "70":
				folder = CloudFolder{ID: "71", Name: name}
			default:
				t.Fatal("unexpected directory creation", parent, name)
			}
			created++
			folders[parent] = append(folders[parent], folder)
			trails[folder.ID] = append(append([]CloudFolder{}, trails[parent]...), folder)
			return quarkResponse(`{"zt":1}`, nil), nil
		default:
			t.Fatal("unexpected request", r.URL, r.Form)
			return nil, nil
		}
	})}
	ctx := context.Background()
	inside := d.Input
	inside.Input.DestinationID = "11"
	if _, _, err := planLanzouFolderCopy(ctx, c, a, inside); err == nil || created != 0 {
		t.Fatal("accepted descendant as destination", err)
	}
	result, err := startLanzouCopy(ctx, c, a, d, func(next diskCopy) error {
		if !validSavedCopy(next) {
			t.Fatal("invalid folder receipt")
		}
		receipt = next
		return nil
	})
	if err != nil || !result.Folder || result.ID != "70" || created != 2 {
		t.Fatal("nested empty folder was not copied", result, err, created)
	}
	if _, err := checkLanzouCopy(ctx, c, a, receipt); err != nil || created != 2 {
		t.Fatal("receipt could not be rechecked read-only", err, created)
	}
}
