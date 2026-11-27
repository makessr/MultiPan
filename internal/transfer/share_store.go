package transfer

import (
	"context"
	"errors"
	"time"
)

const maxShareRecords = 1000

// diskShare is stored inside the existing OS-protected state, never in a public
// history response. No login credentials or expiring download nodes belong here.
type diskShare struct {
	RecreatedFrom       string      `json:"recreatedFrom,omitempty"`
	Superseded          bool        `json:"superseded,omitempty"`
	Input               ShareInput  `json:"input"`
	Scope               string      `json:"scope"`
	Key                 string      `json:"key"`
	Result              ShareResult `json:"result"`
	Created             time.Time   `json:"created"`
	Task                string      `json:"task,omitempty"`
	Share               string      `json:"share,omitempty"`
	Key123              string      `json:"key123,omitempty"`
	QuarkGroupVerified  bool        `json:"quarkGroupVerified,omitempty"`
	BaiduSource         string      `json:"baiduSource,omitempty"`
	BaiduURL            string      `json:"baiduURL,omitempty"`
	BaiduExpiryPassword *string     `json:"baiduExpiryPassword,omitempty"`
	LanzouURL           string      `json:"lanzouURL,omitempty"`
	Revoked             bool        `json:"revoked,omitempty"`
}

func snapshotShare(op *shareOperation) diskShare {
	return diskShare{RecreatedFrom: op.recreatedFrom, Superseded: op.superseded, Input: op.in, Scope: op.scope, Key: op.key, Result: op.result,
		Created: op.created, Task: op.task, Share: op.share, Key123: op.key123, QuarkGroupVerified: op.quarkGroupVerified,
		BaiduSource: op.baiduSource, BaiduURL: op.baiduURL,
		BaiduExpiryPassword: op.baiduExpiryPassword, LanzouURL: op.lanzouURL, Revoked: op.revoked}
}

// Only the operation's worker invokes checkpoint, after updating provider IDs.
// Other workers save the last frozen value under Manager.mu, avoiding races with
// provider parsing and preserving write-ahead identity across process exits.
func (m *Manager) attachShareCheckpoint(op *shareOperation) {
	op.settingsCheckpoint = func(h managedShareHandle) error { return m.prepareShareSettings(op, h) }
	op.checkpoint = func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		op.saved = snapshotShare(op)
		return m.saveLocked()
	}
}

func (op *shareOperation) saveCheckpoint() error {
	if op.checkpoint != nil {
		return op.checkpoint()
	}
	return nil // Standalone provider adapters have no local-state owner.
}

func shareStorageUnknown() ShareResult {
	return ShareResult{State: "unknown", Message: "原分享回执未能保存，请检查磁盘后核对原操作；不要重新创建 [SHARE/local-state]"}
}

func (m *Manager) beginShareCheckLocked(ctx context.Context, a Account, op *shareOperation) (context.Context, error) {
	if err := m.reserveShareLocked(a.ID); err != nil {
		return nil, err
	}
	previous, saved := op.result, op.saved
	checkCtx, cancel := context.WithCancel(ctx)
	op.cancel, op.running = cancel, true
	op.result.State = "working"
	op.result.URL, op.result.Password = "", ""
	op.result.Message = "正在检查原分享结果，不重新创建"
	op.saved = snapshotShare(op)
	if m.saveLocked() != nil {
		op.result, op.saved, op.running, op.cancel = previous, saved, false, nil
		cancel()
		delete(m.renaming, a.ID)
		return nil, errors.New("无法保存检查回执，请检查磁盘后再核对原结果；没有重新创建分享")
	}
	m.workers.Add(1)
	return checkCtx, nil
}

