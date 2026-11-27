package installation

import (
	"fmt"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"multipan/internal/appguard"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

func FileVersion(path string) (string, error) {
	size, e := windows.GetFileVersionInfoSize(path, nil)
	if e != nil || size < 52 {
		return "", fmt.Errorf("无法读取程序版本")
	}
	data := make([]byte, size)
	if e = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&data[0])); e != nil {
		return "", e
	}
	var ptr unsafe.Pointer
	var length uint32
	if e = windows.VerQueryValue(unsafe.Pointer(&data[0]), `\`, unsafe.Pointer(&ptr), &length); e != nil || length < 52 {
		return "", fmt.Errorf("程序版本资源无效")
	}
	fixed := (*[13]uint32)(ptr)
	if fixed[0] != 0xFEEF04BD || fixed[5]&0xffff != 0 {
		return "", fmt.Errorf("程序版本资源不受支持")
	}
	return fmt.Sprintf("%d.%d.%d", fixed[4]>>16, fixed[4]&0xffff, fixed[5]>>16), nil
}

// Shared with main.go; includes the legacy session lock for older versions.
func LockProfile(configDir string) (func(), error) {
	return appguard.LockProfile(configDir)
}
func WritableManaged(dir string) error {
	for _, n := range append(append([]string{}, PayloadNames...), Uninstaller) {
		p := filepath.Join(dir, n)
		if _, e := os.Stat(p); os.IsNotExist(e) {
			continue
		}
		name, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return e
		}
		h, e := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if e != nil {
			return fmt.Errorf("%s 正被使用或不可写，请关闭程序后重试", n)
		}
		windows.CloseHandle(h)
	}
	return nil
}

type Integration struct {
	RegistryPath string
	StartLink    string
	DesktopLink  string
	// Non-user integration is constructed only by the marked temp sandbox.
	// The registry root remains HKCU, including in machine-scope simulations.
	expectedScope  Scope
	boundDirectory string
}

func (i Integration) checkDirectory(dir string) error {
	if !i.expectedScope.valid() || i.expectedScope == ScopeMachine && i.boundDirectory == "" {
		return fmt.Errorf("安装集成缺少明确范围与目录绑定")
	}
	if i.boundDirectory != "" && !sameLocation(dir, i.boundDirectory) {
		return fmt.Errorf("安装集成目录与隔离范围绑定不一致")
	}
	return nil
}

func (i Integration) checkRegistrationScope(k registry.Key) error {
	s, _, e := k.GetStringValue("InstallScope")
	if e == registry.ErrNotExist {
		s, e = "", nil // legacy registrations belong to the current user only
	}
	if e != nil || !Scope(s).valid() || Scope(s).effective() != i.expectedScope.effective() {
		return fmt.Errorf("安装登记范围不一致，未改动")
	}
	return nil
}

func (i Integration) registeredLocation() (string, error) {
	k, e := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE)
	if e == registry.ErrNotExist {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	defer k.Close()
	if e = i.checkRegistrationScope(k); e != nil {
		return "", e
	}
	location, _, e := k.GetStringValue("InstallLocation")
	if e != nil || location == "" {
		return "", fmt.Errorf("当前用户安装登记缺少目录，未改动")
	}
	if e = i.checkDirectory(location); e != nil {
		return "", e
	}
	// Reject network/device/protected paths before attempting any filesystem
	// reads through an untrusted or stale registration value.
	if e = ValidateTarget(location, ""); e != nil {
		return "", e
	}
	if _, e = readMarkerForScope(location, i.expectedScope); e != nil {
		return "", fmt.Errorf("登记目录缺少有效安装标识，请先人工核对：%w", e)
	}
	return location, nil
}

func DefaultIntegration() (Integration, error) {
	menu, e := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
	if e != nil {
		return Integration{}, e
	}
	desktop, e := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if e != nil {
		return Integration{}, e
	}
	return Integration{RegistryPath: uninstallRegistryPath, StartLink: filepath.Join(menu, "MultiPan.lnk"), DesktopLink: filepath.Join(desktop, "MultiPan.lnk")}, nil
}
func (i Integration) Preflight(dir string) error {
	if e := i.checkDirectory(dir); e != nil {
		return e
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE)
	if e != nil && e != registry.ErrNotExist {
		return e
	}
	if e == nil {
		if e = i.checkRegistrationScope(k); e != nil {
			k.Close()
			return e
		}
		location, _, readErr := k.GetStringValue("InstallLocation")
		if i.expectedScope == ScopeMachine && !matchesUninstallCommand(k, dir) {
			k.Close()
			return fmt.Errorf("系统级卸载命令不一致，未改动")
		}
		k.Close()
		if readErr != nil || !strings.EqualFold(filepath.Clean(location), dir) {
			return fmt.Errorf("已有另一位置的 MultiPan 安装登记，请先核对；未修改")
		}
	}
	marker, e := readMarkerForScope(dir, i.expectedScope)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	for _, link := range []string{i.StartLink, i.DesktopLink} {
		if i.expectedScope == ScopeMachine {
			switch result := inspectPublicShortcut(link, filepath.Join(dir, App), marker.Version, false); result.Status {
			case "missing", "matches":
				continue
			default:
				return fmt.Errorf("系统级快捷方式核对未通过：%s", result.Detail)
			}
		}
		if _, e := os.Stat(link); os.IsNotExist(e) {
			continue
		}
		e := withShortcut(link, func(s *ole.IDispatch) error {
			v, e := oleutil.GetProperty(s, "TargetPath")
			if e != nil {
				return e
			}
			defer v.Clear()
			if !strings.EqualFold(filepath.Clean(v.ToString()), filepath.Join(dir, App)) {
				return fmt.Errorf("已有同名快捷方式指向其他程序，请先保留并核对")
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func withShortcut(path string, fn func(*ole.IDispatch) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); e != nil {
		if oe, ok := e.(*ole.OleError); !ok || oe.Code() != 1 {
			return e
		}
	}
	defer ole.CoUninitialize()
	obj, e := oleutil.CreateObject("WScript.Shell")
	if e != nil {
		return e
	}
	defer obj.Release()
	shell, e := obj.QueryInterface(ole.IID_IDispatch)
	if e != nil {
		return e
	}
	defer shell.Release()
	v, e := oleutil.CallMethod(shell, "CreateShortcut", path)
	if e != nil {
		return e
	}
	defer v.Clear()
	return fn(v.ToIDispatch())
}
func Shortcut(path, target string) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return withShortcut(path, func(s *ole.IDispatch) error {
		for _, pair := range [][2]string{{"TargetPath", target}, {"WorkingDirectory", filepath.Dir(target)}, {"IconLocation", target + ",0"}, {"Description", "MultiPan 多网盘客户端"}} {
			if _, e := oleutil.PutProperty(s, pair[0], pair[1]); e != nil {
				return e
			}
		}
		_, e := oleutil.CallMethod(s, "Save")
		return e
	})
}
func removeShortcut(path, target string) error {
	if _, e := os.Stat(path); os.IsNotExist(e) {
		return nil
	}
	return withShortcut(path, func(s *ole.IDispatch) error {
		v, e := oleutil.GetProperty(s, "TargetPath")
		if e != nil {
			return e
		}
		defer v.Clear()
		if !strings.EqualFold(filepath.Clean(v.ToString()), target) {
			return nil
		}
		return os.Remove(path)
	})
}
func uninstallCommand(dir string) string {
	return `"` + filepath.Join(dir, Uninstaller) + `" --action uninstall`
}

func matchesUninstallCommand(k registry.Key, dir string) bool {
	command, kind, err := k.GetStringValue("UninstallString")
	return err == nil && kind == registry.SZ && command == uninstallCommand(dir)
}

func (i Integration) Apply(dir, version string, desktop bool) []string {
	warnings := []string{}
	if e := i.Preflight(dir); e != nil {
		return []string{"安装集成未改动：" + e.Error()}
	}
	if _, e := readMarkerForScope(dir, i.expectedScope); e != nil {
		return []string{"安装集成缺少匹配范围的标识：" + e.Error()}
	}
	app := filepath.Join(dir, App)
	if e := Shortcut(i.StartLink, app); e != nil {
		warnings = append(warnings, "开始菜单快捷方式未完成："+e.Error())
	}
	if desktop {
		if e := Shortcut(i.DesktopLink, app); e != nil {
			warnings = append(warnings, "桌面快捷方式未完成："+e.Error())
		}
	}
	k, _, e := registry.CreateKey(registry.CURRENT_USER, i.RegistryPath, registry.SET_VALUE)
	if e != nil {
		return append(warnings, "安装登记未完成："+e.Error())
	}
	defer k.Close()
	values := map[string]string{"DisplayName": "MultiPan", "DisplayVersion": version, "Publisher": "MultiPan", "InstallLocation": dir, "DisplayIcon": app, "InstallScope": string(i.expectedScope.effective()), "UninstallString": uninstallCommand(dir)}
	for n, v := range values {
		if e = k.SetStringValue(n, v); e != nil {
			warnings = append(warnings, "安装登记未完成："+e.Error())
			break
		}
	}
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return warnings
}
func (i Integration) Remove(dir string) []string {
	warnings := []string{}
	if e := i.checkDirectory(dir); e != nil {
		return []string{"安装集成保留：" + e.Error()}
	}
	// Validate the registration before touching even shortcuts. Missing marker
	// is permitted because Uninstall may already have removed an empty root.
	k, e := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE)
	if e != nil && e != registry.ErrNotExist {
		return []string{"安装登记无法核对，集成保留：" + e.Error()}
	}
	if e == nil {
		scopeErr := i.checkRegistrationScope(k)
		location, _, readErr := k.GetStringValue("InstallLocation")
		k.Close()
		if scopeErr != nil || readErr != nil || !sameLocation(location, dir) {
			return []string{"安装登记位置或范围不一致，集成保留"}
		}
	}
	if _, err := readMarkerForScope(dir, i.expectedScope); err != nil && !os.IsNotExist(err) {
		return []string{"安装标识范围无法核对，集成保留：" + err.Error()}
	}
	for _, link := range []string{i.StartLink, i.DesktopLink} {
		if e := removeShortcut(link, filepath.Join(dir, App)); e != nil {
			warnings = append(warnings, "快捷方式保留："+e.Error())
		}
	}
	k, e = registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE)
	if e == nil {
		location, _, readErr := k.GetStringValue("InstallLocation")
		k.Close()
		if readErr == nil && strings.EqualFold(filepath.Clean(location), dir) {
			if e = registry.DeleteKey(registry.CURRENT_USER, i.RegistryPath); e != nil && e != registry.ErrNotExist {
				warnings = append(warnings, "安装登记保留："+e.Error())
			}
		}
	}
	return warnings
}
func WebViewInstalled() bool {
	p := `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
			k, e := registry.OpenKey(root, p, registry.QUERY_VALUE|view)
			if e != nil {
				continue
			}
			v, _, e := k.GetStringValue("pv")
			k.Close()
			if e == nil && v != "" && v != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}
