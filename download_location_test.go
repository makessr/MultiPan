package main

import (
	"errors"
	"fmt"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadLocationOpensOnlyContainingFolder(t *testing.T) {
	s := &DesktopService{}
	folder := filepath.Join(t.TempDir(), "目录 & spaces, test")
	if e := os.Mkdir(folder, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(folder, "download.exe")
	s.rememberDownloadPath(file, false)
	var launched string
	if e := s.openDownloadFolder(file, func(path string) error { launched = path; return nil }); e != nil || launched != folder {
		t.Fatalf("not containing folder: %q %v", launched, e)
	}
	// The original file may have been moved/deleted; its containing folder is
	// still useful. The downloaded executable is never passed to the launcher.
	s.rememberDownloadPath(folder, true)
	if e := s.openDownloadFolder(folder, func(path string) error { launched = path; return nil }); e != nil || launched != folder {
		t.Fatal("root folder failed", e)
	}
	if e := s.openDownloadFolder(file, func(string) error { return errors.New("launch failure") }); e == nil {
		t.Fatal("launch failure hidden")
	}
}

func TestDownloadLocationGuards(t *testing.T) {
	s := &DesktopService{}
	calls := 0
	launch := func(string) error { calls++; return nil }
	for _, path := range []string{"", "relative.exe", "https://example.invalid/file", "file:///C:/file", "C:\\file\x00.exe", filepath.Join(t.TempDir(), "unknown.exe"), `\\unknown.invalid\share\file.exe`} {
		if e := s.openDownloadFolder(path, launch); e == nil {
			t.Fatal("unknown location accepted", path)
		}
	}
	if calls != 0 {
		t.Fatal("invalid path launched explorer")
	}
	directory := filepath.Join(t.TempDir(), "missing")
	s.rememberDownloadPath(directory, true)
	if e := s.openDownloadFolder(directory, launch); e == nil {
		t.Fatal("missing folder accepted")
	}
	file := filepath.Join(t.TempDir(), "not-a-folder")
	if e := os.WriteFile(file, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	s.rememberDownloadPath(file, true)
	if e := s.openDownloadFolder(file, launch); e == nil {
		t.Fatal("file launched as directory")
	}
	if calls != 0 {
		t.Fatal("missing or replaced folder launched explorer")
	}
}

func TestDownloadLocationRemembersOnlySavedItems(t *testing.T) {
	s := &DesktopService{}
	root := t.TempDir()
	saved := filepath.Join(root, "saved.lnk")
	failed := filepath.Join(root, "failed.exe")
	s.rememberDownloadBatch(transfer.BatchDownloadResult{Directory: root, Items: []transfer.BatchDownloadItem{{Status: "saved", Result: transfer.DownloadResult{Path: saved}}, {Status: "failed", Result: transfer.DownloadResult{Path: failed}}}}, true)
	for _, path := range []string{saved, root} {
		if e := s.openDownloadFolder(path, func(folder string) error {
			if folder != root {
				t.Error("file/shortcut execution attempted")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.openDownloadFolder(failed, func(string) error { t.Error("failed item launched"); return nil }); e == nil {
		t.Fatal("failed item registered")
	}
	// Retrying an ordinary batch must preserve previously saved paths.
	s.rememberDownloadBatch(transfer.BatchDownloadResult{Items: []transfer.BatchDownloadItem{{Status: "saved", Result: transfer.DownloadResult{Path: filepath.Join(root, "second.txt")}}}}, false)
	if e := s.openDownloadFolder(saved, func(string) error { return nil }); e != nil {
		t.Fatal("retry discarded saved path", e)
	}
}

func TestDownloadLocationRegistryBound(t *testing.T) {
	s := &DesktopService{}
	root := t.TempDir()
	for i := 0; i < maxDownloadLocations+2; i++ {
		s.rememberDownloadPath(filepath.Join(root, fmt.Sprint(i)), false)
	}
	if len(s.downloadPaths) != maxDownloadLocations || len(s.downloadPathOrder) != maxDownloadLocations {
		t.Fatal("unbounded registry")
	}
	if _, exists := s.downloadPaths[downloadLocationKey(filepath.Join(root, "0"))]; exists {
		t.Fatal("old record not evicted")
	}
	if _, exists := s.downloadPaths[downloadLocationKey(filepath.Join(root, fmt.Sprint(maxDownloadLocations+1)))]; !exists {
		t.Fatal("latest record lost")
	}
}