func (m *Manager) restoreShares(rows []diskShare) error {
	if len(rows) > maxShareRecords {
		return errors.New("本机分享记录数量异常；为保留未确认操作，未加载或覆盖原数据")
	}
	m.shares = make(map[string]*shareOperation, len(rows))
	for _, d := range rows {
		if d.Result.ID == "" || d.Input.AccountID == "" || d.Scope == "" || d.Key == "" || d.Created.IsZero() || m.shares[d.Result.ID] != nil {
			return errors.New("本机分享回执不完整或重复；未覆盖原数据")
		}
		if d.QuarkGroupVerified && (d.Result.Kind != "quark" || len(d.Input.Targets) < 2 || !shareToken.MatchString(d.Share)) {
			return errors.New("本机夸克合并分享回执不完整；未覆盖原数据")
		}
		if d.LanzouURL != "" {
			link, err := shareURL("lanzou", d.LanzouURL)
			if err != nil || link != d.LanzouURL || d.Result.Kind != "lanzou" || d.Input.Action != "password" {
				return errors.New("本机蓝奏原链接记录无效；未覆盖原数据")
			}
		}
		switch d.Result.State {
		case "ready":
			link, e := shareURL(d.Result.Kind, d.Result.URL)
			if e != nil {
				return errors.New("本机分享链接记录无效；未覆盖原数据")
			}
			if d.Result.Kind == "lanzou" && d.Input.Action == "password" && d.LanzouURL == "" {
				// Older confirmed records still have a previously displayed URL.
				d.LanzouURL = link
			}
			if d.Result.Kind == "baidu" && d.Input.Action == "settings" && d.BaiduExpiryPassword == nil {
				// Preserve the last confirmed access settings on older ready receipts.
				password := d.Result.Password
				d.BaiduExpiryPassword = &password
			}
		case "working", "unknown", "failed":
		default:
			return errors.New("本机分享回执状态无效；未覆盖原数据")
		}
		if d.Result.State == "working" {
			d.Result.State = "unknown"
			d.Result.Message = "上次退出时操作尚未确认；请检查原结果或到官网核对，没有重新创建分享"
		}
		if d.Result.State != "ready" {
			d.Result.URL, d.Result.Password = "", ""
		}
		d.Result.CanCheck = d.Result.Kind == "lanzou" || d.Task != "" || d.Share != "" || d.Key123 != ""
		if d.Revoked || d.Superseded {
			d.Result.State = "failed"
			d.Result.URL, d.Result.Password, d.Result.CanCheck = "", "", false
			d.Result.Message = "此原分享回执已失效，请使用最新回执或到官网核对"
		}
		op := &shareOperation{recreatedFrom: d.RecreatedFrom, in: d.Input, scope: d.Scope, key: d.Key, result: d.Result, created: d.Created,
			task: d.Task, share: d.Share, key123: d.Key123, quarkGroupVerified: d.QuarkGroupVerified,
			baiduSource: d.BaiduSource, baiduURL: d.BaiduURL,
			baiduExpiryPassword: d.BaiduExpiryPassword, lanzouURL: d.LanzouURL, saved: d, revoked: d.Revoked, superseded: d.Superseded}
		m.attachShareCheckpoint(op)
		m.shares[d.Result.ID] = op
	}
	return nil
}

// ClearShareHistory removes only definitive local receipts. It never revokes a
// cloud share and cannot discard an uncertain write or an operation in progress.
func (m *Manager) ClearShareHistory() (int, error) {
	return m.clearShareHistory(nil)
}

// ClearSelectedShareHistory clears exactly the confirmed local receipt IDs.
// A missing or no longer eligible receipt leaves the whole selection intact.
func (m *Manager) ClearSelectedShareHistory(ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > maxShareRecords {
		return 0, errors.New("清理范围无效，请刷新本机分享记录")
	}
	return m.clearShareHistory(ids)
}

func (m *Manager) clearShareHistory(ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	retained := m.shareSourcesInUseLocked()
	removed := make(map[string]*shareOperation)
	for id, op := range m.shares {
		if ids != nil && !requested[id] {
			continue
		}
		if retained[id] {
			if ids != nil {
				return 0, errors.New("所选记录包含重新分享仍在使用的原回执，本次未清理")
			}
			continue
		}
		a := m.downloadAccountLocked(op.in.AccountID)
		if r, blocked := m.shareRevocationOverlayLocked(op); blocked && r.State == "unknown" {
			continue
		}
		if !op.running && a.ID != "" && downloadScope(a) == op.scope && (op.result.State == "ready" || op.result.State == "failed") {
			removed[id] = op
		}
	}
	if ids != nil && len(removed) != len(ids) {
		return 0, errors.New("所选分享记录已变化，本次未清理。请刷新后重试")
	}
	if len(removed) == 0 {
		return 0, nil
	}
	for id := range removed {
		delete(m.shares, id)
	}
	if m.saveLocked() != nil {
		for id, op := range removed {
			m.shares[id] = op
		}
		return 0, errors.New("无法保存清理结果，本机记录已保留；请检查磁盘")
	}
	return len(removed), nil
}
