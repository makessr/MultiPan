package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

func validRelayTargetFolderID(kind, id string) bool {
	if kind == "quark" {
		return validQuarkFolder(id) && id != "0"
	}
	if kind == "guangya" {
		return validGuangyaCopyFileID(id)
	}
	return (kind == "baidu" || kind == "pan123") && valid123Folder(id) && id != "0"
}

func validRelayTreeRecord(d diskRelay) bool {
	if d.TargetFileID != "" && (d.Folder || !relayTargetNeedsFileID(d.TargetKind) || !d.Submitted || !validRelayTargetFolderID(d.TargetKind, d.TargetFileID)) ||
		relayTargetNeedsFileID(d.TargetKind) && !d.Folder && (d.Job.State == "verified" || d.Move.State != "") && d.TargetFileID == "" {
		return false
	}
	if d.TargetFolderID != "" && (!d.Folder || !d.CreatedAck || !validRelayTargetFolderID(d.TargetKind, d.TargetFolderID)) {
		return false
	}
	if d.Folder && (d.TargetKind == "baidu" || relayIDTreeTarget(d.TargetKind)) && d.CreatedAck && d.TargetFolderID == "" {
		return false
	}
	if d.Folder != d.Job.Folder || d.CreatedAck && (!d.Folder || !d.Submitted) {
		return false
	}
	if d.ParentJobID != "" && !shareToken.MatchString(d.ParentJobID) {
		return false
	}
	if d.Folder || d.ParentJobID != "" {
		if !relayDirectoryProvider(relayDirectorySavedAccount(d.SourceKind)) || !relayDirectoryTargetProvider(relayDirectorySavedAccount(d.TargetKind)) {
			return false
		}
	}
	return !d.Folder || d.Input.Size == 0 && d.Job.Size == 0 && d.Job.CacheBytes == 0 && d.Job.SHA256 == "" && validRelayDirectoryStamp(d.SourceKind, d.ETag)
}
func validateRelayTreeParents(rows []diskRelay) error {
	previous := map[string]diskRelay{}
	for _, d := range rows {
		if d.ParentJobID != "" {
			p, ok := previous[d.ParentJobID]
			if !ok || !p.Folder || p.TargetKind != d.TargetKind || p.Batch != d.Batch || p.SourceScope != d.SourceScope || p.TargetScope != d.TargetScope || p.Input.SourceAccountID != d.Input.SourceAccountID || p.Input.TargetAccountID != d.Input.TargetAccountID || p.Input.EntryID != d.Input.ParentID || relayPlannedChildTarget(p) != d.Input.DestinationID {
				return errors.New("中转目录依赖记录无效，未覆盖原数据")
			}
		}
		previous[d.Job.ID] = d
	}
	return nil
}
func (m *Manager) relayParentReadyLocked(d diskRelay) bool {
	if d.ParentJobID == "" {
		return true
	}
	p := m.relayByIDLocked(d.ParentJobID)
	return p != nil && p.Folder && p.CreatedAck && p.Job.State == "verified" && p.Batch == d.Batch && p.SourceScope == d.SourceScope && p.TargetScope == d.TargetScope
}
func (m *Manager) relayHasChildrenLocked(id string) bool {
	for _, d := range m.relays {
		if d.ParentJobID == id {
			return true
		}
	}
	return false
}

// QueueRelayDirectory captures a bounded, complete manifest before any writes.
// Each directory and file is an individual durable receipt; this is not an
// atomic tree operation. Newly added source entries are outside this manifest.
func (m *Manager) QueueRelayDirectory(ctx context.Context, in RelayInput) (int, error) {
	return m.queueRelayDirectory(ctx, in, false)
}

func (m *Manager) QueueRelaySnapshotDirectory(ctx context.Context, in RelayInput) (int, error) {
	return m.queueRelayDirectory(ctx, in, true)
}

