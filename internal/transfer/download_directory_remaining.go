package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

func downloadGuangyaDirectory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	if account.Endpoint != GuangyaAPI || in.AccountID == "" || !validQuarkFolder(in.ParentID) || !validQuarkFolder(in.EntryID) || in.EntryID == guangyaRoot || in.EntryID == in.ParentID || in.Name == "" {
		return BatchDownloadResult{}, errors.New("光鸭文件夹来源无效")
	}
	c := gyClient{client, account}
	list := func(ctx context.Context, id string) (FilePage, error) {
		entries, e := c.list(ctx, id)
		if e != nil {
			return FilePage{}, e
		}
		page := FilePage{CurrentID: id, Entries: []CloudEntry{}}
		for _, f := range entries {
			if f.Parent != "" && f.Parent != id {
				return FilePage{}, errors.New("光鸭条目父目录不符，未开始保存")
			}
			if f.Name == "" || f.Size < 0 || (f.Type != 1 && f.Type != 2) {
				return FilePage{}, errors.New("光鸭目录条目信息无效")
			}
			size := f.Size
			page.Entries = append(page.Entries, CloudEntry{ID: f.ID, Name: f.Name, Folder: f.Type == 2, Size: &size})
		}
		return page, nil
	}
	plan, e := planDirectoryDownload(ctx, in, list, update, func(parent string, entry CloudEntry) bool {
		return validQuarkFolder(entry.ID) && entry.ID != guangyaRoot && entry.ID != parent && entry.Name != ""
	})
	if e != nil {
		return BatchDownloadResult{}, e
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}
func downloadBaiduDirectory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	if account.Endpoint != BaiduAPI {
		return BatchDownloadResult{}, errors.New("百度文件夹接口来源无效")
	}
	c := newBaiduClient(client, account)
	list := func(ctx context.Context, id string) (FilePage, error) {
		entries, e := c.list(ctx, id)
		if e != nil {
			return FilePage{}, e
		}
		page := FilePage{CurrentID: id, Entries: []CloudEntry{}}
		for _, f := range entries {
			if f.ID == "" || f.Dir == nil || (*f.Dir != 0 && *f.Dir != 1) || (f.Size != nil && *f.Size < 0) {
				return FilePage{}, errors.New("百度目录条目信息无效")
			}
			page.Entries = append(page.Entries, CloudEntry{ID: f.Path, Name: f.Name, Folder: *f.Dir == 1, Size: f.Size, revision: string(f.ID) + ":" + f.MD5})
		}
		return page, nil
	}
	plan, e := planDAVDownload(ctx, in, list, update)
	if e != nil {
		return BatchDownloadResult{}, e
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}
func downloadLanzouDirectory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	validID := func(id string) bool { return valid123Folder(id) && id != "0" }
	if account.Endpoint != "https://up.woozooo.com" || in.AccountID == "" || (in.ParentID != "-1" && !validID(in.ParentID)) || !validID(in.EntryID) || in.EntryID == in.ParentID || in.Name == "" {
		return BatchDownloadResult{}, errors.New("蓝奏文件夹来源无效")
	}
	// Folder IDs and file IDs are separate namespaces in Lanzou.
	original := in
	in.ParentID = "d:" + in.ParentID
	in.EntryID = "d:" + in.EntryID
	list := func(ctx context.Context, id string) (FilePage, error) {
		if !strings.HasPrefix(id, "d:") {
			return FilePage{}, errors.New("蓝奏目录ID无效")
		}
		raw := strings.TrimPrefix(id, "d:")
		page, e := ListAccountFiles(ctx, client, account, raw)
		if e != nil {
			return FilePage{}, e
		}
		for i := range page.Entries {
			f := &page.Entries[i]
			if !validID(f.ID) || f.Name == "" {
				return FilePage{}, errors.New("蓝奏目录条目信息无效")
			}
			prefix := "f:"
			if f.Folder {
				prefix = "d:"
			}
			f.ID = prefix + f.ID
		}
		return page, nil
	}
	plan, e := planDirectoryDownload(ctx, in, list, update, func(parent string, entry CloudEntry) bool {
		prefix := "f:"
		if entry.Folder {
			prefix = "d:"
		}
		return strings.HasPrefix(entry.ID, prefix) && validID(strings.TrimPrefix(entry.ID, prefix)) && entry.ID != parent && entry.Name != ""
	})
	if e != nil {
		return BatchDownloadResult{}, e
	}
	for i := range plan.files {
		file := &plan.files[i]
		file.input.AccountID = original.AccountID
		file.input.ParentID = strings.TrimPrefix(file.input.ParentID, "d:")
		file.input.EntryID = strings.TrimPrefix(file.input.EntryID, "f:")
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}
