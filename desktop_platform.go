package main

import (
	_ "embed"
	"runtime"
	"strings"
)

// DesktopPlatform reports the OS for the account and update controls.
// All transfer and storage decisions are still enforced in Go.
func (s *DesktopService) DesktopPlatform() string { return runtime.GOOS }

func currentAppVersion() string {
	version, ok := strings.CutPrefix(strings.TrimSpace(updateVersionSource), "export const APP_VERSION='")
	version, okEnd := strings.CutSuffix(version, "'")
	if !ok || !okEnd {
		return ""
	}
	return version
}

//go:embed frontend/src/version.ts
var updateVersionSource string

type ApplicationUpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	Configured     bool   `json:"configured"`
	Version        string `json:"version,omitempty"`
	Asset          string `json:"asset,omitempty"`
	Automatic      bool   `json:"automatic"`
	LastCheck      string `json:"lastCheck,omitempty"`
	CheckError     string `json:"checkError,omitempty"`
}

type ApplicationUpdateDownload struct {
	Path      string `json:"path,omitempty"`
	Cancelled bool   `json:"cancelled"`
}
