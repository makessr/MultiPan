package installation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const machineHelperName = "MultiPan-Machine-Helper.exe"

type machineHelperLease struct {
	path string
	file *os.File
	dirs []windows.Handle
	info machineNodeSnapshot
}

// The digest must originate in a trusted release policy. Neither a sidecar
// beside the executable nor a claim received from the pipe is a trust anchor.
func verifyMachineHelper(root string, expected [sha256.Size]byte) (*machineHelperLease, error) {
	base, dir, err := knownMachineDirectory()
	if err != nil {
		return nil, err
	}
	if !sameLocation(root, dir) {
		return nil, errors.New("管理员辅助程序必须位于固定机器安装目录")
	}
	policy, err := productionMachinePolicy()
	if err != nil {
		return nil, err
	}
	return pinMachineHelper(base, dir, filepath.VolumeName(base)+`\`, policy, expected, verifyHelperSignature)
}

func pinMachineHelper(base, root, floor string, policy machineSecurityPolicy, expected [sha256.Size]byte, signature func(string, windows.Handle) error) (*machineHelperLease, error) {
	if expected == ([sha256.Size]byte{}) || signature == nil {
		return nil, errors.New("缺少可信发行摘要或辅助程序验签策略")
	}
	audit, err := captureMachineSecurityAtFloor(base, root, policy, floor)
	if err != nil {
		return nil, err
	}
	lease := &machineHelperLease{path: filepath.Join(root, machineHelperName)}
	fail := func(err error) (*machineHelperLease, error) {
		lease.close()
		return nil, err
	}
	// Pin ancestors first, then revalidate. An ancestor cannot be renamed
	// normally while these handles disallow FILE_SHARE_DELETE.
	for i := len(audit.entries) - 1; i >= 0; i-- {
		entry := audit.entries[i]
		if !entry.directory || entry.path == root {
			continue
		}
		if err := lease.pinDirectory(policy, entry); err != nil {
			return fail(err)
		}
	}
	for _, entry := range audit.entries {
		if entry.path == root {
			if err := lease.pinDirectory(policy, entry); err != nil {
				return fail(err)
			}
			break
		}
	}
	name, err := windows.UTF16PtrFromString(lease.path)
	if err != nil {
		return fail(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fail(err)
	}
	lease.file = os.NewFile(uintptr(handle), lease.path)
	lease.info, err = policy.nodeHandle(handle, false, true, machineMutationMask)
	if err != nil {
		return fail(err)
	}
	const maxHelperSize = 100 << 20
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(lease.file, maxHelperSize+1))
	if err != nil {
		return fail(err)
	}
	if count == 0 || count > maxHelperSize || [sha256.Size]byte(hash.Sum(nil)) != expected {
		return fail(errors.New("辅助程序与可信发行摘要不一致"))
	}
	if _, err := lease.file.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	if err := signature(lease.path, handle); err != nil {
		return fail(err)
	}
	if err := audit.Check(); err != nil {
		return fail(err)
	}
	after, err := policy.nodeHandle(handle, false, true, machineMutationMask)
	if err != nil || after != lease.info {
		return fail(errors.New("辅助程序验签期间身份或权限发生变化"))
	}
	return lease, nil
}

func (l *machineHelperLease) pinDirectory(policy machineSecurityPolicy, entry machineAuditEntry) error {
	name, err := windows.UTF16PtrFromString(entry.path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	l.dirs = append(l.dirs, h)
	got, err := policy.nodeHandle(h, true, entry.protected, entry.mask)
	if err != nil || got != entry.before {
		return errors.New("辅助程序目录身份或权限变化")
	}
	return nil
}

func (l *machineHelperLease) close() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	for i := len(l.dirs) - 1; i >= 0; i-- {
		windows.CloseHandle(l.dirs[i])
	}
	l.dirs = nil
}

func verifyHelperSignature(path string, handle windows.Handle) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := verifyWindowsAuthenticode(name, handle, nil, windows.WTD_CACHE_ONLY_URL_RETRIEVAL); err != nil {
		return fmt.Errorf("辅助程序 Authenticode 核对失败：%w", err)
	}
	return nil
}

// The caller supplies the OS handle returned by the eventual launcher.
// Validate the image and token before using this identity in the pipe offer.
func (l *machineHelperLease) authorizeProcess(process windows.Handle, session uint32) (handoffPeer, error) {
	if err := l.verifyProcessImage(process); err != nil {
		return handoffPeer{}, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return handoffPeer{}, err
	}
	defer token.Close()
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return handoffPeer{}, err
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return handoffPeer{}, err
	}
	enabledAdmin := false
	for _, group := range groups.AllGroups() {
		if group.Sid.Equals(admin) && group.Attributes&windows.SE_GROUP_ENABLED != 0 && group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 {
			enabledAdmin = true
			break
		}
	}
	peer, err := identifyHandoffPeer(process)
	if err != nil {
		return handoffPeer{}, err
	}
	if !token.IsElevated() || !enabledAdmin || session != peer.session {
		return handoffPeer{}, errors.New("辅助进程缺少同一会话的已提升管理员令牌")
	}
	return peer, nil
}

func (l *machineHelperLease) offer(ctx context.Context, pipe, launchedProcess windows.Handle, binding machineHandoff) error {
	initiator, err := identifyHandoffPeer(windows.CurrentProcess())
	if err != nil {
		return err
	}
	peer, err := l.authorizeProcess(launchedProcess, initiator.session)
	if err != nil {
		return err
	}
	return offerMachineHandoff(ctx, pipe, peer, binding)
}

func (l *machineHelperLease) verifyProcessImage(process windows.Handle) error {
	if l == nil || l.file == nil {
		return errors.New("辅助程序验证句柄已关闭")
	}
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); err != nil {
		return err
	}
	image := windows.UTF16ToString(buf[:size])
	if !strings.EqualFold(filepath.Clean(image), l.path) {
		return errors.New("运行中的辅助进程映像与已验证文件不匹配")
	}
	// Normal replacement of this path is blocked by the retained read handle
	// and ancestor handles. This does not attest mapped code or its DLLs.
	return nil
}
