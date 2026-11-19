package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

const relayMaxFile int64 = 8 << 30
const relayMaxTotal int64 = 32 << 30
const relaySnapshotMaxFile = 2 << 30
const relaySnapshotMaxBatch = 4 << 30

type RelayInput struct {
	SourceAccountID string `json:"sourceAccountId"`
	ParentID        string `json:"parentId"`
	EntryID         string `json:"entryId"`
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	TargetAccountID string `json:"targetAccountId"`
	DestinationID   string `json:"destinationId"`
}
type RelayJob struct {
	Folder          bool      `json:"folder"`
	ID              string    `json:"id"`
	SourceAccountID string    `json:"sourceAccountId"`
	TargetAccountID string    `json:"targetAccountId"`
	SourceName      string    `json:"sourceName"`
	TargetName      string    `json:"targetName"`
	SourceKind      string    `json:"sourceKind,omitempty"`
	TargetKind      string    `json:"targetKind,omitempty"`
	SourcePath      string    `json:"sourcePath,omitempty"`
	Name            string    `json:"name"`
	Destination     string    `json:"destination"`
	State           string    `json:"state"`
	Phase           string    `json:"phase"`
	Message         string    `json:"message"`
	Size            int64     `json:"size"`
	Progress        int64     `json:"progress"`
	CacheBytes      int64     `json:"cacheBytes"`
	SHA256          string    `json:"sha256"`
	Created         time.Time `json:"created"`
	CanResume       bool      `json:"canResume"`
	CanCheck        bool      `json:"canCheck"`
	CanClear        bool      `json:"canClear"`
	CanMove         bool      `json:"canMove"`
	CanCheckMove    bool      `json:"canCheckMove"`
}
type diskRelay struct {
	Move           diskRelayMove `json:"move,omitempty"`
	targetParent   string        // Runtime-only ID resolved from the acknowledged parent receipt.
	Folder         bool          `json:"folder,omitempty"`
	ParentJobID    string        `json:"parentJobId,omitempty"`
	CreatedAck     bool          `json:"createdAck,omitempty"`
	TargetFolderID string        `json:"targetFolderId,omitempty"`
	TargetFileID   string        `json:"targetFileId,omitempty"`
	SourceKind     string        `json:"sourceKind,omitempty"` // Empty is the legacy DAV-only record.
	TargetKind     string        `json:"targetKind,omitempty"` // Empty is the legacy DAV-only target.
	Input          RelayInput    `json:"input"`
	SourceScope    string        `json:"sourceScope"`
	TargetScope    string        `json:"targetScope"`
	ETag           string        `json:"etag"`
	Submitted      bool          `json:"submitted"`
	Batch          string        `json:"batch"`
	Job            RelayJob      `json:"job"`
}

func relayProvider(a Account) bool { return a.Kind == "webdav" || a.Kind == "ctdav" }
func validRelayInput(in RelayInput) bool {
	sourcePath := validBaiduPath(in.ParentID) && validBaiduPath(in.EntryID) && in.EntryID != "/" && path.Dir(in.EntryID) == in.ParentID && path.Base(in.EntryID) == in.Name
	sourceID := validQuarkFolder(in.ParentID) && validQuarkFolder(in.EntryID) && in.EntryID != "0" && in.EntryID != in.ParentID
	return in.SourceAccountID != "" && in.TargetAccountID != "" && (sourcePath || sourceID) && (validBaiduPath(in.DestinationID) || validQuarkFolder(in.DestinationID)) && relayName(in.Name) && in.Size >= 0 && in.Size <= relayMaxFile
}
func relayName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func strongRelayTag(tag string) bool {
	if len(tag) < 2 || len(tag) > 512 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, b := range []byte(tag[1 : len(tag)-1]) {
		if b < 0x21 || b == 0x7f || b == '"' {
			return false
		}
	}
	return true
}
func relayTag(row CloudEntry) string {
	if i := strings.LastIndexByte(row.revision, '|'); i >= 0 {
		return row.revision[:i]
	}
	return ""
}
func relayKey(d diskRelay) string {
	return d.SourceScope + "\x00" + d.Input.EntryID + "\x00" + d.TargetScope + "\x00" + path.Join(d.Input.DestinationID, d.Input.Name)
}
func validRelayAccounts(d diskRelay) bool {
	if d.Input.SourceAccountID != d.Input.TargetAccountID {
		return true
	}
	return d.SourceKind == "quark" && d.TargetKind == "quark" && d.SourceScope == d.TargetScope &&
		d.Input.ParentID != d.Input.DestinationID && d.Input.EntryID != d.Input.DestinationID &&
		d.TargetFileID != d.Input.EntryID && d.TargetFolderID != d.Input.EntryID
}

