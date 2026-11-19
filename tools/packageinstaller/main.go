package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"multipan/internal/installation"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func command(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func main() {
	exe := flag.String("exe", "", "application path")
	out := flag.String("out", "", "separate installer output path")
	flag.Parse()
	if e := build(*exe, *out); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func build(exe, out string) error {
	source, e := os.ReadFile("frontend/src/version.ts")
	if e != nil {
		return e
	}
	match := regexp.MustCompile(`APP_VERSION\s*=\s*'([0-9]+\.[0-9]+\.[0-9]+)'`).FindSubmatch(source)
	if len(match) != 2 {
		return fmt.Errorf("缺少应用版本")
	}
	v := string(match[1])
	if exe == "" {
		exe = filepath.Join("bin", "multipan-"+v+".exe")
	}
	exe, e = filepath.Abs(exe)
	if e != nil {
		return e
	}
	appVersion, e := installation.FileVersion(exe)
	if e != nil || appVersion != v {
		return fmt.Errorf("应用程序版本与安装器不符：%s / %s", appVersion, v)
	}
	explicitOutput := out != ""
	if !explicitOutput {
		out = filepath.Join("releases", "MultiPan-"+v+"-windows-x64-setup.exe")
	} else if !strings.EqualFold(filepath.Ext(out), ".exe") {
		return fmt.Errorf("安装器输出必须为 EXE")
	}
	out, e = filepath.Abs(out)
	if e != nil {
		return e
	}
	lock, e := os.OpenFile(".multipan-installer.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("安装器打包正在进行或遗留锁待核对：%w", e)
	}
	lock.Close()
	defer os.Remove(".multipan-installer.lock")
	if explicitOutput {
		for _, name := range []string{out, out + ".sha256"} {
			if _, err := os.Lstat(name); !os.IsNotExist(err) {
				return fmt.Errorf("安装器输出或摘要路径不可用，未覆盖现有文件")
			}
		}
	}
	syso := filepath.Join("tools", "setup", "setup_windows_amd64.syso")
	if _, e = os.Stat(syso); !os.IsNotExist(e) {
		return fmt.Errorf("已有安装器资源文件，未覆盖")
	}
	if e = command("go", "run", "-buildvcs=false", "./tools/setupbundle", "-exe", exe, "-version", v); e != nil {
		return e
	}
	defer os.Remove(syso)
	if e = command("wails3", "generate", "syso", "-arch", "amd64", "-icon", "build/windows/icon.ico", "-manifest", "build/windows/wails.exe.manifest", "-info", "build/windows/info.json", "-out", syso); e != nil {
		return e
	}
	if e = command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-w -s -H windowsgui", "-o", out, "./tools/setup"); e != nil {
		return e
	}
	inspection, e := exec.Command(out, "--inspect").Output()
	if e != nil {
		return e
	}
	var meta struct {
		Version string `json:"version"`
		Scope   string `json:"scope"`
	}
	if json.Unmarshal(inspection, &meta) != nil || meta.Version != v || meta.Scope != "current-user" {
		return fmt.Errorf("安装器内嵌版本或范围不符")
	}
	raw, e := os.ReadFile(out)
	if e != nil {
		return e
	}
	hash := strings.ToUpper(installation.Digest(raw))
	if e = os.WriteFile(out+".sha256", []byte(hash+"  "+filepath.Base(out)+"\r\n"), 0600); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"Installer": out, "Bytes": len(raw), "SHA256": hash, "Scope": meta.Scope, "Version": v})
}
