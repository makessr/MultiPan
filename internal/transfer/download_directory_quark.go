package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

func downloadQuarkDirectory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	if account.Endpoint != QuarkAPI || in.AccountID == "" || !validQuarkFolder(in.ParentID) || !validQuarkFolder(in.EntryID) || in.EntryID == "0" || in.EntryID == in.ParentID || in.Name == "" {
		return BatchDownloadResult{}, errors.New("夸克文件夹下载来源无效 [QK-DIR/0.30.0]")
	}
	c := newQuarkClient(client, account)
	list := func(ctx context.Context, id string) (FilePage, error) {
		entries, e := c.list(ctx, id)
		if e != nil {
			return FilePage{}, e
		}
		page := FilePage{CurrentID: id, Entries: []CloudEntry{}}
		for _, f := range entries {
			if !quarkDirectoryParentMatches(f.ParentID, id) {
				return FilePage{}, errors.New("夸克目录条目的父目录不符，未开始保存 [QK-DIR/0.30.0]")
			}
			if !validQuarkFolder(f.FID) || f.FID == "0" || f.Name == "" || (f.Size != nil && *f.Size < 0) {
				return FilePage{}, errors.New("夸克目录条目信息无效，未开始保存 [QK-DIR/0.30.0]")
			}
			page.Entries = append(page.Entries, CloudEntry{ID: f.FID, Name: f.Name, Folder: f.isDir(), Size: f.Size, revision: string(f.UpdatedAt)})
		}
		return page, nil
	}
	plan, e := planDirectoryDownload(ctx, in, list, update, func(parent string, entry CloudEntry) bool {
		return validQuarkFolder(entry.ID) && entry.ID != "0" && entry.ID != parent && entry.Name != ""
	})
	if e != nil {
		return BatchDownloadResult{}, e
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}

func quarkDirectoryParentMatches(raw json.RawMessage, parent string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text == parent
	}
	var number json.Number
	return json.Unmarshal(raw, &number) == nil && string(number) == parent
}
