// Package appguard coordinates cooperating applications and installers without
// loading account data. File handles, not session-local names, protect profiles.
package appguard

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

var ErrBusy = errors.New("MultiPan 正在运行或程序目录正在维护")

func once(fn func()) func() { var o sync.Once; return func() { o.Do(fn) } }

// Keep the old session-local mutex too: older versions do not know the file
// lock. No state is read until both locks have been acquired successfully.
func LockProfile(configDir string) (func(), error) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("Local\\MultiPan-%x", sha256.Sum256([]byte(configDir))))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, ErrBusy
		}
		return nil, err
	}
	unlock, err := lockProfileFile(configDir)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return once(func() { unlock(); windows.CloseHandle(h) }), nil
}

func noReparseAncestors(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(name)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("任务数据锁路径不能包含链接或目录联接")
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

func lockProfileFile(configDir string) (func(), error) {
	if !filepath.IsAbs(configDir) || filepath.Clean(configDir) != configDir {
		return nil, errors.New("任务数据目录必须为规范的绝对路径")
	}
	dir := filepath.Join(configDir, "MultiPan")
	if err := noReparseAncestors(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := noReparseAncestors(dir); err != nil {
		return nil, err
	}
	path, _ := windows.UTF16PtrFromString(filepath.Join(dir, ".multipan-profile.lock"))
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("任务数据锁文件无效；请保留原文件并核对")
	}
	var ov windows.Overlapped
	if err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov); err != nil {
		windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, err
	}
	// The zero-length file stays on disk. It is neither stale ownership nor a
	// stored credential; closing/crashing releases the kernel byte-range lock.
	return once(func() { windows.UnlockFileEx(h, 0, 1, 0, &ov); windows.CloseHandle(h) }), nil
}

func finalName(h windows.Handle) (string, error) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		if n > 32768 {
			return "", errors.New("程序路径过长")
		}
		buf = make([]uint16, n+1)
	}
}

// Resolve existing ancestors through a handle, so case, junction aliases and
// short-name aliases use the same maintenance name. This does not authenticate
// directory ownership or replace the installer's path/ACL checks.
func canonicalRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return "", errors.New("程序目录必须为规范的绝对路径")
	}
	var suffix []string
	for p := dir; ; p = filepath.Dir(p) {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return "", err
		}
		h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err == nil {
			var info windows.ByHandleFileInformation
			err = windows.GetFileInformationByHandle(h, &info)
			if err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
				windows.CloseHandle(h)
				return "", errors.New("程序目录祖先不是可核对的目录")
			}
			path, err := finalName(h)
			windows.CloseHandle(h)
			if err != nil {
				return "", err
			}
			for j := len(suffix) - 1; j >= 0; j-- {
				path = filepath.Join(path, suffix[j])
			}
			return strings.ToLower(path), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(p) == p {
			return "", err
		}
		suffix = append(suffix, filepath.Base(p))
	}
}

func maintenanceName(root string) *uint16 {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Global\\MultiPan-Install-%x", sha256.Sum256([]byte(root))))
	return name
}

// AcquireRun pins the actual executable against write/delete, THEN checks the
// cross-session maintenance marker. Reversing this order would leave a race.
// Multiple users may hold read leases simultaneously with separate profiles.
func AcquireRun(executable string) (func(), error) {
	path, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err != nil || info.NumberOfLinks != 1 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("程序文件无法建立独立运行锁")
	}
	actual, err := finalName(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	root, err := canonicalRoot(filepath.Dir(actual))
	if err == nil {
		var marker windows.Handle
		marker, err = windows.OpenMutex(windows.SYNCHRONIZE, false, maintenanceName(root))
		if marker != 0 {
			windows.CloseHandle(marker)
		}
		if err == nil || !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			err = ErrBusy // includes access denied/type collisions: never bypass
		} else {
			err = nil
		}
	}
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return once(func() { windows.CloseHandle(h) }), nil
}

type InstallationLease struct {
	mu     sync.Mutex
	root   string
	handle windows.Handle
}

// Installers acquire the global marker BEFORE probing managed files for writes
// and keep it throughout staging, commit or rollback. New applications pin then
// inspect this marker; existing applications' read handles reject write probes.
func AcquireInstallation(dir string) (*InstallationLease, error) {
	root, err := canonicalRoot(dir)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, maintenanceName(root))
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("%w：无法独占程序目录维护状态", ErrBusy)
	}
	return &InstallationLease{root: root, handle: h}, nil
}

func (l *InstallationLease) Check(dir string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle == 0 {
		return ErrBusy
	}
	root, err := canonicalRoot(dir)
	if err != nil || root != l.root {
		return errors.New("程序目录维护期间位置发生变化，未继续替换")
	}
	return nil
}

func (l *InstallationLease) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle != 0 {
		windows.CloseHandle(l.handle)
		l.handle = 0
	}
}
