package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	uninstallRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MultiPan`
	// The all-users MSI marks its shortcuts here. It registers itself in ARP
	// through Windows Installer instead of the legacy uninstall key above.
	msiIntegrationKey = `Software\MultiPan\MachineInstaller`
)

// The all-users MSI creates this literal shortcut name. Tests substitute an
// ASCII name because WScript.Shell cannot save the Chinese name on runners
// whose ANSI code page lacks those characters.
var msiShortcutName = "MultiPan（所有用户）.lnk"

type machineIntegrationEntry struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type MachineIntegrationReport struct {
	// Installer is "msi", "legacy" or "none". It tells which installation
	// owns the machine directory so the report is not read as a failure.
	Installer         string                  `json:"installer"`
	DirectorySecurity machineIntegrationEntry `json:"directorySecurity"`
	Directory         string                  `json:"directory"`
	Registration      machineIntegrationEntry `json:"registration"`
	StartMenu         machineIntegrationEntry `json:"startMenu"`
	Desktop           machineIntegrationEntry `json:"desktop"`
}

// InspectMachineIntegration reads the native machine uninstall registration
// and the two public shortcuts. The report describes observations only.
func InspectMachineIntegration() (MachineIntegrationReport, error) {
	_, dir, err := knownMachineDirectory()
	if err != nil {
		return MachineIntegrationReport{}, err
	}
	menu, err := windows.KnownFolderPath(windows.FOLDERID_CommonPrograms, 0)
	if err != nil {
		return MachineIntegrationReport{}, err
	}
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_PublicDesktop, 0)
	if err != nil {
		return MachineIntegrationReport{}, err
	}
	var auditErr error
	if !msiInstallMarked(registry.LOCAL_MACHINE, msiIntegrationKey, registry.WOW64_64KEY, dir) {
		_, auditErr = auditMachineLocation()
	}
	return inspectMachineIntegration(dir, menu, desktop, registry.LOCAL_MACHINE, uninstallRegistryPath, msiIntegrationKey, registry.WOW64_64KEY, auditErr), nil
}

// A marked key alone is not an installation. Require the shipped program so a
// leftover key cannot make an unrelated directory look installable.
func msiInstallMarked(hive registry.Key, path string, view uint32, dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, App)); err != nil || !info.Mode().IsRegular() {
		return false
	}
	k, err := registry.OpenKey(hive, path, registry.QUERY_VALUE|view)
	if err != nil {
		return false
	}
	defer k.Close()
	for _, name := range []string{"StartMenu", "Desktop"} {
		if value, _, err := k.GetIntegerValue(name); err == nil && value == 1 {
			return true
		}
	}
	return false
}

// The MSI keeps its own registration and creates the two public shortcuts with
// a distinct name. Its directory has no marker file, so checking it against the
// legacy layout would report a healthy installation as missing.
func inspectMSIIntegration(dir, menu, desktop string, blocked bool) MachineIntegrationReport {
	safety := machineIntegrationEntry{Status: "checked", Detail: "系统级安装由 Windows Installer 创建和维护"}
	if blocked {
		safety = machineIntegrationEntry{Status: "blocked", Detail: "系统级安装目录核对未通过"}
	}
	target := filepath.Join(dir, App)
	shortcut := func(directory string) machineIntegrationEntry {
		if blocked {
			return machineIntegrationEntry{Status: "unverified", Detail: "系统级安装目录核对未通过"}
		}
		return inspectShortcutTarget(filepath.Join(directory, msiShortcutName), target, false)
	}
	return MachineIntegrationReport{
		Installer:         "msi",
		Directory:         dir,
		DirectorySecurity: safety,
		Registration:      machineIntegrationEntry{Status: "managed-by-installer", Detail: "Windows Installer 登记，请在「设置 → 应用」中卸载 MultiPan"},
		StartMenu:         shortcut(menu),
		Desktop:           shortcut(desktop),
	}
}

func inspectMachineIntegration(dir, menu, desktop string, hive registry.Key, key, msiKey string, view uint32, auditErr error) MachineIntegrationReport {
	if msiInstallMarked(hive, msiKey, view, dir) {
		return inspectMSIIntegration(dir, menu, desktop, auditErr != nil)
	}
	safety := machineIntegrationEntry{Status: "checked"}
	var version string
	if auditErr == nil {
		auditErr = checkMachineInterrupted(dir)
	}
	if auditErr != nil {
		safety = machineIntegrationEntry{Status: "blocked", Detail: auditErr.Error()}
	} else {
		markerPath := filepath.Join(dir, Marker)
		if err := inspectLocalFile(markerPath); err == nil {
			marker, err := readMarkerForScope(dir, ScopeMachine)
			if err != nil {
				safety = machineIntegrationEntry{Status: "blocked", Detail: err.Error()}
			} else {
				version = marker.Version
			}
		} else if !os.IsNotExist(err) {
			safety = machineIntegrationEntry{Status: "blocked", Detail: err.Error()}
		}
	}

	target := filepath.Join(dir, App)
	blocked := safety.Status == "blocked"
	report := MachineIntegrationReport{
		Installer:         "legacy",
		Directory:         dir,
		DirectorySecurity: safety,
		Registration:      inspectMachineRegistration(hive, key, view, dir, version, blocked),
		StartMenu:         inspectPublicShortcut(filepath.Join(menu, "MultiPan.lnk"), target, version, blocked),
		Desktop:           inspectPublicShortcut(filepath.Join(desktop, "MultiPan.lnk"), target, version, blocked),
	}
	if report.Registration.Status == "missing" && report.StartMenu.Status == "missing" && report.Desktop.Status == "missing" {
		report.Installer = "none"
	}
	return report
}

func inspectMachineRegistration(hive registry.Key, path string, view uint32, dir, version string, blocked bool) machineIntegrationEntry {
	k, err := registry.OpenKey(hive, path, registry.QUERY_VALUE|view)
	if errors.Is(err, registry.ErrNotExist) {
		return machineIntegrationEntry{Status: "missing"}
	}
	if err != nil {
		return machineIntegrationEntry{Status: "unreadable", Detail: "无法读取安装登记"}
	}
	defer k.Close()

	for _, field := range []struct {
		name, expected string
		path           bool
	}{
		{"DisplayName", "MultiPan", false},
		{"InstallScope", string(ScopeMachine), false},
		{"InstallLocation", dir, true},
		{"DisplayIcon", filepath.Join(dir, App), true},
		{"UninstallString", uninstallCommand(dir), false},
		{"DisplayVersion", version, false},
	} {
		if blocked && field.name == "DisplayVersion" {
			return machineIntegrationEntry{Status: "unverified", Detail: "系统级安装目录核对未通过"}
		}
		value, kind, err := k.GetStringValue(field.name)
		if err != nil && !errors.Is(err, registry.ErrNotExist) && !errors.Is(err, registry.ErrUnexpectedType) {
			return machineIntegrationEntry{Status: "unreadable", Detail: "无法读取登记字段 " + field.name}
		}
		matches := value == field.expected
		if field.path {
			matches = filepath.IsAbs(value) && filepath.Clean(value) == value && sameLocation(value, field.expected)
		}
		if err != nil || kind != registry.SZ || !matches || field.expected == "" {
			return machineIntegrationEntry{Status: "conflict", Detail: "登记字段缺失或不一致 " + field.name}
		}
	}
	return machineIntegrationEntry{Status: "matches"}
}

// Check ancestors from the volume root down before accessing a file. Public
// Known Folders can be redirected. Reject network paths and junctions.
func inspectLocalFile(path string) error {
	dir := filepath.Dir(path)
	volume := filepath.VolumeName(dir)
	if len(volume) != 2 || volume[1] != ':' || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("集成路径不是本机绝对路径")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return fmt.Errorf("集成路径需要本机固定磁盘")
	}
	var ancestors []string
	for p := dir; ; p = filepath.Dir(p) {
		ancestors = append(ancestors, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for j := len(ancestors) - 1; j >= 0; j-- {
		if err := noLink(ancestors[j]); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("集成路径不是普通文件")
	}
	return noLink(path)
}

func inspectPublicShortcut(path, target, version string, blocked bool) machineIntegrationEntry {
	entry := inspectShortcutTarget(path, target, blocked)
	if entry.Status != "matches" {
		return entry
	}
	if version == "" {
		return machineIntegrationEntry{Status: "conflict", Detail: "缺少系统级安装标识"}
	}
	return entry
}

// inspectShortcutTarget checks the public shortcut fields that a user clicks.
// The MSI layout has no marker file to compare a version against.
func inspectShortcutTarget(path, target string, blocked bool) machineIntegrationEntry {
	if err := inspectLocalFile(path); os.IsNotExist(err) {
		return machineIntegrationEntry{Status: "missing"}
	} else if err != nil {
		return machineIntegrationEntry{Status: "unreadable", Detail: err.Error()}
	}
	if blocked {
		return machineIntegrationEntry{Status: "unverified", Detail: "系统级安装目录核对未通过"}
	}
	result := machineIntegrationEntry{Status: "matches"}
	err := withShortcut(path, func(link *ole.IDispatch) error {
		for _, field := range []struct {
			name, expected string
		}{
			{"TargetPath", target},
			{"WorkingDirectory", filepath.Dir(target)},
			{"Arguments", ""},
		} {
			value, err := oleutil.GetProperty(link, field.name)
			if err != nil {
				return err
			}
			actual := value.ToString()
			value.Clear()
			matches := actual == ""
			if field.expected != "" {
				matches = filepath.IsAbs(actual) && filepath.Clean(actual) == actual && sameLocation(actual, field.expected)
			}
			if !matches {
				result = machineIntegrationEntry{Status: "conflict", Detail: "快捷方式字段不一致 " + field.name}
				break
			}
		}
		return nil
	})
	if err != nil {
		return machineIntegrationEntry{Status: "unreadable", Detail: "无法读取快捷方式属性"}
	}
	return result
}
