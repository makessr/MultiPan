package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Read-only foundation: no production caller enables machine installation.
// A descriptor/identity snapshot is not a write authorization or a transaction.
type machineSecurityPolicy struct{ trusted map[string]bool }

const directoryDeleteChild uint32 = 0x40 // FILE_DELETE_CHILD, Win32 file access rights
const machineMutationMask uint32 = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | directoryDeleteChild
const ancestorMutationMask uint32 = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | directoryDeleteChild
const machineKnownMask uint32 = machineMutationMask | windows.GENERIC_READ | windows.GENERIC_EXECUTE | windows.READ_CONTROL | windows.SYNCHRONIZE | windows.FILE_READ_DATA | windows.FILE_READ_EA | windows.FILE_EXECUTE | windows.FILE_READ_ATTRIBUTES

func productionMachinePolicy() (machineSecurityPolicy, error) {
	p := machineSecurityPolicy{trusted: map[string]bool{}}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			return p, err
		}
		p.trusted[sid.String()] = true
	}
	// Resolve the local service principal, never an arbitrary account from UI.
	ti, _, _, err := windows.LookupSID("", `NT SERVICE\TrustedInstaller`)
	if err != nil {
		return p, err
	}
	p.trusted[ti.String()] = true
	return p, nil
}

func (p machineSecurityPolicy) descriptor(sd *windows.SECURITY_DESCRIPTOR, protected, ancestor bool, mutationMask uint32) (string, error) {
	if sd == nil || !sd.IsValid() {
		return "", errors.New("机器目录安全描述符无法核对")
	}
	defer func() { runtime.KeepAlive(sd) }()
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() || !p.trusted[owner.String()] {
		return "", errors.New("机器目录或受管文件的所有者不受信任")
	}
	control, _, err := sd.Control()
	if err != nil || protected && control&windows.SE_DACL_PROTECTED == 0 {
		return "", errors.New("机器程序根必须具有受保护的DACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || control&windows.SE_DACL_PRESENT == 0 {
		return "", errors.New("机器目录不能使用空指针或缺失DACL")
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, n, &ace); err != nil || ace == nil {
			return "", errors.New("机器目录ACE读取失败")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			return "", errors.New("机器目录含尚不支持的ACE类型")
		}
		if uint32(ace.Mask)&^machineKnownMask != 0 || ace.Header.AceFlags&^uint8(0x1f) != 0 {
			return "", errors.New("机器目录ACE权限位或标志尚不支持")
		}
		offset := unsafe.Offsetof(ace.SidStart)
		if uintptr(ace.Header.AceSize) < offset+8 {
			return "", errors.New("机器目录ACE结构不完整")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || uintptr(sid.Len()) > uintptr(ace.Header.AceSize)-offset {
			return "", errors.New("机器目录ACE身份无效")
		}
		// On an ancestor, an inherit-only ACE cannot modify the directory
		// itself. Descendants on the installation path are audited separately,
		// including the protected installation root and each managed file.
		// Deny ACEs never cancel an unsafe allow for this audit.
		inactiveOnAncestor := ancestor && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0
		if !inactiveOnAncestor && ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && uint32(ace.Mask)&mutationMask != 0 && !p.trusted[sid.String()] {
			return "", errors.New("机器目录向非受信任身份授予修改权限")
		}
	}
	sddl := sd.String()
	if sddl == "" {
		return "", errors.New("机器目录权限摘要生成失败")
	}
	return Digest([]byte(sddl)), nil
}

type machineNodeSnapshot struct {
	Missing     bool
	Volume      uint32
	IndexHigh   uint32
	IndexLow    uint32
	SizeHigh    uint32
	SizeLow     uint32
	LastWrite   windows.Filetime
	Permissions string
}

// No GetNamedSecurityInfo path race: permissions and identity come from the
// same non-following handle. No descriptor or SID is returned to UI/logs.
func (p machineSecurityPolicy) node(path string, directory, protected, optional bool, mask uint32) (machineNodeSnapshot, error) {
	var out machineNodeSnapshot
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return out, err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if optional && os.IsNotExist(err) {
			out.Missing = true
			return out, nil
		}
		return out, err
	}
	defer windows.CloseHandle(h)
	return p.nodeHandle(h, directory, protected, mask)
}

