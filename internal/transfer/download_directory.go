package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type directoryFile struct {
	input    DownloadInput
	relative string
}
type directoryPlan struct {
	name        string
	directories []string
	files       []directoryFile
}

func planDAVDownload(ctx context.Context, in DownloadInput, list func(context.Context, string) (FilePage, error), update func(BatchDownloadEvent)) (directoryPlan, error) {
	if in.AccountID == "" || !validBaiduPath(in.ParentID) || !validBaiduPath(in.EntryID) || path.Dir(in.EntryID) != in.ParentID || in.Name != path.Base(in.EntryID) {
		return directoryPlan{}, errors.New("文件夹下载来源无效")
	}
	return planDirectoryDownload(ctx, in, list, update, func(parent string, entry CloudEntry) bool {
		return validBaiduPath(entry.ID) && path.Dir(entry.ID) == parent && entry.Name == path.Base(entry.ID)
	})
}
func planDirectoryDownload(ctx context.Context, in DownloadInput, list func(context.Context, string) (FilePage, error), update func(BatchDownloadEvent), validChild func(string, CloudEntry) bool) (directoryPlan, error) {
	plan := directoryPlan{name: SuggestedDownloadName(in.Name), directories: []string{""}}
	parent, e := list(ctx, in.ParentID)
	if e != nil {
		return plan, e
	}
	found := 0
	for _, entry := range parent.Entries {
		if entry.ID == in.EntryID && entry.Name == in.Name && entry.Folder {
			found++
		}
	}
	if found != 1 {
		return plan, errors.New("文件夹已变化或不在当前目录，请刷新后下载")
	}
	seen := map[string]bool{in.EntryID: true}
	count := 0
	var visit func(string, string, int) error
	visit = func(id, relative string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > 20 {
			return errors.New("文件夹层级超过20层，请选择较小的子目录")
		}
		if update != nil {
			update(BatchDownloadEvent{Name: "正在读取目录：" + relative, Progress: DownloadProgress{Total: -1}})
		}
		page, e := list(ctx, id)
		if e != nil {
			return e
		}
		names := map[string]bool{}
		for _, entry := range page.Entries {
			count++
			if count > 2000 {
				return errors.New("文件夹超过2000个项目，请分批下载子目录")
			}
			if !validChild(id, entry) || seen[entry.ID] {
				return errors.New("目录条目越界或重复，未开始保存")
			}
			seen[entry.ID] = true
			name := SuggestedDownloadName(entry.Name)
			key := strings.ToLower(name)
			if names[key] {
				return errors.New("目录项目清理后名称冲突，未开始保存")
			}
			names[key] = true
			rel := filepath.Join(relative, name)
			if entry.Folder {
				plan.directories = append(plan.directories, rel)
				if e := visit(entry.ID, rel, depth+1); e != nil {
					return e
				}
			} else {
				plan.files = append(plan.files, directoryFile{input: DownloadInput{AccountID: in.AccountID, ParentID: id, EntryID: entry.ID, Name: entry.Name}, relative: rel})
			}
		}
		return nil
	}
	e = visit(in.EntryID, "", 0)
	return plan, e
}
func runDirectoryDownload(ctx context.Context, plan directoryPlan, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	result := BatchDownloadResult{Items: []BatchDownloadItem{}}
	if plan.name == "" || SuggestedDownloadName(plan.name) != plan.name || filepath.Base(plan.name) != plan.name {
		return result, errors.New("本机根目录名称无效")
	}
	for _, rel := range plan.directories {
		if rel != "" && !filepath.IsLocal(rel) {
			return result, errors.New("本机目录路径越界")
		}
	}
	for _, file := range plan.files {
		if !filepath.IsLocal(file.relative) {
			return result, errors.New("本机文件路径越界")
		}
	}
	info, e := os.Stat(parent)
	if e != nil || !info.IsDir() || !filepath.IsAbs(parent) {
		return result, errors.New("保存目录不存在或无法访问")
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	root := filepath.Join(parent, plan.name)
	if e = os.Mkdir(root, 0700); e != nil {
		return result, errors.New("无法创建下载根文件夹，已有同名项目时不会合并或覆盖，请选择其他保存目录")
	}
	result.Directory = root
	owned := map[string]os.FileInfo{}
	owned[root], e = directoryIdentity(root)
	if e != nil {
		return result, e
	}
	check := func(dir string) error { return checkDownloadDirectory(root, dir, owned) }
	for _, rel := range plan.directories {
		if rel == "" {
			continue
		}
		if e = ctx.Err(); e != nil {
			result.Cancelled = true
			return result, nil
		}
		dir := filepath.Join(root, rel)
		if e = check(filepath.Dir(dir)); e != nil {
			return result, e
		}
		if e = os.Mkdir(dir, 0700); e != nil {
			return result, fmt.Errorf("本机子目录创建失败；已创建目录保留在：%s", root)
		}
		owned[dir], e = directoryIdentity(dir)
		if e != nil {
			return result, e
		}
	}
	if collect, ok := ctx.Value(directoryQueueKey{}).(func(directoryPlan, string) (BatchDownloadResult, error)); ok {
		return collect(plan, root)
	}
	return runDirectoryFiles(ctx, plan, root, owned, nil, download, update)
}

// A retry is limited to the exact in-memory plan and directories created by us.
// No local path, file list or saved status is accepted from the frontend.
func runDirectoryFiles(ctx context.Context, plan directoryPlan, root string, owned map[string]os.FileInfo, previous []BatchDownloadItem, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	result := BatchDownloadResult{Directory: root, Items: append([]BatchDownloadItem(nil), previous...)}
	if len(result.Items) == 0 {
		for _, file := range plan.files {
			result.Items = append(result.Items, BatchDownloadItem{Name: file.relative, Status: "skipped", Message: "未开始下载"})
		}
	}
	if len(result.Items) != len(plan.files) {
		return result, errors.New("文件夹重试记录无效")
	}
	result.retry = &directoryRetryState{plan: plan, root: root, owned: owned}
	check := func(dir string) error { return checkDownloadDirectory(root, dir, owned) }
	for i, file := range plan.files {
		if result.Items[i].Status == "saved" {
			continue
		}
		item := BatchDownloadItem{Name: file.relative}
		if ctx.Err() != nil {
			result.Cancelled = true
			item.Status = "skipped"
			item.Message = "已取消，未开始下载"
		} else if e := check(filepath.Dir(filepath.Join(root, file.relative))); e != nil {
			item.Status = "failed"
			item.Message = e.Error()
		} else {
			if update != nil {
				update(BatchDownloadEvent{Index: i + 1, Count: len(plan.files), Name: file.relative, Progress: DownloadProgress{Total: -1}})
			}
			saved, err := download(ctx, file.input, filepath.Join(root, file.relative), func(p DownloadProgress) {
				if update != nil {
					update(BatchDownloadEvent{Index: i + 1, Count: len(plan.files), Name: file.relative, Progress: p})
				}
			})
			item.Result = saved
			if err == nil && saved.Path != "" {
				item.Status = "saved"
			} else if errors.Is(err, context.Canceled) || ctx.Err() != nil || saved.Cancelled {
				result.Cancelled = true
				item.Status = "cancelled"
				item.Message = "下载已取消，未保存正式文件"
			} else {
				item.Status = "failed"
				if err != nil {
					item.Message = err.Error()
				} else {
					item.Message = "下载未返回保存结果"
				}
			}
		}
		result.Items[i] = item
		if update != nil {
			copy := item
			update(BatchDownloadEvent{Index: i + 1, Count: len(plan.files), Name: file.relative, Item: &copy})
		}
	}
	return result, nil
}
func (m *Manager) DownloadFolder(ctx context.Context, in DownloadInput, parent string, update func(BatchDownloadEvent)) (result BatchDownloadResult, failure error) {
	if !m.directoryDownloadMu.TryLock() {
		return result, errors.New("请先完成或取消当前文件夹下载")
	}
	defer m.directoryDownloadMu.Unlock()
	m.directoryRetry = nil
	var account Account
	defer func() {
		if failure == nil && result.retry != nil && hasDirectoryPending(result.Items) {
			state := result.retry
			state.id = newID()
			state.input = in
			state.kind = account.Kind
			state.endpoint = account.Endpoint
			state.username = account.Username
			state.items = append([]BatchDownloadItem(nil), result.Items...)
			m.directoryRetry = state
			result.RetryID = state.id
		}
	}()
	m.mu.Lock()
	for _, a := range m.accounts {
		if a.ID == in.AccountID {
			account = a
			break
		}
	}
	closing := m.closing
	m.mu.Unlock()
	if closing {
		return BatchDownloadResult{}, errors.New("程序正在退出")
	}
	if account.Kind != "ctdav" && account.Kind != "webdav" && account.Kind != "quark" && account.Kind != "pan123" && account.Kind != "baidu" && account.Kind != "guangya" && account.Kind != "lanzou" {
		return BatchDownloadResult{}, errors.New("此网盘暂不支持文件夹下载")
	}
	client := NewHTTPUploader().Client
	if account.Kind == "baidu" {
		return downloadBaiduDirectory(ctx, client, account, in, parent, m.DownloadFile, update)
	}
	if account.Kind == "guangya" {
		return downloadGuangyaDirectory(ctx, client, account, in, parent, m.DownloadFile, update)
	}
	if account.Kind == "lanzou" {
		return downloadLanzouDirectory(ctx, client, account, in, parent, m.DownloadFile, update)
	}
	if account.Kind == "pan123" {
		return download123Directory(ctx, client, account, in, parent, m.DownloadFile, update)
	}
	if account.Kind == "quark" {
		return downloadQuarkDirectory(ctx, client, account, in, parent, m.DownloadFile, update)
	}
	return downloadDAVDirectory(ctx, client, account, in, parent, m.DownloadFile, update)
}
func downloadDAVDirectory(ctx context.Context, client *http.Client, account Account, in DownloadInput, parent string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	plan, e := planDAVDownload(ctx, in, func(ctx context.Context, id string) (FilePage, error) { return ListDAVFiles(ctx, client, account, id) }, update)
	if e != nil {
		return BatchDownloadResult{}, e
	}
	return runDirectoryDownload(ctx, plan, parent, download, update)
}

// Stat through a handle captures Windows file IDs immediately; Lstat identity
// can otherwise be loaded lazily after a directory has been replaced.
func directoryIdentity(dir string) (os.FileInfo, error) {
	handle, e := os.Open(dir)
	if e != nil {
		return nil, e
	}
	defer handle.Close()
	return handle.Stat()
}
