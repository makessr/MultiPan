package transfer

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"
)

type ShareRevocationResult struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	State    string    `json:"state"` // working, unknown, confirmed, not_sent
	Message  string    `json:"message"`
	Created  time.Time `json:"created"`
	CanCheck bool      `json:"canCheck"`
	Paid123  bool      `json:"paid123,omitempty"`
}
type diskShareRevocation struct {
	Source    managedShareHandle    `json:"source"`
	Result    ShareRevocationResult `json:"result"`
	Submitted bool                  `json:"submitted"`
}
type shareRevocation struct {
	saved   diskShareRevocation // Only accessed under Manager.mu.
	running bool
	cancel  context.CancelFunc
}

func (m *Manager) restoreShareRevocations(rows []diskShareRevocation) error {
	if len(rows) > 1000 {
		return errors.New("本机撤销回执数量异常，未覆盖原数据")
	}
	m.shareRevokes = make(map[string]*shareRevocation, len(rows))
	type identity struct {
		scope, id string
		paid      bool
	}
	seen := map[identity]bool{}
	for _, d := range rows {
		if d.Source.Kind == "" {
			d.Source.Kind = "quark"
		}
		if !supportedRevocationKind(d.Source.Kind) || (d.Source.Kind == "pan123" || d.Source.Kind == "baidu") && !valid123Folder(d.Source.ProviderID) || d.Source.Kind == "guangya" && shareScalar(d.Source.RawID) != d.Source.ProviderID {
			return errors.New("本机撤销回执网盘或标识无效，未覆盖原数据")
		}
		if d.Source.Paid123 && (d.Source.Kind != "pan123" || d.Source.URL == "") {
			return errors.New("本机付费分享撤销回执来源无效，未覆盖原数据")
		}
		d.Result.Paid123 = d.Source.Paid123
		key := identity{d.Source.Scope, d.Source.ProviderID, d.Source.Paid123}
		if d.Result.ID == "" || d.Result.Created.IsZero() || d.Source.AccountID == "" || d.Source.Scope == "" || !shareToken.MatchString(d.Source.ProviderID) || d.Source.ProviderID == "0" || m.shareRevokes[d.Result.ID] != nil || seen[key] {
			return errors.New("本机撤销回执不完整或重复，未覆盖原数据")
		}
		seen[key] = true
		switch d.Result.State {
		case "working", "unknown", "confirmed", "not_sent":
		default:
			return errors.New("本机撤销回执状态无效，未覆盖原数据")
		}
		if d.Result.State == "confirmed" && !d.Submitted {
			return errors.New("本机撤销回执提交状态矛盾，未覆盖原数据")
		}
		if d.Result.State == "not_sent" && d.Submitted {
			return errors.New("本机撤销回执提交状态矛盾，未覆盖原数据")
		}
		if d.Result.State == "working" || d.Result.State == "unknown" {
			if d.Submitted {
				d.Result.State = "unknown"
				d.Result.Message = "上次撤销结果尚未确认，请检查原结果，不会重复提交"
			} else {
				d.Result.State = "not_sent"
				d.Result.Message = "上次退出时尚未提交撤销请求，原分享没有由本次操作撤销"
			}
		}
		d.Result.CanCheck = d.Submitted && d.Result.State == "unknown"
		m.shareRevokes[d.Result.ID] = &shareRevocation{saved: d}
	}
	return nil
}

func (m *Manager) revocationForLocked(h managedShareHandle) *shareRevocation {
	if h.ProviderID == "" {
		return nil
	}
	for _, op := range m.shareRevokes {
		if src := op.saved.Source; src.Scope == h.Scope && src.ProviderID == h.ProviderID && src.Paid123 == h.Paid123 {
			return op
		}
	}
	return nil
}
func (m *Manager) managedShareRevoking(h managedShareHandle) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.revocationForLocked(h)
	return op != nil && op.saved.Submitted
}

func (m *Manager) shareRevocationOverlayLocked(op *shareOperation) (ShareResult, bool) {
	r := op.result
	if !supportedRevocationKind(r.Kind) {
		return r, false
	}
	var revoke *shareRevocation
	for _, candidate := range m.shareRevokes {
		if candidate.saved.Submitted && revocationMatchesCreation(op, candidate.saved.Source) {
			revoke = candidate
			break
		}
	}
	if revoke == nil || !revoke.saved.Submitted {
		return r, false
	}
	r.URL, r.Password, r.CanCheck = "", "", false
	r.State = "unknown"
	if revoke.saved.Result.State == "confirmed" {
		r.State = "failed"
	}
	r.Message = "原分享已进入撤销流程；请到云端分享页查看撤销回执，没有重新创建分享"
	return r, true
}

