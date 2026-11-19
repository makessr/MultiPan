package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const recycleEmptyLimit = 1000

type RecycleEmptyPreview struct {
	Token   string         `json:"token"`
	Items   []RecycleEntry `json:"items"`
	Total   int            `json:"total"`
	Expires time.Time      `json:"expires"`
}

type recycleEmptySnapshot struct {
	token, accountID, kind, scope string
	items                         []RecycleEntry
	expires                       time.Time
	used, executed                bool
}

const guangyaEmptyPurpose = "guangya-recycle-empty-v1"
const guangyaEmptyName = "清空整个回收站"

func nativeRecycleEmpty(a Account) bool {
	return a.Kind == "guangya" && a.Endpoint == GuangyaAPI
}

func cloneRecycleEntries(rows []RecycleEntry) []RecycleEntry {
	copy := make([]RecycleEntry, len(rows))
	for i, r := range rows {
		r.RestoreHandle = ""
		if r.Size != nil {
			size := *r.Size
			r.Size = &size
		}
		copy[i] = r
	}
	return copy
}

// A full, bounded scan is mandatory. A partial page is never a clear-all plan.
// Exact totals are required for this destructive operation, even though
// ordinary read-only browsing can display pages without a provider total.
func scanRecycleEmpty(ctx context.Context, client *http.Client, a Account) ([]RecycleEntry, error) {
	rows := []RecycleEntry{}
	seen := map[string]bool{}
	var expected int64
	for page := 1; page <= 11; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := listAccountRecycle(ctx, client, a, page)
		if err != nil {
			return nil, err
		}
		if p.Incomplete || p.Total == nil || *p.Total > recycleEmptyLimit {
			return nil, errors.New("回收站完整清单未确认或超过1000项，未创建清空计划；请到官网处理")
		}
		if page == 1 {
			expected = *p.Total
		} else if *p.Total != expected {
			return nil, errors.New("回收站分页总数变化，清单不完整，未创建清空计划")
		}
		for _, r := range p.Items {
			r.RestoreHandle = ""
			valid := validRecyclePurgeSource(recyclePurgeSource{Purpose: "recycle-purge-v1", AccountID: a.ID, Kind: a.Kind, Scope: downloadScope(a), Entry: r})
			if nativeRecycleEmpty(a) {
				valid = r.Kind == "file" || r.Kind == "folder"
			}
			if seen[r.ID] || !valid {
				return nil, errors.New("回收站记录重复或删除依据不完整，未创建清空计划")
			}
			seen[r.ID] = true
			rows = append(rows, r)
		}
		if len(rows) > recycleEmptyLimit || int64(len(rows)) > expected {
			return nil, errors.New("回收站累计数量矛盾，清单不完整")
		}
		if !p.HasMore {
			if int64(len(rows)) != expected {
				return nil, errors.New("回收站提前结束，清单不完整")
			}
			return rows, nil
		}
		if len(p.Items) != recyclePageSize {
			return nil, errors.New("回收站分页提前截断，清单不完整")
		}
	}
	return nil, errors.New("回收站读取超限，清单不完整")
}

func (m *Manager) emptyEligibleLocked(a Account, rows []RecycleEntry) error {
	if m.closing || (!recyclePurgeProvider(a) && !nativeRecycleEmpty(a)) || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) {
		return errors.New("账号来源已变化，未创建清空计划")
	}
	if nativeRecycleEmpty(a) {
		if len(m.recyclePurges) >= 1000 {
			return errors.New("本机永久删除记录空间不足，请先清理已完成记录")
		}
		scope := downloadScope(a)
		if m.pendingGuangyaEmptyLocked(scope) {
			return errors.New("已有光鸭清空结果待核对")
		}
		for _, op := range m.recyclePurges {
			if op.saved.Source.Scope == scope && op.saved.Source.Purpose == guangyaPurgePurpose &&
				op.saved.Submitted && (op.running || op.saved.Receipt.State == "unknown") {
				return errors.New("光鸭仍有单项永久删除结果待核对，未创建清空计划")
			}
		}
		for _, op := range m.recycleRestores {
			if op.saved.Source.Scope == scope && op.saved.Submitted && (op.running || op.saved.Result.State == "unknown") {
				return errors.New("光鸭仍有还原结果待核对，未创建清空计划")
			}
		}
		return nil
	}
	if len(m.recyclePurges)+len(rows) > 1000 {
		return errors.New("永久删除本机记录空间不足，请先核对并清理已完成记录")
	}
	for _, r := range rows {
		if m.pendingRestoreLocked(downloadScope(a), r.ID) {
			return errors.New("清单内有待核对还原，未创建清空计划")
		}
		for _, op := range m.recyclePurges {
			if op.saved.Source.Scope == downloadScope(a) && op.saved.Source.Entry.ID == r.ID {
				return errors.New("清单内已有永久删除记录，请先核对原结果，不会重复提交")
			}
		}
	}
	return nil
}

