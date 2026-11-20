//go:build linux || darwin

package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openRegularNoFollow(path string, singleLink bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG ||
		(singleLink && info.Nlink != 1) {
		_ = unix.Close(fd)
		return nil, errors.New("需要一个独立的常规文件")
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openUploadSource(path string) (*os.File, error) {
	file, err := openRegularNoFollow(path, false)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func canRecordUploadFingerprint(ctx context.Context, file *os.File, source File, expected string) bool {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	sum := sha256.New()
	n, err := io.Copy(sum, ctxReader{ctx, io.LimitReader(file, source.Size+1)})
	return err == nil && n == source.Size && unchanged(file, source) == nil && hex.EncodeToString(sum.Sum(nil)) == expected
}

func relayOpenCache(path string) (*os.File, error) {
	return openRegularNoFollow(path, true)
}

func relayNoLink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("中转缓存含符号链接，已停止")
	}
	return nil
}

func availableDiskBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
