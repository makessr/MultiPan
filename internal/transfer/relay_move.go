package transfer

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type diskRelayMove struct {
	State        string    `json:"state,omitempty"`
	Created      time.Time `json:"created,omitempty"`
	Submitted    bool      `json:"submitted,omitempty"`
	Acknowledged bool      `json:"acknowledged,omitempty"`
}

func relayMoveToRecycle(kind string) bool {
	return kind == "pan123" || kind == "quark"
}

func relayMoveSource(d diskRelay) bool {
	if d.Input.SourceAccountID != "" && d.Input.SourceAccountID == d.Input.TargetAccountID {
		return false
	}
	if d.SourceKind == "quark" {
		return validRelayQuarkVersion(d.ETag) && relayPinnedSHA(d.SourceKind, d.ETag) == d.Job.SHA256 &&
			validQuarkTrashSource(relayFileTrashSource(d))
	}
	return d.SourceKind == "" || d.SourceKind == "webdav" || d.SourceKind == "ctdav" || relayMoveToRecycle(d.SourceKind)
}
func relayMoveEligible(d diskRelay) bool {
	return relayMoveSource(d) && !d.Folder && d.ParentJobID == "" && d.Job.State == "verified" && d.Submitted &&
		validRelayVersion(d.SourceKind, d.ETag) && len(d.Job.SHA256) == 64 &&
		(!relayTargetNeedsFileID(d.TargetKind) || validRelayTargetFolderID(d.TargetKind, d.TargetFileID))
}
func validRelayMoveRecord(d diskRelay) bool {
	r := d.Move
	if r.State == "" {
		return r.Created.IsZero() && !r.Submitted && !r.Acknowledged && d.Job.State != "moving" && d.Job.State != "move_unknown" && d.Job.State != "moved"
	}
	if r.Created.IsZero() || !relayMoveSource(d) || d.Folder || d.ParentJobID != "" || !d.Submitted ||
		len(d.Job.SHA256) != 64 || !validRelayVersion(d.SourceKind, d.ETag) || r.Acknowledged && !r.Submitted {
		return false
	}
	switch r.State {
	case "working":
		return !r.Submitted && !r.Acknowledged && d.Job.State == "moving"
	case "unknown":
		return r.Submitted && (d.Job.State == "move_unknown" || d.Job.State == "moving")
	case "not_sent":
		return !r.Submitted && !r.Acknowledged && d.Job.State == "verified"
	case "confirmed":
		return r.Submitted && r.Acknowledged && d.Job.State == "moved"
	}
	return false
}

// A fresh complete backup is required before removing an independent original.
func (m *Manager) StartRelayMove(id, confirmation string) error {
	return m.startRelayMove(id, confirmation, false)
}
func (m *Manager) CheckRelayMove(id string) error { return m.startRelayMove(id, "", true) }
func (m *Manager) startRelayMove(id, confirmation string, checkOnly bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.relayByIDLocked(id)
	if m.closing || m.relayActive != "" || d == nil {
		return errors.New("请等待当前中转任务结束")
	}
	if checkOnly {
		if d.Job.State != "move_unknown" || !d.Move.Submitted {
			return errors.New("没有可只读核对的搬运删除回执")
		}
	} else {
		if !relayMoveEligible(*d) || d.Move.Submitted {
			return errors.New("仅支持符合搬运条件的 DAV、123 或夸克独立文件，已提交的搬运须只读核对")
		}
		expected := "删除源文件"
		if relayMoveToRecycle(d.SourceKind) {
			expected = "移入回收站"
		}
		if confirmation != expected {
			return errors.New("请输入“" + expected + "”确认搬运")
		}
	}
	a, b := m.downloadAccountLocked(d.Input.SourceAccountID), m.downloadAccountLocked(d.Input.TargetAccountID)
	if !relayMoveSource(*d) || !relayAccountPair(a, b, *d) {
		return errors.New("原账号来源已变化，未开始搬运")
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		return e
	}
	if e := m.reserveShareLocked(b.ID); e != nil {
		delete(m.renaming, a.ID)
		return e
	}
	old := *d
	if !checkOnly {
		d.Move = diskRelayMove{State: "working", Created: time.Now()}
		d.Job.State = "moving"
		d.Job.Message = "正在重新核对目标、本机缓存和源文件，尚未删除源文件"
	}
	d.Job.Phase = "搬运只读预检"
	if m.saveLocked() != nil {
		*d = old
		delete(m.renaming, a.ID)
		delete(m.renaming, b.ID)
		return errors.New("搬运初始记录保存失败，未开始")
	}
	ctx, cancel := context.WithTimeout(withTransferLimits(context.Background(), m.limits), relayJobTimeout(d.Input.Size))
	m.relayActive = id
	m.relayCancel = cancel
	copy := *d
	root := m.relayRoot()
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		err := m.performRelayMove(ctx, NewHTTPUploader().Client, a, b, copy, root, checkOnly)
		m.finishRelayMove(ctx, a, b, copy, err)
	}()
	return nil
}

