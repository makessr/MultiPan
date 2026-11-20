package main

import (
	"context"
	"embed"
	"log"
	"multipan/internal/transfer"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed build/appicon.png
var trayIcon []byte

//go:embed all:frontend/dist
var assets embed.FS

func runWorkspace(statePath string, dropObserver func(transfer.DropSelection)) {
	manager, err := transfer.NewManager(statePath, transfer.NewHTTPUploader())
	if err != nil {
		startupError("无法读取 MultiPan 本机数据。请保留 " + statePath + " 文件以便排查。")
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
