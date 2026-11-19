package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const quarkManagedShareReadLimit = 10

// ManagedQuarkShareLinks returns results in selection order. A nil entry can
// use the original single-item read when the batch endpoint omits details.
func (m *Manager) ManagedQuarkShareLinks(ctx context.Context, handles []string) ([]*ManagedShareLink, error) {
	if len(handles) == 0 || len(handles) > quarkManagedShareReadLimit {
		return nil, errors.New("夸克每组可读取 1 至 10 条分享")
	}
	sources := make([]managedShareHandle, len(handles))
	seen := make(map[string]bool, len(handles))
	for i, handle := range handles {
		h, err := decodeManagedShareHandle(handle)
		if err != nil {
			return nil, err
		}
		if h.Kind != "quark" || seen[h.ProviderID] || i > 0 && (h.AccountID != sources[0].AccountID || h.Scope != sources[0].Scope) {
			return nil, errors.New("夸克批量读取要求同一账号的不同分享")
		}
		seen[h.ProviderID] = true
		sources[i] = h
	}
	first := sources[0]
	a, err := m.managedShareAccount(first.AccountID)
	if err != nil || a.Kind != "quark" || downloadScope(a) != first.Scope {
		return nil, errors.New("夸克账号来源已变化，请刷新云端列表")
	}
	eligible := make([]managedShareHandle, 0, len(sources))
	for _, h := range sources {
		if !m.managedShareRevoking(h) {
			eligible = append(eligible, h)
		}
	}
	results := make([]*ManagedShareLink, len(sources))
	if len(eligible) == 0 {
		return results, nil
	}
	links, err := quarkManagedShareLinks(ctx, NewHTTPUploader().Client, a, eligible)
	if err != nil {
		return nil, err
	}
	if current, err := m.managedShareAccount(first.AccountID); err != nil || downloadScope(current) != first.Scope {
		return nil, errors.New("夸克账号来源已变化，请刷新云端列表")
	}
	for i, h := range sources {
		if link, ok := links[h.ProviderID]; ok && !m.managedShareRevoking(h) {
			results[i] = &link
		}
	}
	return results, nil
}

func quarkManagedShareLinks(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle) (map[string]ManagedShareLink, error) {
	ids := make([]string, len(sources))
	for i, h := range sources {
		ids[i] = h.ProviderID
	}
	var data struct {
		Shares map[string]json.RawMessage `json:"shares"`
	}
	_, err := newQuarkClient(client, a).request(ctx, "POST", "/share/password/batch", nil, map[string][]string{"share_ids": ids}, &data)
	if err != nil {
		return nil, err
	}
	if data.Shares == nil {
		return nil, errors.New("夸克未返回批量分享明细")
	}
	results := make(map[string]ManagedShareLink, len(sources))
	for _, h := range sources {
		var row quarkShareDetails
		if json.Unmarshal(data.Shares[h.ProviderID], &row) != nil {
			continue
		}
		if link, err := verifyQuarkManagedShareLink(h, row); err == nil {
			results[h.ProviderID] = link
		}
	}
	return results, nil
}
