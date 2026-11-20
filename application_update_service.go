//go:build windows

package main

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"multipan/internal/installation"
	"runtime"
	"sync"
	"time"
)

//go:embed build/update-channel.json
var updateChannelJSON []byte

type applicationUpdateChannel struct {
	ReleaseURL   string `json:"releaseURL"`
	PublisherKey string `json:"publisherKey"`
}

type applicationUpdates struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	verified    installation.VerifiedUpdate
	automatic   bool
	lastAttempt time.Time
	lastCheck   time.Time
	checkError  string
	stagedPath  string
	stagedFor   string
	setupDone   <-chan struct{}
}

func readApplicationUpdateChannel() (ApplicationUpdateInfo, string, ed25519.PublicKey) {
	version := currentAppVersion()
	if version == "" {
		return ApplicationUpdateInfo{}, "", nil
	}
	info := ApplicationUpdateInfo{CurrentVersion: version}
	if runtime.GOARCH != "amd64" {
		return info, "", nil
	}
	var channel applicationUpdateChannel
	if json.Unmarshal(updateChannelJSON, &channel) != nil {
		return info, "", nil
	}
	key, err := hex.DecodeString(channel.PublisherKey)
	if err != nil || len(key) != ed25519.PublicKeySize || channel.ReleaseURL == "" {
		return info, "", nil
	}
	info.Configured = true
	return info, channel.ReleaseURL, key
}

func (s *DesktopService) ApplicationUpdateChannel() ApplicationUpdateInfo {
	info, _, _ := readApplicationUpdateChannel()
	info.Automatic = info.Configured && s.manager.AutomaticUpdateChecks()
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	info.Version = s.updates.verified.Version()
	info.Asset = s.updates.verified.AssetName()
	info.CheckError = s.updates.checkError
	if !s.updates.lastCheck.IsZero() {
		info.LastCheck = s.updates.lastCheck.Format(time.RFC3339)
	}
	return info
}

func (s *DesktopService) CheckApplicationUpdate() (ApplicationUpdateInfo, error) {
	return s.checkApplicationUpdate(context.Background(), false)
}

func (s *DesktopService) checkApplicationUpdate(parent context.Context, automatic bool) (ApplicationUpdateInfo, error) {
	info, origin, publisher := readApplicationUpdateChannel()
	if !info.Configured {
		return info, errors.New("此构建尚未配置可信更新源")
	}
	ctx, done, err := s.updates.begin(parent, automatic)
	if err != nil {
		return info, err
	}
	defer done()
	if automatic && !s.manager.AutomaticUpdateChecks() {
		return info, context.Canceled
	}
	s.updates.mu.Lock()
	s.updates.lastAttempt = time.Now()
	s.updates.mu.Unlock()
	verified, err := installation.CheckUpdateOrigin(ctx, origin, publisher, info.CurrentVersion)
	if errors.Is(err, installation.ErrSameUpdateVersion) {
		err = nil
	}
	s.updates.mu.Lock()
	if ctx.Err() != nil {
		s.updates.mu.Unlock()
		return info, ctx.Err()
	}
	s.updates.verified = verified
	s.updates.lastCheck = time.Now()
	s.updates.checkError = ""
	if err != nil {
		s.updates.checkError = err.Error()
	}
	s.updates.mu.Unlock()
	return s.ApplicationUpdateChannel(), err
}

func (s *DesktopService) SetAutomaticApplicationUpdateChecks(enabled bool) error {
	info, _, _ := readApplicationUpdateChannel()
	if enabled && !info.Configured {
		return errors.New("此构建尚未配置可信更新源")
	}
	if err := s.manager.SetAutomaticUpdateChecks(enabled); err != nil {
		return err
	}
	if !enabled {
		s.updates.mu.Lock()
		defer s.updates.mu.Unlock()
		if s.updates.automatic && s.updates.cancel != nil {
			s.updates.cancel()
		}
	}
	return nil
}

