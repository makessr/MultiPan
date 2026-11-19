package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
	"io"
	"log"
	"multipan/internal/appguard"
	"multipan/internal/lanzoulogin"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

//go:embed build/appicon.png
var trayIcon []byte

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	executable, err := os.Executable()
	var releaseProgram func()
	if err == nil {
		releaseProgram, err = appguard.AcquireRun(executable)
	}
	if err != nil {
		text := "程序目录正在更新或无法锁定程序文件，请退出安装器后重试。"
		if len(os.Args) > 1 && strings.HasSuffix(os.Args[1], "-login") {
			_ = lanzoulogin.WriteResult(os.Stdout, lanzoulogin.Result{Error: text})
			return
		}
		startupError(text)
		return
	}
	defer releaseProgram()
	if len(os.Args) == 3 && os.Args[1] == "--drop-check" {
		// The integration check gets an explicitly empty temporary profile;
		// never load the normal account state or contend with its instance lock.
		profile := filepath.Clean(os.Args[2])
		tempRoot, err := filepath.Abs(os.TempDir())
		absolute, absErr := filepath.Abs(profile)
		if err != nil || absErr != nil || !strings.HasPrefix(strings.ToLower(absolute), strings.ToLower(tempRoot)+string(filepath.Separator)) {
			return
		}
		if _, err = os.Stat(filepath.Join(profile, "state.enc")); !os.IsNotExist(err) {
			return
		}
		runWorkspace(filepath.Join(profile, "state.enc"), func(selection transfer.DropSelection) { _ = json.NewEncoder(os.Stdout).Encode(selection) })
		return
	}
	if len(os.Args) == 3 && (os.Args[1] == "--lanzou-login" || os.Args[1] == "--quark-login" || os.Args[1] == "--pan123-login" || os.Args[1] == "--baidu-login" || os.Args[1] == "--guangya-login") {
		if err := containLoginProcessTree(); err != nil {
			lanzoulogin.WriteResult(os.Stdout, lanzoulogin.Result{Error: "无法创建登录窗口进程组"})
			os.Exit(2)
		}
		runner := lanzoulogin.Run
		if os.Args[1] == "--guangya-login" {
			runner = lanzoulogin.RunGuangya
		}
		if os.Args[1] == "--baidu-login" {
			runner = lanzoulogin.RunBaidu
		}
		if os.Args[1] == "--quark-login" {
			runner = lanzoulogin.RunQuark
		}
		if os.Args[1] == "--pan123-login" {
			runner = lanzoulogin.Run123
		}
		result, err := runner(os.Args[2], func(closeWindow func(), state lanzoulogin.Result) {
			lanzoulogin.WriteResult(os.Stdout, state)
			if state.Ready {
				go func() { io.Copy(io.Discard, os.Stdin); closeWindow() }()
			}
		})
		if err != nil {
			lanzoulogin.WriteResult(os.Stdout, lanzoulogin.Result{Error: "登录窗口：" + err.Error()})
			os.Exit(2)
		}
		if lanzoulogin.WriteResult(os.Stdout, result) != nil {
			os.Exit(2)
		}
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		startupError("无法定位用户数据目录")
		return
	}
	// Acquire both the cross-session file lock and legacy session lock before
	// loading/saving any account state. Installer uses the exact same guard.
	releaseProfile, lockErr := appguard.LockProfile(dir)
	if errors.Is(lockErr, appguard.ErrBusy) {
		startupError("MultiPan 已在运行，请使用已打开的窗口。")
		return
	}
	if lockErr != nil {
		startupError("无法锁定本机任务数据，请稍后重试。")
		return
	}
	defer releaseProfile()
	runWorkspace(filepath.Join(dir, "MultiPan", "state.enc"), nil)
}

func runWorkspace(statePath string, dropObserver func(transfer.DropSelection)) {
	manager, err := transfer.NewManager(statePath, transfer.NewHTTPUploader())
	if err != nil {
		startupError("无法读取 MultiPan 本机数据。请保留 %APPDATA%\\MultiPan\\state.enc 文件以便排查；不要删除或覆盖原数据。")
		return
	}
	defer manager.Close()
	service := &DesktopService{manager: manager}
	defer service.CancelLanzouLogin()
	defer service.CancelDownload()
	defer service.CancelApplicationUpdate()
	app := application.New(application.Options{Name: "MultiPan", Icon: trayIcon, Description: "本地文件，多网盘直传", Services: []application.Service{application.NewService(service)}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)}})
	service.app = app
	updateContext, stopUpdateChecks := context.WithCancel(context.Background())
	defer stopUpdateChecks()
	title := "MultiPan · 多网盘上传"
	if dropObserver != nil {
		title = "MultiPan · 拖放检查（临时空状态）"
	}
	service.window = app.Window.NewWithOptions(application.WebviewWindowOptions{Title: title, Width: 1180, Height: 800, MinWidth: 900, MinHeight: 640, EnableFileDrop: true, BackgroundColour: application.NewRGB(245, 247, 250), URL: "/"})
	service.bindFileDrop(dropObserver)
	if dropObserver == nil {
		go service.runApplicationUpdateChecks(updateContext)
		tray := app.SystemTray.New()
		tray.SetIcon(trayIcon)
		tray.SetTooltip("MultiPan · 本地多网盘传输")
		show := func() { service.window.Show(); service.window.Focus() }
		tray.OnClick(show)
		menu := app.NewMenu()
		menu.Add("打开 MultiPan").OnClick(func(*application.Context) { show() })
		menu.Add("退出并停止传输").OnClick(func(*application.Context) { app.Quit() })
		tray.SetMenu(menu)
		service.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if manager.Snapshot().Settings.CloseToTray {
				e.Cancel()
				service.window.Hide()
			}
		})
	}
	if err := app.Run(); err != nil {
		log.Print(err)
	}
}

// The OS closes this job handle on helper exit, terminating only its own
// WebView2 descendants so temporary cookie databases are no longer locked.
var loginProcessJob windows.Handle

func containLoginProcessTree() error {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(job)
		return e
	}
	if e = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); e != nil {
		windows.CloseHandle(job)
		return e
	}
	loginProcessJob = job
	return nil
}

func startupError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("MultiPan")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONINFORMATION)
}
