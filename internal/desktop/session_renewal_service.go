package desktop

import (
	"context"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) SessionRenewals() []transfer.SessionRenewalView {
	return s.manager.SessionRenewals()
}
func (s *DesktopService) SetSessionAutoRenew(id string, enabled bool) error {
	return s.manager.SetSessionAutoRenew(id, enabled)
}
func (s *DesktopService) RenewAccountSession(id string) (transfer.SessionRenewalView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RenewAccountSession(ctx, id)
}
func (s *DesktopService) CheckSessionRenewal(id string) (transfer.SessionRenewalView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckSessionRenewal(ctx, id)
}
