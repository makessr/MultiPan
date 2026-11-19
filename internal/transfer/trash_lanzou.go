package transfer

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const lanzouTrashPurpose = "lanzou-trash-file-v1"
const lanzouFolderTrashPurpose = "lanzou-trash-folder-v1"

type lanzouFolderFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lanzouFolderContents struct {
	Files []lanzouFolderFile `json:"files"`
}

func validLanzouFolderContents(contents *lanzouFolderContents) bool {
	if contents.Files == nil || len(contents.Files) > 200 {
		return false
	}
	seen := map[string]bool{}
	bytes := 0
	for _, f := range contents.Files {
		bytes += len(f.ID) + len(f.Name)
		if bytes > 16<<10 || !valid123Folder(f.ID) || f.ID == "0" || !relayName(f.Name) || seen[f.ID] {
			return false
		}
		seen[f.ID] = true
	}
	return true
}

func captureLanzouFolderContents(ctx context.Context, client *http.Client, a Account, folderID string) *lanzouFolderContents {
	// An unavailable inventory never substitutes for the separate live-folder
	// identity and subfolder checks used to authorize a move into the recycle bin.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	files, err := readLanzouFiles(ctx, client, a, folderID, true)
	if err != nil || len(files) > 200 {
		return nil
	}
	inventory := &lanzouFolderContents{Files: make([]lanzouFolderFile, 0, len(files))}
	for _, f := range files {
		inventory.Files = append(inventory.Files, lanzouFolderFile{ID: f.ID, Name: f.Name})
	}
	if !validLanzouFolderContents(inventory) {
		return nil
	}
	return inventory
}

func validLanzouTrashParent(parent string) bool {
	return parent == "-1" || valid123Folder(parent) && parent != "0"
}

func lanzouTrashEntry(parent string, entry CloudEntry) RecycleEntry {
	location := "原目录 ID " + parent
	if parent == "-1" {
		location = "根目录"
	}
	kind := "file"
	if entry.Folder {
		kind = "folder"
	}
	return RecycleEntry{ID: entry.ID, Name: entry.Name, Kind: kind, ParentID: parent, OriginalPath: location + " / " + entry.Name}
}

func lanzouTrashFolders(ctx context.Context, client *http.Client, a Account, parent string) ([]RecycleEntry, error) {
	if a.Kind != "lanzou" || a.Endpoint != lanzouRecycleOrigin || a.Secret == "" || !validLanzouTrashParent(parent) {
		return nil, errors.New("蓝奏原目录或登录无效")
	}
	page, err := listLanzouFolders(ctx, client, a, parent, true)
	if err != nil {
		return nil, err
	}
	var rows []RecycleEntry
	for _, folder := range page.Folders {
		rows = append(rows, lanzouTrashEntry(parent, CloudEntry{ID: folder.ID, Name: folder.Name, Folder: true}))
	}
	return rows, nil
}

func trashBatchLive(ctx context.Context, client *http.Client, a Account, inputs []TrashInput) ([]RecycleEntry, error) {
	parent := inputs[0].ParentID
	if a.Kind != "lanzou" {
		return trashLive(ctx, client, a, parent)
	}
	files, folders := false, false
	for _, input := range inputs {
		if input.Entry.Folder {
			folders = true
		} else {
			files = true
		}
	}
	var rows []RecycleEntry
	if folders {
		var err error
		rows, err = lanzouTrashFolders(ctx, client, a, parent)
		if err != nil {
			return nil, err
		}
	}
	if files {
		live, err := trashLive(ctx, client, a, parent)
		if err != nil {
			return nil, err
		}
		rows = append(rows, live...)
	}
	return rows, nil
}

func trashSourceLive(ctx context.Context, client *http.Client, a Account, s trashSource) ([]RecycleEntry, error) {
	if s.Purpose == lanzouFolderTrashPurpose {
		return lanzouTrashFolders(ctx, client, a, s.Input.ParentID)
	}
	return trashLive(ctx, client, a, s.Input.ParentID)
}

func checkLanzouLeafFolder(ctx context.Context, client *http.Client, a Account, s trashSource) error {
	page, err := listLanzouFolders(ctx, client, a, s.Original.ID, true)
	if err != nil {
		return err
	}
	n := len(page.Breadcrumbs)
	if n < 2 || page.Breadcrumbs[n-2].ID != s.Input.ParentID || page.Breadcrumbs[n-1].Name != s.Original.Name {
		return errors.New("蓝奏文件夹的名称或父目录已变化")
	}
	if len(page.Folders) != 0 {
		return errors.New("蓝奏只支持将没有子文件夹的文件夹移入回收站")
	}
	return nil
}

func checkLanzouTrash(ctx context.Context, client *http.Client, a Account, s trashSource) error {
	body, err := readLanzouRecyclePage(ctx, client, a, url.Values{"item": {"recycle"}, "action": {"files"}})
	if err != nil {
		return err
	}
	items, err := parseLanzouRecycle(body)
	if err != nil {
		return err
	}
	for _, r := range items {
		if r.ID == s.Original.Kind+":"+s.Original.ID {
			if r.Name != s.Original.Name {
				return errors.New("蓝奏回收站的同 ID 名称不一致或已被截短，请到官网核对")
			}
			return nil
		}
	}
	return errors.New("尚未在蓝奏回收站核对到同一项目，请到官网检查")
}
