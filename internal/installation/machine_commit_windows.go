package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"unsafe"

	"golang.org/x/sys/windows"
)

type heldMachineFile struct {
	file *os.File
	name string
}

func openMachineFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func renameMachineFile(f *os.File, dir windows.Handle, name string) error {
	if name != Marker && !managed(name) {
		return errors.New("安装文件重命名目标无效")
	}
	return renameFileInDirectory(f, dir, name)
}

func renameFileInDirectory(f *os.File, dir windows.Handle, name string) error {
	if dir == 0 || name == "." || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return errors.New("文件重命名目标无效")
	}
	// Win32 rejected a non-null RootDirectory on the validation host. Resolve
	// the held destination directory through its handle and use a full path.
	path := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(dir, &path[0], uint32(len(path)), 0)
	if err != nil || n == 0 || n >= uint32(len(path)) {
		return errors.New("无法解析已锁定的安装目标目录")
	}
	utf16, err := windows.UTF16FromString(filepath.Join(windows.UTF16ToString(path[:n]), name))
	if err != nil {
		return err
	}
	type renameInfo struct {
		Flags  uint32
		Root   windows.Handle
		Length uint32
		Name   [1]uint16
	}
	const offset = int(unsafe.Offsetof(renameInfo{}.Name))
	size := offset + len(utf16)*2
	// Use pointer-aligned storage for the fixed header and trailing UTF-16.
	buf := make([]uintptr, (size+int(unsafe.Sizeof(uintptr(0)))-1)/int(unsafe.Sizeof(uintptr(0))))
	info := (*renameInfo)(unsafe.Pointer(&buf[0]))
	info.Length = uint32((len(utf16) - 1) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Add(unsafe.Pointer(info), offset)), len(utf16)), utf16)
	return windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileRenameInfo, (*byte)(unsafe.Pointer(info)), uint32(size))
}

func disposeMachineFile(f *os.File) error {
	remove := byte(1)
	return windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfo, &remove, 1)
}

func (p *protectedMachineInstall) hold(path string, expected machineNodeSnapshot, digest string, old *Manifest) (*os.File, error) {
	f, err := openMachineFile(path)
	if err != nil {
		return nil, err
	}
	got, err := p.policy.nodeHandle(windows.Handle(f.Fd()), false, false, machineMutationMask)
	if err == nil && got == expected {
		const maxManagedBytes = 100 << 20
		b, readErr := io.ReadAll(io.LimitReader(f, maxManagedBytes+1))
		if readErr == nil && len(b) <= maxManagedBytes {
			if old != nil {
				var marker Manifest
				if json.Unmarshal(b, &marker) == nil && reflect.DeepEqual(marker, *old) {
					return f, nil
				}
			} else if Digest(b) == digest {
				return f, nil
			}
		}
	}
	_ = f.Close()
	return nil, errors.New("持有安装文件时发现内容、身份或权限变化")
}

func (p *protectedMachineInstall) commit(stage, backup string, names []string, old Manifest, beforeMove func(string, string) error) (bool, error) {
	var previous, next []heldMachineFile
	defer func() {
		for _, group := range [][]heldMachineFile{previous, next} {
			for _, item := range group {
				_ = item.file.Close()
			}
		}
	}()
	for _, name := range names {
		path := filepath.Join(p.dir, name)
		for _, entry := range p.snapshot.entries {
			if entry.path != path || entry.before.Missing {
				continue
			}
			var marker *Manifest
			if name == Marker {
				marker = &old
			}
			f, err := p.hold(path, entry.before, old.Hashes[name], marker)
			if err != nil {
				return false, err
			}
			previous = append(previous, heldMachineFile{f, name})
		}
		f, err := p.hold(filepath.Join(stage, name), p.staged[name], p.hashes[name], nil)
		if err != nil {
			return false, err
		}
		next = append(next, heldMachineFile{f, name})
	}
	if err := p.beginCheckpoint(stage, backup, old, previous); err != nil {
		return p.checkpoint != nil, fmt.Errorf("无法记录机器安装事务：%w", err)
	}
	movedOld, movedNew := 0, 0
	rollback := func(cause error) (bool, error) {
		failed := false
		for _, item := range next[:movedNew] {
			if err := renameMachineFile(item.file, p.pinned[stage], item.name); err != nil {
				failed = true
			}
		}
		for _, item := range previous[:movedOld] {
			if err := renameMachineFile(item.file, p.pinned[p.dir], item.name); err != nil {
				failed = true
			}
		}
		if failed {
			return true, fmt.Errorf("安装未完成且部分原文件未恢复，请保留备份：%w", cause)
		}
		p.resolved = true
		return false, fmt.Errorf("安装未完成，已恢复原文件：%w", cause)
	}
	move := func(from, to string, item heldMachineFile) error {
		if beforeMove != nil {
			if err := beforeMove(filepath.Join(from, item.name), filepath.Join(to, item.name)); err != nil {
				return err
			}
		}
		return renameMachineFile(item.file, p.pinned[to], item.name)
	}
	for _, item := range previous {
		if err := move(p.dir, backup, item); err != nil {
			return rollback(err)
		}
		movedOld++
	}
	for _, item := range next {
		if err := move(stage, p.dir, item); err != nil {
			return rollback(err)
		}
		movedNew++
	}
	for _, item := range previous {
		if err := disposeMachineFile(item.file); err != nil {
			return true, fmt.Errorf("新版本文件已就位，旧文件清理未完成：%w", err)
		}
	}
	p.resolved = true
	return false, nil
}
