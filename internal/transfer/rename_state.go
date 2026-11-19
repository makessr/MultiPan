package transfer

import (
	"context"
	"errors"
	"strings"
)

func (m *Manager) RenameEntry(ctx context.Context, in RenameInput) (CloudEntry, error) {
	if len(in.ParentDisplay) > 4096 {
		return CloudEntry{}, errors.New("目录显示路径过长")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	var account Account
	for _, a := range m.accounts {
		if a.ID == in.AccountID {
			account = a
			break
		}
	}
	if account.ID == "" {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("账号不存在")
	}
	for _, t := range m.tasks {
		if t.AccountID == account.ID && (t.Status == "running" || t.Status == "queued" || m.active[t.ID] != nil) {
			m.mu.Unlock()
			return CloudEntry{}, errors.New("请先完成或停止此网盘的上传任务，再重命名")
		}
	}
	if m.renaming == nil {
		m.renaming = map[string]bool{}
	}
	m.renaming[account.ID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.renaming, account.ID); m.mu.Unlock() }()
	entry, e := RenameAccountEntry(ctx, NewHTTPUploader().Client, account, in)
	if e != nil {
		return CloudEntry{}, e
	}
	if !in.Folder || in.NewName == in.OldName {
		return entry, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateRenamedFolderLocked(in, account.Kind, entry.ID) {
		if e = m.saveLocked(); e != nil {
			return entry, errors.New("云端已重命名，但本机目录配置保存失败，请勿重复改名；请检查数据目录并重新选择上传位置")
		}
	}
	return entry, nil
}

func renamedPath(value, old, next string) string {
	if value == old || strings.HasPrefix(value, old+"/") {
		return next + strings.TrimPrefix(value, old)
	}
	return value
}
func renamedDisplay(value, old, next string) string {
	if value == old || strings.HasPrefix(value, old+" / ") {
		return next + strings.TrimPrefix(value, old)
	}
	return value
}

// Keep default paths and retryable destinations valid. Completed history keeps
// the original path recorded at upload time.
func (m *Manager) updateRenamedFolderLocked(in RenameInput, kind, newID string) bool {
	changed := false
	pathIDs := kind == "baidu" || kind == "ctdav" || kind == "webdav"
	oldDisplay, newDisplay := "", ""
	if in.ParentDisplay != "" {
		oldDisplay = in.ParentDisplay + " / " + in.OldName
		newDisplay = in.ParentDisplay + " / " + in.NewName
	}
	update := func(folder, display *string) {
		oldFolder, oldLabel := *folder, *display
		if pathIDs {
			*folder = renamedPath(*folder, in.EntryID, newID)
			if *folder != oldFolder {
				*display = "根目录" + strings.ReplaceAll(*folder, "/", " / ")
			}
		}
		if !pathIDs && oldDisplay != "" {
			*display = renamedDisplay(*display, oldDisplay, newDisplay)
		}
		if !pathIDs && *folder == in.EntryID && *display == oldLabel {
			i := strings.LastIndex(*display, " / ")
			if i >= 0 {
				*display = (*display)[:i+3] + in.NewName
			} else {
				*display = in.NewName
			}
		}
		if *folder != oldFolder || *display != oldLabel {
			changed = true
		}
	}
	for i := range m.accounts {
		a := &m.accounts[i]
		if a.ID == in.AccountID {
			update(&a.Folder, &a.FolderDisplay)
		}
	}
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.AccountID == in.AccountID && (t.Status == "paused" || t.Status == "failed") {
			update(&t.Folder, &t.FolderDisplay)
		}
	}
	return changed
}
