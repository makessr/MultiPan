package transfer

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestMoveBatchPreflightChecksWholeDAVGroupWithoutWrites(t *testing.T) {
	for _, kind := range []string{"webdav", "ctdav"} {
		for _, mode := range []string{"ready", "later-conflict", "later-missing", "duplicate", "mixed-account", "wrong-kind", "descendant", "too-many"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				a := Account{ID: "a", Kind: kind, Endpoint: "https://dav.fixture/base", Username: "user", Secret: "fixture"}
				fixture, tr := folderMoveTransport(t, a, "/目标", "success")
				fixture.resources["/来源/第二目录"] = true
				fixture.resources["/来源/file.txt"] = false
				entries := []MoveBatchEntry{
					{Folder: true, Input: MoveFileInput{AccountID: "a", ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/资料 & 备份", Name: "资料 & 备份"}},
					{Folder: true, Input: MoveFileInput{AccountID: "a", ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/第二目录", Name: "第二目录"}},
					{Input: MoveFileInput{AccountID: "a", ParentID: "/来源", DestinationID: "/目标", EntryID: "/来源/file.txt", Name: "file.txt"}},
				}
				switch mode {
				case "later-conflict":
					fixture.resources["/目标/第二目录"] = false
				case "later-missing":
					delete(fixture.resources, "/来源/第二目录")
				case "duplicate":
					entries[1] = entries[0]
				case "mixed-account":
					entries[1].Input.AccountID = "other"
				case "wrong-kind":
					entries[1].Folder = false
				case "descendant":
					for i := range entries {
						entries[i].Input.DestinationID = "/来源/资料 & 备份/子目录"
					}
				case "too-many":
					for len(entries) <= 50 {
						entries = append(entries, entries[0])
					}
				}
				e := validateAccountMoveBatch(context.Background(), &http.Client{Transport: tr}, a, entries)
				if (e == nil) != (mode == "ready") {
					t.Fatal(mode, e)
				}
				if fixture.writes != 0 {
					t.Fatal("preflight mutated remote", fixture.writes)
				}
			})
		}
	}
}

func TestMoveBatchRejectsEmptyAndUnsupported(t *testing.T) {
	for _, entries := range [][]MoveBatchEntry{nil, {{Input: MoveFileInput{AccountID: "a", ParentID: "0", DestinationID: "1", EntryID: "2", Name: "dir"}, Folder: true}}} {
		e := validateAccountMoveBatch(context.Background(), nil, Account{ID: "a", Kind: "lanzou"}, entries)
		if e == nil || !strings.Contains(e.Error(), "移动") {
			t.Fatal(e)
		}
	}
}
