package desktop

import (
	"context"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) CloudShares() []transfer.ShareRecord {
	return s.manager.ShareHistory()
}

func (s *DesktopService) LocateLanzouShareReceipt(accountID, receiptID string) (transfer.LanzouShareLocation, error) {
	return s.manager.LanzouShareLocation(accountID, receiptID)
}

func (s *DesktopService) ManagedCloudShares(accountID string, page int) (transfer.ManagedSharePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedShares(ctx, accountID, page)
}

func (s *DesktopService) ManagedCloudSharesNext(accountID, token string) (transfer.ManagedSharePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedSharesNext(ctx, accountID, token)
}

func (s *DesktopService) ManagedQuarkShares(accountID string, page int, filter string) (transfer.ManagedSharePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedQuarkShares(ctx, accountID, page, filter)
}

func (s *DesktopService) ManagedPan123PaidShares(accountID string) (transfer.ManagedSharePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedPan123PaidShares(ctx, accountID)
}

func (s *DesktopService) ReadManagedCloudShare(handle string) (transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedShareLink(ctx, handle)
}

func (s *DesktopService) ReadManagedQuarkShareLinks(handles []string) ([]*transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedQuarkShareLinks(ctx, handles)
}

func (s *DesktopService) ReadManagedQuarkShareRestrictions(handle string) (transfer.QuarkShareRestrictions, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ManagedQuarkShareRestrictions(ctx, handle)
}

func (s *DesktopService) ReadLanzouShareReceiptLink(accountID, receiptID string) (transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ReadLanzouShareReceiptLink(ctx, accountID, receiptID)
}

func (s *DesktopService) ReadLanzouShareReceiptLinks(accountID string, ids []string) ([]*transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ReadLanzouShareReceiptLinks(ctx, accountID, ids)
}

func (s *DesktopService) EditLanzouShareReceiptPassword(accountID, receiptID, expectedURL, expectedPassword, password string, clear bool) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.EditLanzouShareReceiptPassword(ctx, accountID, receiptID, expectedURL, expectedPassword, password, clear)
}

func (s *DesktopService) ReadLanzouDirectoryShare(accountID, parentID, entryID, name string, folder bool) (transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ReadLanzouDirectoryShare(ctx, accountID, parentID, entryID, name, folder)
}
func (s *DesktopService) ReadLanzouDirectoryShares(accountID string, entries []transfer.LanzouDirectoryShareEntry) ([]*transfer.ManagedShareLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ReadLanzouDirectoryShares(ctx, accountID, entries)
}

func (s *DesktopService) UpdateLanzouDirectorySharePassword(accountID, parentID, entryID, name string, folder bool, expectedURL, expectedPassword, password string) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.UpdateLanzouDirectorySharePassword(ctx, accountID, parentID, entryID, name, folder, expectedURL, expectedPassword, password)
}

func (s *DesktopService) ClearLanzouDirectorySharePassword(accountID, parentID, entryID, name string, folder bool, expectedURL, expectedPassword string) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ClearLanzouDirectorySharePassword(ctx, accountID, parentID, entryID, name, folder, expectedURL, expectedPassword)
}

func (s *DesktopService) ValidateManagedCloudShareRevokeBatch(handles []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateShareRevocationBatch(ctx, handles)
}

func (s *DesktopService) ValidateManagedCloudShareRevokeResume(handles []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ValidateShareRevocationResume(ctx, handles)
}

func (s *DesktopService) RevokeManagedCloudShare(handle string) (transfer.ShareRevocationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RevokeManagedShare(ctx, handle)
}
func (s *DesktopService) CheckManagedCloudShareRevoke(id string) (transfer.ShareRevocationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckShareRevocation(ctx, id)
}
func (s *DesktopService) ManagedCloudShareRevokes(accountID string) []transfer.ShareRevocationResult {
	return s.manager.ShareRevocations(accountID)
}
func (s *DesktopService) ClearManagedCloudShareRevokes(accountID string) (int, error) {
	return s.manager.ClearShareRevocations(accountID)
}

func (s *DesktopService) ClearCloudShareHistory() (int, error) {
	return s.manager.ClearShareHistory()
}

func (s *DesktopService) ShareCloudEntry(in transfer.ShareInput) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ShareEntry(ctx, in)
}
func (s *DesktopService) ShareBatchCloudEntry(in transfer.ShareInput) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ShareBatchEntry(ctx, in)
}
func (s *DesktopService) ValidateShareCreationBatch(inputs []transfer.ShareInput) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.ValidateShareCreationBatch(ctx, inputs)
}
func (s *DesktopService) CheckCloudShare(id string) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckShare(ctx, id)
}

func (s *DesktopService) UpdateManagedCloudShare(handle, password string, days int) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.UpdateManagedShare(ctx, handle, password, days)
}
func (s *DesktopService) ActivateManagedBaiduShare(handle string) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ActivateManagedBaiduShare(ctx, handle)
}
func (s *DesktopService) RenameManagedPan123Share(handle, title string) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RenameManagedPan123Share(ctx, handle, title)
}

func (s *DesktopService) SetManagedPan123ShareTrafficLimit(handle string, limit int64) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.SetManagedPan123ShareTrafficLimit(ctx, handle, limit)
}

func (s *DesktopService) SetManagedPan123ShareTrafficMode(handle string, mode int) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.SetManagedPan123ShareTrafficMode(ctx, handle, mode)
}

func (s *DesktopService) SetManagedPan123ShareDownloadSettings(handle string, mode int, limit int64) (transfer.ShareResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.SetManagedPan123ShareDownloadSettings(ctx, handle, mode, limit)
}

func (s *DesktopService) ValidateManagedCloudShareExpiryBatch(handles []string, days int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateManagedShareExpiryBatch(ctx, handles, days)
}

func (s *DesktopService) ValidatePan123CloudShareTrafficBatch(handles []string, permission int, limit int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidatePan123ShareTrafficBatch(ctx, handles, permission, limit)
}

func (s *DesktopService) ValidatePan123CloudShareDownloadSettingsBatch(handles []string, permission int, limit int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidatePan123ShareDownloadSettingsBatch(ctx, handles, permission, limit)
}

func (s *DesktopService) ValidateBaiduCloudShareActivationBatch(handles []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateBaiduShareActivationBatch(ctx, handles)
}

func (s *DesktopService) ValidateGuangyaCloudShareSettingsBatch(handles []string, password string, days int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateGuangyaShareSettingsBatch(ctx, handles, password, days)
}
