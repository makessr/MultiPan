//go:build linux || darwin

package desktop

import (
	"context"
	"errors"
	"multipan/internal/transfer"
)

var errManualLogin = errors.New("请使用手动导入方式配置网盘登录信息")
var errPackageUpdate = errors.New("请通过系统软件源或发布页面更新 MultiPan")

func (s *DesktopService) LoginLanzou(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) LoginQuark(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) Login123(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) LoginGuangya(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) LoginBaidu(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) LoginCtWeb(string, string, string) (transfer.Account, error) {
	return transfer.Account{}, errManualLogin
}
func (s *DesktopService) CancelLanzouLogin() {}

type applicationUpdates struct{}

func (s *DesktopService) ApplicationUpdateChannel() ApplicationUpdateInfo {
	return ApplicationUpdateInfo{CurrentVersion: s.currentAppVersion()}
}
func (s *DesktopService) CheckApplicationUpdate() (ApplicationUpdateInfo, error) {
	return s.ApplicationUpdateChannel(), errPackageUpdate
}
func (s *DesktopService) SetAutomaticApplicationUpdateChecks(bool) error { return errPackageUpdate }
func (s *DesktopService) DownloadApplicationUpdate(string) (ApplicationUpdateDownload, error) {
	return ApplicationUpdateDownload{}, errPackageUpdate
}
func (s *DesktopService) OpenApplicationUpdateInstaller(string) (bool, error) {
	return false, errPackageUpdate
}
func (s *DesktopService) CancelApplicationUpdate()                   {}
func (s *DesktopService) runApplicationUpdateChecks(context.Context) {}
