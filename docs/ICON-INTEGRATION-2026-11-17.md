# PNG application icon integration — 2026-11-17

Repository had no commits; all existing sources were untracked. No existing source was reset or committed. Existing release EXE and ZIP were preserved.

Source: assets/branding/multipan-icon.png, user supplied 816x816 RGBA PNG; transparent corner alpha=0. SHA256 483919B8CD3B6D72153FE747EF24484FF1D27830CE31AB277DFD8308BD5C06E2. build/appicon.png and frontend/public/icon.png are byte-identical copies.

Changed: tools/icon/main.go now copies the approved PNG rather than recreating the old mark; main.go sets application.Options.Icon; internal/lanzoulogin/window_windows.go loads the shared embedded IDI_APPLICATION resource for both class icons; frontend/index.html references icon.png. Wails regenerated build/windows/icon.ico and build/darwin/icons.icns. The Windows main-window class loads IDI_APPLICATION in the installed Wails 3.1.0 source. Existing NSIS MUI_ICON/MUI_UNICON both reference build/windows/icon.ico; MSIX uses the existing Wails icon pipeline. No installer configuration change was necessary. Other-platform template resources and unused legacy frontend/public/icon.svg were not changed.

Backups: assets/branding/backups/_2026-11-17-before-png-icon/ contains original resources and edited source files, with SHA256.txt. Backup hashes verified. Leading underscore prevents go test ./... from compiling backup source. Initial validation exposed this backup-directory issue; resolved and complete build rerun passed.

Validation: scripts/build.ps1 -AppName multipan-0.68.1-icon exited 0. Includes Wails production build, TS/Vite, go test ./..., go vet ./..., ten Node auxiliary checks and five packaged login-window startup checks. Full log: build-icon-verification.log. ICO has six 32-bit entries: 16,32,48,64,128,256. No installer was produced and visual taskbar appearance was not manually inspected.

Output: bin/multipan-0.68.1-icon.exe SHA256 4C511CC6E9EF85884513A43D3CDA0287ECA98CD9B63D72F6CBC71CA368E86602.
Original bin/multipan-0.68.1.exe SHA256 A38797C97DBB0B3CE22534FE4A99266B0D71AD9D1A5ADD7C8234FDCA07474B88 remains unchanged.
