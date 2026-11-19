package installation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Used only by the marked sandbox path. Its test policy does not participate
// in the production Known Folder audit.
type protectedMachineInstall struct {
	base, floor, dir string
	policy           machineSecurityPolicy
	directorySD      *windows.SECURITY_DESCRIPTOR
	fileSD           *windows.SECURITY_DESCRIPTOR
	snapshot         *machineSecuritySnapshot
	pinned           map[string]windows.Handle
	staged           map[string]machineNodeSnapshot
	hashes           map[string]string
	checkpoint       *os.File
	resolved         bool
}

// installProtectedMachine exercises the actual installer transaction using
// an isolated parent prepared by the test harness.
func (s *ScopedSandbox) installProtectedMachine(b Bundle, self []byte, opts Options) error {
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid.String()
	directorySD, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)")
	if err != nil {
		return err
	}
	fileSD, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;GRGX;;;BU)")
	if err != nil {
		return err
	}
	opts.protected = &protectedMachineInstall{
		base: filepath.Dir(layout.Directory), floor: s.root,
		policy:      sandboxMachinePolicy(sid),
		directorySD: directorySD, fileSD: fileSD,
	}
	return s.Install(ScopeMachine, b, self, opts)
}

func sandboxMachinePolicy(sid string) machineSecurityPolicy {
	return machineSecurityPolicy{trusted: map[string]bool{sid: true, "S-1-5-18": true, "S-1-5-32-544": true}}
}

func securityAttributes(sd *windows.SECURITY_DESCRIPTOR) *windows.SecurityAttributes {
	return &windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd,
	}
}

func (p *protectedMachineInstall) createDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(name, securityAttributes(p.directorySD))
	runtime.KeepAlive(p.directorySD)
	return err
}

func (p *protectedMachineInstall) pin(path string) error {
	h, err := openPinnedDirectoryWithAccess(path, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL)
	if err != nil {
		return err
	}
	p.pinned[path] = h
	return nil
}

func openPinnedDirectory(path string) (windows.Handle, error) {
	return openPinnedDirectoryWithAccess(path, windows.FILE_READ_ATTRIBUTES)
}

func openPinnedDirectoryWithAccess(path string, access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != windows.FILE_ATTRIBUTE_DIRECTORY {
		windows.CloseHandle(h)
		return 0, errors.New("无法保持安全的目录句柄")
	}
	return h, nil
}

func (p *protectedMachineInstall) prepare(dir string) error {
	p.dir = dir
	p.pinned = make(map[string]windows.Handle)
	p.staged = make(map[string]machineNodeSnapshot)
	p.hashes = make(map[string]string)
	before, err := captureMachineSecurityAtFloor(p.base, dir, p.policy, p.floor)
	if err != nil {
		return err
	}
	for _, entry := range before.entries {
		if entry.directory && entry.path != dir {
			if err = p.pin(entry.path); err != nil {
				return err
			}
		}
	}
	if err = before.Check(); err != nil {
		return err
	}
	if _, err = os.Lstat(dir); os.IsNotExist(err) {
		if err = p.createDirectory(dir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = p.pin(dir); err != nil {
		return err
	}
	p.snapshot, err = captureMachineSecurityAtFloor(p.base, dir, p.policy, p.floor)
	return err
}

func (p *protectedMachineInstall) makeTemp(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	path := filepath.Join(p.dir, prefix+hex.EncodeToString(random[:]))
	if err := p.createDirectory(path); err != nil {
		return "", err
	}
	if err := p.pin(path); err != nil {
		return "", err
	}
	if _, err := p.policy.node(path, true, true, false, machineMutationMask); err != nil {
		return "", err
	}
	return path, nil
}

func (p *protectedMachineInstall) writeStage(path string, data []byte) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, securityAttributes(p.fileSD), windows.CREATE_NEW, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(p.fileSD)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	writeAndRead := func() error {
		if _, err := f.Write(data); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != Digest(data) {
			return errors.New("暂存文件回读摘要不一致")
		}
		return nil
	}
	if err := writeAndRead(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	snapshot, err := p.policy.node(path, false, true, false, machineMutationMask)
	if err == nil {
		p.staged[filepath.Base(path)] = snapshot
		p.hashes[filepath.Base(path)] = Digest(data)
	}
	return err
}

func (p *protectedMachineInstall) check(stage, backup string, names []string) error {
	if err := p.snapshot.Check(); err != nil {
		return err
	}
	for _, dir := range []string{stage, backup} {
		if _, err := p.policy.node(dir, true, true, false, machineMutationMask); err != nil {
			return err
		}
	}
	for _, name := range names {
		path := filepath.Join(stage, name)
		got, err := p.policy.node(path, false, true, false, machineMutationMask)
		if err != nil || got != p.staged[name] {
			return errors.New("暂存文件身份、内容属性或权限发生变化")
		}
		data, err := os.ReadFile(path)
		if err != nil || Digest(data) != p.hashes[name] {
			return errors.New("暂存文件内容发生变化")
		}
		if _, err := os.Lstat(filepath.Join(backup, name)); !os.IsNotExist(err) {
			return errors.New("备份目录出现同名对象")
		}
	}
	return nil
}

func (p *protectedMachineInstall) cleanup(path string, names []string) {
	for _, name := range names {
		want, ok := p.staged[name]
		if !ok {
			continue
		}
		f, err := openMachineFile(filepath.Join(path, name))
		if err != nil {
			continue
		}
		if got, err := p.policy.nodeHandle(windows.Handle(f.Fd()), false, true, machineMutationMask); err == nil &&
			got.Volume == want.Volume && got.IndexHigh == want.IndexHigh && got.IndexLow == want.IndexLow {
			_ = disposeMachineFile(f)
		}
		_ = f.Close()
	}
	windows.CloseHandle(p.pinned[path])
	delete(p.pinned, path)
	_ = os.Remove(path)
}

func (p *protectedMachineInstall) close() {
	p.finishCheckpoint()
	for path, handle := range p.pinned {
		windows.CloseHandle(handle)
		delete(p.pinned, path)
	}
}

func moveWithoutReplace(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, 0)
}