func (s *DesktopService) runApplicationUpdateChecks(ctx context.Context) {
	info, _, _ := readApplicationUpdateChannel()
	if !info.Configured {
		return
	}
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !s.manager.AutomaticUpdateChecks() {
			continue
		}
		s.updates.mu.Lock()
		due := s.updates.cancel == nil && time.Since(s.updates.lastAttempt) >= 24*time.Hour
		s.updates.mu.Unlock()
		if due {
			result, _ := s.checkApplicationUpdate(ctx, true)
			if ctx.Err() == nil && result.LastCheck != "" {
				s.app.Event.Emit("multipan:application-update-checked")
			}
		}
	}
}

func (s *DesktopService) DownloadApplicationUpdate(version string) (ApplicationUpdateDownload, error) {
	ctx, done, err := s.updates.begin(context.Background(), false)
	if err != nil {
		return ApplicationUpdateDownload{}, err
	}
	defer done()
	verified := s.updates.verified
	if version == "" || verified.Version() != version {
		return ApplicationUpdateDownload{}, errors.New("请先检查更新并选择已核对的版本")
	}
	dir, err := s.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).
		SetTitle("选择更新安装包保存目录").AttachToWindow(s.window).PromptForSingleSelection()
	if err != nil {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			if cause.Error() == "cancelled by user" || errors.Is(cause, context.Canceled) {
				return ApplicationUpdateDownload{Cancelled: true}, nil
			}
		}
		return ApplicationUpdateDownload{}, errors.New("无法打开安装包保存目录选择窗口")
	}
	if dir == "" || ctx.Err() != nil {
		return ApplicationUpdateDownload{Cancelled: true}, nil
	}
	path, err := verified.StageSignedInstaller(ctx, dir)
	if err == context.Canceled {
		return ApplicationUpdateDownload{Cancelled: true}, nil
	}
	if err == nil {
		s.updates.mu.Lock()
		s.updates.stagedPath = path
		s.updates.stagedFor = version
		s.updates.mu.Unlock()
	}
	return ApplicationUpdateDownload{Path: path}, err
}

func (s *DesktopService) OpenApplicationUpdateInstaller(version string) (bool, error) {
	ctx, done, err := s.updates.begin(context.Background(), false)
	if err != nil {
		return false, err
	}
	defer done()
	s.updates.mu.Lock()
	verified, path, stagedFor, setupDone := s.updates.verified, s.updates.stagedPath, s.updates.stagedFor, s.updates.setupDone
	s.updates.mu.Unlock()
	if setupDone != nil {
		select {
		case <-setupDone:
		default:
			return false, errors.New("更新安装器仍在运行")
		}
	}
	if path == "" || version == "" || version != stagedFor || version != verified.Version() {
		return false, errors.New("请先下载当前清单对应的安装包")
	}
	dialog := s.app.Dialog.Question().AttachToWindow(s.window).SetTitle("打开 MultiPan 更新安装器").
		SetMessage("准备打开 " + version + " 版更新安装器。请先保存正在进行的工作，安装前需从托盘退出 MultiPan。")
	cancel := dialog.AddButton("取消")
	start := dialog.AddButton("重新核对并打开")
	dialog.SetDefaultButton(cancel).SetCancelButton(cancel)
	confirmed := false
	start.OnClick(func() { confirmed = true })
	dialog.Show()
	if !confirmed || ctx.Err() != nil {
		return false, nil
	}
	finished, err := verified.LaunchSignedInstaller(ctx, path)
	if errors.Is(err, context.Canceled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.updates.mu.Lock()
	s.updates.setupDone = finished
	s.updates.mu.Unlock()
	return true, nil
}

func (s *DesktopService) CancelApplicationUpdate() {
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	if s.updates.cancel != nil {
		s.updates.cancel()
	}
}

func (u *applicationUpdates) begin(parent context.Context, automatic bool) (context.Context, func(), error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		return nil, nil, errors.New("已有更新检查或下载正在处理")
	}
	if automatic && time.Since(u.lastAttempt) < 24*time.Hour {
		return nil, nil, errors.New("尚未到下次自动检查时间")
	}
	ctx, cancel := context.WithCancel(parent)
	u.cancel = cancel
	u.automatic = automatic
	return ctx, func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		cancel()
		u.cancel = nil
		u.automatic = false
	}, nil
}
