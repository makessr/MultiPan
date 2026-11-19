package main

import (
	"errors"
	"strings"
)

func (s *DesktopService) CopyDownloadReport(report string) error {
	return copyDownloadReport(report, s.app.Clipboard.SetText)
}
func copyDownloadReport(report string, put func(string) bool) error {
	if strings.TrimSpace(report) == "" || len(report) > 2<<20 || strings.ContainsRune(report, 0) {
		return errors.New("结果为空或过长，无法复制")
	}
	if !put(report) {
		return errors.New("无法写入Windows剪贴板，请稍后重试")
	}
	return nil
}
