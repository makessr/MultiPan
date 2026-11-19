package transfer

import (
	"os"
	"path/filepath"
	"strings"
)

// DropSelection holds metadata only. File contents are read by the uploader
// only after the user explicitly starts an upload.
type DropSelection struct {
	TargetID    string   `json:"targetId"`
	Files       []File   `json:"files"`
	Directories int      `json:"directories"`
	Unreadable  int      `json:"unreadable"`
	Duplicates  int      `json:"duplicates"`
	Messages    []string `json:"messages,omitempty"`
}

// Folder trees are supported only by the multi-target preparation view. This
// scan does not create remote directories or enqueue uploads.
func InspectDroppedUploadItems(paths []string) DropSelection {
	result := DropSelection{Files: []File{}}
	seen := map[string]bool{}
	add := func(f File) {
		key := strings.ToLower(filepath.Clean(f.Path)) + "\x00" + strings.ToLower(f.RelativePath)
		if seen[key] {
			result.Duplicates++
			return
		}
		seen[key] = true
		result.Files = append(result.Files, f)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			result.Unreadable++
			continue
		}
		info, e := os.Lstat(p)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			result.Unreadable++
			continue
		}
		if info.IsDir() {
			tree, e := ScanUploadTree(p)
			if e != nil {
				result.Directories++
				result.Messages = append(result.Messages, filepath.Base(p)+"："+e.Error())
				continue
			}
			if len(result.Files)+len(tree) > 1000 {
				result.Directories++
				result.Messages = append(result.Messages, "单次拖入最多1000项，请分批选择")
				continue
			}
			for _, f := range tree {
				add(f)
			}
		} else {
			f, e := Inspect(p)
			if e != nil {
				result.Unreadable++
				continue
			}
			if len(result.Files) >= 1000 {
				result.Unreadable++
				continue
			}
			add(f)
		}
	}
	return result
}

func InspectDroppedFiles(paths []string) DropSelection {
	result := DropSelection{Files: []File{}}
	seen := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			result.Unreadable++
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			result.Duplicates++
			continue
		}
		seen[key] = true
		info, e := os.Stat(p)
		if e != nil {
			result.Unreadable++
			continue
		}
		if info.IsDir() {
			result.Directories++
			continue
		}
		if !info.Mode().IsRegular() {
			result.Unreadable++
			continue
		}
		f, e := Inspect(p)
		if e != nil {
			result.Unreadable++
			continue
		}
		result.Files = append(result.Files, f)
	}
	return result
}
