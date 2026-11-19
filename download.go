package main

import (
	"context"
	"errors"
	"multipan/internal/transfer"
)

func (s *DesktopService) DownloadCloudFile(in transfer.DownloadInput) (transfer.DownloadResult, error) {
	return s.downloadWithSelection(in, func() (string, error) {
		return s.app.Dialog.SaveFile().SetFilename(transfer.SuggestedDownloadName(in.Name)).SetMessage("选择下载保存位置（不覆盖已有文件）").AttachToWindow(s.window).PromptForSingleSelection()
	})
}

func (s *DesktopService) downloadWithSelection(in transfer.DownloadInput, choose func() (string, error)) (transfer.DownloadResult, error) {
	s.downloadMu.Lock()
	if s.downloadCancel != nil {
		s.downloadMu.Unlock()
		return transfer.DownloadResult{}, errors.New("请先完成或取消当前下载")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.downloadCancel = cancel
	s.downloadMu.Unlock()
	defer func() { cancel(); s.downloadMu.Lock(); s.downloadCancel = nil; s.downloadMu.Unlock() }()
	destination, e := choose()
	if e != nil {
		// Wails 3.1.0 does not re-export its internal CFD cancellation
		// sentinel. Match its exact value only at the save-dialog boundary.
		for cause := e; cause != nil; cause = errors.Unwrap(cause) {
			if cause.Error() == "cancelled by user" || errors.Is(cause, context.Canceled) {
				return transfer.DownloadResult{Cancelled: true}, nil
			}
		}
		return transfer.DownloadResult{}, errors.New("无法打开下载保存窗口")
	}
	if destination == "" || ctx.Err() != nil {
		return transfer.DownloadResult{Cancelled: true}, nil
	}
	result, e := s.manager.DownloadFile(ctx, in, destination, func(p transfer.DownloadProgress) { s.app.Event.Emit("multipan:download-progress", p) })
	if errors.Is(e, context.Canceled) {
		return transfer.DownloadResult{Cancelled: true}, nil
	}
	if e == nil && !result.Cancelled {
		s.rememberDownloadPath(result.Path, false)
	}
	return result, e
}

func (s *DesktopService) CancelDownload() {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	if s.downloadCancel != nil {
		s.downloadCancel()
	}
}
