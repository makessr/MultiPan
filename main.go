package main

import (
	"embed"
	"multipan/internal/desktop"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

//go:embed frontend/src/version.ts
var versionSource string

//go:embed build/update-channel.json
var updateChannel []byte

func main() {
	desktop.Run(desktop.Resources{
		Assets:        assets,
		Icon:          trayIcon,
		VersionSource: versionSource,
		UpdateChannel: updateChannel,
	})
}
