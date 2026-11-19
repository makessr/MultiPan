package transfer

import (
	"context"
	"errors"
	"net/http"
)

func download123Directory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	if account.Endpoint != Pan123API || in.AccountID == "" || !valid123Folder(in.ParentID) || !valid123Folder(in.EntryID) || in.EntryID == "0" || in.EntryID == in.ParentID || in.Name == "" {
		return BatchDownloadResult{}, errors.New("123文件夹下载来源无效 [123-DIR/0.31.0]")
	}
	c := pan123Client{client, account}
	list := func(ctx context.Context, id string) (FilePage, error) {
		entries, e := c.list(ctx, id)
		if e != nil {
			return FilePage{}, e
		}
		page := FilePage{CurrentID: id, Entries: []CloudEntry{}}
		for _, f := range entries {
			if f.ParentID != "" && string(f.ParentID) != id {
				return FilePage{}, errors.New("123目录条目的父目录不符，未开始保存 [123-DIR/0.31.0]")
			}
			if f.Name == "" || f.Size < 0 || (f.Type != 0 && f.Type != 1) {
				return FilePage{}, errors.New("123目录条目信息无效，未开始保存 [123-DIR/0.31.0]")
			}
			size := f.Size
			page.Entries = append(page.Entries, CloudEntry{ID: string(f.ID), Name: f.Name, Folder: f.Type == 1, Size: &size, revision: f.MD5})
		}
		return page, nil
	}
	plan, e := planDirectoryDownload(ctx, in, list, update, func(parent string, entry CloudEntry) bool {
		return valid123Folder(entry.ID) && entry.ID != "0" && entry.ID != parent && entry.Name != ""
	})
	if e != nil {
		return BatchDownloadResult{}, e
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}