func relayMoveTarget(ctx context.Context, client *http.Client, b Account, d diskRelay) error {
	if relayTargetNeedsFileID(b.Kind) {
		return relayNativeTargetReadback(ctx, client, b, d)
	}
	if b.Kind == "pan123" {
		return relay123Readback(ctx, client, b, d)
	}
	if b.Kind == "baidu" {
		return relayBaiduReadback(ctx, client, b, d)
	}
	resp, err := relayRequest(ctx, client, b, "GET", path.Join(d.Input.DestinationID, d.Input.Name), nil, nil, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return errors.New("目标完整字节读取未确认")
	}
	hash, err := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if err != nil {
		return err
	}
	if hash != d.Job.SHA256 {
		return errors.New("目标SHA256与原副本不符，未确认搬运")
	}
	return nil
}
func relayMoveSourceMetadata(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	p, err := relayDirectoryPage(ctx, client, a, d.Input.ParentID)
	if err != nil {
		return err
	}
	found := false
	for _, r := range p.Entries {
		if r.ID == d.Input.EntryID {
			found = r.Name == d.Input.Name && !r.Folder && r.Size != nil && *r.Size == d.Input.Size && relayTag(r) == d.ETag
		}
	}
	if !found {
		return errors.New("原DAV文件类型、大小或版本已变化，未删除")
	}
	return nil
}
func relayMoveOriginal(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	switch a.Kind {
	case "pan123":
		return relayMove123Original(ctx, client, a, d)
	case "quark":
		return relayFinalSource(ctx, client, a, d, nil)
	}
	if err := relayMoveSourceMetadata(ctx, client, a, d); err != nil {
		return err
	}
	resp, err := relayRequest(ctx, client, a, "GET", d.Input.EntryID, nil, map[string]string{"If-Match": d.ETag}, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("ETag") != d.ETag || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return errors.New("原DAV强ETag或字节响应未确认，未删除")
	}
	hash, err := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if err != nil {
		return err
	}
	if hash != d.Job.SHA256 {
		return errors.New("原DAV完整内容已变化，未删除")
	}
	// A long source read must not leave an old file/type listing as the last
	// pre-write observation. The DELETE still carries the original If-Match.
	return relayMoveSourceMetadata(ctx, client, a, d)
}
func relayFileTrashSource(d diskRelay) trashSource {
	in := d.Input
	size := in.Size
	purpose := "trash-v1"
	if d.SourceKind == "quark" {
		purpose = quarkTrashPurpose
	}
	return trashSource{
		Purpose: purpose, Scope: d.SourceScope,
		Input: TrashInput{
			AccountID: in.SourceAccountID, ParentID: in.ParentID,
			Entry: CloudEntry{ID: in.EntryID, Name: in.Name, Size: &size},
		},
		Original: RecycleEntry{
			ID: in.EntryID, ParentID: in.ParentID, Name: in.Name, Kind: "file", Size: &size,
			OriginalPath: trashDirectoryPath(in.ParentID, in.Name),
		},
	}
}

