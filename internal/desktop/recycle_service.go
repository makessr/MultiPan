package desktop

import (
	"context"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) PreviewCloudRecycleEmpty(accountID string) (transfer.RecycleEmptyPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.PreviewRecycleEmpty(ctx, accountID)
}

func (s *DesktopService) PrepareCloudRecycleEmpty(token, confirmation string) ([]transfer.RecyclePurgePrepared, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.PrepareRecycleEmpty(ctx, token, confirmation)
}

func (s *DesktopService) PrepareCloudRecyclePurge(accountID string, page int, entries []transfer.RecycleEntry) ([]transfer.RecyclePurgePrepared, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.PrepareRecyclePurge(ctx, accountID, page, entries)
}
func (s *DesktopService) PrepareCloudRecyclePurgeAcrossPages(accountID string, selections []transfer.RecyclePurgeSelection) ([]transfer.RecyclePurgePrepared, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.PrepareRecyclePurgeAcrossPages(ctx, accountID, selections)
}
func (s *DesktopService) PurgeCloudRecycle(handle, confirmation string) (transfer.TrashReceipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.PurgeRecycle(ctx, handle, confirmation)
}
func (s *DesktopService) CheckCloudRecyclePurge(id string) (transfer.TrashReceipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckRecyclePurge(ctx, id)
}
func (s *DesktopService) CloudRecyclePurgeHistory(accountID string) []transfer.TrashReceipt {
	return s.manager.RecyclePurgeHistory(accountID)
}
func (s *DesktopService) ClearCloudRecyclePurgeHistory(accountID string) (int, error) {
	return s.manager.ClearRecyclePurgeHistory(accountID)
}

func (s *DesktopService) PrepareCloudTrash(inputs []transfer.TrashInput) ([]transfer.TrashPrepared, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.PrepareTrash(ctx, inputs)
}
func (s *DesktopService) TrashCloudEntry(handle string) (transfer.TrashReceipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.TrashEntry(ctx, handle)
}
func (s *DesktopService) CheckCloudTrash(id string) (transfer.TrashReceipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckTrash(ctx, id)
}
func (s *DesktopService) CloudTrashHistory(accountID string) []transfer.TrashReceipt {
	return s.manager.TrashHistory(accountID)
}
func (s *DesktopService) ClearCloudTrashHistory(accountID string) (int, error) {
	return s.manager.ClearTrashHistory(accountID)
}

func (s *DesktopService) ListCloudRecycle(accountID string, page int) (transfer.RecyclePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ListRecycle(ctx, accountID, page)
}

func (s *DesktopService) ListLanzouRecycleFolder(accountID, entryID, name string) (transfer.LanzouRecycleFolderPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ListLanzouRecycleFolder(ctx, accountID, entryID, name)
}

func (s *DesktopService) PreviewLanzouRecycleCoverage(accountID string) (transfer.LanzouRecycleCoverage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.PreviewLanzouRecycleCoverage(ctx, accountID)
}

func (s *DesktopService) ValidateCloudRecycleRestoreBatch(handles []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateRecycleRestoreBatch(ctx, handles)
}
func (s *DesktopService) ValidateCloudRecycleRestoreBatchTo(handles []string, trail []transfer.CloudFolder) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateRecycleRestoreBatchTo(ctx, handles, trail)
}
func (s *DesktopService) RestoreCloudRecycle(handle string) (transfer.RecycleRestoreResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return s.manager.RestoreRecycle(ctx, handle)
}
func (s *DesktopService) RestoreCloudRecycleTo(handle string, trail []transfer.CloudFolder) (transfer.RecycleRestoreResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return s.manager.RestoreRecycleTo(ctx, handle, trail)
}
func (s *DesktopService) PreviewGuangyaRecycleRestore(handle string) (transfer.GuangyaRestorePreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.PreviewGuangyaRestore(ctx, handle)
}
func (s *DesktopService) PreviewLanzouRecycleRestore(handle string) (transfer.LanzouRecycleRestorePreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.PreviewLanzouRecycleRestore(ctx, handle)
}
func (s *DesktopService) CheckCloudRecycleRestore(id string) (transfer.RecycleRestoreResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckRecycleRestore(ctx, id)
}
func (s *DesktopService) CloudRecycleRestores(accountID string) []transfer.RecycleRestoreResult {
	return s.manager.RecycleRestoreHistory(accountID)
}
func (s *DesktopService) LocateCloudRecycleRestore(accountID, receiptID string) (transfer.RecycleRestoreLocation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.LocateRecycleRestore(ctx, accountID, receiptID)
}
func (s *DesktopService) ClearCloudRecycleRestores(accountID string) (int, error) {
	return s.manager.ClearRecycleRestores(accountID)
}

func (s *DesktopService) ValidateRestored123Moves(accountID, destination string, ids []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateRestored123Moves(ctx, accountID, destination, ids)
}

func (s *DesktopService) ValidateRestoredFileMoves(accountID, destination string, ids []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateRestoredFileMoves(ctx, accountID, destination, ids)
}

func (s *DesktopService) MoveRestored123File(accountID, receiptID, destination string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return s.manager.MoveRestored123File(ctx, accountID, receiptID, destination)
}

func (s *DesktopService) MoveConfirmedRestoredFile(accountID, receiptID, destination string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.MoveConfirmedRestoredFile(ctx, accountID, receiptID, destination)
}

func (s *DesktopService) CheckConfirmedRestoredFileMove(accountID, receiptID, destination string) (transfer.RestoredFileMoveCheck, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return s.manager.CheckConfirmedRestoredFileMove(ctx, accountID, receiptID, destination)
}

func (s *DesktopService) CheckConfirmedRestoredFolderMove(accountID, receiptID, destination string) (transfer.RestoredFileMoveCheck, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return s.manager.CheckConfirmedRestoredFolderMove(ctx, accountID, receiptID, destination)
}

func (s *DesktopService) MoveConfirmedRestoredFolder(accountID, receiptID, destination string, sourceTrail, destinationTrail []transfer.CloudFolder) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return s.manager.MoveConfirmedRestoredFolder(ctx, accountID, receiptID, destination, sourceTrail, destinationTrail)
}

func (s *DesktopService) MoveRestored123Folder(accountID, receiptID, destination string, sourceTrail, destinationTrail []transfer.CloudFolder) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.MoveRestored123Folder(ctx, accountID, receiptID, destination, sourceTrail, destinationTrail)
}
