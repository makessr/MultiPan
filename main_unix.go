//go:build linux || darwin

package main

import (
	"errors"
	"log"
	"multipan/internal/appguard"
	"os"
	"path/filepath"
)

func main() {
	dir, err := os.UserConfigDir()
	if err != nil {
		startupError("无法定位用户数据目录")
		return
	}
	release, err := appguard.LockProfile(dir)
	if errors.Is(err, appguard.ErrBusy) {
		startupError("MultiPan 已在运行，请使用已打开的窗口。")
		return
	}
	if err != nil {
		startupError("无法锁定本机任务数据")
		return
	}
	defer release()
	runWorkspace(filepath.Join(dir, "MultiPan", "state.enc"), nil)
}

func startupError(message string) { log.Print(message) }
