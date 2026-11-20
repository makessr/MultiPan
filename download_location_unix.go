//go:build linux || darwin

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func downloadLocationKey(path string) string {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return ""
	}
	return filepath.Clean(path)
}

func launchDownloadFolder(folder string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "/usr/bin/open"
	}
	return exec.Command(opener, folder).Run()
}
