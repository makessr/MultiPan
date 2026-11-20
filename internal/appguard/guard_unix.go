//go:build linux || darwin

package appguard

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("MultiPan 正在运行")

func LockProfile(configDir string) (func(), error) {
	dir := filepath.Join(configDir, "MultiPan")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("MultiPan 数据目录必须仅允许当前用户访问")
	}
	path := filepath.Join(dir, ".multipan-profile.lock")
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var lock unix.Stat_t
	if err := unix.Fstat(fd, &lock); err != nil || lock.Mode&unix.S_IFMT != unix.S_IFREG ||
		lock.Uid != uint32(os.Geteuid()) || lock.Nlink != 1 || lock.Mode&0077 != 0 {
		_ = unix.Close(fd)
		return nil, errors.New("MultiPan 锁文件不安全")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = unix.Close(fd) }, nil
}
