package transfer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanUploadTree never follows links/junctions; the full plan is validated before enqueue.
func ScanUploadTree(root string) ([]File, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("请选择普通本地文件夹，不支持链接目录")
	}
	var entries []File
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return errors.New("文件夹含无法读取的项目")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("文件夹含链接项目，请移除链接后重选")
		}
		rel, e := filepath.Rel(filepath.Dir(root), path)
		if e != nil {
			return e
		}
		if len(strings.Split(rel, string(filepath.Separator))) > 20 {
			return errors.New("上传目录超过20层")
		}
		inf, e := d.Info()
		if e != nil {
			return e
		}
		f := File{Path: path, Name: d.Name(), Modified: inf.ModTime().UnixMilli(), Directory: d.IsDir(), RelativePath: filepath.ToSlash(rel)}
		if !d.IsDir() {
			if !inf.Mode().IsRegular() {
				return errors.New("不支持特殊文件")
			}
			f.Size = inf.Size()
		}
		entries = append(entries, f)
		if len(entries) > 1000 {
			return errors.New("文件夹超过1000项，请分批选择")
		}
		return nil
	})
	return entries, err
}

func inspectUploadEntry(f File) (File, error) {
	if f.RelativePath == "" {
		if f.Directory {
			return File{}, errors.New("目录上传缺少相对路径")
		}
		return Inspect(f.Path)
	}
	parts := strings.Split(f.RelativePath, "/")
	if len(parts) > 20 || filepath.IsAbs(f.RelativePath) || strings.Contains(f.RelativePath, "\\") {
		return File{}, errors.New("目录结构无效")
	}
	for _, p := range parts {
		if p == "." || p == ".." || validateCloudFolderName(p) != nil {
			return File{}, errors.New("目录或文件名称无效：" + p)
		}
	}
	if parts[len(parts)-1] != filepath.Base(f.Path) {
		return File{}, errors.New("源文件与目录结构不一致")
	}
	info, e := os.Lstat(f.Path)
	if e != nil {
		return File{}, e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return File{}, errors.New("不支持链接")
	}
	if f.Directory {
		if !info.IsDir() {
			return File{}, errors.New("目录已变化")
		}
		return f, nil
	}
	current, e := Inspect(f.Path)
	current.RelativePath = f.RelativePath
	return current, e
}

func (m *Manager) prepareUploadPath(ctx context.Context, a Account, parent string, f File) (string, error) {
	parts := strings.Split(f.RelativePath, "/")
	if !f.Directory {
		parts = parts[:len(parts)-1]
	}
	for _, name := range parts {
		page, err := m.ListFiles(ctx, a.ID, parent)
		if err != nil {
			return "", err
		}
		found := ""
		matches := 0
		for _, entry := range page.Entries {
			if entry.Name == name {
				if !entry.Folder {
					return "", fmt.Errorf("目录路径被同名文件占用：%s", name)
				}
				found = entry.ID
				matches++
			}
		}
		if matches > 1 {
			return "", errors.New("存在多个同名目录，无法确定目标")
		}
		if found == "" {
			created, e := m.CreateFolder(ctx, a.ID, parent, name)
			if e != nil {
				return "", e
			}
			found = created.ID
		}
		parent = found
	}
	return parent, nil
}