func (m *Manager) queueRelayDirectory(ctx context.Context, in RelayInput, snapshot bool) (int, error) {
	if !validRelayInput(in) || in.Size != 0 {
		return 0, errors.New("目录中转信息无效")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a, b := m.downloadAccountLocked(in.SourceAccountID), m.downloadAccountLocked(in.TargetAccountID)
	closing := m.closing
	pauseVersion := m.relayPauseVersion
	originals := append([]UploadFingerprint(nil), m.fingerprints...)
	if snapshot && !closing {
		var finish func()
		ctx, finish = m.beginRelaySnapshotLocked(ctx)
		defer finish()
	}
	m.mu.Unlock()
	if closing || ctx.Err() != nil || !relayDirectoryProvider(a) || !relayDirectoryTargetProvider(b) || !validRelaySource(a, in) || !validRelayTarget(b, in.DestinationID) {
		return 0, errors.New("来源或目标账号不支持当前目录中转")
	}
	if a.ID == b.ID {
		if a.Kind != "quark" {
			return 0, errors.New("同账号目录中转目前仅支持夸克")
		}
		if in.DestinationID == in.EntryID || in.DestinationID == in.ParentID {
			return 0, errors.New("请选择源目录及其所在目录以外的目标位置，整批未创建")
		}
	}
	if snapshot && !relayUsesOriginalPin(a.Kind) {
		return 0, errors.New("目录原件预读支持夸克和光鸭来源")
	}
	if a.Kind == "baidu" && b.Kind == "baidu" {
		dest := path.Join(in.DestinationID, in.Name)
		if dest == in.EntryID || strings.HasPrefix(dest, in.EntryID+"/") || strings.HasPrefix(in.EntryID, dest+"/") {
			return 0, errors.New("百度来源和目标目录路径重叠，整批未创建；请选择不同目标位置")
		}
	}
	var e error
	if relayProvider(a) && relayProvider(b) {
		su, e := relayURL(a, in.EntryID)
		if e != nil {
			return 0, e
		}
		tu, e := relayURL(b, path.Join(in.DestinationID, in.Name))
		if e != nil {
			return 0, e
		}
		s, _ := url.Parse(su)
		t, _ := url.Parse(tu)
		if s.Scheme == t.Scheme && strings.EqualFold(s.Host, t.Host) && (s.Path == t.Path || strings.HasPrefix(t.Path, s.Path+"/") || strings.HasPrefix(s.Path, t.Path+"/")) {
			return 0, errors.New("源目录与目标路径重叠，整批未创建")
		}
	}
	if !snapshot {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
	}
	client := NewHTTPUploader().Client
	source, e := relayDirectoryPage(ctx, client, a, in.ParentID)
	if e != nil {
		return 0, e
	}
	stamp := ""
	for _, r := range source.Entries {
		if r.ID == in.EntryID && r.Name == in.Name && r.Folder {
			stamp = relayDirectoryStamp(a.Kind, r)
		}
	}
	if stamp == "" {
		return 0, errors.New("源目录不存在或位置已变化")
	}
	target, e := relayDirectoryPage(ctx, client, b, in.DestinationID)
	if e != nil {
		return 0, e
	}
	for _, r := range target.Entries {
		if strings.EqualFold(r.Name, in.Name) {
			return 0, errors.New("目标有同名项目，整棵目录未创建，不合并或覆盖")
		}
	}
	batch := newID()
	rows := []diskRelay{}
	dirs, files := 0, 0
	var total int64
	visited := map[string]bool{}
	var scan func(RelayInput, string, string, string, int) error
	scan = func(node RelayInput, parent, stamp, relative string, depth int) error {
		if b.Kind == "baidu" || relayIDTreeTarget(b.Kind) {
			if e := validateCloudFolderName(node.Name); e != nil {
				return errors.New("原生目标目录名称不受支持，整批未创建")
			}
		}
		if depth > 8 {
			return errors.New("目录超过8层，整批未创建")
		}
		dirs++
		if dirs > 20 {
			return errors.New("目录中转最多20个目录（含根），整批未创建")
		}
		if visited[node.EntryID] {
			return errors.New("目录清单有重复或循环ID，整批未创建")
		}
		visited[node.EntryID] = true
		if a.Kind == "baidu" {
			visited["object:"+strings.TrimPrefix(stamp, "directory-baidu-v1:")] = true
		}
		id := newID()
		makeRow := func(input RelayInput, folder bool, tag, pid, jid string) diskRelay {
			return diskRelay{Folder: folder, ParentJobID: pid, SourceKind: a.Kind, TargetKind: b.Kind, Input: input, SourceScope: downloadScope(a), TargetScope: downloadScope(b), ETag: tag, Batch: batch, Job: RelayJob{ID: jid, Folder: folder, SourceAccountID: a.ID, TargetAccountID: b.ID, SourceName: a.Name, TargetName: b.Name, Name: input.Name, Destination: input.DestinationID, Size: input.Size, State: "queued", Message: "按预检清单复制，源保留；每项独立确认", Created: time.Now()}}
		}
		rows = append(rows, makeRow(node, true, stamp, parent, id))
		page, e := relayDirectoryPage(ctx, client, a, node.EntryID)
		if e != nil {
			return e
		}
		sort.Slice(page.Entries, func(i, j int) bool { return page.Entries[i].Name < page.Entries[j].Name })
		names := map[string]bool{}
		for _, r := range page.Entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			key := strings.ToLower(r.Name)
			if !relayName(r.Name) || names[key] || visited[r.ID] || a.Kind == "baidu" && visited["object:"+r.ObjectID] || (relayProvider(a) || a.Kind == "baidu") && r.ID != path.Join(node.EntryID, r.Name) {
				return errors.New("源目录含无效或重名子项，整批未创建")
			}
			names[key] = true
			child := RelayInput{SourceAccountID: a.ID, ParentID: node.EntryID, EntryID: r.ID, Name: r.Name, TargetAccountID: b.ID, DestinationID: relayPlannedChildTarget(diskRelay{TargetKind: b.Kind, Input: node})}
			if r.Folder {
				folderStamp := relayDirectoryStamp(a.Kind, r)
				if folderStamp == "" {
					return errors.New("子目录缺少可靠身份，整批未创建")
				}
				if e := scan(child, id, folderStamp, path.Join(relative, r.Name), depth+1); e != nil {
					return e
				}
				continue
			}
			version := relaySourceVersion(a.Kind, r)
			if relayUsesOriginalPin(a.Kind) {
				version = relayPinnedVersion(a.Kind, a.ID, r, originals)
			}
			fileName := path.Join(relative, r.Name)
			if r.Size == nil || *r.Size < 0 {
				return fmt.Errorf("源文件“%s”未返回可靠大小，整棵目录未创建", fileName)
			}
			if *r.Size > relayMaxFile {
				return fmt.Errorf("源文件“%s”大小 %s，超过单文件8 GiB上限，整棵目录未创建", fileName, relaySizeLabel(*r.Size))
			}
			if snapshot {
				if !relaySnapshotEligible(a.Kind, r) || total+*r.Size > relaySnapshotMaxBatch {
					return fmt.Errorf("源文件“%s”缺少预读所需版本或超过单文件 2 GiB、目录合计 4 GiB 的限制，整棵目录未创建", fileName)
				}
				version = r.revision
			} else if !validRelayVersion(a.Kind, version) {
				reason := "缺少可靠的原内容MD5"
				if relayProvider(a) {
					reason = "缺少强ETag版本依据"
				} else if relayUsesOriginalPin(a.Kind) {
					reason = "缺少可信的本机上传指纹或必要版本"
				}
				return fmt.Errorf("源文件“%s”大小 %s，%s，整棵目录未创建", fileName, relaySizeLabel(*r.Size), reason)
			}
			if !relayTargetAcceptsEmpty(b.Kind) && *r.Size == 0 {
				return errors.New("所选目标暂不支持空文件，整棵目录未创建")
			}
			visited[r.ID] = true
			if a.Kind == "baidu" {
				visited["object:"+r.ObjectID] = true
			}
			files++
			if files > 20 {
				return errors.New("目录中转最多20个文件，整批未创建")
			}
			child.Size = *r.Size
			total += child.Size
			if total > relayMaxTotal {
				return errors.New("目录文件合计超过32GiB，整批未创建")
			}
			rows = append(rows, makeRow(child, false, version, id, newID()))
		}
		return nil
	}
	if e = scan(in, "", stamp, in.Name, 1); e != nil {
		return 0, e
	}
	if a.Kind == b.Kind && relayIDTreeTarget(b.Kind) && visited[in.DestinationID] {
		return 0, errors.New("目标目录 ID 位于来源树内，整批未创建，请选择其他目标位置")
	}
	if e := relayCacheSpace(m.relayRoot(), total); e != nil {
		return 0, e
	}
	if snapshot {
		if err := readRelaySnapshotRows(ctx, client, a, rows, m.updateRelayPreflight); err != nil {
			return 0, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || ctx.Err() != nil || m.relayPauseVersion != pauseVersion || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) || downloadScope(m.downloadAccountLocked(b.ID)) != downloadScope(b) {
		return 0, errors.New("账号来源变化或预检取消，未创建队列")
	}
	if len(m.relays)+len(rows) > 1000 {
		return 0, errors.New("中转记录最多1000条，整批未创建")
	}
	for _, old := range m.relays {
		total += old.Input.Size
		for _, d := range rows {
			if relayKey(old) == relayKey(d) {
				return 0, errors.New("已有同源同目标记录，先核对原结果")
			}
		}
	}
	if total > relayMaxTotal {
		return 0, errors.New("中转记录合计最多32GiB，整批未创建")
	}
	n := len(m.relays)
	m.relays = append(m.relays, rows...)
	if e = m.saveLocked(); e != nil {
		m.relays = m.relays[:n]
		return 0, errors.New("目录清单未保存，未开始创建或上传")
	}
	m.dispatchRelaysLocked()
	return len(rows), nil
}

func relayDirectorySource(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	page, e := relayDirectoryPage(ctx, client, a, d.Input.ParentID)
	if e != nil {
		return e
	}
	for _, r := range page.Entries {
		if r.ID == d.Input.EntryID && r.Name == d.Input.Name && r.Folder && relayDirectoryStamp(a.Kind, r) == d.ETag {
			return nil
		}
	}
	return errors.New("源目录已变化，未确认目录复制")
}

// Check the live source tree again before creating a same-account root.
// The destination may have moved since the queue was saved.
func checkRelayQuarkDirectoryDestination(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	type directory struct {
		id    string
		depth int
	}
	pending := []directory{{id: d.Input.EntryID, depth: 1}}
	seen := map[string]bool{d.Input.EntryID: true}
	for i := 0; i < len(pending); i++ {
		current := pending[i]
		page, err := relayDirectoryPage(ctx, client, a, current.id)
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			if entry.ID == d.Input.DestinationID {
				return errors.New("目标目录已进入来源树，未创建目标目录")
			}
			if !entry.Folder {
				continue
			}
			if seen[entry.ID] || current.depth >= 8 || len(pending) >= 20 {
				return errors.New("来源目录已变化或超出核对范围，未创建目标目录")
			}
			seen[entry.ID] = true
			pending = append(pending, directory{id: entry.ID, depth: current.depth + 1})
		}
	}
	return nil
}

