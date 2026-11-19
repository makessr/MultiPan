package transfer

import (
	"context"
	"errors"
	"sort"
	"time"
)

// Deliberately excludes URLs, provider handles, paths, passwords and raw errors.
type PendingReviewRow struct {
	Key       string    `json:"key"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	AccountID string    `json:"accountId"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Created   time.Time `json:"created"`
	CanCheck  bool      `json:"canCheck"`
}
type PendingReviewSnapshot struct {
	Rows    []PendingReviewRow `json:"rows"`
	Total   int                `json:"total"`
	Limited bool               `json:"limited"`
}
type PendingReviewOutcome struct {
	State   string `json:"state"`
	Message string `json:"message"`
}
type pendingReviewSource struct {
	account, kind string
	busy          bool
}

// Use the original history accessors for eligibility. Only pending raw records
// are candidates, so a finalized receipt does not reappear merely because its
// account was later removed. Removed-account pending records remain visible.
func (m *Manager) PendingReview() PendingReviewSnapshot {
	m.mu.Lock()
	wanted := map[string]pendingReviewSource{}
	moveTimes := map[string]time.Time{}
	renewals := []PendingReviewRow{}
	for _, a := range m.accounts {
		if a.renewal.Pending {
			state := "unknown"
			if m.renewingID == a.ID {
				state = "working"
			}
			renewals = append(renewals, PendingReviewRow{Key: "session_renewal:" + a.ID, Kind: "session_renewal", ID: a.ID, AccountID: a.ID, Name: "123登录令牌更新", State: state, Created: a.renewal.Last})
		}
	}
	groups := map[string]map[string]bool{}
	closing := m.closing
	add := func(kind, id, account, state string, busy bool) {
		if state != "unknown" && state != "working" {
			return
		}
		wanted[kind+":"+id] = pendingReviewSource{account, kind, busy}
		if groups[kind] == nil {
			groups[kind] = map[string]bool{}
		}
		groups[kind][account] = true
	}
	for id, op := range m.shares {
		kind := "share"
		if op.in.Action == "settings" || op.in.Action == "password" {
			kind = "share_settings"
		}
		add(kind, id, op.in.AccountID, op.result.State, op.running)
	}
	for id, op := range m.shareRevokes {
		add("share_revoke", id, op.saved.Source.AccountID, op.saved.Result.State, op.running)
	}
	for id, op := range m.copies {
		add("copy", id, op.saved.Receipt.AccountID, op.saved.Receipt.State, op.running)
	}
	for id, op := range m.trashes {
		add("trash", id, op.saved.Receipt.AccountID, op.saved.Receipt.State, op.running)
	}
	for id, op := range m.recycleRestores {
		add("restore", id, op.saved.Source.AccountID, op.saved.Result.State, op.running)
	}
	for id, op := range m.recyclePurges {
		add("purge", id, op.saved.Source.AccountID, op.saved.Receipt.State, op.running)
	}
	for _, d := range m.relays {
		state := d.Job.State
		if state == "moving" {
			state = "working"
			moveTimes[d.Job.ID] = d.Move.Created
		}
		if state == "move_unknown" {
			state = "unknown"
			moveTimes[d.Job.ID] = d.Move.Created
		}
		add("relay", d.Job.ID, d.Input.SourceAccountID, state, m.relayActive == d.Job.ID)
	}
	m.mu.Unlock()
	rows := append([]PendingReviewRow{}, renewals...)
	appendRow := func(kind, id, name, state string, created time.Time, canCheck bool) {
		key := kind + ":" + id
		s, ok := wanted[key]
		if !ok || state != "unknown" && state != "working" {
			return
		}
		rows = append(rows, PendingReviewRow{Key: key, Kind: kind, ID: id, AccountID: s.account, Name: name, State: state, Created: created, CanCheck: canCheck && !s.busy && !closing && state == "unknown" && kind != "relay"})
	}
	for _, r := range m.ShareHistory() {
		kind := "share"
		if _, ok := wanted["share_settings:"+r.Result.ID]; ok {
			kind = "share_settings"
		}
		appendRow(kind, r.Result.ID, r.Result.Name, r.Result.State, r.Created, r.Result.CanCheck)
	}
	for account := range groups["share_revoke"] {
		for _, r := range m.ShareRevocations(account) {
			appendRow("share_revoke", r.ID, r.Name, r.State, r.Created, r.CanCheck)
		}
	}
	for account := range groups["copy"] {
		for _, r := range m.CopyHistory(account) {
			appendRow("copy", r.ID, r.Name, r.State, r.Created, r.CanCheck)
		}
	}
	for account := range groups["trash"] {
		for _, r := range m.TrashHistory(account) {
			appendRow("trash", r.ID, r.Name, r.State, r.Created, r.CanCheck)
		}
	}
	for account := range groups["restore"] {
		for _, r := range m.RecycleRestoreHistory(account) {
			appendRow("restore", r.ID, r.Name, r.State, r.Created, r.CanCheck)
		}
	}
	for account := range groups["purge"] {
		for _, r := range m.RecyclePurgeHistory(account) {
			appendRow("purge", r.ID, r.Name, r.State, r.Created, r.CanCheck)
		}
	}
	for _, r := range m.RelayJobs() {
		state := r.State
		if state == "moving" {
			state = "working"
		}
		if state == "move_unknown" {
			state = "unknown"
		}
		created := r.Created
		if stamp, ok := moveTimes[r.ID]; ok {
			created = stamp
		}
		appendRow("relay", r.ID, r.Name, state, created, r.CanCheck)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Created.Equal(rows[j].Created) {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Created.After(rows[j].Created)
	})
	s := PendingReviewSnapshot{Rows: rows, Total: len(rows)}
	if len(s.Rows) > 2000 {
		s.Rows = s.Rows[:2000]
		s.Limited = true
	}
	return s
}

