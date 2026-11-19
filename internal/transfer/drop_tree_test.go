package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDroppedFolderTreeKeepsStandaloneAndNestedDestinations(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	os.MkdirAll(filepath.Join(root, "empty"), 0700)
	file := filepath.Join(root, "same.txt")
	os.WriteFile(file, []byte("source"), 0600)
	selection := InspectDroppedUploadItems([]string{file, root, file, root})
	standalone, nested, empty := 0, 0, 0
	for _, f := range selection.Files {
		if f.Path == file {
			if f.RelativePath == "" {
				standalone++
			} else if f.RelativePath == "tree/same.txt" {
				nested++
			}
		}
		if f.Directory && f.RelativePath == "tree/empty" {
			empty++
		}
	}
	if standalone != 1 || nested != 1 || empty != 1 || len(selection.Files) != 4 || selection.Duplicates == 0 {
		t.Fatalf("lost target mapping: %+v", selection)
	}
	if selection.Directories != 0 || selection.Unreadable != 0 {
		t.Fatal("valid tree skipped")
	}
	if b, e := os.ReadFile(file); e != nil || string(b) != "source" {
		t.Fatal("drop changed source")
	}
	invalid := InspectDroppedUploadItems([]string{"relative", filepath.Join(root, "missing")})
	if len(invalid.Files) != 0 || invalid.Unreadable != 2 {
		t.Fatal("invalid drop accepted")
	}
}

func TestDroppedOversizedTreeDoesNotPartiallyEnterPlan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	os.MkdirAll(root, 0700)
	// Depth is checked before enqueue; reject the entire tree, preserving unrelated files.
	current := root
	for i := 0; i < 21; i++ {
		current = filepath.Join(current, "sub")
		os.Mkdir(current, 0700)
	}
	good := filepath.Join(t.TempDir(), "good.txt")
	os.WriteFile(good, []byte("good"), 0600)
	result := InspectDroppedUploadItems([]string{root, good})
	if len(result.Files) != 1 || result.Files[0].Path != good || result.Directories != 1 || len(result.Messages) != 1 {
		t.Fatalf("partial tree escaped scan: %+v", result)
	}
}
