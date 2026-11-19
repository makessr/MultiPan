package main

import (
	"context"
	"errors"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
	"strings"
)

func (s *DesktopService) ControlAllQueuedDownloads(action string) ([]transfer.DownloadActionResult, error) {
	return s.manager.ControlAllDownloads(action)
}
func (s *DesktopService) ClearQueuedDownloadPartials(ids []string) ([]transfer.DownloadActionResult, error) {
	return s.manager.ClearDownloadPartials(ids)
}
func (s *DesktopService) ExportQueuedDownloadReport(ids []string, version string) (string, error) {
	report, e := s.manager.DownloadReport(ids, version)
	if e != nil {
		return "", e
	}
	return saveDownloadReport(report, func() (string, error) {
		return s.app.Dialog.SaveFile().SetFilename("MultiPan-download-results.txt").SetMessage("保存所选下载报告（TXT，不覆盖已有文件）").AttachToWindow(s.window).PromptForSingleSelection()
	})
}
func saveDownloadReport(report string, choose func() (string, error)) (string, error) {
	if strings.TrimSpace(report) == "" || len(report) > 2<<20 || strings.ContainsRune(report, 0) {
		return "", errors.New("报告为空或过长")
	}
	path, e := choose()
	if e != nil {
		for cause := e; cause != nil; cause = errors.Unwrap(cause) {
			if cause.Error() == "cancelled by user" || errors.Is(cause, context.Canceled) {
				return "", nil
			}
		}
		return "", errors.New("无法选择报告保存位置")
	}
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".txt") || transfer.SuggestedDownloadName(filepath.Base(path)) != filepath.Base(path) {
		return "", errors.New("请选择绝对路径的 .txt 文本文件")
	}
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", errors.New("保存位置已存在或不可写，报告未覆盖")
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	if _, e = file.WriteString(report); e != nil {
		return "", errors.New("报告写入失败")
	}
	if e = file.Sync(); e != nil {
		return "", errors.New("报告保存失败")
	}
	if e = file.Close(); e != nil {
		return "", errors.New("报告关闭失败")
	}
	success = true
	return path, nil
}
