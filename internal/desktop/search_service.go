package desktop

import "multipan/internal/transfer"

func (s *DesktopService) StartCloudSearch(in transfer.CloudSearchInput) (transfer.CloudSearchStatus, error) {
	return s.manager.StartCloudSearch(in)
}
func (s *DesktopService) CloudSearchStatus(id string) (transfer.CloudSearchStatus, error) {
	return s.manager.CloudSearchStatus(id)
}
func (s *DesktopService) CancelCloudSearch(id string) error { return s.manager.CancelCloudSearch(id) }
