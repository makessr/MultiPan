package transfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func folderFixture() map[string][]CloudEntry {
	return map[string][]CloudEntry{"/": {{ID: "/R", Name: "R", Folder: true}}, "/R": {{ID: "/R/a.txt", Name: "a.txt"}, {ID: "/R/empty", Name: "empty", Folder: true}, {ID: "/R/sub", Name: "sub", Folder: true}}, "/R/empty": {}, "/R/sub": {{ID: "/R/sub/b.txt", Name: "b.txt"}, {ID: "/R/sub/c.txt", Name: "c.txt"}}}
}
func TestFolderPlanAndSave(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "existing", "replaced-directory"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tree := folderFixture()
			plan, e := planDAVDownload(ctx, DownloadInput{AccountID: "a", ParentID: "/", EntryID: "/R", Name: "R"}, func(_ context.Context, id string) (FilePage, error) { return FilePage{Entries: tree[id]}, nil }, nil)
			if e != nil {
				t.Fatal(e)
			}
			parent := t.TempDir()
			calls := 0
			if mode == "existing" {
				os.Mkdir(filepath.Join(parent, "R"), 0700)
				os.WriteFile(filepath.Join(parent, "R", "marker"), []byte("original"), 0600)
			}
			result, e := runDirectoryDownload(ctx, plan, parent, func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
				calls++
				if in.AccountID != "a" || !strings.HasPrefix(in.EntryID, "/R/") {
					t.Error("wrong source")
				}
				if mode == "failure" && calls == 1 {
					return DownloadResult{}, errors.New("fixture failure")
				}
				if mode == "cancel" && calls == 2 {
					cancel()
					return DownloadResult{}, context.Canceled
				}
				if mode == "replaced-directory" && calls == 1 {
					dir := filepath.Join(parent, "R", "sub")
					os.Rename(dir, dir+"-old")
					os.Mkdir(dir, 0700)
				}
				if e := os.WriteFile(dest, []byte("file"), 0600); e != nil {
					return DownloadResult{}, e
				}
				p(DownloadProgress{Received: 4, Total: 4})
				return DownloadResult{Path: dest, Bytes: 4, Verification: "unverified"}, nil
			}, nil)
			if mode == "existing" {
				if e == nil || calls != 0 {
					t.Fatal("merged existing root")
				}
				b, _ := os.ReadFile(filepath.Join(parent, "R", "marker"))
				if string(b) != "original" {
					t.Fatal("overwrote root")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if len(result.Items) != 3 {
				t.Fatal("missing per-file results")
			}
			info, e := os.Stat(filepath.Join(parent, "R", "empty"))
			if e != nil || !info.IsDir() {
				t.Fatal("empty directory lost")
			}
			if mode == "success" {
				for _, item := range result.Items {
					if item.Status != "saved" {
						t.Fatal(item)
					}
				}
			}
			if mode == "failure" && (result.Items[0].Status != "failed" || result.Items[2].Status != "saved") {
				t.Fatal("failure aborted other files")
			}
			if mode == "cancel" && (!result.Cancelled || calls != 2 || result.Items[2].Status != "skipped") {
				t.Fatal("cancellation started another file")
			}
			if mode == "replaced-directory" {
				if calls != 1 || result.Items[1].Status != "failed" {
					t.Fatal("wrote into replaced directory")
				}
			}
		})
	}
}
func TestFolderScanRejectsUnsafeTree(t *testing.T) {
	for _, mode := range []string{"collision", "escape", "duplicate", "missing-root", "scan-error", "depth", "limit", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			tree := folderFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "collision":
				tree["/R"] = []CloudEntry{{ID: "/R/x?", Name: "x?"}, {ID: "/R/x*", Name: "x*"}}
			case "escape":
				tree["/R"] = []CloudEntry{{ID: "/outside", Name: "outside"}}
			case "duplicate":
				tree["/R"] = append(tree["/R"], tree["/R"][0])
			case "missing-root":
				tree["/"] = nil
			case "depth":
				id := "/R"
				for i := 0; i < 22; i++ {
					next := id + "/d"
					tree[id] = []CloudEntry{{ID: next, Name: "d", Folder: true}}
					id = next
				}
			case "limit":
				tree["/R"] = nil
				for i := 0; i < 2001; i++ {
					name := fmt.Sprintf("%d.txt", i)
					tree["/R"] = append(tree["/R"], CloudEntry{ID: "/R/" + name, Name: name})
				}
			case "cancelled":
				cancel()
			}
			_, e := planDAVDownload(ctx, DownloadInput{AccountID: "a", ParentID: "/", EntryID: "/R", Name: "R"}, func(_ context.Context, id string) (FilePage, error) {
				if mode == "scan-error" && id == "/R/sub" {
					return FilePage{}, errors.New("fixture list failure")
				}
				return FilePage{Entries: tree[id]}, nil
			}, nil)
			if e == nil {
				t.Fatal("unsafe tree accepted")
			}
		})
	}
	parent := t.TempDir()
	if _, e := runDirectoryDownload(context.Background(), directoryPlan{name: "../escape"}, parent, nil, nil); e == nil {
		t.Fatal("unsafe local root accepted")
	}
	m := &Manager{accounts: []Account{{ID: "a", Kind: "ctfile"}}}
	if _, e := m.DownloadFolder(context.Background(), DownloadInput{AccountID: "a"}, parent, nil); e == nil {
		t.Fatal("unsupported provider accepted")
	}
}