func (p machineSecurityPolicy) nodeHandle(h windows.Handle, directory, protected bool, mask uint32) (machineNodeSnapshot, error) {
	var out machineNodeSnapshot
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return out, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || !directory && info.NumberOfLinks != 1 {
		return out, errors.New("机器安装对象类型、链接或重解析属性不安全")
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return out, err
	}
	out.Permissions, err = p.descriptor(sd, protected, directory && !protected, mask)
	if err != nil {
		return out, err
	}
	out.Volume, out.IndexHigh, out.IndexLow = info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow
	if !directory {
		out.SizeHigh, out.SizeLow, out.LastWrite = info.FileSizeHigh, info.FileSizeLow, info.LastWriteTime
	}
	return out, nil
}

type machineAuditEntry struct {
	path                           string
	directory, protected, optional bool
	mask                           uint32
	before                         machineNodeSnapshot
}

type machineSecuritySnapshot struct {
	policy  machineSecurityPolicy
	entries []machineAuditEntry
}

func knownMachineDirectory() (string, string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFilesX64, 0)
	if err != nil {
		return "", "", err
	}
	return base, filepath.Join(base, "MultiPan"), nil
}

func auditMachineLocation() (*machineSecuritySnapshot, error) {
	base, dir, err := knownMachineDirectory()
	if err != nil {
		return nil, err
	}
	policy, err := productionMachinePolicy()
	if err != nil {
		return nil, err
	}
	return captureMachineSecurity(base, dir, policy)
}

// The production base must come exclusively from KnownFolderPath. Tests use
// a private synthetic base and policy, never change Program Files ownership.
func captureMachineSecurity(base, dir string, p machineSecurityPolicy) (*machineSecuritySnapshot, error) {
	return captureMachineSecurityAtFloor(base, dir, p, filepath.VolumeName(base)+`\`)
}

// Tests bound the audit to their owned temp fixture. Production always calls
// the wrapper above, which audits every ancestor through the volume root.
func captureMachineSecurityAtFloor(base, dir string, p machineSecurityPolicy, floor string) (*machineSecuritySnapshot, error) {
	volume := filepath.VolumeName(base)
	if len(volume) != 2 || !filepath.IsAbs(base) || filepath.Clean(base) != base || filepath.Dir(base) == base || !sameLocation(filepath.Join(base, "MultiPan"), dir) || filepath.Clean(dir) != dir {
		return nil, errors.New("机器目录必须为可信Program Files中的固定MultiPan子目录")
	}
	if !filepath.IsAbs(floor) || filepath.Clean(floor) != floor || !withinLocation(floor, base) {
		return nil, errors.New("机器目录祖先核对边界无效")
	}
	root, _ := windows.UTF16PtrFromString(volume + `\`)
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, errors.New("机器目录必须位于本机固定磁盘")
	}
	if err := safeDirectory(dir); err != nil {
		return nil, err
	}
	s := &machineSecuritySnapshot{policy: p}
	// Ancestors are audited for structural replacement/ACL rights. The final
	// parent additionally must not allow ordinary users to pre-create entries.
	for path := base; ; path = filepath.Dir(path) {
		mask := uint32(ancestorMutationMask)
		if path == base {
			mask = machineMutationMask
		}
		s.entries = append(s.entries, machineAuditEntry{path: path, directory: true, mask: mask})
		if sameLocation(path, floor) {
			break
		}
	}
	s.entries = append(s.entries, machineAuditEntry{path: dir, directory: true, protected: true, optional: true, mask: machineMutationMask})
	for _, n := range append(append(append([]string{}, PayloadNames...), Uninstaller), Marker) {
		s.entries = append(s.entries, machineAuditEntry{path: filepath.Join(dir, n), optional: true, mask: machineMutationMask})
	}
	for i := range s.entries {
		e := &s.entries[i]
		var err error
		e.before, err = p.node(e.path, e.directory, e.protected, e.optional, e.mask)
		if err != nil {
			return nil, fmt.Errorf("机器安装只读权限核对失败：%w", err)
		}
	}
	return s, nil
}

func (s *machineSecuritySnapshot) Check() error {
	if s == nil || len(s.entries) == 0 {
		return errors.New("缺少机器安装权限快照")
	}
	for _, e := range s.entries {
		after, err := s.policy.node(e.path, e.directory, e.protected, e.optional, e.mask)
		if err != nil {
			return err
		}
		if after != e.before {
			return errors.New("机器安装对象身份、属性或权限已变化，未继续")
		}
	}
	return nil
}