func relayAccountPair(a, b Account, d diskRelay) bool {
	kindMatches := d.SourceKind == a.Kind || d.SourceKind == "" && relayProvider(a)
	targetMatches := d.TargetKind == b.Kind || d.TargetKind == "" && relayProvider(b)
	return validRelayAccounts(d) && a.ID == d.Input.SourceAccountID && b.ID == d.Input.TargetAccountID && kindMatches && targetMatches && validRelaySource(a, d.Input) && validRelayPlannedTarget(b, d) && downloadScope(a) == d.SourceScope && downloadScope(b) == d.TargetScope
}
func (m *Manager) restoreRelays(rows []diskRelay) error {
	if len(rows) > 1000 {
		return errors.New("中转复制记录数量异常")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	var total int64
	for i := range rows {
		d := &rows[i]
		j := &d.Job
		digest, e := hex.DecodeString(j.SHA256)
		if relayUsesOriginalPin(d.SourceKind) && j.SHA256 != "" {
			pin := relayPinnedSHA(d.SourceKind, d.ETag)
			if j.SHA256 != pin {
				return errors.New("中转副本与可信原件指纹矛盾")
			}
		}
		total += d.Input.Size
		if total > relayMaxTotal || len(d.SourceScope) != 64 || len(d.TargetScope) != 64 || !shareToken.MatchString(d.Batch) {
			return errors.New("中转复制记录容量或来源无效")
		}
		if !validRelayAccounts(*d) || !validRelayTreeRecord(*d) || !validRelayInput(d.Input) || !validRelaySavedSource(*d) || !validRelaySavedTarget(*d) || !shareToken.MatchString(j.ID) || j.ID == "" || ids[j.ID] || seen[relayKey(*d)] || d.SourceScope == "" || d.TargetScope == "" || d.Batch == "" || j.Created.IsZero() || j.SourceAccountID != d.Input.SourceAccountID || j.TargetAccountID != d.Input.TargetAccountID || j.Name != d.Input.Name || j.Size != d.Input.Size || j.Destination != d.Input.DestinationID || j.CacheBytes < 0 || j.CacheBytes > j.Size || j.SHA256 != "" && (e != nil || len(digest) != 32) || d.Submitted && j.SHA256 == "" && !d.Folder {
			return errors.New("中转复制记录损坏或重复，未覆盖原数据")
		}
		if !validRelayMoveRecord(*d) {
			return errors.New("搬运删除回执与原中转记录矛盾，未覆盖原数据")
		}
		seen[relayKey(*d)] = true
		ids[j.ID] = true
		switch j.State {
		case "moving", "move_unknown":
			if d.Move.Submitted {
				j.State = "move_unknown"
				d.Move.State = "unknown"
				j.Message = "上次搬运删除结果待核对，只读检查，不重发源删除"
			} else {
				j.State = "verified"
				d.Move.State = "not_sent"
				j.Message = "上次搬运未提交源删除，副本仍保留；需重新明确确认"
			}
		case "moved":
		case "queued", "downloading", "ready", "uploading", "verifying", "unknown", "paused", "failed":
			if d.Submitted {
				j.State = "unknown"
				j.Message = "上次目标结果待核对，只读检查，不会重新上传"
			} else {
				j.State = "paused"
				j.Message = "上次任务已暂停，明确继续后检查原版本再处理"
			}
		case "verified":
			if !d.Submitted || d.Folder && !d.CreatedAck || !d.Folder && j.SHA256 == "" {
				return errors.New("中转复制确认状态矛盾")
			}
		default:
			return errors.New("中转复制状态无效")
		}
		j.CanResume = false
		j.CanCheck = false
		j.CanClear = false
		j.CanMove = false
		j.CanCheckMove = false
		j.Phase = ""
	}
	if e := validateRelayTreeParents(rows); e != nil {
		return e
	}
	m.relays = rows
	return nil
}
func (m *Manager) relayByIDLocked(id string) *diskRelay {
	for i := range m.relays {
		if m.relays[i].Job.ID == id {
			return &m.relays[i]
		}
	}
	return nil
}
func (m *Manager) relayRoot() string { return filepath.Join(filepath.Dir(m.path), "relay-cache") }
func (m *Manager) RelayJobs() []RelayJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RelayJob{}
	for _, d := range m.relays {
		j := d.Job
		j.SourcePath = d.Input.EntryID
		if relayMoveToRecycle(d.SourceKind) {
			j.SourcePath = trashDirectoryPath(d.Input.ParentID, d.Input.Name)
		}
		j.SourceKind = d.SourceKind
		j.TargetKind = d.TargetKind
		if j.SourceKind == "" {
			j.SourceKind = "legacy-dav"
		}
		if j.TargetKind == "" {
			j.TargetKind = "legacy-dav"
		}
		a, b := m.downloadAccountLocked(d.Input.SourceAccountID), m.downloadAccountLocked(d.Input.TargetAccountID)
		same := relayAccountPair(a, b, d)
		active := m.relayActive == j.ID
		j.CanResume = same && m.relayParentReadyLocked(d) && !active && !d.Submitted && (j.State == "paused" || j.State == "failed")
		j.CanCheck = same && !active && d.Submitted && j.State == "unknown"
		j.CanClear = !active && (j.State == "verified" || !d.Submitted && (j.State == "paused" || j.State == "failed"))
		j.CanMove = same && m.relayActive == "" && relayMoveEligible(d) && !d.Move.Submitted
		j.CanCheckMove = same && m.relayActive == "" && j.State == "move_unknown" && d.Move.Submitted
		if j.State == "moved" {
			j.CanClear = !active
		}
		if m.relayHasChildrenLocked(j.ID) {
			j.CanClear = false
		}
		if !same {
			j.Message = "账号来源已变化，不能继续或核对旧任务，请在原网盘核查"
		}
		out = append(out, j)
	}
	return out
}

