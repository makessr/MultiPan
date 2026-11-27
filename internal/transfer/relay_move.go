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
	return kind == "pan123" || kind == "quark" || kind == "baidu"
}

func relayMoveSource(d diskRelay) bool {
	if d.Input.SourceAccountID != "" && d.Input.SourceAccountID == d.Input.TargetAccountID {
		return false
	}
	if d.SourceKind == "quark" {
		stamp, _ := relayQuarkParts(d.ETag)
		return validRelayQuarkStamp(stamp) && validRelayQuarkVersion(d.ETag) && relayPinnedSHA(d.SourceKind, d.ETag) == d.Job.SHA256 &&
			validQuarkTrashSource(relayFileTrashSource(d))
	}
	if d.SourceKind == "baidu" {
		return validRelayVersion(d.SourceKind, d.ETag) && validTrashSource(relayFileTrashSource(d))
	}
	return d.SourceKind == "" || d.SourceKind == "webdav" || d.SourceKind == "ctdav" || relayMoveToRecycle(d.SourceKind)
}
func relayMoveEligible(d diskRelay) bool {
	return relayMoveSource(d) && !d.Folder && d.Job.State == "verified" && d.Submitted &&
		validRelayVersion(d.SourceKind, d.ETag) && len(d.Job.SHA256) == 64 &&
		(!relayTargetNeedsFileID(d.TargetKind) || validRelayTargetFolderID(d.TargetKind, d.TargetFileID))
}
func validRelayMoveRecord(d diskRelay) bool {
	r := d.Move
	if r.State == "" {
		return r.Created.IsZero() && !r.Submitted && !r.Acknowledged && d.Job.State != "moving" && d.Job.State != "move_unknown" && d.Job.State != "moved"
	}
	if r.Created.IsZero() || !relayMoveSource(d) || d.Folder || !d.Submitted ||
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

const relayMoveMaxFiles = 20

// Only files in the recorded copy can be selected. Directory nodes are
// retained, including empty directories and those with newly added files.
func (m *Manager) relayFolderMoveFilesLocked(root diskRelay, a, b Account) []string {
	if !root.Folder || root.ParentJobID != "" || root.Job.State != "verified" || !root.CreatedAck || !relayAccountPair(a, b, root) {
		return nil
	}
	var ids []string
	for _, d := range m.relays {
		if d.Batch != root.Batch || d.Job.ID == root.Job.ID {
			continue
		}
		if d.ParentJobID == "" || !m.relayParentReadyLocked(d) || !relayAccountPair(a, b, d) {
			return nil
		}
		if d.Folder && d.Job.State == "verified" && d.CreatedAck || !d.Folder && d.Job.State == "moved" {
			continue
		}
		if !relayMoveEligible(d) || d.Move.Submitted {
			return nil
		}
		ids = append(ids, d.Job.ID)
	}
	if len(ids) > relayMoveMaxFiles {
		return nil
	}
	return ids
}

func (m *Manager) StartRelayMove(id, confirmation string) error {
	return m.startRelayMoves([]string{id}, "", confirmation, false)
}

func (m *Manager) StartRelayMoves(ids []string, folderID, confirmation string) error {
	return m.startRelayMoves(ids, folderID, confirmation, false)
}

func (m *Manager) CheckRelayMove(id string) error {
	return m.startRelayMoves([]string{id}, "", "", true)
}

func (m *Manager) startRelayMoves(ids []string, folderID, confirmation string, checkOnly bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.relayActive != "" {
		return errors.New("请等待当前中转任务结束")
	}
	if len(ids) == 0 || len(ids) > relayMoveMaxFiles {
		return errors.New("每批可搬运 1 至 20 个文件")
	}
	first := m.relayByIDLocked(ids[0])
	if first == nil {
		return errors.New("中转记录已变化，请刷新后重新选择")
	}
	a, b := m.downloadAccountLocked(first.Input.SourceAccountID), m.downloadAccountLocked(first.Input.TargetAccountID)
	if !checkOnly && first.SourceScope == first.TargetScope {
		return errors.New("源与目标指向同一网盘配置，未开始搬运")
	}
	var folderFiles map[string]bool
	if folderID != "" {
		if folder := m.relayByIDLocked(folderID); folder != nil {
			folderFiles = make(map[string]bool)
			for _, id := range m.relayFolderMoveFilesLocked(*folder, a, b) {
				folderFiles[id] = true
			}
		}
		if len(folderFiles) != len(ids) {
			return errors.New("目录文件清单已变化，请刷新后重新确认")
		}
	}

	rows := make([]diskRelay, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		d := m.relayByIDLocked(id)
		if d == nil || !relayMoveSource(*d) || !relayAccountPair(a, b, *d) ||
			!m.relayParentReadyLocked(*d) || seen[d.Input.EntryID] {
			return errors.New("存在重复源文件或来源、目标不一致，请重新选择")
		}
		if checkOnly {
			if d.Job.State != "move_unknown" || !d.Move.Submitted {
				return errors.New("没有可只读核对的搬运删除回执")
			}
		} else if !relayMoveEligible(*d) || d.Move.Submitted ||
			(folderID == "" && d.ParentJobID != "" || folderID != "" && !folderFiles[id]) {
			return errors.New("所选文件尚未满足搬运条件，请刷新后重新选择")
		}
		seen[d.Input.EntryID] = true
		rows = append(rows, *d)
	}
	if !checkOnly {
		if relayMoveOverlaps(a, b, rows) {
			return errors.New("所选来源与目标文件重叠，未开始搬运")
		}
		expected := "删除源文件"
		if relayMoveToRecycle(a.Kind) {
			expected = "移入回收站"
		}
		if confirmation != expected {
			return errors.New("请输入“" + expected + "”确认搬运")
		}
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		return e
	}
	if e := m.reserveShareLocked(b.ID); e != nil {
		delete(m.renaming, a.ID)
		return e
	}
	now := time.Now()
	for _, row := range rows {
		d := m.relayByIDLocked(row.Job.ID)
		if !checkOnly {
			d.Move = diskRelayMove{State: "working", Created: now}
			d.Job.State = "moving"
			d.Job.Message = "等待逐项搬运核对，尚未提交源操作"
		}
		d.Job.Phase = "搬运只读预检"
	}
	if m.saveLocked() != nil {
		for _, row := range rows {
			*m.relayByIDLocked(row.Job.ID) = row
		}
		delete(m.renaming, a.ID)
		delete(m.renaming, b.ID)
		return errors.New("搬运初始记录保存失败，未开始")
	}
	ctx, cancel := context.WithCancel(withTransferLimits(context.Background(), m.limits))
	m.relayActive = first.Job.ID
	m.relayCancel = cancel
	root := m.relayRoot()
	m.workers.Add(1)
	go m.runRelayMoves(ctx, a, b, rows, root, checkOnly)
	return nil
}

// Separately configured accounts can expose the same underlying DAV path.
func relayMoveOverlaps(a, b Account, rows []diskRelay) bool {
	if !relayProvider(a) || !relayProvider(b) {
		return false
	}
	sources := make(map[string]bool, len(rows))
	for _, d := range rows {
		source, err := relayURL(a, d.Input.EntryID)
		if err != nil {
			return true
		}
		sources[strings.ToLower(source)] = true
	}
	for _, d := range rows {
		target, err := relayURL(b, path.Join(d.Input.DestinationID, d.Input.Name))
		if err != nil || sources[strings.ToLower(target)] {
			return true
		}
	}
	return false
}

func (m *Manager) runRelayMoves(ctx context.Context, a, b Account, rows []diskRelay, root string, checkOnly bool) {
	defer m.workers.Done()
	client := NewHTTPUploader().Client
	remaining := rows
	completed := false
	for len(remaining) > 0 && ctx.Err() == nil {
		d := remaining[0]
		remaining = remaining[1:]
		m.mu.Lock()
		m.relayActive = d.Job.ID
		m.mu.Unlock()
		jobCtx, cancel := context.WithTimeout(ctx, relayJobTimeout(d.Input.Size))
		resolved, err := m.resolveRelayTarget(d)
		if err == nil {
			err = m.performRelayMove(jobCtx, client, a, b, resolved, root, checkOnly)
		}
		m.mu.Lock()
		ok := m.finishRelayMoveLocked(jobCtx, a, b, d, err)
		m.mu.Unlock()
		cancel()
		if !ok {
			break
		}
		completed = len(remaining) == 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range remaining {
		d := m.relayByIDLocked(row.Job.ID)
		d.Move = row.Move
		d.Job = row.Job
		d.Job.Message = "批次已停止，此文件尚未提交源操作，副本保留"
	}
	if len(remaining) > 0 {
		// Restart also restores unsubmitted moving records to verified.
		_ = m.saveLocked()
	}
	stopped := ctx.Err() != nil
	m.relayCancel()
	m.relayCancel = nil
	m.relayActive = ""
	delete(m.renaming, a.ID)
	delete(m.renaming, b.ID)
	if completed && !stopped {
		m.dispatchRelaysLocked()
	}
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
	case "baidu":
		return relayMoveBaiduOriginal(ctx, client, a, d)
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
	source := trashSource{
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
	if d.SourceKind == "baidu" {
		id, _, _ := strings.Cut(d.ETag, ":")
		source.Input.Entry.ObjectID = id
		source.Original.ID, source.Original.OriginalPath, source.Original.ParentID = id, in.EntryID, ""
	}
	return source
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
	if err := m.relayTargetDirectoryAncestors(ctx, client, b, d); err != nil {
		return err
	}
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
	var baidu *baiduClient
	if a.Kind == "baidu" {
		baidu = newBaiduClient(client, a)
		if err := baidu.session(ctx); err != nil {
			return err
		}
	}
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
	case "baidu":
		ack = baidu.submitTrash(ctx, d.Input.EntryID) == nil
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
	if err = m.relayTargetDirectoryAncestors(ctx, client, b, d); err != nil {
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
func (m *Manager) finishRelayMoveLocked(ctx context.Context, a, b Account, d diskRelay, err error) bool {
	op := m.relayByIDLocked(d.Job.ID)
	if op == nil {
		return false
	}
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
	return err == nil && op.Job.State == "moved"
}