// No start/resume/clear/write dispatch exists here. Relay full-content checks
// stay in their original module and cannot be started by this endpoint.
func (m *Manager) CheckPendingReview(ctx context.Context, kind, id string) (PendingReviewOutcome, error) {
	allowed := false
	for _, r := range m.PendingReview().Rows {
		if r.Kind == kind && r.ID == id && r.CanCheck {
			allowed = true
			break
		}
	}
	if !allowed {
		return PendingReviewOutcome{}, errors.New("此回执当前不能在此核对，请刷新或前往原模块")
	}
	state := ""
	var e error
	switch kind {
	case "share", "share_settings":
		var r ShareResult
		r, e = m.CheckShare(ctx, id)
		state = r.State
	case "share_revoke":
		var r ShareRevocationResult
		r, e = m.CheckShareRevocation(ctx, id)
		state = r.State
	case "copy":
		var r CopyReceipt
		r, e = m.CheckCopy(ctx, id)
		state = r.State
	case "trash":
		var r TrashReceipt
		r, e = m.CheckTrash(ctx, id)
		state = r.State
	case "restore":
		var r RecycleRestoreResult
		r, e = m.CheckRecycleRestore(ctx, id)
		state = r.State
	case "purge":
		var r TrashReceipt
		r, e = m.CheckRecyclePurge(ctx, id)
		state = r.State
	default:
		return PendingReviewOutcome{}, errors.New("此操作只可在原模块核对")
	}
	if e != nil {
		return PendingReviewOutcome{}, errors.New("核对未完成，请保留原回执并到原模块查看详情；没有重发原操作")
	}
	message := "原回执已更新，请到原模块查看结果；没有重发原操作"
	if state == "unknown" || state == "working" {
		message = "原结果仍待核对或处理中；没有重发原操作"
	}
	return PendingReviewOutcome{State: state, Message: message}, nil
}
