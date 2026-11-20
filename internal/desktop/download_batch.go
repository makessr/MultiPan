package desktop

import (
	"context"
	"errors"
	"multipan/internal/transfer"
)

func (s *DesktopService) DownloadCloudFiles(inputs []transfer.DownloadInput) (transfer.BatchDownloadResult, error) {
	return s.batchWithSelection(inputs, func() (string, error) {
		return s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).SetTitle("选择批量下载保存目录").AttachToWindow(s.window).PromptForSingleSelection()
	})
}
func (s *DesktopService) batchWithSelection(inputs []transfer.DownloadInput, choose func() (string, error)) (transfer.BatchDownloadResult, error) {
	if e := transfer.ValidateDownloadBatch(inputs); e != nil {
		return transfer.BatchDownloadResult{}, e
	}
	s.downloadMu.Lock()
	if s.downloadCancel != nil {
		s.downloadMu.Unlock()
		return transfer.BatchDownloadResult{}, errors.New("请先完成或取消当前下载")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.downloadCancel = cancel
	s.downloadMu.Unlock()
	defer func() { cancel(); s.downloadMu.Lock(); s.downloadCancel = nil; s.downloadMu.Unlock() }()
	directory, e := choose()
	if e != nil {
		for cause := e; cause != nil; cause = errors.Unwrap(cause) {
			if cause.Error() == "cancelled by user" || errors.Is(cause, context.Canceled) {
				return transfer.BatchDownloadResult{Cancelled: true}, nil
			}
		}
		return transfer.BatchDownloadResult{}, errors.New("无法打开批量下载目录选择窗口")
	}
	if directory == "" || ctx.Err() != nil {
		return transfer.BatchDownloadResult{Cancelled: true}, nil
	}
	result, e := transfer.RunDownloadBatch(ctx, inputs, directory, s.manager.DownloadFile, func(event transfer.BatchDownloadEvent) { s.app.Event.Emit("multipan:batch-download", event) })
	s.rememberDownloadBatch(result, false)
	return result, e
}
