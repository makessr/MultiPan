package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"multipan/internal/lanzoulogin"
	"multipan/internal/transfer"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (s *DesktopService) LoginLanzou(id, name, folder string) (transfer.Account, error) {
	return s.loginCloud("lanzou", id, name, folder)
}
func (s *DesktopService) LoginQuark(id, name, folder string) (transfer.Account, error) {
	return s.loginCloud("quark", id, name, folder)
}
func (s *DesktopService) Login123(id, name, folder string) (transfer.Account, error) {
	return s.loginCloud("pan123", id, name, folder)
}
func (s *DesktopService) LoginGuangya(id, name, folder string) (transfer.Account, error) {
	return s.loginCloud("guangya", id, name, folder)
}
func (s *DesktopService) LoginBaidu(id, name, folder string) (transfer.Account, error) {
	return s.loginCloud("baidu", id, name, folder)
}
func (s *DesktopService) loginCloud(kind, id, name, folder string) (transfer.Account, error) {
	if strings.TrimSpace(name) == "" {
		return transfer.Account{}, errors.New("请先填写账号名称")
	}
	s.loginMu.Lock()
	if s.loginCancel != nil {
		s.loginMu.Unlock()
		return transfer.Account{}, errors.New("已有登录窗口，请先完成或取消登录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.loginCancel = cancel
	s.loginMu.Unlock()
	defer func() { cancel(); s.loginMu.Lock(); s.loginCancel = nil; s.loginMu.Unlock() }()
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "MultiPan", "LoginSessions")
	if e := os.MkdirAll(root, 0700); e != nil {
		return transfer.Account{}, errors.New("无法创建登录窗口数据目录")
	}
	profile, e := os.MkdirTemp(root, "session-")
	if e != nil {
		return transfer.Account{}, errors.New("无法准备登录窗口")
	}
	defer cleanupLoginProfile(root, profile)
	exe, e := os.Executable()
	if e != nil {
		return transfer.Account{}, e
	}
	arg := "--lanzou-login"
	if kind == "guangya" {
		arg = "--guangya-login"
	}
	if kind == "baidu" {
		arg = "--baidu-login"
	}
	if kind == "quark" {
		arg = "--quark-login"
	}
	if kind == "pan123" {
		arg = "--pan123-login"
	}
	cmd := exec.CommandContext(ctx, exe, arg, profile)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stderr = io.Discard
	input, e := cmd.StdinPipe()
	if e != nil {
		return transfer.Account{}, errors.New("无法连接登录窗口控制通道")
	}
	defer input.Close()
	cmd.Cancel = func() error { return input.Close() }
	cmd.WaitDelay = 3 * time.Second
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return transfer.Account{}, errors.New("无法连接登录窗口")
	}
	if e = cmd.Start(); e != nil {
		return transfer.Account{}, errors.New("无法启动登录窗口")
	}
	result, e := readLoginResult(pipe)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return transfer.Account{}, errors.New("登录已取消或超时，请重试")
	}
	if e != nil || waitErr != nil {
		if result.Error != "" {
			return transfer.Account{}, errors.New(result.Error)
		}
		return transfer.Account{}, errors.New("登录未完成；请重新打开登录窗口，完成后会自动返回")
	}
	if result.Cookie == "" {
		return transfer.Account{}, errors.New("未获取到登录状态，请重试")
	}
	a := transfer.Account{Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: result.Cookie, UserAgent: result.UserAgent}
	if kind == "quark" {
		a.Kind = "quark"
		a.Endpoint = transfer.QuarkAPI
		result.Cookie, e = transfer.CheckQuarkSession(ctx, transfer.NewHTTPUploader().Client, a)
	} else if kind == "guangya" {
		a.Kind = kind
		a.Endpoint = transfer.GuangyaAPI
		a.Username = result.DeviceID
		e = transfer.VerifyGuangyaSession(ctx, transfer.NewHTTPUploader().Client, a)
	} else if kind == "baidu" {
		a.Kind = kind
		a.Endpoint = transfer.BaiduAPI
		result.Cookie, e = transfer.CheckBaiduSession(ctx, transfer.NewHTTPUploader().Client, a)
	} else if kind == "pan123" {
		a.Kind = kind
		a.Endpoint = transfer.Pan123API
		e = transfer.Verify123Session(ctx, transfer.NewHTTPUploader().Client, a)
	} else {
		e = transfer.VerifyLanzouSession(ctx, transfer.NewHTTPUploader().Client, a)
	}
	if e != nil {
		return transfer.Account{}, e
	}
	return s.manager.SaveAccount(transfer.AccountInput{ID: id, Name: name, Kind: kind, Folder: folder, Secret: result.Cookie, UserAgent: result.UserAgent, Username: result.DeviceID})
}

func readLoginResult(r io.Reader) (lanzoulogin.Result, error) {
	decoder := json.NewDecoder(io.LimitReader(r, 128<<10))
	for {
		var result lanzoulogin.Result
		if e := decoder.Decode(&result); e != nil {
			return result, e
		}
		if result.Ready || result.PageLoaded || result.LoadingError != "" {
			continue
		}
		return result, nil
	}
}
func (s *DesktopService) CancelLanzouLogin() {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginCancel != nil {
		s.loginCancel()
	}
}
func cleanupLoginProfile(root, profile string) {
	rootAbs, e1 := filepath.Abs(root)
	target, e2 := filepath.Abs(profile)
	if e1 != nil || e2 != nil || filepath.Dir(target) != rootAbs || !strings.HasPrefix(filepath.Base(target), "session-") {
		return
	}
	// Both paths are verified; only remove the temporary profile created for this login.
	for i := 0; i < 5; i++ {
		if os.RemoveAll(target) == nil {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}
