package desktop

import "multipan/internal/transfer"

func (s *DesktopService) StartAccountCheck(ids []string) (transfer.AccountCheckStatus, error) {
	return s.manager.StartAccountCheck(ids)
}
func (s *DesktopService) AccountCheckStatus(id string) (transfer.AccountCheckStatus, error) {
	return s.manager.AccountCheckStatus(id)
}
func (s *DesktopService) CancelAccountCheck(id string) error { return s.manager.CancelAccountCheck(id) }
