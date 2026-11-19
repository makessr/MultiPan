package installation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// This protocol is exercised by the isolated installer harness. A production
// launcher must establish the trusted executable and elevated token first.
type machineHandoff struct {
	operation byte
	target    string
	version   string
	payload   [sha256.Size]byte
}

const (
	handoffInstall byte = 1
	handoffRemove  byte = 2
	handoffSize         = 4 + 1 + sha256.Size*2
	pipePrefix          = `\\.\pipe\MultiPan-setup-`
)

type handoffFrame [handoffSize]byte

func (b machineHandoff) frame(nonce [sha256.Size]byte) (handoffFrame, error) {
	var f handoffFrame
	if b.operation != handoffInstall && b.operation != handoffRemove ||
		!validVersion(b.version) || len(b.version) > 32 ||
		!filepath.IsAbs(b.target) || filepath.Clean(b.target) != b.target ||
		!strings.EqualFold(filepath.Base(b.target), "MultiPan") ||
		!utf8.ValidString(b.target) || strings.ContainsRune(b.target, 0) ||
		b.payload == ([sha256.Size]byte{}) || nonce == ([sha256.Size]byte{}) {
		return f, errors.New("机器安装交接内容无效")
	}
	copy(f[:4], "MPH1")
	f[4] = b.operation
	copy(f[5:], nonce[:])
	h := sha256.New()
	// NUL-separated fields cannot be supplied as paths or commands by a peer.
	_, _ = io.WriteString(h, "machine\x00"+strings.ToLower(b.target)+"\x00"+b.version+"\x00")
	_, _ = h.Write(b.payload[:])
	copy(f[5+sha256.Size:], h.Sum(nil))
	return f, nil
}

type handoffPeer struct {
	pid, session uint32
	created      windows.Filetime
	sid          string
}

func identifyHandoffPeer(process windows.Handle) (handoffPeer, error) {
	var out handoffPeer
	var err error
	if out.pid, err = windows.GetProcessId(process); err != nil {
		return out, err
	}
	if err = windows.ProcessIdToSessionId(out.pid, &out.session); err != nil {
		return out, err
	}
	var exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(process, &out.created, &exit, &kernel, &user); err != nil {
		return out, err
	}
	if exit != (windows.Filetime{}) {
		return out, errors.New("安装交接进程已退出")
	}
	var token windows.Token
	if err = windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return out, err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return out, err
	}
	out.sid = tu.User.Sid.String()
	return out, nil
}

func verifyHandoffPeer(pipe windows.Handle, expected handoffPeer, server bool) error {
	query := windows.GetNamedPipeClientProcessId
	if server {
		query = windows.GetNamedPipeServerProcessId
	}
	var pid uint32
	if err := query(pipe, &pid); err != nil {
		return err
	}
	if pid == 0 || pid != expected.pid {
		return errors.New("安装交接连接的进程不匹配")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	peer, err := identifyHandoffPeer(h)
	if err != nil || peer != expected {
		return errors.New("安装交接进程身份变化或无法核对")
	}
	return nil
}

func newHandoffPipe() (string, windows.Handle, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", 0, err
	}
	// Grant only the initiating SID and administrators, and omit the
	// FILE_CREATE_PIPE_INSTANCE bit from the administrator client grant.
	const clientRights = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_WRITE_ATTRIBUTES
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;;GA;;;%s)(A;;0x%x;;;BA)", user.User.Sid.String(), clientRights))
	if err != nil {
		return "", 0, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", 0, err
	}
	name := pipePrefix + hex.EncodeToString(random[:])
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", 0, err
	}
	h, err := windows.CreateNamedPipe(p, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_NOWAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 4096, 4096, 0, securityAttributes(sd))
	return name, h, err
}

func openHandoffPipe(name string) (windows.Handle, error) {
	suffix, ok := strings.CutPrefix(name, pipePrefix)
	if !ok || len(suffix) != 64 {
		return 0, errors.New("安装交接管道名称无效")
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return 0, err
	}
	path, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateFile(path, windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.FILE_WRITE_ATTRIBUTES,
		0, nil, windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return 0, err
	}
	mode := uint32(windows.PIPE_READMODE_MESSAGE | windows.PIPE_NOWAIT)
	if err = windows.SetNamedPipeHandleState(h, &mode, nil, nil); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

func waitHandoff(ctx context.Context, retry error, step func() error) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := step()
		if !errors.Is(err, retry) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func readHandoff(ctx context.Context, h windows.Handle) (handoffFrame, error) {
	var f handoffFrame
	err := waitHandoff(ctx, windows.ERROR_NO_DATA, func() error {
		var n uint32
		if err := windows.ReadFile(h, f[:], &n, nil); err != nil {
			return err
		}
		if n != uint32(len(f)) {
			return errors.New("安装交接消息长度错误")
		}
		return nil
	})
	return f, err
}

func writeHandoff(ctx context.Context, h windows.Handle, f handoffFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var n uint32
	if err := windows.WriteFile(h, f[:], &n, nil); err != nil {
		return err
	}
	if n != uint32(len(f)) {
		return io.ErrShortWrite
	}
	return nil
}

func offerMachineHandoff(ctx context.Context, h windows.Handle, helper handoffPeer, binding machineHandoff) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := waitHandoff(ctx, windows.ERROR_PIPE_LISTENING, func() error {
		err := windows.ConnectNamedPipe(h, nil)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return nil
		}
		return err
	}); err != nil {
		return err
	}
	if err := verifyHandoffPeer(h, helper, false); err != nil {
		return err
	}
	var nonce [sha256.Size]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	f, err := binding.frame(nonce)
	if err != nil {
		return err
	}
	if err = writeHandoff(ctx, h, f); err != nil {
		return err
	}
	want := f
	want[0] = 'A'
	reply, err := readHandoff(ctx, h)
	if err != nil {
		return fmt.Errorf("读取安装辅助进程确认：%w", err)
	}
	if reply != want {
		return errors.New("安装辅助进程未确认相同载荷与操作")
	}
	f[0] = 'C'
	if err = writeHandoff(ctx, h, f); err != nil {
		return err
	}
	f[0] = 'R'
	reply, err = readHandoff(ctx, h)
	if err != nil {
		return fmt.Errorf("读取安装交接回执：%w", err)
	}
	if reply != f {
		return errors.New("安装交接确认未核实")
	}
	return nil
}

func acceptMachineHandoff(ctx context.Context, h windows.Handle, initiator handoffPeer, binding machineHandoff) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := verifyHandoffPeer(h, initiator, true); err != nil {
		return err
	}
	request, err := readHandoff(ctx, h)
	if err != nil {
		return err
	}
	var nonce [sha256.Size]byte
	copy(nonce[:], request[5:])
	f, err := binding.frame(nonce)
	if err != nil || request != f {
		return errors.New("安装交接的目标、版本、载荷或操作不匹配")
	}
	f[0] = 'A'
	if err = writeHandoff(ctx, h, f); err != nil {
		return err
	}
	f[0] = 'C'
	commit, err := readHandoff(ctx, h)
	if err != nil {
		return fmt.Errorf("读取安装交接许可：%w", err)
	}
	if commit != f {
		return errors.New("未收到匹配的安装交接确认")
	}
	f[0] = 'R'
	return writeHandoff(ctx, h, f)
}
