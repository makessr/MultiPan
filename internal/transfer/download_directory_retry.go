package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// The latest retry plan is process-local, bounded by the scanner's item limit.
// It contains no credentials and is never written into state.enc.
type directoryRetryState struct {
	id, kind, endpoint, username string
	input                        DownloadInput
	plan                         directoryPlan
	root                         string
	owned                        map[string]os.FileInfo
	items                        []BatchDownloadItem
}

func hasDirectoryPending(items []BatchDownloadItem) bool {
	for _, item := range items {
		if item.Status != "saved" {
			return true
		}
	}
	return false
}

func checkDownloadDirectory(root, dir string, owned map[string]os.FileInfo) error {
	for {
		current, err := os.Lstat(dir)
		original, ok := owned[dir]
		if err != nil || !ok || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, original) {
			return errors.New("本机下载目录已变化，未向该目录写入；请重新下载到新的保存位置")
		}
		if dir == root {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return errors.New("本机下载目录记录无效")
		}
		dir = parent
	}
}

func (m *Manager) RetryDownloadFolder(ctx context.Context, in DownloadInput, retryID string, update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	return m.retryDownloadFolder(ctx, in, retryID, m.DownloadFile, update)
}

func (m *Manager) retryDownloadFolder(ctx context.Context, in DownloadInput, retryID string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	if !m.directoryDownloadMu.TryLock() {
		return BatchDownloadResult{}, errors.New("请先完成或取消当前文件夹下载")
	}
	defer m.directoryDownloadMu.Unlock()
	state := m.directoryRetry
	if state == nil || retryID == "" || state.id != retryID || state.input != in {
		return BatchDownloadResult{}, errors.New("文件夹重试记录已失效，请重新选择文件夹下载")
	}
	m.mu.Lock()
	var account Account
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
	if account.ID == "" || account.Kind != state.kind || account.Endpoint != state.endpoint || account.Username != state.username {
		m.directoryRetry = nil
		return BatchDownloadResult{}, errors.New("账号来源已变化，文件夹重试记录已失效")
	}
	if !hasDirectoryPending(state.items) {
		return BatchDownloadResult{}, errors.New("没有未完成的文件")
	}
	// Validate all original directory identities before any content request.
	// This does not recreate deleted directories or merge foreign directories.
	for dir := range state.owned {
		if ctx.Err() != nil {
			break
		}
		if e := checkDownloadDirectory(state.root, dir, state.owned); e != nil {
			return BatchDownloadResult{}, e
		}
	}
	result, e := runDirectoryFiles(ctx, state.plan, state.root, state.owned, state.items, download, update)
	if e != nil {
		return result, e
	}
	state.items = append([]BatchDownloadItem(nil), result.Items...)
	if hasDirectoryPending(state.items) {
		result.RetryID = state.id
	} else {
		m.directoryRetry = nil
	}
	return result, nil
}