func relayMoveAbsent(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	if relayMoveToRecycle(a.Kind) {
		return checkTrash(ctx, client, a, relayFileTrashSource(d))
	}
	p, err := relayDirectoryPage(ctx, client, a, d.Input.ParentID)
	if err != nil {
		return err
	}
	for _, r := range p.Entries {
		if r.ID == d.Input.EntryID || strings.EqualFold(r.Name, d.Input.Name) {
			return errors.New("原位置仍有项目，保留待核对，不重发删除")
		}
	}
	resp, err := relayRequest(ctx, client, a, "GET", d.Input.EntryID, nil, nil, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		return errors.New("原路径缺失未获完整列表及404双重确认")
	}
	return nil
}
func relayMoveCache(ctx context.Context, root string, d diskRelay) (*os.File, error) {
	dir, err := relaySafePath(root, d.Job.ID)
	if err != nil {
		return nil, err
	}
	owner, err := relayReadOwner(filepath.Join(dir, ".owner"))
	if err != nil || string(owner) != "multipan-relay-v1:"+d.Job.ID {
		return nil, errors.New("本机原副本归属未确认，未删除源文件")
	}
	f, err := relayOpenCache(filepath.Join(dir, "data.bin"))
	if err != nil {
		return nil, err
	}
	hash, err := relayHash(ctx, f, d.Input.Size, nil)
	if err != nil || hash != d.Job.SHA256 {
		f.Close()
		return nil, errors.New("本机原副本不完整，未删除源文件")
	}
	return f, nil
}
func (m *Manager) performRelayMove(ctx context.Context, client *http.Client, a, b Account, d diskRelay, root string, checkOnly bool) error {
	if checkOnly {
		if err := relayMoveTarget(ctx, client, b, d); err != nil {
			return err
		}
		if err := relayMoveAbsent(ctx, client, a, d); err != nil {
			return err
		}
		if !d.Move.Acknowledged {
			return errors.New("源已不见但缺少已保存删除受理回执，无法确认，不重发")
		}
		return nil
	}
	cache, err := relayMoveCache(ctx, root, d)
	if err != nil {
		return err
	}
	defer cache.Close()
	m.relayProgress(d.Job.ID, "搬运回读目标", 0)
	if err = relayMoveTarget(ctx, client, b, d); err != nil {
		return err
	}
	m.relayProgress(d.Job.ID, "搬运复核源文件", 0)
	if err = relayMoveOriginal(ctx, client, a, d); err != nil {
		return err
	}
	m.mu.Lock()
	op := m.relayByIDLocked(d.Job.ID)
	if op == nil || m.closing || ctx.Err() != nil || !relayAccountPair(m.downloadAccountLocked(a.ID), m.downloadAccountLocked(b.ID), d) {
		m.mu.Unlock()
		return errors.New("搬运取消或来源变化，未删除源文件")
	}
	op.Move.Submitted = true
	op.Move.State = "unknown"
	op.Job.State = "move_unknown"
	op.Job.Phase = "条件删除源文件"
	if relayMoveToRecycle(a.Kind) {
		op.Job.Phase = "源文件移入回收站"
	}
	if m.saveLocked() != nil {
		op.Move.Submitted = false
		op.Move.State = "working"
		op.Job.State = "moving"
		m.mu.Unlock()
		return errors.New("源删除写前记录保存失败，未发送")
	}
	m.mu.Unlock()
	var ack bool
	switch a.Kind {
	case "pan123":
		ack = submitPan123Trash(ctx, client, a, d.Input.EntryID) == nil
	case "quark":
		ack = submitQuarkTrash(ctx, client, a, d.Input.EntryID)
	default:
		resp, requestErr := relayRequest(ctx, client, a, "DELETE", d.Input.EntryID, nil, map[string]string{"If-Match": d.ETag}, 0)
		ack = requestErr == nil && resp != nil && (resp.StatusCode == 200 || resp.StatusCode == 204)
		if resp != nil {
			resp.Body.Close()
		}
	}
	m.mu.Lock()
	op = m.relayByIDLocked(d.Job.ID)
	op.Move.Acknowledged = ack
	if m.saveLocked() != nil {
		op.Move.Acknowledged = false
		ack = false
	}
	m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.relayProgress(d.Job.ID, "搬运删除后只读核对", 0)
	if err = relayMoveAbsent(ctx, client, a, d); err != nil {
		return err
	}
	if err = relayMoveTarget(ctx, client, b, d); err != nil {
		return err
	}
	if !ack {
		return errors.New("没有已保存的源删除受理凭据，保留待核对，不重发")
	}
	return nil
}
func (m *Manager) finishRelayMove(ctx context.Context, a, b Account, d diskRelay, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.relayByIDLocked(d.Job.ID)
	if op != nil {
		if err == nil && (!relayAccountPair(m.downloadAccountLocked(a.ID), m.downloadAccountLocked(b.ID), d) || m.closing || ctx.Err() != nil) {
			err = errors.New("退出、停止或账号来源已变化，保留待核对")
		}
		if err == nil {
			op.Move.State = "confirmed"
			op.Job.State = "moved"
			op.Job.Message = "搬运已核对：源删除获明确受理且原位置不再列出、GET返回404，目标内容仍匹配；本机缓存保留"
			if relayMoveToRecycle(a.Kind) {
				op.Job.Message = "搬运已核对：源文件已移入回收站，目标内容仍匹配，本机缓存保留"
			}
		} else if op.Move.Submitted {
			op.Move.State = "unknown"
			op.Job.State = "move_unknown"
			op.Job.Message = "搬运删除结果待核对，不重发、不清理缓存：" + err.Error()
		} else {
			op.Move.State = "not_sent"
			op.Job.State = "verified"
			op.Job.Message = "搬运未提交源删除，副本保留；重新确认前请检查：" + err.Error()
		}
		op.Job.Phase = ""
		if m.saveLocked() != nil {
			if op.Move.Submitted {
				op.Move.State = "unknown"
				op.Job.State = "move_unknown"
			}
			op.Job.Message = "搬运最终记录保存失败，请核对原结果，不重发源删除"
		}
	}
	if m.relayCancel != nil {
		m.relayCancel()
	}
	m.relayCancel = nil
	m.relayActive = ""
	delete(m.renaming, a.ID)
	delete(m.renaming, b.ID)
	// A source-deletion uncertainty never starts additional queued work.
	if err == nil && op != nil && op.Job.State == "moved" {
		m.dispatchRelaysLocked()
	}
}
