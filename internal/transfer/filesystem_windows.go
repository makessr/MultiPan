package transfer

import (
	"context"
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// The Windows upload handle already denies concurrent writes and deletion.
func canRecordUploadFingerprint(context.Context, *os.File, File, string) bool { return true }

func openUploadSource(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errors.New("源文件路径无效")
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, errors.New("无法锁定源文件，请关闭正在修改此文件的程序后重试")
	}
	return os.NewFile(uintptr(handle), path), nil
}

func availableDiskBytes(probe string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(probe)
	if err != nil {
		return 0, errors.New("本机缓存路径无法识别")
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return available, err
}

func commitNewDownload(source, destination string) error {
	extended := func(p string) string {
		if strings.HasPrefix(p, `\\?\`) {
			return p
		}
		if strings.HasPrefix(p, `\\`) {
			return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
		}
		return `\\?\` + p
	}
	s, e := windows.UTF16PtrFromString(extended(source))
	if e != nil {
		return e
	}
	d, e := windows.UTF16PtrFromString(extended(destination))
	if e != nil {
		return e
	}
	// Same-directory rename, without MOVEFILE_REPLACE_EXISTING: the destination
	// must still be absent at the final commit, even if another process created it.
	return windows.MoveFileEx(s, d, 0)
}

func relayNoLink(p string) error {
	name, e := windows.UTF16PtrFromString(p)
	if e != nil {
		return e
	}
	attrs, e := windows.GetFileAttributes(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("中转缓存含链接或重解析点，已停止")
	}
	return nil
}
func relayOpenCache(file string) (*os.File, error) {
	name, e := windows.UTF16PtrFromString(file)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &info); e != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		return nil, errors.New("缓存不是独立常规文件，已停止")
	}
	return os.NewFile(uintptr(h), file), nil
}
