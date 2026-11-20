package desktop

import (
	"context"
	"errors"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) CheckUploadPlan(files []transfer.File, targets []transfer.Target) (transfer.UploadReview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.CheckUploadPlan(ctx, files, targets)
}
func (s *DesktopService) EnqueueReviewedUploads(files []transfer.File, targets []transfer.Target, skips []transfer.UploadSkip, stamp string) (int, error) {
	return s.manager.EnqueueReviewedUploads(files, targets, skips, stamp)
}
func (s *DesktopService) RetryFailedUploads(ids []string) ([]transfer.UploadRetryResult, error) {
	return s.manager.RetryFailedUploads(ids)
}

func (s *DesktopService) PickUploadFolder() ([]transfer.File, error) {
	path, e := s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).SetTitle("选择上传文件夹（保留目录结构，最多1000项）").AttachToWindow(s.window).PromptForSingleSelection()
	if e != nil {
		if e.Error() == "cancelled by user" {
			return nil, nil
		}
		return nil, errors.New("无法选择文件夹")
	}
	if path == "" {
		return nil, nil
	}
	return transfer.ScanUploadTree(path)
}
func (s *DesktopService) SaveTransferSettings(settings transfer.TransferSettings) error {
	return s.manager.SetTransferSettings(settings)
}
func (s *DesktopService) QueueCloudDownloads(inputs []transfer.DownloadInput) (int, error) {
	path, e := s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).SetTitle("选择队列下载保存目录（不覆盖已有文件）").AttachToWindow(s.window).PromptForSingleSelection()
	if e != nil {
		if e.Error() == "cancelled by user" {
			return 0, nil
		}
		return 0, errors.New("无法选择保存目录")
	}
	if path == "" {
		return 0, nil
	}
	return s.manager.QueueDownloads(inputs, path)
}
func (s *DesktopService) ControlQueuedDownload(id string, resume bool) error {
	return s.manager.ControlDownload(id, resume)
}
func (s *DesktopService) ClearSavedDownloadHistory() error { return s.manager.ClearSavedDownloads() }
func (s *DesktopService) OpenQueuedDownloadFolder(id string) error {
	for _, j := range s.manager.Snapshot().Downloads {
		if j.ID == id && j.Status == "saved" {
			s.rememberDownloadPath(j.Result.Path, false)
			return s.OpenDownloadFolder(j.Result.Path)
		}
	}
	return errors.New("任务尚未保存或不存在")
}

func (s *DesktopService) RestartUpload(id string) error { return s.manager.RestartUpload(id) }
func (s *DesktopService) QueueCloudFolder(in transfer.DownloadInput) (int, error) {
	path, e := s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).SetTitle("选择保存位置（新建同名文件夹并加入队列）").AttachToWindow(s.window).PromptForSingleSelection()
	if e != nil || path == "" {
		return 0, e
	}
	return s.manager.QueueDownloadFolder(context.Background(), in, path)
}

func (s *DesktopService) BatchQueuedDownloads(ids []string, action string) ([]transfer.DownloadActionResult, error) {
	return s.manager.BatchDownloadAction(ids, action)
}