func (m *Manager) ShareRevocations(accountID string) []ShareRevocationResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []ShareRevocationResult{}
	a := m.downloadAccountLocked(accountID)
	for _, op := range m.shareRevokes {
		if op.saved.Source.AccountID != accountID {
			continue
		}
		r := op.saved.Result
		if a.ID == "" || downloadScope(a) != op.saved.Source.Scope {
			r.State = "unknown"
			r.CanCheck = false
			r.Message = "账号来源已变化，请到原账号官网核对撤销结果"
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Created.Equal(rows[j].Created) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Created.After(rows[j].Created)
	})
	return rows
}

// ValidateShareRevocationBatch reads the selected source pages without creating
// receipts. Each item still needs its usual checks at the time of submission.
func (m *Manager) ValidateShareRevocationBatch(ctx context.Context, handles []string) error {
	return m.validateShareRevocationBatch(ctx, handles, validateShareRevocationPages)
}

// ValidateShareRevocationResume rechecks the remaining share identities after
// earlier revocations may have changed their positions in the provider list.
func (m *Manager) ValidateShareRevocationResume(ctx context.Context, handles []string) error {
	return m.validateShareRevocationBatch(ctx, handles, validateResumedShareRevocations)
}

func shareRevocationPageLimit(kind string) int {
	if kind == "baidu" {
		return 5
	}
	return 10
}

func (m *Manager) validateShareRevocationBatch(ctx context.Context, handles []string,
	check func(context.Context, *http.Client, Account, []managedShareHandle) error,
) error {
	if len(handles) == 0 || len(handles) > 50 {
		return errors.New("每批请选择 1 至 50 条分享")
	}
	sources := make([]managedShareHandle, 0, len(handles))
	seen := make(map[string]bool, len(handles))
	for _, handle := range handles {
		h, err := decodeManagedShareHandle(handle)
		if err != nil {
			return err
		}
		if h.Page < 1 || h.Page > shareRevocationPageLimit(h.Kind) {
			return errors.New("所选分享超出撤销核对范围，整批未提交")
		}
		if seen[h.ProviderID] || len(sources) != 0 && (sources[0].AccountID != h.AccountID || sources[0].Scope != h.Scope || sources[0].Kind != h.Kind || sources[0].Paid123 != h.Paid123) {
			return errors.New("所选分享重复或来自不同账号或分享类型，整批未提交")
		}
		seen[h.ProviderID] = true
		sources = append(sources, h)
	}
	first := sources[0]
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(first.AccountID)
	if m.closing || !revocationAccountMatches(a, first) || len(m.shareRevokes)+len(sources) > 1000 {
		m.mu.Unlock()
		return errors.New("账号来源已变化或撤销回执空间不足，整批未提交")
	}
	for _, h := range sources {
		if !revocationAccountMatches(a, h) {
			m.mu.Unlock()
			return errors.New("所选分享来源未能核对，整批未提交")
		}
		if m.revocationForLocked(h) != nil {
			m.mu.Unlock()
			return errors.New("所选分享已有撤销回执，请先核对原结果")
		}
	}
	m.mu.Unlock()
	client := NewHTTPUploader().Client
	if err := check(ctx, client, a, sources); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || !revocationAccountMatches(m.downloadAccountLocked(first.AccountID), first) {
		return errors.New("账号来源已变化，整批未提交")
	}
	return nil
}

