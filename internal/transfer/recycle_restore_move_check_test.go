package transfer

import (
	"context"
	"net/http"
	"testing"
)

func TestRestoredFolderCheckOnlyRequiresUnambiguousIdentity(t *testing.T) {
	const target = `[{"FileId":42,"ParentFileId":20,"FileName":"目录","Type":1}]`
	for _, tc := range []struct{ name, origin, want string }{
		{"absent", "[]", "confirmed"},
		{"replaced", `[{"FileId":43,"ParentFileId":10,"FileName":"目录","Type":1}]`, "unknown"},
		{"renamed", `[{"FileId":42,"ParentFileId":10,"FileName":"其他","Type":1}]`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				rows := target
				if r.URL.Query().Get("parentFileId") == "10" {
					rows = tc.origin
				}
				return quarkResponse(`{"code":0,"data":{"Next":-1,"InfoList":`+rows+`}}`, nil), nil
			})}
			src := recycleRestoreSource{Purpose: "pan123-recycle-restore-v1", Entry: RecycleEntry{
				ID: "42", ParentID: "10", Name: "目录", Kind: "folder",
			}}
			result := checkRestoredMoveLocations(context.Background(), client, Account{Kind: "pan123", Endpoint: Pan123API},
				MoveFileInput{ParentID: "10", DestinationID: "20"}, src)
			if result.State != tc.want {
				t.Fatalf("got %+v, want %s", result, tc.want)
			}
		})
	}
}

func TestRestoredFileMoveCheckOnlyReadsExpectedLocations(t *testing.T) {
	const atOriginal = `[{"FileId":42,"ParentFileId":10,"FileName":"原件","Type":0,"Size":7}]`
	const atTarget = `[{"FileId":42,"ParentFileId":20,"FileName":"原件","Type":0,"Size":7}]`
	const renamed = `[{"FileId":42,"ParentFileId":10,"FileName":"改名","Type":0,"Size":7}]`
	for _, tc := range []struct {
		name, origin, target, state string
	}{
		{"still original", atOriginal, "[]", "at_original"},
		{"confirmed target", "[]", atTarget, "confirmed"},
		{"identity still present", renamed, atTarget, "unknown"},
		{"neither location", "[]", "[]", "unknown"},
		{"contradictory", atOriginal, atTarget, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/b/api/file/list/new" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var rows string
				switch r.URL.Query().Get("parentFileId") {
				case "10":
					rows = tc.origin
				case "20":
					rows = tc.target
				default:
					t.Fatal("unexpected parent")
				}
				return quarkResponse(`{"code":0,"data":{"Next":-1,"InfoList":`+rows+`}}`, nil), nil
			})}
			size := int64(7)
			src := recycleRestoreSource{Purpose: "pan123-recycle-restore-v1", Entry: RecycleEntry{
				ID: "42", ParentID: "10", Name: "原件", Kind: "file", Size: &size,
			}}
			in := MoveFileInput{ParentID: "10", DestinationID: "20"}
			result := checkRestoredMoveLocations(context.Background(), client, Account{Kind: "pan123", Endpoint: Pan123API}, in, src)
			if result.State != tc.state {
				t.Fatalf("got %+v, want %s", result, tc.state)
			}
		})
	}
}
