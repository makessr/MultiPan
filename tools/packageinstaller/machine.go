package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"multipan/internal/installation"
)

func buildMachineMSI(exe, out, version, platform, wix, wixUI string) error {
	arch := strings.TrimPrefix(platform, "windows-")
	if arch != "x64" && arch != "arm64" {
		return fmt.Errorf("不支持的系统级安装包架构 %s", platform)
	}
	if out == "" {
		out = filepath.Join("releases", "MultiPan-"+version+"-"+platform+"-all-users.msi")
	}
	if !strings.EqualFold(filepath.Ext(out), ".msi") {
		return fmt.Errorf("系统级安装包输出必须为 MSI")
	}
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "multipan-msi-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	rtf := filepath.Join(stage, "license.rtf")
	escape := strings.NewReplacer("\\", "\\\\", "{", "\\{", "}", "\\}", "\r\n", "\\par"+"\n", "\n", "\\par"+"\n")
	if err = os.WriteFile(rtf, []byte("{\\rtf1\\ansi\\deff0 "+escape.Replace(string(license))+"}"), 0600); err != nil {
		return err
	}
	pkg := filepath.Join(stage, filepath.Base(out))
	args := []string{"build", "-arch", arch, "-culture", "zh-CN", "-ext", wixUI,
		"-d", "AppExe=" + exe, "-d", "AppVersion=" + version, "-d", "LicenseRtf=" + rtf,
		"-pdbtype", "none", "-out", pkg, "build/windows/msi/MultiPan.wxs"}
	if strings.EqualFold(filepath.Ext(wix), ".dll") {
		args = append([]string{"--roll-forward", "Major", wix}, args...)
		wix = "dotnet"
	}
	if err = command(wix, args...); err != nil {
		return fmt.Errorf("WiX 打包失败：%w", err)
	}
	source, err := os.Open(pkg)
	if err != nil {
		return err
	}
	defer source.Close()
	content, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	hash := strings.ToUpper(installation.Digest(content))
	if err = writeNewArtifact(out, content); err != nil {
		return err
	}
	if err = writeNewArtifact(out+".sha256", []byte(hash+"  "+filepath.Base(out)+"\r\n")); err != nil {
		_ = os.Remove(out)
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"Installer": out, "Bytes": len(content), "SHA256": hash,
		"Scope": "machine", "Version": version, "Architecture": platform,
	})
}

func writeNewArtifact(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(contents)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
