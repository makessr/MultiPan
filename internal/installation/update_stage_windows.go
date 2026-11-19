package installation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// StageInstaller downloads only the named asset on the verified release
// origin. The temporary file stays open through verification and publication.
func (u VerifiedUpdate) StageInstaller(ctx context.Context, dir string) (output string, err error) {
	return u.stageInstaller(ctx, dir, nil)
}

// StageSignedInstaller additionally enforces the Windows trust policy on
// the same open file before publication.
func (u VerifiedUpdate) StageSignedInstaller(ctx context.Context, dir string) (string, error) {
	return u.stageInstaller(ctx, dir, func(file *os.File) error {
		return verifyUpdateAuthenticode(file, windows.WTD_MOTW)
	})
}

func (u VerifiedUpdate) stageInstaller(ctx context.Context, dir string, verifySignature func(*os.File) error) (output string, err error) {
	m := u.manifest
	if u.origin == nil || !m.Expires.After(time.Now()) {
		return "", errors.New("需要未过期的线上签名清单")
	}
	target, release, err := pinUpdateDirectory(dir)
	if err != nil {
		return "", err
	}
	defer release()
	destination := filepath.Join(dir, m.Asset)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return "", errors.New("安装包目标已存在或无法核对，未下载")
	}

	resp, err := openUpdateOriginFile(ctx, updateOriginClient(10*time.Minute), u.origin.JoinPath(m.Asset), m.Size)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != m.Size {
		return "", errors.New("安装包响应长度与签名清单不一致")
	}
	tmp, err := newUpdateTemporaryFile(dir)
	if err != nil {
		return "", err
	}
	published := false
	defer func() {
		if !published {
			if cleanupErr := disposeMachineFile(tmp); cleanupErr != nil {
				err = errors.Join(err, errors.New("更新临时文件清理待核对"))
			}
		}
		_ = tmp.Close()
	}()
	if err := u.VerifyInstaller(io.TeeReader(resp.Body, tmp)); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if verifySignature != nil {
		signatureErr := verifySignature(tmp)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if signatureErr != nil {
			return "", signatureErr
		}
	}
	if !m.Expires.After(time.Now()) {
		return "", errors.New("签名清单在下载或验签期间到期")
	}
	if err := renameFileInDirectory(tmp, target, m.Asset); err != nil {
		return "", errors.New("无法将已核对的安装包保存到指定目录，请检查目录权限和同名文件")
	}
	published = true
	return destination, nil
}

func pinUpdateDirectory(dir string) (windows.Handle, func(), error) {
	volume := filepath.VolumeName(dir)
	if len(volume) != 2 || volume[1] != ':' {
		return 0, nil, errors.New("更新目录需要本机固定磁盘")
	}
	rootName, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(rootName) != windows.DRIVE_FIXED {
		return 0, nil, errors.New("更新目录需要本机固定磁盘")
	}
	if err := safeDirectory(dir); err != nil {
		return 0, nil, err
	}
	var ancestry []string
	for p := dir; ; p = filepath.Dir(p) {
		ancestry = append(ancestry, p)
		if p == filepath.Dir(p) {
			break
		}
	}
	var handles []windows.Handle
	release := func() {
		for i := len(handles) - 1; i >= 0; i-- {
			_ = windows.CloseHandle(handles[i])
		}
	}
	for i := len(ancestry) - 1; i >= 0; i-- {
		handle, err := openPinnedDirectory(ancestry[i])
		if err != nil {
			release()
			return 0, nil, err
		}
		handles = append(handles, handle)
	}
	return handles[len(handles)-1], release, nil
}

func newUpdateTemporaryFile(dir string) (*os.File, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".multipan-update-"+hex.EncodeToString(random[:])+".partial")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0,
		nil, windows.CREATE_NEW, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
