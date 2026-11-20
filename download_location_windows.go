package main

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"path/filepath"
	"strings"
)

func downloadLocationKey(path string) string {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.VolumeName(path) == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(path))
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
