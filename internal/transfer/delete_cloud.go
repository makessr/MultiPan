package transfer

import (
	"context"
	"errors"
	"net/http"
)

// DeleteInput names one permanent delete. The 诚通 web account has no recycle
// bin flow in the desktop client, so its delete is immediate.
type DeleteInput struct {
	AccountID string `json:"accountId"`
	ParentID  string `json:"parentId"`
	EntryID   string `json:"entryId"`
	Name      string `json:"name"`
	Folder    bool   `json:"folder"`
}

// DeleteAccountEntry permanently removes one entry. Only kinds whose delete was
// verified against the provider are accepted; everything else keeps using the
// recycle-bin receipts.
func DeleteAccountEntry(ctx context.Context, client *http.Client, a Account, parent, id string) error {
	if a.Kind != "ctweb" {
		return errors.New("此网盘请在回收站中删除项目")
	}
	if !validCtWebFolder(parent) || !validCtWebEntryID(id) {
		return errors.New("目标项目位置无效，请刷新目录后重试")
	}
	return ctWebDelete(ctx, client, a, id)
}

// DeleteEntry removes one entry after checking that it still exists in the
// directory the client is looking at, so a stale row cannot delete a different
// item.
func (m *Manager) DeleteEntry(ctx context.Context, in DeleteInput) error {
	if in.ParentID == "" || in.EntryID == "" || in.Name == "" {
		return errors.New("删除目标不完整，请刷新列表")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	var account Account
	for _, a := range m.accounts {
		if a.ID == in.AccountID {
			account = a
			break
		}
	}
	m.mu.Unlock()
	if account.ID == "" {
		return errors.New("账号不存在，请先保存账号")
	}
	client := NewHTTPUploader().Client
	before, err := ListAccountFiles(ctx, client, account, in.ParentID)
	if err != nil {
		return err
	}
	count := 0
	for _, f := range before.Entries {
		if f.ID == in.EntryID && f.Folder == in.Folder && f.Name == in.Name {
			count++
		}
	}
	if count != 1 {
		return errors.New("目标已变化或不在当前目录，请刷新后重试")
	}
	return DeleteAccountEntry(ctx, client, account, in.ParentID, in.EntryID)
}
