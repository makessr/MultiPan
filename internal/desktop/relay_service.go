package desktop

import (
	"context"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) QueueCloudRelays(inputs []transfer.RelayInput) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.QueueRelays(ctx, inputs)
}
func (s *DesktopService) QueueCloudRelaySnapshots(inputs []transfer.RelayInput) (int, error) {
	return s.manager.QueueRelaySnapshots(context.Background(), inputs)
}
func (s *DesktopService) QueueCloudRelayDirectory(input transfer.RelayInput) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.QueueRelayDirectory(ctx, input)
}
func (s *DesktopService) QueueCloudRelaySnapshotDirectory(input transfer.RelayInput) (int, error) {
	return s.manager.QueueRelaySnapshotDirectory(context.Background(), input)
}
func (s *DesktopService) CloudRelayJobs() []transfer.RelayJob { return s.manager.RelayJobs() }
func (s *DesktopService) CloudRelayPreflight() *transfer.RelayPreflight {
	return s.manager.RelayPreflight()
}
func (s *DesktopService) ListRelaySourceFiles(accountID, parent string) (transfer.FilePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RelaySourceFiles(ctx, accountID, parent)
}
func (s *DesktopService) ListRelaySnapshotSourceFiles(accountID, parent string) (transfer.FilePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RelaySnapshotSourceFiles(ctx, accountID, parent)
}
func (s *DesktopService) ResumeCloudRelay(id string) error { return s.manager.ResumeRelay(id) }
func (s *DesktopService) PauseCloudRelays() error          { return s.manager.PauseRelays() }
func (s *DesktopService) CheckCloudRelay(id string) error  { return s.manager.CheckRelay(id) }
func (s *DesktopService) ClearCloudRelay(id string) error  { return s.manager.ClearRelay(id) }
func (s *DesktopService) MoveCloudRelay(id, confirmation string) error {
	return s.manager.StartRelayMove(id, confirmation)
}
func (s *DesktopService) CheckCloudRelayMove(id string) error { return s.manager.CheckRelayMove(id) }

func (s *DesktopService) MoveCloudRelays(ids []string, folderID, confirmation string) error {
	return s.manager.StartRelayMoves(ids, folderID, confirmation)
}
