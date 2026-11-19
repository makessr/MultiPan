package main

import (
	"errors"
	"multipan/internal/transfer"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const maxDownloadLocations = 8192

func downloadLocationKey(path string) string {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.VolumeName(path) == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(path))
}

// Remember only paths produced by our downloader. In particular, an arbitrary
// bridge argument cannot open a URL, executable, shell command or unknown share.
func (s *DesktopService) rememberDownloadPath(path string, directory bool) {
	key := downloadLocationKey(path)
	if key == "" {
		return
	}
	s.downloadPathMu.Lock()
	defer s.downloadPathMu.Unlock()
	if s.downloadPaths == nil {
		s.downloadPaths = map[string]bool{}
	}
	if _, known := s.downloadPaths[key]; !known {
		s.downloadPathOrder = append(s.downloadPathOrder, key)
		if len(s.downloadPathOrder) > maxDownloadLocations {
			delete(s.downloadPaths, s.downloadPathOrder[0])
			s.downloadPathOrder = s.downloadPathOrder[1:]
		}
	}
	s.downloadPaths[key] = directory
}

func (s *DesktopService) rememberDownloadBatch(result transfer.BatchDownloadResult, includeDirectory bool) {
	if includeDirectory {
		s.rememberDownloadPath(result.Directory, true)
	}
	for _, item := range result.Items {
		if item.Status == "saved" {
			s.rememberDownloadPath(item.Result.Path, false)
		}
	}
}

func (s *DesktopService) OpenDownloadFolder(savedPath string) error {
	return s.openDownloadFolder(savedPath, launchDownloadFolder)
}

func (s *DesktopService) openDownloadFolder(savedPath string, launch func(string) error) error {
	key := downloadLocationKey(savedPath)
	s.downloadPathMu.Lock()
	directory, known := s.downloadPaths[key]
	s.downloadPathMu.Unlock()
	if key == "" || !known {
		return errors.New("保存位置不在本次下载记录中，请重新下载后再试")
	}
	folder := filepath.Clean(savedPath)
	if !directory {
		folder = filepath.Dir(folder)
	}
	info, e := os.Stat(folder)
	if e != nil || !info.IsDir() {
		return errors.New("保存文件夹不存在或无法访问，可能已被移动或删除")
	}
	if e = launch(folder); e != nil {
		return errors.New("无法打开资源管理器，请稍后重试")
	}
	return nil
}

func launchDownloadFolder(folder string) error {
	windowsDirectory, e := windows.GetSystemWindowsDirectory()
	if e != nil {
		return e
	}
	// No shell and no saved file execution. Explorer receives one directory arg.
	command := exec.Command(filepath.Join(windowsDirectory, "explorer.exe"), folder)
	if e = command.Start(); e != nil {
		return e
	}
	_ = command.Process.Release()
	return nil
}
