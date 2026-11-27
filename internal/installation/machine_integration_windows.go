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

const uninstallRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MultiPan`

type machineIntegrationEntry struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type MachineIntegrationReport struct {
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
	_, auditErr := auditMachineLocation()
	menu, err := windows.KnownFolderPath(windows.FOLDERID_CommonPrograms, 0)
	if err != nil {
		return MachineIntegrationReport{}, err
	}
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_PublicDesktop, 0)
	if err != nil {
		return MachineIntegrationReport{}, err
	}
	return inspectMachineIntegration(dir, menu, desktop, registry.LOCAL_MACHINE, uninstallRegistryPath, registry.WOW64_64KEY, auditErr), nil
}

func inspectMachineIntegration(dir, menu, desktop string, hive registry.Key, key string, view uint32, auditErr error) MachineIntegrationReport {
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
	return MachineIntegrationReport{
		Directory:         dir,
		DirectorySecurity: safety,
		Registration:      inspectMachineRegistration(hive, key, view, dir, version, blocked),
		StartMenu:         inspectPublicShortcut(filepath.Join(menu, "MultiPan.lnk"), target, version, blocked),
		Desktop:           inspectPublicShortcut(filepath.Join(desktop, "MultiPan.lnk"), target, version, blocked),
	}
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
	if err := inspectLocalFile(path); os.IsNotExist(err) {
		return machineIntegrationEntry{Status: "missing"}
	} else if err != nil {
		return machineIntegrationEntry{Status: "unreadable", Detail: err.Error()}
	}
	if blocked {
		return machineIntegrationEntry{Status: "unverified", Detail: "系统级安装目录核对未通过"}
	}
	if version == "" {
		return machineIntegrationEntry{Status: "conflict", Detail: "缺少系统级安装标识"}
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