// Queueing is read-only against both servers. All selected files pass before
// the durable batch is saved; no source content or target writes occur here.
func (m *Manager) QueueRelays(ctx context.Context, inputs []RelayInput) (int, error) {
	return m.queueRelays(ctx, inputs, false)
}

func (m *Manager) QueueRelaySnapshots(ctx context.Context, inputs []RelayInput) (int, error) {
	return m.queueRelays(ctx, inputs, true)
}

func (m *Manager) queueRelays(ctx context.Context, inputs []RelayInput, snapshot bool) (int, error) {
	if len(inputs) < 1 || len(inputs) > 20 {
		return 0, errors.New("每批中转复制请选择1至20个文件")
	}
	for _, in := range inputs {
		if !validRelayInput(in) {
			return 0, errors.New("中转文件信息无效；单文件最多8GiB，仅支持文件")
		}
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a, b := m.downloadAccountLocked(inputs[0].SourceAccountID), m.downloadAccountLocked(inputs[0].TargetAccountID)
	closing := m.closing
	pauseVersion := m.relayPauseVersion
	originals := append([]UploadFingerprint(nil), m.fingerprints...)
	if snapshot && !closing {
		var finish func()
		ctx, finish = m.beginRelaySnapshotLocked(ctx)
		defer finish()
	}
	m.mu.Unlock()
	if closing || !validRelaySource(a, inputs[0]) || !validRelayTarget(b, inputs[0].DestinationID) {
		return 0, errors.New("来源或目标账号不支持当前文件中转")
	}
	if a.ID == b.ID && (a.Kind != "quark" || inputs[0].ParentID == inputs[0].DestinationID) {
		return 0, errors.New("同账号中转仅支持夸克文件复制，请选择不同的目标目录")
	}
	if snapshot && !relayUsesOriginalPin(a.Kind) {
		return 0, errors.New("读取网盘原件模式目前支持夸克和光鸭来源")
	}
	if !relayTargetAcceptsEmpty(b.Kind) {
		for _, in := range inputs {
			if in.Size == 0 {
				if b.Kind == "baidu" {
					return 0, errors.New("百度当前目标上传不支持空文件，整批未创建；请取消选择空文件")
				}
				return 0, errors.New("所选目标暂不支持空文件，整批未创建；请取消选择空文件")
			}
		}
	}
	client := NewHTTPUploader().Client
	source, e := relaySourcePage(ctx, client, a, inputs[0].ParentID)
	if e != nil {
		return 0, e
	}
	target, e := relayTargetPage(ctx, client, b, diskRelay{Input: inputs[0]})
	if e != nil {
		return 0, e
	}
	batch := newID()
	rows := []diskRelay{}
	seen, names := map[string]bool{}, map[string]bool{}
	var total int64
	for _, in := range inputs {
		if in.SourceAccountID != a.ID || in.TargetAccountID != b.ID || in.ParentID != inputs[0].ParentID || in.DestinationID != inputs[0].DestinationID || seen[in.EntryID] || names[strings.ToLower(in.Name)] {
			return 0, errors.New("中转批次来源/目标不一致或重复")
		}
		seen[in.EntryID] = true
		names[strings.ToLower(in.Name)] = true
		total += in.Size
		var src CloudEntry
		for _, r := range source.Entries {
			if r.ID == in.EntryID && r.Name == in.Name && !r.Folder && r.Size != nil && *r.Size == in.Size {
				src = r
			}
		}
		tag := relaySourceVersion(a.Kind, src)
		if snapshot {
			if !relaySnapshotEligible(a.Kind, src) || total > relaySnapshotMaxBatch {
				return 0, errors.New("原件缺少可靠身份、大小或必要版本，或超过单文件 2 GiB／整批 4 GiB 限制，整批未创建")
			}
			tag = src.revision
		} else if relayUsesOriginalPin(a.Kind) {
			tag = relayPinnedVersion(a.Kind, a.ID, src, originals)
		}
		if src.ID == "" || !snapshot && !validRelayVersion(a.Kind, tag) {
			return 0, errors.New("源文件已变化或缺少版本依据（DAV强ETag / 123内容MD5 / 百度ID与原始MD5 / 夸克光鸭本机可信原件SHA256），整批未创建")
		}
		for _, r := range target.Entries {
			if strings.EqualFold(r.Name, in.Name) {
				return 0, errors.New("目标有同名项目，整批未创建，不覆盖")
			}
		}
		if relayProvider(a) && relayProvider(b) {
			tu, e := relayURL(b, path.Join(in.DestinationID, in.Name))
			if e != nil {
				return 0, e
			}
			su, e := relayURL(a, in.EntryID)
			if e != nil {
				return 0, e
			}
			if su == tu {
				return 0, errors.New("源与目标指向同一资源")
			}
		}
		rows = append(rows, diskRelay{SourceKind: a.Kind, TargetKind: b.Kind, Input: in, SourceScope: downloadScope(a), TargetScope: downloadScope(b), ETag: tag, Batch: batch, Job: RelayJob{ID: newID(), SourceAccountID: a.ID, TargetAccountID: b.ID, SourceName: a.Name, TargetName: b.Name, Name: in.Name, Destination: in.DestinationID, Size: in.Size, State: "queued", Message: "等待本机中转复制，源文件保留", Created: time.Now()}})
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
		return 0, errors.New("预检已停止、取消或账号来源变化，未创建队列")
	}
	if len(m.relays)+len(rows) > 1000 {
		return 0, errors.New("中转记录已达1000条，请清理可清理记录")
	}
	for _, old := range m.relays {
		total += old.Input.Size
		for _, d := range rows {
			if relayKey(d) == relayKey(old) {
				return 0, errors.New("已有同源同目标记录，请先核对原结果，勿重复创建")
			}
		}
	}
	if total > relayMaxTotal {
		return 0, errors.New("中转记录合计最多32GiB，请先清理已完成记录及缓存")
	}
	n := len(m.relays)
	m.relays = append(m.relays, rows...)
	if e = m.saveLocked(); e != nil {
		m.relays = m.relays[:n]
		return 0, errors.New("队列保存失败，未启动中转")
	}
	m.dispatchRelaysLocked()
	return len(rows), nil
}
func (m *Manager) dispatchRelaysLocked() {
	if m.closing || m.relayActive != "" {
		return
	}
	for i := range m.relays {
		d := &m.relays[i]
		if d.Job.State != "queued" {
			continue
		}
		if !m.relayParentReadyLocked(*d) {
			d.Job.State = "paused"
			d.Job.Message = "先核对或完成父目录创建，再继续此项"
			continue
		}
		a, b := m.downloadAccountLocked(d.Input.SourceAccountID), m.downloadAccountLocked(d.Input.TargetAccountID)
		if !relayAccountPair(a, b, *d) {
			d.Job.State = "failed"
			d.Job.Message = "账号来源已变化，未提交"
			continue
		}
		if e := m.reserveShareLocked(a.ID); e != nil {
			d.Job.State = "paused"
			d.Job.Message = "源账号忙，请稍后继续"
			continue
		}
		if a.ID != b.ID {
			if e := m.reserveShareLocked(b.ID); e != nil {
				delete(m.renaming, a.ID)
				d.Job.State = "paused"
				d.Job.Message = "目标账号忙，请稍后继续"
				continue
			}
		}
		ctx, cancel := context.WithTimeout(withTransferLimits(context.Background(), m.limits), relayJobTimeout(d.Input.Size))
		m.relayActive = d.Job.ID
		m.relayCancel = cancel
		d.Job.State = "downloading"
		d.Job.Phase = "读取源文件"
		d.Job.Progress = 0
		d.Job.Message = "正在检查源版本与本机缓存"
		if m.saveLocked() != nil {
			d.Job.State = "paused"
			d.Job.Message = "启动记录保存失败，未发送数据"
			cancel()
			m.relayActive = ""
			m.relayCancel = nil
			delete(m.renaming, a.ID)
			delete(m.renaming, b.ID)
			for k := range m.relays {
				if m.relays[k].Job.State == "queued" {
					m.relays[k].Job.State = "paused"
					m.relays[k].Job.Message = "启动记录保存失败，后续已停止"
				}
			}
			return
		}
		copy := *d
		root := m.relayRoot()
		m.workers.Add(1)
		go func() { defer m.workers.Done(); m.runRelay(ctx, a, b, copy, root, false) }()
		return
	}
	_ = m.saveLocked()
}
func relayURL(a Account, id string) (string, error) {
	u, e := url.Parse(a.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !validBaiduPath(id) {
		return "", errors.New("中转WebDAV路径或来源无效")
	}
	u.Path = path.Join(u.Path, id)
	u.RawPath = ""
	return u.String(), nil
}
func relayRequest(ctx context.Context, client *http.Client, a Account, method, id string, body io.Reader, headers map[string]string, size int64) (*http.Response, error) {
	u, e := relayURL(a, id)
	if e != nil {
		return nil, e
	}
	if method == "MKCOL" {
		u += "/"
	}
	req, e := http.NewRequestWithContext(ctx, method, u, body)
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(a.Username, a.Secret)
	req.Header.Set("Accept-Encoding", "identity")
	for n, v := range headers {
		req.Header.Set(n, v)
	}
	if body != nil {
		req.ContentLength = size
	}
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("中转连接失败，请核对网络和登录")
	}
	return resp, nil
}
func relaySource(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	p, e := relaySourcePage(ctx, client, a, d.Input.ParentID)
	if e != nil {
		return e
	}
	for _, r := range p.Entries {
		versionMatches := relaySourceVersion(a.Kind, r) == d.ETag
		if relayUsesOriginalPin(a.Kind) {
			versionMatches = relayPinMetaMatches(a.Kind, r, d)
		}
		if r.ID == d.Input.EntryID && r.Name == d.Input.Name && !r.Folder && r.Size != nil && *r.Size == d.Input.Size && versionMatches {
			return nil
		}
	}
	return errors.New("源文件版本、大小或位置已变化")
}
func relayTargetPage(ctx context.Context, client *http.Client, b Account, d diskRelay) (FilePage, error) {
	parent := relayTargetParent(d)
	if relayTargetNeedsFileID(b.Kind) || b.Kind == "pan123" && (d.Folder || d.ParentJobID != "") {
		return relayDirectoryPage(ctx, client, b, parent)
	}
	return relaySourcePage(ctx, client, b, parent)
}

func relayTargetFree(ctx context.Context, client *http.Client, b Account, d diskRelay) error {
	p, e := relayTargetPage(ctx, client, b, d)
	if e != nil {
		return e
	}
	for _, r := range p.Entries {
		if strings.EqualFold(r.Name, d.Input.Name) {
			return errors.New("目标已有同名项目，未发送上传")
		}
	}
	return nil
}
func relaySafePath(root, id string) (string, error) {
	if !filepath.IsAbs(root) || !shareToken.MatchString(id) || id == "" {
		return "", errors.New("中转缓存路径无效")
	}
	dir := filepath.Join(root, id)
	for p := dir; ; p = filepath.Dir(p) {
		if e := relayNoLink(p); e != nil {
			return "", e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return dir, nil
}
func relayNoLink(p string) error {
	name, e := windows.UTF16PtrFromString(p)
	if e != nil {
		return e
	}
	attrs, e := windows.GetFileAttributes(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("中转缓存含链接或重解析点，已停止")
	}
	return nil
}
func relayOpenCache(file string) (*os.File, error) {
	name, e := windows.UTF16PtrFromString(file)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &info); e != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		return nil, errors.New("缓存不是独立常规文件，已停止")
	}
	return os.NewFile(uintptr(h), file), nil
}
func relayReadOwner(file string) ([]byte, error) {
	f, e := relayOpenCache(file)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 256))
}
func relayCache(root, id string) (string, error) {
	dir, e := relaySafePath(root, id)
	if e != nil {
		return "", e
	}
	owner := []byte("multipan-relay-v1:" + id)
	mark := filepath.Join(dir, ".owner")
	for _, p := range []string{mark, filepath.Join(dir, "data.bin")} {
		if e := relayNoLink(p); e != nil {
			return "", e
		}
	}
	if b, e := relayReadOwner(mark); e == nil {
		if string(b) != string(owner) {
			return "", errors.New("中转缓存归属不符")
		}
		return filepath.Join(dir, "data.bin"), nil
	}
	if rows, e := os.ReadDir(dir); e == nil && len(rows) > 0 {
		return "", errors.New("中转缓存目录含未知文件，未覆盖")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	f, e := os.OpenFile(mark, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	_, e = f.Write(owner)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return filepath.Join(dir, "data.bin"), e
}
func relayHash(ctx context.Context, r io.Reader, size int64, progress func(int64)) (string, error) {
	h := sha256.New()
	buf := make([]byte, 128<<10)
	var n int64
	reader := io.LimitReader(r, size+1)
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		count, e := reader.Read(buf)
		if count > 0 {
			n += int64(count)
			h.Write(buf[:count])
			if progress != nil {
				progress(n)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	if n != size {
		return "", errors.New("内容大小与原文件不符")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func relayDownload(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	if a.Kind == "guangya" {
		return relayGuangyaDownload(ctx, client, a, d, file, progress)
	}
	if a.Kind == "quark" {
		return relayQuarkDownload(ctx, client, a, d, file, progress)
	}
	if a.Kind == "baidu" {
		return relayBaiduDownload(ctx, client, a, d, file, progress)
	}
	if a.Kind == "pan123" {
		return relay123Download(ctx, client, a, d, file, progress)
	}
	resp, e := relayRequest(ctx, client, a, "GET", d.Input.EntryID, nil, map[string]string{"If-Match": d.ETag}, 0)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", statusError(resp.StatusCode)
	}
	if resp.Header.Get("ETag") != d.ETag || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return "", errors.New("源响应未确认原强ETag或返回压缩内容")
	}
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	hash, e := relayHash(ctx, io.TeeReader(resp.Body, f), d.Input.Size, progress)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return hash, e
}
func verifyRelay(ctx context.Context, client *http.Client, a, b Account, d diskRelay, stage ...func(string, int64)) error {
	finish := func() error {
		var progress func(int64)
		if relayUsesOriginalPin(a.Kind) && len(stage) > 0 {
			label := "复核" + filePinLabel(a.Kind) + "来源"
			stage[0](label, 0)
			progress = func(n int64) { stage[0](label, n) }
		}
		return relayFinalSource(ctx, client, a, d, progress)
	}
	if e := relaySource(ctx, client, a, d); e != nil {
		return e
	}
	if b.Kind == "baidu" {
		if e := relayBaiduReadback(ctx, client, b, d); e != nil {
			return e
		}
		return finish()
	}
	if b.Kind == "pan123" {
		if e := relay123Readback(ctx, client, b, d); e != nil {
			return e
		}
		return finish()
	}
	if relayTargetNeedsFileID(b.Kind) {
		if e := relayNativeTargetReadback(ctx, client, b, d); e != nil {
			return e
		}
		return finish()
	}
	resp, e := relayRequest(ctx, client, b, "GET", path.Join(d.Input.DestinationID, d.Input.Name), nil, nil, 0)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return statusError(resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return errors.New("目标返回压缩表示，未确认原字节")
	}
	hash, e := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if e != nil {
		return e
	}
	if hash != d.Job.SHA256 {
		return errors.New("目标完整SHA256与本机源副本不符")
	}
	return finish()
}
func (m *Manager) relayProgress(id, phase string, n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.relayByIDLocked(id); d != nil {
		d.Job.Phase = phase
		if n > d.Job.Size {
			n = d.Job.Size
		}
		d.Job.Progress = n
		if phase == "下载源文件" {
			d.Job.CacheBytes = n
		}
	}
}
func (m *Manager) runRelay(ctx context.Context, a, b Account, d diskRelay, root string, checkOnly bool) {
	client := NewHTTPUploader().Client
	var err error
	d, err = m.resolveRelayTarget(d)
	if err != nil {
		m.finishRelay(ctx, a, b, d, err)
		return
	}
	if d.Folder {
		err = m.runRelayDirectory(ctx, client, a, b, d, checkOnly)
		m.finishRelay(ctx, a, b, d, err)
		return
	}
	var spool *os.File
	defer func() {
		if spool != nil {
			spool.Close()
		}
	}()
	if !checkOnly {
		reusedCache := false
		err = m.relayDirectoryAncestors(ctx, client, a, d)
		if err == nil {
			err = m.relayTargetDirectoryAncestors(ctx, client, b, d)
		}
		if err == nil {
			err = relaySource(ctx, client, a, d)
		}
		var cache string
		if err == nil {
			cache, err = relayCache(root, d.Job.ID)
		}
		if err == nil {
			reused := false
			if d.Job.SHA256 != "" {
				f, e := relayOpenCache(cache)
				if e == nil {
					hash, e := relayHash(ctx, f, d.Input.Size, nil)
					f.Close()
					reused = e == nil && hash == d.Job.SHA256
					reusedCache = reused
				}
			}
			if !reused {
				if e := os.Remove(cache); e != nil && !os.IsNotExist(e) {
					err = e
				} else {
					err = relayCacheSpace(root, d.Input.Size)
					if err == nil {
						d.Job.SHA256, err = relayDownload(ctx, client, a, d, cache, func(n int64) { m.relayProgress(d.Job.ID, "下载源文件", n) })
					}
				}
			}
		}
		if err == nil {
			spool, err = relayOpenCache(cache)
			if err == nil {
				var digest string
				digest, err = relayHash(ctx, spool, d.Input.Size, nil)
				if err == nil && digest != d.Job.SHA256 {
					err = errors.New("本机副本已变化，未发送上传")
				}
				if err == nil {
					_, err = spool.Seek(0, io.SeekStart)
				}
			}
		}
		if err == nil {
			err = relaySource(ctx, client, a, d)
		}
		if err == nil && relayUsesOriginalPin(a.Kind) && reusedCache {
			_, err = relayPinnedContent(ctx, client, a, d, io.Discard, nil)
		}
		if err == nil {
			err = relayTargetFree(ctx, client, b, d)
		}
		if err == nil {
			err = m.relayDirectoryAncestors(ctx, client, a, d)
		}
		if err == nil {
			err = m.relayTargetDirectoryAncestors(ctx, client, b, d)
		}
		if err == nil {
			m.mu.Lock()
			op := m.relayByIDLocked(d.Job.ID)
			if m.closing || ctx.Err() != nil || op == nil || !relayAccountPair(m.downloadAccountLocked(a.ID), m.downloadAccountLocked(b.ID), d) {
				err = errors.New("操作取消或来源变化")
			} else {
				op.Job.SHA256 = d.Job.SHA256
				op.Job.CacheBytes = d.Input.Size
				op.Job.State = "ready"
				op.Job.Message = "本机副本完整，源版本已核对"
				if m.saveLocked() != nil {
					err = errors.New("源副本记录保存失败，未上传")
				} else {
					op.Submitted = true
					op.Job.State = "uploading"
					op.Job.Message = "写前检查点已保存，目标结果待核对"
					if m.saveLocked() != nil {
						op.Submitted = false
						err = errors.New("上传检查点保存失败，未提交目标")
					} else {
						targetParent := d.targetParent
						d = *op
						d.targetParent = targetParent
					}
				}
			}
			m.mu.Unlock()
		}
		if err == nil {
			if b.Kind == "baidu" {
				err = relayBaiduUpload(ctx, client, b, d, spool, func(n int64) { m.relayProgress(d.Job.ID, "上传目标", n) })
			} else if b.Kind == "pan123" {
				err = relay123Upload(ctx, client, b, d, spool, func(n int64) { m.relayProgress(d.Job.ID, "上传目标", n) })
			} else if relayTargetNeedsFileID(b.Kind) {
				err = m.relayNativeTargetUpload(ctx, client, b, &d, spool, func(n int64) { m.relayProgress(d.Job.ID, "上传目标", n) })
			} else {
				resp, e := relayRequest(ctx, client, b, "PUT", path.Join(d.Input.DestinationID, d.Input.Name), &progressReader{r: spool, fn: func(n int64) { m.relayProgress(d.Job.ID, "上传目标", n) }}, map[string]string{"If-None-Match": "*", "Content-Type": "application/octet-stream"}, d.Input.Size)
				if resp != nil {
					resp.Body.Close()
					if resp.StatusCode != 201 {
						e = statusError(resp.StatusCode)
					}
				}
				err = e
			}
			// Native file ID targets require a saved upload result ID.
			// Other providers can verify an ambiguous upload by reading back the target.
			if ctx.Err() == nil && (!relayTargetNeedsFileID(b.Kind) || err == nil) {
				m.relayProgress(d.Job.ID, "回读目标校验", 0)
				err = verifyRelay(ctx, client, a, b, d, func(phase string, n int64) { m.relayProgress(d.Job.ID, phase, n) })
			}
		}
	} else {
		err = verifyRelay(ctx, client, a, b, d, func(phase string, n int64) { m.relayProgress(d.Job.ID, phase, n) })
	}
	if err == nil {
		err = m.relayDirectoryAncestors(ctx, client, a, d)
	}
	if err == nil {
		err = m.relayTargetDirectoryAncestors(ctx, client, b, d)
	}
	m.finishRelay(ctx, a, b, d, err)
}
func (m *Manager) finishRelay(ctx context.Context, a, b Account, d diskRelay, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.relayByIDLocked(d.Job.ID)
	wasCancelled := errors.Is(ctx.Err(), context.Canceled)
	if m.relayCancel != nil {
		m.relayCancel()
	}
	m.relayCancel = nil
	m.relayActive = ""
	delete(m.renaming, a.ID)
	delete(m.renaming, b.ID)
	if op != nil {
		if err == nil {
			op.Job.State = "verified"
			op.Job.Message = "目标完整SHA256与源副本一致，源版本仍匹配；源文件保留"
			if op.Folder {
				op.Job.Message = "目录创建已受理并回读确认；子项请分别查看，源目录保留"
			}
			op.Job.Progress = op.Input.Size
		} else if op.Submitted {
			op.Job.State = "unknown"
			op.Job.Message = "目标结果待核对，只读检查不重新上传：" + err.Error()
			if b.Kind == "pan123" {
				op.Job.Message += "；123可能有改名或未完成上传项目，请在目标盘核对，不自动删除"
			}
			if b.Kind == "baidu" {
				op.Job.Message += "；百度可能保留未完成上传项目，请在目标盘核对，不自动重传或删除"
			}
		} else if wasCancelled || m.closing {
			op.Job.State = "paused"
			op.Job.Message = "已暂停，未提交目标上传"
		} else {
			op.Job.State = "failed"
			op.Job.Message = "未提交目标上传：" + err.Error()
		}
		op.Job.Phase = ""
		if op.Job.State != "verified" {
			for i := range m.relays {
				r := &m.relays[i]
				if r.Batch == op.Batch && r.Job.State == "queued" {
					r.Job.State = "paused"
					r.Job.Message = "前项未确认，后续尚未开始"
				}
			}
		}
		if m.saveLocked() != nil {
			if op.Submitted {
				op.Job.State = "unknown"
			}
			op.Job.Message = "最终中转记录保存失败，请勿重复创建或上传"
			for i := range m.relays {
				if m.relays[i].Job.State == "queued" {
					m.relays[i].Job.State = "paused"
					m.relays[i].Job.Message = "记录保存失败，已停止后续"
				}
			}
			return
		}
	}
	m.dispatchLocked()
	m.dispatchRelaysLocked()
}
func (m *Manager) ResumeRelay(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.relayByIDLocked(id)
	if m.closing || d == nil || !m.relayParentReadyLocked(*d) || d.Submitted || d.Job.State != "paused" && d.Job.State != "failed" {
		return errors.New("此任务不能继续；已提交任务只检查原结果")
	}
	if !relayAccountPair(m.downloadAccountLocked(d.Input.SourceAccountID), m.downloadAccountLocked(d.Input.TargetAccountID), *d) {
		return errors.New("账号来源变化，不能继续")
	}
	old := d.Job
	d.Job.State = "queued"
	d.Job.Message = "等待继续，先核对源版本"
	if e := m.saveLocked(); e != nil {
		d.Job = old
		return e
	}
	m.dispatchRelaysLocked()
	return nil
}
func (m *Manager) PauseRelays() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return errors.New("程序正在退出")
	}
	m.relayPauseVersion++
	for i := range m.relays {
		if m.relays[i].Job.State == "queued" {
			m.relays[i].Job.State = "paused"
			m.relays[i].Job.Message = "用户停止后续，尚未开始"
		}
	}
	if m.relayCancel != nil {
		m.relayCancel()
	}
	if m.relaySnapshotCancel != nil {
		m.relaySnapshotCancel()
	}
	return m.saveLocked()
}
func (m *Manager) CheckRelay(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.relayByIDLocked(id)
	if m.closing || m.relayActive != "" || d == nil || !d.Submitted || d.Job.State != "unknown" {
		return errors.New("暂不能核对，请等待当前中转任务返回")
	}
	a, b := m.downloadAccountLocked(d.Input.SourceAccountID), m.downloadAccountLocked(d.Input.TargetAccountID)
	if !relayAccountPair(a, b, *d) {
		return errors.New("账号来源已变化")
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		return e
	}
	if a.ID != b.ID {
		if e := m.reserveShareLocked(b.ID); e != nil {
			delete(m.renaming, a.ID)
			return e
		}
	}
	ctx, cancel := context.WithTimeout(withTransferLimits(context.Background(), m.limits), relayJobTimeout(d.Input.Size))
	m.relayActive = id
	m.relayCancel = cancel
	d.Job.Phase = "只读核对"
	copy := *d
	root := m.relayRoot()
	m.workers.Add(1)
	go func() { defer m.workers.Done(); m.runRelay(ctx, a, b, copy, root, true) }()
	return nil
}
func (m *Manager) ClearRelay(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.relayByIDLocked(id)
	if m.closing || d == nil || m.relayHasChildrenLocked(id) || m.relayActive == id || d.Job.State != "verified" && d.Job.State != "moved" && (d.Submitted || d.Job.State != "paused" && d.Job.State != "failed") {
		return errors.New("运行中、排队中或待核对任务不能清理")
	}
	root := m.relayRoot()
	dir, e := relaySafePath(root, id)
	if e != nil {
		return e
	}
	for _, name := range []string{".owner", "data.bin"} {
		if e := relayNoLink(filepath.Join(dir, name)); e != nil {
			return e
		}
	}
	if b, e := relayReadOwner(filepath.Join(dir, ".owner")); e == nil {
		if string(b) != "multipan-relay-v1:"+id {
			return errors.New("缓存归属不符，未清理")
		}
		for _, name := range []string{"data.bin", ".owner"} {
			p := filepath.Join(dir, name)
			info, e := os.Lstat(p)
			if e == nil && info.Mode()&os.ModeSymlink != 0 {
				return errors.New("缓存含链接，未清理")
			}
			if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		_ = os.Remove(dir)
	} else if !os.IsNotExist(e) {
		return e
	}
	before := append([]diskRelay{}, m.relays...)
	rows := []diskRelay{}
	for _, r := range m.relays {
		if r.Job.ID != id {
			rows = append(rows, r)
		}
	}
	m.relays = rows
	if e = m.saveLocked(); e != nil {
		m.relays = before
		return errors.New("清理记录保存失败，记录已保留；已清理的本机缓存可在未提交任务继续时重下")
	}
	return nil
}