func readShareRevocationPage(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (ManagedSharePage, error) {
	switch a.Kind {
	case "quark":
		return quarkManagedSharesFiltered(ctx, client, a, h.Page, h.QuarkFilter)
	case "pan123":
		if !shareToken.MatchString(h.Cursor) || (h.Page == 1) != (h.Cursor == "0") {
			return ManagedSharePage{}, errors.New("123 分享分页信息已失效，请刷新重选")
		}
		return pan123ManagedSharesByType(ctx, client, a, h.Cursor, h.Page, h.Paid123)
	case "baidu":
		return baiduManagedShares(ctx, client, a, h.Page)
	case "guangya":
		return gyManagedShares(ctx, client, a, h.Page)
	default:
		return ManagedSharePage{}, errors.New("此网盘不支持批量撤销预检")
	}
}

func matchesShareRevocationTarget(row ManagedShare, h managedShareHandle) bool {
	current, err := decodeManagedShareHandle(row.ID)
	return err == nil && row.CanRevoke && current.Name == h.Name &&
		(h.URL == "" || current.URL == h.URL) && (!h.Paid123 || current.Key123 == h.Key123)
}

func validateResumedShareRevocations(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle) error {
	remaining := make(map[string]managedShareHandle, len(sources))
	for _, h := range sources {
		remaining[managedShareSelectionKey(h)] = h
	}
	location := managedShareHandle{Cursor: "0", Paid123: sources[0].Paid123}
	seen := map[string]bool{}
	seenCursors := map[string]bool{"0": true}
	var total *int64
	for page := 1; page <= shareRevocationPageLimit(a.Kind); page++ {
		location.Page = page
		result, err := readShareRevocationPage(ctx, client, a, location)
		if err != nil {
			return err
		}
		if total != nil && (result.quarkTotal == nil || *result.quarkTotal != *total) {
			return errors.New("原分享列表在本轮核对期间变化，未提交撤销")
		}
		total = result.quarkTotal
		for _, row := range result.Items {
			if row.SelectionKey == "" || seen[row.SelectionKey] {
				return errors.New("原分享列表出现无效或重复记录，未提交撤销")
			}
			seen[row.SelectionKey] = true
			if h, ok := remaining[row.SelectionKey]; ok {
				if !matchesShareRevocationTarget(row, h) {
					return errors.New("原分享已变更或不可撤销，请刷新后重选")
				}
				delete(remaining, row.SelectionKey)
			}
		}
		if len(remaining) == 0 {
			return nil
		}
		if !result.HasMore {
			break
		}
		if a.Kind == "pan123" {
			if result.pan123Next == "" || seenCursors[result.pan123Next] {
				return errors.New("原分享分页标识无效或重复，未提交撤销")
			}
			location.Cursor = result.pan123Next
			seenCursors[location.Cursor] = true
		}
	}
	return errors.New("未能在核对范围内确认剩余分享，未提交撤销，请刷新后重选")
}

func validateShareRevocationPages(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle) error {
	type location struct {
		page                int
		cursor, quarkFilter string
	}
	pages := map[location][]ManagedShare{}
	for _, h := range sources {
		key := location{h.Page, h.Cursor, h.QuarkFilter}
		items, ok := pages[key]
		if !ok {
			page, err := readShareRevocationPage(ctx, client, a, h)
			if err != nil {
				return err
			}
			items = page.Items
			pages[key] = items
		}
		matched := false
		selectionKey := managedShareSelectionKey(h)
		for _, row := range items {
			if row.SelectionKey != selectionKey {
				continue
			}
			matched = matchesShareRevocationTarget(row, h)
			break
		}
		if !matched {
			return errors.New("有分享已移页、变更或不可撤销，整批未提交，请刷新后重选")
		}
	}
	return nil
}

func (m *Manager) RevokeManagedShare(ctx context.Context, handle string) (ShareRevocationResult, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareRevocationResult{}, err
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(h.AccountID)
	if m.closing || !revocationAccountMatches(a, h) {
		m.mu.Unlock()
		return ShareRevocationResult{}, errors.New("账号来源已变化，请刷新云端分享，不提交撤销")
	}
	if op := m.revocationForLocked(h); op != nil {
		r := op.saved.Result
		m.mu.Unlock()
		return r, nil
	}
	if len(m.shareRevokes) >= 1000 {
		m.mu.Unlock()
		return ShareRevocationResult{}, errors.New("撤销回执已达1000项，请先清理已确认回执，未知回执不会自动删除")
	}
	if err = m.reserveShareLocked(a.ID); err != nil {
		m.mu.Unlock()
		return ShareRevocationResult{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	op := &shareRevocation{saved: diskShareRevocation{Source: h, Result: ShareRevocationResult{ID: newID(), Name: h.Name, State: "working", Message: "正在核对原分享，尚未提交撤销", Created: time.Now(), Paid123: h.Paid123}}, running: true, cancel: cancel}
	if m.shareRevokes == nil {
		m.shareRevokes = map[string]*shareRevocation{}
	}
	m.shareRevokes[op.saved.Result.ID] = op
	if m.saveLocked() != nil {
		delete(m.shareRevokes, op.saved.Result.ID)
		delete(m.renaming, a.ID)
		cancel()
		m.mu.Unlock()
		return ShareRevocationResult{}, errors.New("无法保存撤销回执，未提交撤销请求")
	}
	m.workers.Add(1)
	m.mu.Unlock()
	return m.runShareRevocation(ctx, a, op, false), nil
}

func (m *Manager) CheckShareRevocation(ctx context.Context, id string) (ShareRevocationResult, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.shareRevokes[id]
	if m.closing || op == nil {
		m.mu.Unlock()
		return ShareRevocationResult{}, errors.New("原撤销回执不存在或程序正在退出")
	}
	a := m.downloadAccountLocked(op.saved.Source.AccountID)
	if !revocationAccountMatches(a, op.saved.Source) {
		m.mu.Unlock()
		return ShareRevocationResult{}, errors.New("账号来源已变化，请到原账号官网核对")
	}
	if op.running || !op.saved.Result.CanCheck {
		r := op.saved.Result
		m.mu.Unlock()
		return r, nil
	}
	if err := m.reserveShareLocked(a.ID); err != nil {
		m.mu.Unlock()
		return ShareRevocationResult{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	op.cancel, op.running = cancel, true
	m.workers.Add(1)
	m.mu.Unlock()
	return m.runShareRevocation(ctx, a, op, true), nil
}

func (m *Manager) runShareRevocation(ctx context.Context, a Account, op *shareRevocation, checkOnly bool) ShareRevocationResult {
	defer m.workers.Done()
	m.mu.Lock()
	d := op.saved
	m.mu.Unlock()
	client := NewHTTPUploader().Client
	r := d.Result
	r.State, r.CanCheck = "unknown", true
	r.Message = "撤销结果待确认，请检查原结果或到官网核对；不会重复提交"
	if !checkOnly {
		present, err := observeShareRevocation(ctx, client, a, d.Source)
		if err != nil || !present {
			r.State, r.CanCheck = "not_sent", false
			r.Message = "未能确认原分享仍与选中项一致，未发送撤销请求；请刷新列表或到官网核对"
		} else {
			m.mu.Lock()
			op.saved.Submitted = true
			op.saved.Result = r
			err = m.saveLocked()
			if err != nil {
				op.saved.Submitted = false
			}
			m.mu.Unlock()
			if err != nil {
				r.State, r.CanCheck = "not_sent", false
				r.Message = "无法保存撤销提交检查点，未发送撤销请求"
			} else {
				_ = submitShareRevocation(ctx, client, a, d.Source)
				checkOnly = true
			}
		}
	}
	if checkOnly {
		present, err := observeShareRevocation(ctx, client, a, d.Source)
		if err == nil && !present {
			r.State, r.CanCheck = "confirmed", false
			r.Message = "当前账号分享列表已无原分享，撤销结果已确认；网盘原文件不受影响"
			if a.Kind == "guangya" {
				r.Message = "已核对原分享已删除或从列表移除，撤销结果已确认；网盘原文件不受影响"
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op.saved.Result = r
	op.running = false
	op.cancel()
	op.cancel = nil
	delete(m.renaming, a.ID)
	if m.saveLocked() != nil {
		r.Message += "；本机回执保存失败，请勿重复提交"
		op.saved.Result = r
	}
	return r
}

// At most ten explicit 50-row pages; absence is only reported after a complete
// list ends. A failed or truncated scan is never evidence of successful revoke.
func quarkObserveShare(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (bool, error) {
	seen := map[string]bool{}
	var total *int64
	for page := 1; page <= 10; page++ {
		rows, err := quarkManagedShares(ctx, client, a, page)
		if err != nil {
			return false, err
		}
		if total != nil && (rows.quarkTotal == nil || *rows.quarkTotal != *total) {
			return false, errors.New("夸克分享总数在本次核对期间变化或缺失")
		}
		total = rows.quarkTotal
		for _, row := range rows.Items {
			other, e := decodeManagedShareHandle(row.ID)
			if e != nil {
				return false, e
			}
			if seen[other.ProviderID] {
				return false, errors.New("夸克分享核对遇到跨页重复记录")
			}
			seen[other.ProviderID] = true
			if other.ProviderID == h.ProviderID {
				if other.Name != h.Name || h.URL != "" && other.URL != h.URL {
					return false, errors.New("原分享名称或链接已变化")
				}
				return true, nil
			}
		}
		if !rows.HasMore {
			return false, nil
		}
	}
	return false, errors.New("分享数量超过本次核对范围，未能确认原结果")
}

func (m *Manager) ClearShareRevocations(accountID string) (int, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(accountID)
	removed := map[string]*shareRevocation{}
	for id, op := range m.shareRevokes {
		if op.saved.Source.AccountID == accountID && a.ID != "" && downloadScope(a) == op.saved.Source.Scope && !op.running && (op.saved.Result.State == "confirmed" || op.saved.Result.State == "not_sent") {
			removed[id] = op
			delete(m.shareRevokes, id)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	// Retire matching local creation receipts before removing a confirmed revoke,
	// so an obsolete cached link cannot reappear after clearing the tombstone.
	old := map[*shareOperation]diskShare{}
	for _, r := range removed {
		if r.saved.Result.State == "confirmed" {
			for _, op := range m.shares {
				if revocationMatchesCreation(op, r.saved.Source) {
					old[op] = op.saved
					op.revoked = true
					op.result.State = "failed"
					op.result.URL, op.result.Password, op.result.CanCheck = "", "", false
					op.result.Message = "此原分享已确认撤销，没有重新创建"
					op.saved = snapshotShare(op)
				}
			}
		}
	}
	if m.saveLocked() != nil {
		for id, op := range removed {
			m.shareRevokes[id] = op
		}
		for op, d := range old {
			op.saved = d
			op.result = d.Result
			op.revoked = d.Revoked
		}
		return 0, errors.New("无法保存清理结果，原回执已保留")
	}
	return len(removed), nil
}