func (m *Manager) runRelayDirectory(ctx context.Context, client *http.Client, a, b Account, d diskRelay, checkOnly bool) error {
	if e := m.relayDirectoryAncestors(ctx, client, a, d); e != nil {
		return e
	}
	if e := m.relayTargetDirectoryAncestors(ctx, client, b, d); e != nil {
		return e
	}
	if !checkOnly {
		if e := relayDirectorySource(ctx, client, a, d); e != nil {
			return e
		}
		if e := relayTargetFree(ctx, client, b, d); e != nil {
			return e
		}
		create, e := prepareRelayDirectoryCreate(ctx, client, b, d)
		if e != nil {
			return e
		}
		if e := m.relayTargetDirectoryAncestors(ctx, client, b, d); e != nil {
			return e
		}
		if a.ID == b.ID && d.ParentJobID == "" {
			if e := checkRelayQuarkDirectoryDestination(ctx, client, a, d); e != nil {
				return e
			}
		}
		m.mu.Lock()
		op := m.relayByIDLocked(d.Job.ID)
		if op == nil || m.closing || ctx.Err() != nil || !m.relayParentReadyLocked(d) || !relayAccountPair(m.downloadAccountLocked(a.ID), m.downloadAccountLocked(b.ID), d) {
			m.mu.Unlock()
			return errors.New("操作取消或来源变化")
		}
		op.Submitted = true
		op.Job.State = "uploading"
		op.Job.Phase = "创建目标目录"
		if e := m.saveLocked(); e != nil {
			op.Submitted = false
			m.mu.Unlock()
			return errors.New("目录写前检查点未保存，未发送创建")
		}
		targetParent := d.targetParent
		d = *op
		d.targetParent = targetParent
		m.mu.Unlock()
		objectID, e := create()
		if e != nil {
			return e
		}
		m.mu.Lock()
		op = m.relayByIDLocked(d.Job.ID)
		if op == nil {
			m.mu.Unlock()
			return errors.New("目录记录已变化")
		}
		op.CreatedAck = true
		op.TargetFolderID = objectID
		if e := m.saveLocked(); e != nil {
			op.CreatedAck = false
			op.TargetFolderID = ""
			m.mu.Unlock()
			return errors.New("目录受理凭据未能保存，保持待核对")
		}
		targetParent = d.targetParent
		d = *op
		d.targetParent = targetParent
		m.mu.Unlock()
	}
	// An existing collection alone cannot prove that an ambiguous MKCOL created it.
	if !d.CreatedAck {
		return errors.New("没有已保存的目录创建受理凭据；仅看到同名目录不能确认，请到目标盘核查")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e := relayDirectoryTarget(ctx, client, b, d); e != nil {
		return e
	}
	if e := relayDirectorySource(ctx, client, a, d); e != nil {
		return e
	}
	if e := m.relayDirectoryAncestors(ctx, client, a, d); e != nil {
		return e
	}
	return m.relayTargetDirectoryAncestors(ctx, client, b, d)
}
