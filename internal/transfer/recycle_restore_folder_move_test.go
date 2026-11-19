package transfer

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestRestoredFolderMoveProviderIdentity(t *testing.T) {
	for _, tc := range []struct {
		kind, want string
	}{
		{"pan123", "42"}, {"quark", "live-fid"}, {"baidu", "/old/docs"}, {"guangya", "42"}, {"lanzou", ""},
	} {
		source := recycleRestoreSource{Purpose: tc.kind + "-recycle-restore-v1",
			Entry: RecycleEntry{Kind: "folder", ID: "42", FileID: "live-fid", OriginalPath: "/old/docs"},
		}
		if got := restoredFolderID(source); got != tc.want {
			t.Errorf("%s folder returned %q, want %q", tc.kind, got, tc.want)
		}
		source.Entry.Kind = "file"
		if got := restoredFolderID(source); got != "" {
			t.Errorf("%s returned a file as a folder: %q", tc.kind, got)
		}
	}
}

func TestRestoredBaiduFolderRechecksOriginalAndTargetObject(t *testing.T) {
	for _, mode := range []string{"success", "wrong-source-id", "same-name", "wrong-target-id"} {
		t.Run(mode, func(t *testing.T) {
			a := Account{ID: "account", Kind: "baidu", Endpoint: BaiduAPI, Secret: "fixture"}
			receipt := recycleRestoreSource{Purpose: "baidu-recycle-restore-v1",
				Entry: RecycleEntry{ID: "42", Name: "Docs", Kind: "folder", OriginalPath: "/old/Docs"}}
			in := MoveFileInput{AccountID: a.ID, ParentID: "/old", DestinationID: "/new", EntryID: "/old/Docs", Name: "Docs", restoredSource: &receipt}
			writes := 0
			c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/gettemplatevariable":
					return quarkResponse(`{"errno":0,"result":{"bdstoken":"fixture-token"}}`, nil), nil
				case "/api/list":
					parent := r.URL.Query().Get("dir")
					rows := ""
					if parent == "/old" && writes == 0 {
						id := 42
						if mode == "wrong-source-id" {
							id = 43
						}
						rows = fmt.Sprintf(`{"fs_id":%d,"path":"/old/Docs","server_filename":"Docs","isdir":1}`, id)
					}
					if parent == "/new" && mode == "same-name" {
						rows = `{"fs_id":43,"path":"/new/docs","server_filename":"docs","isdir":1}`
					}
					if parent == "/new" && writes > 0 {
						id := 42
						if mode == "wrong-target-id" {
							id = 43
						}
						rows = fmt.Sprintf(`{"fs_id":%d,"path":"/new/Docs","server_filename":"Docs","isdir":1}`, id)
					}
					return quarkResponse(`{"errno":0,"list":[`+rows+`]}`, nil), nil
				case "/api/filemanager":
					writes++
					if r.Method != http.MethodPost || r.URL.Query().Get("opera") != "move" ||
						r.ParseForm() != nil || r.Form.Get("ondup") != "fail" {
						t.Fatal("unexpected folder move payload", r.URL, r.Form)
					}
					if mode == "wrong-target-id" {
						return nil, fmt.Errorf("missing response")
					}
					return quarkResponse(`{"errno":0}`, nil), nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL)
				}
			})}
			got, err := moveAccountEntry(context.Background(), c, a, in, true, nil)
			if (mode == "success") != (err == nil) {
				t.Fatal("unexpected move result", got, err)
			}
			wantWrite := mode == "success" || mode == "wrong-target-id"
			if writes > 1 || (writes == 1) != wantWrite {
				t.Fatal("wrong number of move requests", writes)
			}
		})
	}
}
