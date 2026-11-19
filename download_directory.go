package main

import (
	"context"
	"errors"
	"multipan/internal/transfer"
)

func (s *DesktopService) DownloadCloudFolder(in transfer.DownloadInput) (transfer.BatchDownloadResult, error) {
	return s.folderWithSelection(in, func() (string, error) {
		return s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).SetTitle("选择保存位置（将新建同名文件夹）").AttachToWindow(s.window).PromptForSingleSelection()
	})
}

func (s *DesktopService) RetryCloudFolder(in transfer.DownloadInput, retryID string) (transfer.BatchDownloadResult, error) {
	s.downloadMu.Lock()
	if s.downloadCancel != nil {
		s.downloadMu.Unlock()
		return transfer.BatchDownloadResult{}, errors.New("请先完成或取消当前下载")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.downloadCancel = cancel
	s.downloadMu.Unlock()
	defer func() { cancel(); s.downloadMu.Lock(); s.downloadCancel = nil; s.downloadMu.Unlock() }()
	result, e := s.manager.RetryDownloadFolder(ctx, in, retryID, func(event transfer.BatchDownloadEvent) { s.app.Event.Emit("multipan:folder-download", event) })
	s.rememberDownloadBatch(result, true)
	return result, e
}
func (s *DesktopService) folderWithSelection(in transfer.DownloadInput, choose func() (string, error)) (transfer.BatchDownloadResult, error) {
	s.downloadMu.Lock()
	if s.downloadCancel != nil {
		s.downloadMu.Unlock()
		return transfer.BatchDownloadResult{}, errors.New("请先完成或取消当前下载")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.downloadCancel = cancel
	s.downloadMu.Unlock()
	defer func() { cancel(); s.downloadMu.Lock(); s.downloadCancel = nil; s.downloadMu.Unlock() }()
	parent, e := choose()
	if e != nil {
		for cause := e; cause != nil; cause = errors.Unwrap(cause) {
			if cause.Error() == "cancelled by user" || errors.Is(cause, context.Canceled) {
				return transfer.BatchDownloadResult{Cancelled: true}, nil
			}
		}
		return transfer.BatchDownloadResult{}, errors.New("无法打开文件夹保存位置选择窗口")
	}
	if parent == "" || ctx.Err() != nil {
		return transfer.BatchDownloadResult{Cancelled: true}, nil
	}
	result, e := s.manager.DownloadFolder(ctx, in, parent, func(event transfer.BatchDownloadEvent) { s.app.Event.Emit("multipan:folder-download", event) })
	if errors.Is(e, context.Canceled) || ctx.Err() != nil {
		result.Cancelled = true
		e = nil
	}
	s.rememberDownloadBatch(result, true)
	return result, e
}
