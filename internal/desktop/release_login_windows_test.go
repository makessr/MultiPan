package desktop

import (
	"context"
	"encoding/json"
	"io"
	"multipan/internal/lanzoulogin"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Run explicitly after building: this exercises the actual GUI executable's entry point.
func TestPackagedLoginWindowStartup(t *testing.T) {
	exe := os.Getenv("MULTIPAN_RELEASE_EXE")
	if exe == "" {
		t.Skip("set MULTIPAN_RELEASE_EXE to test the packaged executable")
	}
	root := t.TempDir()
	profile := filepath.Join(root, "login-profile")
	if e := os.Mkdir(profile, 0700); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for i := 0; i < 25; i++ {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Error("login profile still in use")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	argument := "--lanzou-login"
	if os.Getenv("MULTIPAN_RELEASE_PROVIDER") == "guangya" {
		argument = "--guangya-login"
	}
	if os.Getenv("MULTIPAN_RELEASE_PROVIDER") == "baidu" {
		argument = "--baidu-login"
	}
	if os.Getenv("MULTIPAN_RELEASE_PROVIDER") == "quark" {
		argument = "--quark-login"
	}
	if os.Getenv("MULTIPAN_RELEASE_PROVIDER") == "pan123" {
		argument = "--pan123-login"
	}
	cmd := exec.CommandContext(ctx, exe, argument, profile)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stderr = io.Discard
	input, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { input.Close(); cmd.Wait(); cancel() }()
	var result lanzoulogin.Result
	decoder := json.NewDecoder(io.LimitReader(out, 16384))
	e = decoder.Decode(&result)
	if e != nil || !result.Ready || result.Error != "" || result.Cookie != "" {
		t.Fatalf("packaged login window did not initialize: decode=%v ready=%v error=%q", e, result.Ready, result.Error)
	}
	if !result.ViewVisible || result.ViewWidth < 400 || result.ViewHeight < 300 {
		t.Fatalf("WebView content is hidden or empty: visible=%v width=%d height=%d", result.ViewVisible, result.ViewWidth, result.ViewHeight)
	}
	if !result.DevToolsEnabled {
		t.Fatal("login developer tools are disabled")
	}
	for {
		var next lanzoulogin.Result
		if e = decoder.Decode(&next); e != nil {
			t.Fatalf("login page did not finish loading: %v", e)
		}
		if next.LoadingError != "" {
			t.Fatal(next.LoadingError)
		}
		if next.PageLoaded {
			break
		}
	}
}
