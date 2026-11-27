package transfer

import (
	"context"
	"errors"
	"net/http"
	"slices"
)

type replacementShare struct {
	id, key, url string
	paid         bool
}

func (m *Manager) previousShareLocked(a Account, current *shareOperation) (replacementShare, replacementShare, bool) {
	if current == nil || current.recreatedFrom == "" || !supportedRevocationKind(a.Kind) {
		return replacementShare{}, replacementShare{}, false
	}
	previous := m.shares[current.recreatedFrom]
	old, ok := m.replacementShareLocked(a, previous)
	newShare, ready := m.replacementShareLocked(a, current)
	return old, newShare, ok && ready && distinctShareLinks(old, newShare)
}

func distinctShareLinks(old, fresh replacementShare) bool {
	return old.url != fresh.url && (old.id == "" || old.id != fresh.id) &&
		(old.key == "" || old.key != fresh.key)
}

// shareSourcesInUseLocked protects the original identity independently of the
// current history filter. Pending replacements may later become usable.
func (m *Manager) shareSourcesInUseLocked() map[string]bool {
	inUse := make(map[string]bool)
	for _, replacement := range m.shares {
		if replacement.recreatedFrom == "" {
			continue
		}
		account := m.downloadAccountLocked(replacement.in.AccountID)
		if account.ID == "" || account.Kind != replacement.result.Kind || downloadScope(account) != replacement.scope {
			continue
		}
		switch replacement.result.State {
		case "ready":
			if _, _, linked := m.previousShareLocked(account, replacement); linked || m.previousShareRevocationLocked(account, replacement) != nil {
				inUse[replacement.recreatedFrom] = true
			}
		case "working", "unknown":
			if _, linked := m.replacementShareLocked(account, m.shares[replacement.recreatedFrom]); linked {
				inUse[replacement.recreatedFrom] = true
			}
		}
	}
	return inUse
}

func (m *Manager) replacementShareLocked(a Account, op *shareOperation) (replacementShare, bool) {
	identity, valid := replacementShareIdentity(a, op)
	if !valid {
		return replacementShare{}, false
	}
	if _, overlaid := m.shareRevocationOverlayLocked(op); overlaid {
		return replacementShare{}, false
	}
	return identity, true
}

func replacementShareIdentity(a Account, op *shareOperation) (replacementShare, bool) {
	if !canLocateCreatedShare(a, op) || op.result.State != "ready" {
		return replacementShare{}, false
	}
	from := op.saved
	link, err := shareURL(a.Kind, op.result.URL)
	if err != nil || !shareToken.MatchString(from.Share) && (a.Kind != "pan123" || !shareToken.MatchString(from.Key123)) {
		return replacementShare{}, false
	}
	return replacementShare{id: from.Share, key: from.Key123, url: link, paid: op.in.PaidPriceFen123 > 0}, true
}

// Match existing receipts by the exact old link and the account scope.
func (m *Manager) previousShareRevocationLocked(a Account, current *shareOperation) *ShareRevocationResult {
	if current.recreatedFrom == "" || !supportedRevocationKind(a.Kind) {
		return nil
	}
	old, original := replacementShareIdentity(a, m.shares[current.recreatedFrom])
	fresh, ready := m.replacementShareLocked(a, current)
	if !original || !ready || !distinctShareLinks(old, fresh) {
		return nil
	}
	for _, revoke := range m.shareRevokes {
		if matchesReplacementShare(a, revoke.saved.Source, old) && !matchesReplacementShare(a, revoke.saved.Source, fresh) {
			result := revoke.saved.Result
			result.CanCheck = result.CanCheck && revoke.saved.Submitted && !revoke.running
			return &result
		}
	}
	return nil
}

func (m *Manager) RevokePreviousShare(ctx context.Context, accountID, replacementID, expectedURL string) (ShareRevocationResult, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(accountID)
	old, newShare, ok := m.previousShareLocked(a, m.shares[replacementID])
	closing := m.closing
	m.mu.Unlock()
	if closing || !ok || old.url != expectedURL {
		return ShareRevocationResult{}, errors.New("新旧分享回执无法核对，未撤销旧链接")
	}
	client := NewHTTPUploader().Client
	current, err := findReplacementShare(ctx, client, a, newShare, true)
	if err != nil {
		return ShareRevocationResult{}, errors.New("云端无法确认新链接，未撤销旧链接")
	}
	previous, err := findReplacementShare(ctx, client, a, old, false)
	if err != nil || current.ProviderID == previous.ProviderID || replacementLinksOverlap(current, previous) {
		return ShareRevocationResult{}, errors.New("云端无法区分新旧链接，未撤销旧链接")
	}
	return m.revokeManagedShareLocked(ctx, previous)
}

func findReplacementShare(ctx context.Context, client *http.Client, a Account, expected replacementShare, requireAvailable bool) (managedShareHandle, error) {
	location := managedShareHandle{Cursor: "0", Paid123: expected.paid}
	nextCursors := map[string]bool{"0": true}
	for page := 1; page <= shareRevocationPageLimit(a.Kind); page++ {
		location.Page = page
		result, err := readShareRevocationPage(ctx, client, a, location)
		if err != nil {
			return managedShareHandle{}, err
		}
		for _, row := range result.Items {
			h, err := decodeManagedShareHandle(row.ID)
			if err != nil {
				return managedShareHandle{}, err
			}
			if matchesReplacementShare(a, h, expected) && row.CanRevoke && (!requireAvailable || row.replacementAvailable) {
				return h, nil
			}
		}
		if !result.HasMore {
			break
		}
		if a.Kind == "pan123" {
			if result.pan123Next == "" || nextCursors[result.pan123Next] {
				break
			}
			location.Cursor = result.pan123Next
			nextCursors[location.Cursor] = true
		}
	}
	return managedShareHandle{}, errors.New("云端列表未核对到回执对应的分享")
}

func replacementLinksOverlap(a, b managedShareHandle) bool {
	for _, link := range append([]string{a.URL}, a.AlternateLinks123...) {
		if link == b.URL || slices.Contains(b.AlternateLinks123, link) {
			return true
		}
	}
	return false
}

func matchesReplacementShare(a Account, h managedShareHandle, receipt replacementShare) bool {
	if h.URL == "" || !revocationAccountMatches(a, h) || h.Paid123 != receipt.paid {
		return false
	}
	if h.URL != receipt.url && (a.Kind != "pan123" || !slices.Contains(h.AlternateLinks123, receipt.url)) {
		return false
	}
	if a.Kind == "guangya" {
		return receipt.id == h.ProviderID || receipt.id == h.PublicID
	}
	if a.Kind == "pan123" {
		return (receipt.id == "" || h.ProviderID == receipt.id) && (receipt.key == "" || h.Key123 == receipt.key)
	}
	return h.ProviderID == receipt.id
}
