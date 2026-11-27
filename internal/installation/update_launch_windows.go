package installation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LaunchSignedInstaller opens the interactive setup for a checked release.
// The caller must obtain confirmation before calling it.
func (u VerifiedUpdate) LaunchSignedInstaller(ctx context.Context, savedPath string) (<-chan struct{}, error) {
	m := u.manifest
	if m.Platform != InstallerPlatform(runtime.GOARCH) || u.origin == nil || !m.Expires.After(time.Now()) || !filepath.IsAbs(savedPath) || filepath.Base(savedPath) != m.Asset {
		return nil, errors.New("需要本次下载的安装包和未过期的更新清单")
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("请以普通用户身份打开 MultiPan，再启动更新安装器")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := openUpdateInstaller(savedPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()

	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return nil, errors.New("无法定位 Windows 当前用户目录")
	}
	_, release, err := pinUpdateDirectory(local)
	if err != nil {
		return nil, err
	}
	defer release()
	dir, err := createPrivateUpdateDirectory(local)
	if err != nil {
		return nil, err
	}
	target, err := openPinnedDirectory(dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	path := filepath.Join(dir, m.Asset)
	started := false
	defer func() {
		if !started {
			_ = windows.CloseHandle(target)
			_ = os.Remove(path)
			_ = os.Remove(dir)
		}
	}()

	if err := u.copyUpdateForLaunch(source, dir, target); err != nil {
		return nil, err
	}
	file, err := openUpdateInstaller(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := u.VerifyInstaller(file); err != nil {
		return nil, err
	}
	if err := u.VerifyInstallerArchitecture(file); err != nil {
		return nil, err
	}
	if err := verifyUpdateAuthenticode(file, windows.WTD_MOTW); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.Expires.After(time.Now()) {
		return nil, errors.New("更新清单在启动前已到期，请重新检查更新")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	start := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(name, nil, nil, nil, false, 0, nil, cwd, &start, &process); err != nil {
		return nil, fmt.Errorf("Windows 无法打开更新安装器：%w", err)
	}
	started = true
	_ = file.Close()
	_ = windows.CloseHandle(process.Thread)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, err := windows.WaitForSingleObject(process.Process, windows.INFINITE)
		_ = windows.CloseHandle(process.Process)
		if err == nil && result == windows.WAIT_OBJECT_0 {
			_ = os.Remove(path)
		}
		_ = windows.CloseHandle(target)
		_ = os.Remove(dir)
	}()
	return finished, nil
}

func (u VerifiedUpdate) copyUpdateForLaunch(source *os.File, dir string, target windows.Handle) error {
	tmp, err := newUpdateTemporaryFile(dir)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = disposeMachineFile(tmp)
		}
		_ = tmp.Close()
	}()
	if err := u.VerifyInstaller(io.TeeReader(source, tmp)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := renameFileInDirectory(tmp, target, u.manifest.Asset); err != nil {
		return err
	}
	renamed = true
	return nil
}

func createPrivateUpdateDirectory(base string) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return "", err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	path := filepath.Join(base, "MultiPan-update-"+hex.EncodeToString(id[:]))
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	err = windows.CreateDirectory(name, securityAttributes(sd))
	runtime.KeepAlive(sd)
	return path, err
}
