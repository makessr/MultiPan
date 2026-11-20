package desktop

import (
	"errors"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadFolderUsesTaskSourceParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "中文 & spaces, files")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	tasks := []transfer.Task{{ID: "source-task", File: transfer.File{Path: filepath.Join(parent, "missing.exe")}}}
	calls := 0
	launch := func(folder string) error {
		calls++
		if folder != parent {
			t.Fatalf("launched file or wrong folder: %q", folder)
		}
		return nil
	}
	// Missing original file is fine: only the containing directory is opened.
	if err := openUploadFolder(tasks, "source-task", launch); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "unknown", parent, "https://example.invalid"} {
		if err := openUploadFolder(tasks, id, launch); err == nil {
			t.Fatal("unknown ID accepted")
		}
	}
	if calls != 1 {
		t.Fatal("unknown ID launched")
	}
	if err := openUploadFolder(tasks, "source-task", func(string) error { return errors.New("fixture") }); err == nil {
		t.Fatal("launch failure hidden")
	}
}

func TestUploadFolderInvalidOrMissingParent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "regular-file")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", "relative.exe", "https://example.invalid/file", "C:\\bad\x00.exe", filepath.Join(root, "missing", "file.exe"), filepath.Join(file, "file.exe")} {
		tasks := []transfer.Task{{ID: "fixture", File: transfer.File{Path: source}}}
		if err := openUploadFolder(tasks, "fixture", func(string) error { t.Fatal("invalid parent launched"); return nil }); err == nil {
			t.Fatal("invalid source accepted", source)
		}
	}
}
