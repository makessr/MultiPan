package installation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func sameLocation(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }
func withinLocation(base, child string) bool {
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(base)), strings.ToLower(filepath.Clean(child)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Only a dedicated local, canonical directory is accepted. Never expand env
// variables or silently clean traversal/ambiguous Win32 names into a target.
func ValidateTarget(dir, profile string) error {
	volume := filepath.VolumeName(dir)
	if len(volume) != 2 || volume[1] != ':' || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.TrimSpace(dir) != dir || filepath.Dir(dir) == dir || len(utf16.Encode([]rune(dir))) > 200 {
		return errors.New("请选择本机磁盘中规范的绝对安装目录（最多200个字符），不能使用盘符根目录、网络路径或相对路径")
	}
	for _, part := range strings.Split(dir[3:], `\`) {
		if part == "" || !filepath.IsLocal(part) || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, `/:*?"<>|`) {
			return errors.New("安装目录包含不支持或不明确的名称")
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return errors.New("安装目录包含控制字符")
			}
		}
		name := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
			return errors.New("安装目录不能使用Windows保留名称")
		}
	}
	root, _ := windows.UTF16PtrFromString(volume + `\`)
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return errors.New("当前用户安装只支持本机固定磁盘")
	}
	if profile != "" && (withinLocation(profile, dir) || withinLocation(dir, profile)) {
		return errors.New("安装目录不能包含账号数据目录，也不能放在账号数据目录内")
	}
	for _, protected := range []string{os.Getenv("SystemRoot"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramData")} {
		if protected != "" && (withinLocation(protected, dir) || withinLocation(dir, protected)) {
			return errors.New("当前用户安装不能使用系统或系统级程序目录")
		}
	}
	return safeDirectory(dir)
}

// ResolveLocation never migrates an existing installation. Upgrades and
// repairs stay bound to the registered root; the installed uninstaller stays
// bound to its own marker even when its helper runs from a temporary path.
func ResolveLocation(defaultDir, requested, registered, self string, uninstall bool) (string, error) {
	dir := defaultDir
	if registered != "" {
		dir = registered
	}
	if uninstall && strings.EqualFold(filepath.Base(self), Uninstaller) {
		own := filepath.Dir(self)
		if err := ValidateTarget(own, ""); err != nil {
			return "", err
		}
		if _, err := readMarkerForScope(own, ScopeUser); err != nil {
			return "", errors.New("卸载程序旁缺少有效安装标识，未执行")
		}
		if registered != "" && !sameLocation(own, registered) {
			return "", errors.New("卸载程序位置与当前用户登记不一致，未执行")
		}
		dir = own
	}
	if requested != "" {
		if registered != "" && !sameLocation(requested, registered) {
			return "", errors.New("已有其他位置安装，请先卸载原程序；不自动移动安装目录")
		}
		if uninstall && strings.EqualFold(filepath.Base(self), Uninstaller) && !sameLocation(requested, dir) {
			return "", errors.New("不能将已安装卸载器指向其他目录")
		}
		dir = requested
	}
	return dir, nil
}

func (i Integration) RegisteredLocation() (string, error) { return i.registeredLocation() }