func (m *Manager) PreviewRecycleEmpty(ctx context.Context, accountID string) (RecycleEmptyPreview, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	m.recycleEmpty = nil // A refresh invalidates any previous destructive preview.
	a := m.downloadAccountLocked(accountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || (!recyclePurgeProvider(a) && !nativeRecycleEmpty(a)) {
		return RecycleEmptyPreview{}, errors.New("清空回收站当前支持夸克、123、百度和光鸭")
	}
	rows, err := scanRecycleEmpty(ctx, NewHTTPUploader().Client, a)
	if err != nil {
		return RecycleEmptyPreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return RecycleEmptyPreview{}, err
	}
	if err = m.emptyEligibleLocked(a, rows); err != nil {
		return RecycleEmptyPreview{}, err
	}
	s := &recycleEmptySnapshot{token: newID(), accountID: a.ID, kind: a.Kind, scope: downloadScope(a), items: cloneRecycleEntries(rows), expires: time.Now().Add(10 * time.Minute)}
	m.recycleEmpty = s
	return RecycleEmptyPreview{Token: s.token, Items: cloneRecycleEntries(rows), Total: len(rows), Expires: s.expires}, nil
}

// PrepareRecycleEmpty reads the current list before issuing any write handle.
func (m *Manager) PrepareRecycleEmpty(ctx context.Context, token, confirmation string) ([]RecyclePurgePrepared, error) {
	if confirmation != "清空回收站" {
		return nil, errors.New("请输入“清空回收站”并确认无法还原")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	s := m.recycleEmpty
	if s == nil || s.token != token || s.used || !time.Now().Before(s.expires) {
		m.mu.Unlock()
		return nil, errors.New("清空预览已失效，请重新读取完整清单")
	}
	s.used = true // Consume before network reads; failed confirmation never auto-retries.
	a := m.downloadAccountLocked(s.accountID)
	if a.Kind != s.kind || downloadScope(a) != s.scope || m.closing {
		m.mu.Unlock()
		return nil, errors.New("账号来源已变化，整批未提交")
	}
	m.mu.Unlock()
	rows, err := scanRecycleEmpty(ctx, NewHTTPUploader().Client, a)
	if err != nil {
		return nil, err
	}
	if !sameRecycleEmptyItems(s.items, rows) {
		return nil, errors.New("回收站清单或原记录已变化，整批未提交，请重新预览")
	}
	purpose, targets := "recycle-purge-v1", s.items
	if nativeRecycleEmpty(a) && len(targets) > 0 {
		purpose = guangyaEmptyPurpose
		targets = []RecycleEntry{{ID: s.token, Kind: "all", Name: guangyaEmptyName}}
	}
	result := make([]RecyclePurgePrepared, 0, len(targets))
	for _, r := range targets {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		proof := recyclePurgeSource{Purpose: purpose, AccountID: a.ID, Kind: a.Kind, Scope: s.scope, Entry: r}
		b, e := json.Marshal(proof)
		if e == nil {
			b, e = protect(b)
		}
		if e != nil {
			return nil, e
		}
		result = append(result, RecyclePurgePrepared{Name: r.Name, Handle: base64.RawURLEncoding.EncodeToString(b)})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = m.emptyEligibleLocked(a, rows); err != nil {
		return nil, err
	}
	return result, nil
}

func sameRecycleEmptyItems(expected, rows []RecycleEntry) bool {
	if len(rows) != len(expected) {
		return false
	}
	current := make(map[string]RecycleEntry, len(rows))
	for _, r := range rows {
		current[r.ID] = r
	}
	for _, r := range expected {
		now, ok := current[r.ID]
		if !ok || !sameRecycleRestoreEntry(now, r) {
			return false
		}
	}
	return true
}

func (m *Manager) pendingGuangyaEmptyLocked(scope string) bool {
	for _, op := range m.recyclePurges {
		d := op.saved
		if d.Source.Purpose == guangyaEmptyPurpose && d.Source.Scope == scope && d.Submitted &&
			(op.running || d.Receipt.State == "unknown") {
			return true
		}
	}
	return false
}
