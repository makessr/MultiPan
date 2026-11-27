package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/sys/windows"
	"multipan/internal/installation"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unsafe"
)

//go:embed payload/*
var payload embed.FS
var messageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

func message(text string, flags uintptr) uintptr {
	body, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString("MultiPan 安装与卸载")
	v, _, _ := messageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), flags)
	return v
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		silent := false
		for _, a := range os.Args[1:] {
			if a == "--silent" {
				silent = true
			}
		}
		if !silent {
			message(e.Error(), 0x10)
		}
		os.Exit(1)
	}
}
func run() error {
	action := flag.String("action", "install", "install or uninstall")
	testRoot := flag.String("test-root", "", "isolated test root")
	silent := flag.Bool("silent", false, "suppress dialogs in isolation or machine integration inspection")
	inspect := flag.Bool("inspect", false, "read bundled metadata")
	inspectIntegration := flag.Bool("inspect-machine-integration", false, "read machine registration and public shortcuts")
	offlineUninstall := flag.Bool("uninstall-protected-machine", false, "uninstall a protected machine fixture in a marked sandbox")
	inspectCheckpoint := flag.Bool("inspect-machine-checkpoint", false, "read a machine checkpoint in a marked sandbox")
	recoverCheckpoint := flag.Bool("recover-machine-checkpoint", false, "restore the recorded starting state in a marked sandbox")
	installDir := flag.String("install-dir", "", "dedicated absolute current-user installation directory")
	helper := flag.Bool("uninstall-helper", false, "internal helper")
	parentPID := flag.Int("parent-pid", 0, "internal uninstall handoff")
	flag.Parse()
	if *action != "install" && *action != "uninstall" {
		return fmt.Errorf("操作无效")
	}
	if *offlineUninstall {
		if *testRoot == "" || *action != "uninstall" || *inspect || *inspectIntegration || *inspectCheckpoint || *recoverCheckpoint || *installDir != "" || *helper || *parentPID != 0 || flag.NArg() != 0 {
			return fmt.Errorf("受保护卸载必须指定独立测试目录且单独运行")
		}
		sandbox, err := installation.OpenScopedSandbox(*testRoot)
		if err != nil {
			return err
		}
		return sandbox.UninstallProtectedMachine()
	}
	if *inspectIntegration {
		if *testRoot != "" || *action != "install" || *inspect || *inspectCheckpoint || *recoverCheckpoint || *installDir != "" || *helper || *parentPID != 0 || flag.NArg() != 0 {
			return fmt.Errorf("机器安装集成核对必须单独运行")
		}
		report, err := installation.InspectMachineIntegration()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if *silent && *testRoot == "" {
		return fmt.Errorf("静默验证只允许独立测试目录")
	}
	if *inspectCheckpoint || *recoverCheckpoint {
		if (*inspectCheckpoint && *recoverCheckpoint) || *testRoot == "" || *action != "install" || *inspect || *installDir != "" || *helper || *parentPID != 0 || flag.NArg() != 0 {
			return fmt.Errorf("机器安装事务核对和恢复必须单独运行，并指定隔离测试目录")
		}
		sandbox, err := installation.OpenScopedSandbox(*testRoot)
		if err != nil {
			return err
		}
		if *recoverCheckpoint {
			report, err := sandbox.RecoverMachineCheckpoint()
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		report, err := sandbox.ReviewMachineCheckpoint()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	data, e := payload.ReadFile("payload/payload.zip")
	if e != nil {
		return fmt.Errorf("未构建安装载荷，请运行 scripts/package-installer.ps1")
	}
	bundle, e := installation.Decode(data)
	if e != nil {
		return e
	}
	arch, e := installation.ExecutableArchitecture(bytes.NewReader(bundle.Files[installation.App]))
	if e != nil || arch != runtime.GOARCH {
		return fmt.Errorf("安装器和内嵌客户端架构不一致")
	}
	if *inspect {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": bundle.Version, "scope": "current-user", "architecture": installation.InstallerPlatform(arch), "files": installation.PayloadNames, "webViewInstalled": installation.WebViewInstalled()})
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	local := os.Getenv("LOCALAPPDATA")
	dir := filepath.Join(local, "Programs", "MultiPan")
	isolated := *testRoot != ""
	if isolated {
		abs, e := filepath.Abs(*testRoot)
		if e != nil {
			return e
		}
		temp, e := filepath.Abs(os.TempDir())
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(temp, abs)
		if e != nil || rel == "." || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.Base(abs), "multipan-setup-check-") {
			return fmt.Errorf("测试目录必须位于本机临时目录且使用指定前缀")
		}
		if _, e = os.Stat(filepath.Join(abs, ".multipan-setup-test")); e != nil {
			return fmt.Errorf("缺少隔离测试标识")
		}
		dir = filepath.Join(abs, "program")
		config = filepath.Join(abs, "profile")
	}
	selfPath, e := os.Executable()
	if e != nil {
		return e
	}
	integration := installation.Integration{}
	registered := ""
	if !isolated {
		integration, e = installation.DefaultIntegration()
		if e != nil {
			return e
		}
		registered, e = integration.RegisteredLocation()
		if e != nil {
			return e
		}
	}
	requested := *installDir
	if !*silent && *action == "install" && registered == "" && requested == "" {
		choice := message("选择安装位置\n\n是：使用默认目录\n"+dir+"\n\n否：自选父目录，程序放在其中的 MultiPan 子目录\n取消：退出安装\n\n仅安装到当前用户，不申请管理员权限。", 0x23)
		if choice == 2 {
			return nil
		}
		if choice == 7 {
			parent, e := chooseParentFolder()
			if e != nil {
				return e
			}
			if parent == "" {
				return nil
			}
			requested = filepath.Join(parent, "MultiPan")
		}
	}
	dir, e = installation.ResolveLocation(dir, requested, registered, selfPath, *action == "uninstall")
	if e != nil {
		return e
	}
	if isolated {
		abs, _ := filepath.Abs(*testRoot)
		rel, e := filepath.Rel(abs, dir)
		if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("隔离验证的安装目录必须位于指定测试根内")
		}
	}
	if e = installation.ValidateTarget(dir, filepath.Join(config, "MultiPan")); e != nil {
		return e
	}
	self, e := os.ReadFile(selfPath)
	if e != nil {
		return e
	}
	if *helper {
		if *parentPID <= 0 {
			return fmt.Errorf("卸载辅助程序缺少父进程标识")
		}
		h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(*parentPID))
		if e == nil {
			result, waitErr := windows.WaitForSingleObject(h, 30000)
			windows.CloseHandle(h)
			if waitErr != nil || result != windows.WAIT_OBJECT_0 {
				return fmt.Errorf("原卸载程序尚未退出，请稍后重试")
			}
		} else if e != windows.ERROR_INVALID_PARAMETER {
			return fmt.Errorf("无法确认原卸载程序退出：%w", e)
		}
	}
	if *action == "uninstall" && strings.EqualFold(filepath.Dir(selfPath), dir) {
		if *helper {
			return fmt.Errorf("卸载辅助程序位置无效")
		}
		f, e := os.CreateTemp("", "multipan-uninstall-*.exe")
		if e != nil {
			return e
		}
		name := f.Name()
		if _, e = f.Write(self); e != nil {
			f.Close()
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		args := []string{"--action", "uninstall", "--uninstall-helper", "--parent-pid", strconv.Itoa(os.Getpid()), "--install-dir", dir}
		if isolated {
			args = append(args, "--test-root", *testRoot)
			if *silent {
				args = append(args, "--silent")
			}
		}
		cmd := exec.Command(name, args...)
		if isolated {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		return cmd.Start()
	}
	desktop := true
	if !*silent {
		label := "安装或升级到 " + bundle.Version + "\n\n程序目录：" + dir + "\n账号及任务数据保留在：" + filepath.Join(config, "MultiPan") + "\n\n继续前请从托盘退出 MultiPan。"
		if *action == "uninstall" {
			label = "卸载 MultiPan 程序和本程序快捷方式。\n\n程序目录：" + dir + "\n保留账号、任务、下载文件和目录中的其他文件。\n\n确认卸载？"
		}
		if isolated {
			label = "【隔离演示，不操作正式安装或用户数据】\n\n" + label
		}
		if message(label, 0x21) != 1 {
			return nil
		}
		if *action == "install" {
			desktop = message("是否创建桌面快捷方式？\n开始菜单入口会自动创建。", 0x24) == 6
		}
	}
	release, e := installation.LockProfile(config)
	if e != nil {
		return e
	}
	defer release()
	if !isolated {
		if e = integration.Preflight(dir); e != nil {
			return e
		}
	}
	if e = installation.WritableManaged(dir); e != nil {
		return e
	}
	warnings := []string{}
	if *action == "install" {
		e = installation.Install(dir, bundle, self, installation.Options{})
		if e == nil && !isolated {
			warnings = integration.Apply(dir, bundle.Version, desktop)
		}
	} else {
		e = installation.Uninstall(dir)
		if e == nil && !isolated {
			warnings = integration.Remove(dir)
		}
	}
	if e != nil {
		return e
	}
	release()
	if *silent {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"action": *action, "version": bundle.Version, "directory": dir, "isolated": true, "warnings": warnings})
	}
	text := "安装完成。可从开始菜单或程序目录打开 MultiPan。\n账号与任务数据保留。"
	if *action == "uninstall" {
		text = "卸载完成。账号、任务、下载及目录中的其他文件已保留。"
	}
	if !installation.WebViewInstalled() && *action == "install" {
		text += "\n\n运行需要 Microsoft Edge WebView2 Runtime。请从微软官方渠道安装后再打开程序。"
	}
	if len(warnings) > 0 {
		text += "\n\n" + strings.Join(warnings, "\n")
	}
	message(text, 0x40)
	return nil
}
