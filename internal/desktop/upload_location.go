package desktop

import (
	"errors"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
)

// The bridge accepts a task ID, never an arbitrary filesystem path.
func (s *DesktopService) OpenUploadFolder(taskID string) error {
	return openUploadFolder(s.manager.Snapshot().Tasks, taskID, launchDownloadFolder)
}

func openUploadFolder(tasks []transfer.Task, taskID string, launch func(string) error) error {
	var source string
	for _, task := range tasks {
		if taskID != "" && task.ID == taskID {
			source = task.File.Path
			break
		}
	}
	if downloadLocationKey(source) == "" {
		return errors.New("上传任务不存在或源文件位置无效")
	}
	folder := filepath.Dir(filepath.Clean(source))
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return errors.New("源文件夹不存在或无法访问，可能已被移动或删除")
	}
	if err := launch(folder); err != nil {
		return errors.New("无法打开资源管理器，请稍后重试")
	}
	return nil
}
