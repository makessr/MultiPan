package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	renewLoopCancel     context.CancelFunc
	renewCancel         context.CancelFunc
	renewingID          string
	accountCheck        *accountCheck
	relays              []diskRelay
	relayActive         string
	relayCancel         context.CancelFunc
	relaySnapshotCancel context.CancelFunc
	relayPreflight      *RelayPreflight
	relayPauseVersion   uint64
	workers             sync.WaitGroup
	settings            TransferSettings
	downloads           []DownloadJob
	downloadActive      map[string]context.CancelFunc
	downloadImmediate   bool // Legacy immediate downloads remain exclusive with queued workers.
	shares              map[string]*shareOperation
	shareRevokes        map[string]*shareRevocation
	recycleRestores     map[string]*recycleRestore
	limits              *transferLimits
	directoryDownloadMu sync.Mutex
	directoryRetry      *directoryRetryState
	mu                  sync.Mutex
	folderMutationMu    sync.Mutex
	renaming            map[string]bool
	accounts            []Account
	tasks               []Task
	path                string
	uploader            Uploader
	active              map[string]context.CancelFunc
	closing             bool
	copyCancel          context.CancelFunc
	activeCopy          *copyOperation
	copies              map[string]*copyOperation
	trashes             map[string]*trashOperation
	trashCancel         context.CancelFunc
	recyclePurges       map[string]*recyclePurgeOperation
	purgeCancel         context.CancelFunc
	recycleEmpty        *recycleEmptySnapshot // Process-local, single-use preview; never resumes after restart.
	search              *cloudSearch
	storageError        string
	fingerprints        []UploadFingerprint
}

func NewManager(path string, u Uploader) (*Manager, error) {
	s, err := readState(path)
	if err != nil {
		return nil, err
	}
	if s.Schema < 1 {
		if raw, e := os.ReadFile(path); e == nil {
			backup, e := os.OpenFile(path+".before-0.46.bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e == nil {
				_, e = backup.Write(raw)
				if e == nil {
					e = backup.Sync()
				}
				closeErr := backup.Close()
				if e != nil || closeErr != nil {
					_ = os.Remove(path + ".before-0.46.bak")
					return nil, errors.New("无法备份旧版任务数据，请检查磁盘")
				}
			} else if !os.IsExist(e) {
				return nil, errors.New("无法备份旧版任务数据，请检查权限")
			}
		}
	}
	m := &Manager{path: path, uploader: u, active: map[string]context.CancelFunc{}, accounts: []Account{}, tasks: s.Tasks}
	m.settings = normalizeSettings(s.Settings)
	m.limits = newTransferLimits(m.settings)
	m.downloadActive = map[string]context.CancelFunc{}
	m.downloads = s.Downloads
	for i := range m.downloads {
		if m.downloads[i].Status == "running" || m.downloads[i].Status == "queued" {
			m.downloads[i].Status = "paused"
		}
	}
	m.fingerprints = s.Fingerprints
	for _, d := range s.Accounts {
		a := d.Account
		a.Secret = d.Secret
		m.accounts = append(m.accounts, a)
	}
	for i := range m.tasks {
		if m.tasks[i].Status == "running" || m.tasks[i].Status == "queued" {
			m.tasks[i].Status = "paused"
			m.tasks[i].Message = "程序已退出；继续时尝试恢复已确认分片，不支持的网盘从头上传"
		}
	}
	if err := m.restoreShares(s.Shares); err != nil {
		return nil, err
	}
	if err := m.restoreShareRevocations(s.ShareRevokes); err != nil {
		return nil, err
	}
	if err := m.restoreRecycleReceipts(s.RecycleRestores); err != nil {
		return nil, err
	}
	if err := m.restoreCopies(s.Copies); err != nil {
		return nil, err
	}
	if err := m.restoreTrash(s.Trashes); err != nil {
		return nil, err
	}
	if err := m.restoreRecyclePurges(s.RecyclePurges); err != nil {
		return nil, err
	}
	if err := m.restoreRelays(s.Relays); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Accounts: append([]Account{}, m.accounts...), Tasks: append([]Task{}, m.tasks...), StorageError: m.storageError, Settings: m.settings, Downloads: append([]DownloadJob{}, m.downloads...)}
	for i := range s.Accounts {
		s.Accounts[i].Secret = ""
	}
	return s
}
func (m *Manager) saveLocked() error {
	s := diskState{Schema: 1, Tasks: m.tasks, Fingerprints: m.fingerprints, Settings: m.settings, Downloads: m.downloads, Relays: m.relays}
	for _, op := range m.recyclePurges {
		s.RecyclePurges = append(s.RecyclePurges, op.saved)
	}
	for _, op := range m.trashes {
		s.Trashes = append(s.Trashes, op.saved)
	}
	for _, op := range m.copies {
		s.Copies = append(s.Copies, op.saved)
	}
	for _, op := range m.recycleRestores {
		s.RecycleRestores = append(s.RecycleRestores, op.saved)
	}
	for _, op := range m.shareRevokes {
		s.ShareRevokes = append(s.ShareRevokes, op.saved)
	}
	for _, op := range m.shares {
		if op.saved.Result.ID != "" {
			s.Shares = append(s.Shares, op.saved)
		}
	}
	for _, a := range m.accounts {
		d := diskAccount{Account: a, Secret: a.Secret}
		s.Accounts = append(s.Accounts, d)
	}
	err := writeState(m.path, s)
	if err != nil {
		m.storageError = "无法保存任务，请检查磁盘空间和权限"
	} else {
		m.storageError = ""
	}
	return err
}
func validateAccount(a Account) error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("请填写账号名称")
	}
	if a.Kind != "webdav" && a.Kind != "lanzou" && a.Kind != "quark" && a.Kind != "pan123" && a.Kind != "baidu" && a.Kind != "ctfile" && a.Kind != "ctdav" && a.Kind != "guangya" && a.Kind != "ctweb" {
		return errors.New("暂不支持该网盘")
	}
	if a.Secret == "" {
		return errors.New("请填写密码或 Cookie")
	}
	if strings.ContainsAny(a.Secret, "\r\n") {
		return errors.New("凭据不能包含换行")
	}
	if a.Kind == "ctweb" {
		if len(strings.TrimSpace(a.Secret)) < 24 {
			return errors.New("诚通网页版登录令牌无效，请重新登录")
		}
		if a.Endpoint != "" && a.Endpoint != CtWebAPI {
			return errors.New("诚通网页版接口地址无效")
		}
		if _, err := strconv.ParseInt(a.Folder, 10, 64); err != nil && a.Folder != "" && !strings.HasPrefix(a.Folder, "d") {
			return errors.New("诚通目录 ID 须为数字或以 d 开头，根目录留空")
		}
	}
	if a.Kind == "webdav" || a.Kind == "ctdav" {
		u, err := url.Parse(a.Endpoint)
		if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("WebDAV 地址须为不含凭据或查询参数的 HTTPS 地址")
		}
		if strings.Contains(a.Folder, "\\") {
			return errors.New("网盘目录请使用 / 分隔")
		}
		for _, p := range strings.Split(a.Folder, "/") {
			if p == ".." {
				return errors.New("目录不能包含 ..")
			}
		}
	}
	if a.Kind == "lanzou" {
		if !strings.Contains(a.Secret, "phpdisk_info=") {
			return errors.New("Cookie 中缺少 phpdisk_info，请从已登录的蓝奏云网页导入")
		}
		if _, err := strconv.ParseInt(a.Folder, 10, 64); err != nil {
			return errors.New("蓝奏云目录 ID 须为整数，根目录为 -1")
		}
	}
	if a.Kind == "quark" {
		if !strings.Contains(a.Secret, "__puus=") {
			return errors.New("夸克登录信息不完整，请重新登录或导入完整 Cookie")
		}
		if !validQuarkFolder(a.Folder) {
			return errors.New("夸克目录 ID 无效，根目录为 0")
		}
	}
	if a.Kind == "pan123" {
		if !valid123Folder(a.Folder) {
			return errors.New("123 目录 ID 无效，根目录为 0")
		}
		token := pan123Token(a.Secret)
		if len(token) < 16 || len(token) > 32768 || strings.ContainsAny(token, " \t\r\n") {
			return errors.New("123 登录令牌格式无效，请重新登录")
		}
	}
	if a.Kind == "baidu" {
		if baiduCookie(a.Secret, "BDUSS") == "" {
			return errors.New("百度登录信息缺少 BDUSS，请重新登录或导入完整 Cookie")
		}
		if !validBaiduPath(a.Folder) {
			return errors.New("百度保存目录须为绝对路径，根目录为 /")
		}
	}
	if a.Kind == "ctfile" {
		if !validCtFolder(a.Folder) {
			return errors.New("诚通目录 ID 无效，根目录为 d0")
		}
		if a.Username != "public" && a.Username != "private" {
			return errors.New("请选择诚通公有云或私有云")
		}
		if len(a.Secret) < 16 || len(a.Secret) > 32768 || strings.ContainsAny(a.Secret, " \t\r\n") {
			return errors.New("诚通会话格式无效，请重新登录")
		}
	}
	if a.Kind == "guangya" {
		if !validQuarkFolder(a.Folder) || len(a.Secret) < 16 || strings.ContainsAny(a.Secret, " \r\n\t") {
			return errors.New("光鸭登录信息或目录无效，请重新登录")
		}
	}
	return nil
}
func (m *Manager) SaveAccount(in AccountInput) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.renaming[in.ID] {
		return Account{}, errors.New("此账号正在修改网盘文件，请稍后保存")
	}
	a := Account{ID: in.ID, Name: strings.TrimSpace(in.Name), Kind: in.Kind, Endpoint: strings.TrimRight(strings.TrimSpace(in.Endpoint), "/"), Username: in.Username, Folder: in.Folder, UserAgent: in.UserAgent, Secret: strings.TrimSpace(in.Secret)}
	a.FolderDisplay = in.FolderDisplay
	if a.Kind == "guangya" {
		a.Endpoint = GuangyaAPI
		a.Secret = pan123Token(a.Secret)
		if a.Folder == "" {
			a.Folder = "root"
		}
		if a.Folder == "root" {
			a.FolderDisplay = "根目录"
		}
	}
	if a.Kind == "ctdav" {
		if a.Folder == "" {
			a.Folder = "/"
		}
		if !validBaiduPath(a.Folder) {
			return Account{}, errors.New("诚通WebDAV目录须为绝对路径")
		}
		if a.Folder == "/" {
			a.FolderDisplay = "根目录"
		}
	}
	if a.Kind == "ctfile" {
		a.Endpoint = CtFileAPI
		if a.Username == "" {
			a.Username = "public"
		}
		if a.Folder == "" || a.Folder == "0" {
			a.Folder = "d0"
		}
		if a.Folder == "d0" {
			a.FolderDisplay = "根目录"
		}
	}
	if a.Kind == "baidu" {
		a.Endpoint = BaiduAPI
		if a.Folder == "" {
			a.Folder = "/"
		}
		if a.Folder == "/" {
			a.FolderDisplay = "根目录"
		}
	}
	if a.Kind == "pan123" {
		a.Endpoint = Pan123API
		a.Secret = pan123Token(a.Secret)
		if a.Folder == "" {
			a.Folder = "0"
		}
		if a.Folder == "0" {
			a.FolderDisplay = "根目录"
		}
	}
	if a.Kind == "quark" {
		a.Endpoint = QuarkAPI
		if a.Folder == "" {
			a.Folder = "0"
		}
		if a.Folder == "0" {
			a.FolderDisplay = "根目录"
		}
	}
	index := -1
	for i, old := range m.accounts {
		if old.ID == a.ID {
			index = i
			if a.Secret == "" {
				a.Secret = old.Secret
			}
			if a.Folder == old.Folder && a.FolderDisplay == "" {
				a.FolderDisplay = old.FolderDisplay
			}
		}
	}
	if a.ID != "" && index < 0 {
		return Account{}, errors.New("账号不存在")
	}
	if a.Kind == "lanzou" {
		a.Endpoint = "https://up.woozooo.com"
		if a.Folder == "" {
			a.Folder = "-1"
		}
		if a.Folder == "-1" {
			a.FolderDisplay = "根目录"
		}
	}
	if err := validateAccount(a); err != nil {
		return Account{}, err
	}
	for _, t := range m.tasks {
		if t.AccountID == a.ID && (t.Status == "running" || t.Status == "queued") {
			return Account{}, errors.New("请先停止该账号的任务再修改配置")
		}
	}
	before := append([]Account{}, m.accounts...)
	previousFingerprints := m.fingerprints
	if index >= 0 {
		old := m.accounts[index]
		if old.Kind != a.Kind || old.Endpoint != a.Endpoint || old.Username != a.Username || old.Secret != a.Secret || downloadScope(old) != downloadScope(a) {
			m.dropFingerprintsLocked(a.ID, "")
		}
	}
	if index < 0 {
		a.ID = newID()
		m.accounts = append(m.accounts, a)
	} else {
		m.accounts[index] = a
	}
	if err := m.saveLocked(); err != nil {
		m.accounts = before
		m.fingerprints = previousFingerprints
		return Account{}, errors.New("账号保存失败，请检查数据目录")
	}
	a.Secret = ""
	return a, nil
}
func (m *Manager) DeleteAccount(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.renaming[id] {
		return errors.New("此账号正在修改网盘文件，请稍后移除")
	}
	for _, t := range m.tasks {
		if t.AccountID == id && (t.Status == "queued" || t.Status == "running") {
			return errors.New("请先停止该账号的任务")
		}
	}
	before := m.accounts
	m.accounts = []Account{}
	found := false
	for _, a := range before {
		if a.ID != id {
			m.accounts = append(m.accounts, a)
		} else {
			found = true
		}
	}
	if !found {
		return errors.New("账号不存在")
	}
	previousFingerprints := m.fingerprints
	m.dropFingerprintsLocked(id, "")
	if err := m.saveLocked(); err != nil {
		m.accounts = before
		m.fingerprints = previousFingerprints
		return err
	}
	return nil
}
func (m *Manager) Enqueue(files []File, targets []Target) (int, error) {
	return m.enqueueUploadPlan(files, targets, nil, "")
}
func (m *Manager) EnqueueReviewedUploads(files []File, targets []Target, skips []UploadSkip, stamp string) (int, error) {
	if stamp == "" {
		return 0, errors.New("请先检查上传计划")
	}
	return m.enqueueUploadPlan(files, targets, skips, stamp)
}
func (m *Manager) enqueueUploadPlan(files []File, targets []Target, skips []UploadSkip, stamp string) (int, error) {
	if len(skips) > 1000 {
		return 0, errors.New("跳过项目超过单批上限")
	}
	if len(files) == 0 || len(targets) == 0 {
		return 0, errors.New("请选择文件和至少一个目标账号")
	}
	if len(files)*len(targets) > 1000 {
		return 0, errors.New("单批最多创建 1000 个任务")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	canonical, err := m.validateUploadPlanLocked(files, targets)
	if err != nil {
		return 0, err
	}
	files = canonical
	if stamp != "" && stamp != uploadPlanStamp(files, targets, m.accounts) {
		return 0, errors.New("上传计划或账号已变化，请重新检查后上传")
	}
	omit := map[[2]int]bool{}
	for _, skip := range skips {
		if skip.FileIndex < 0 || skip.FileIndex >= len(files) || skip.TargetIndex < 0 || skip.TargetIndex >= len(targets) {
			return 0, errors.New("跳过项目的标识无效")
		}
		omit[[2]int{skip.FileIndex, skip.TargetIndex}] = true
	}
	if len(omit) == len(files)*len(targets) {
		return 0, errors.New("没有需要上传的项目；请保留至少一项或取消")
	}
	batch := newID()
	before := len(m.tasks)
	for fi, f := range files {
		for ti, target := range targets {
			if omit[[2]int{fi, ti}] {
				continue
			}
			var a Account
			for _, v := range m.accounts {
				if v.ID == target.AccountID {
					a = v
				}
			}
			display := target.Folder
			if a.Kind == "ctfile" && target.Folder == "d0" {
				display = "根目录"
			}
			if a.Kind == "baidu" && target.Folder == "/" {
				display = "根目录"
			}
			if target.Folder == a.Folder && a.FolderDisplay != "" {
				display = a.FolderDisplay
			}
			if (a.Kind == "lanzou" && target.Folder == "-1") || ((a.Kind == "quark" || a.Kind == "pan123") && target.Folder == "0") {
				display = "根目录"
			}
			m.tasks = append(m.tasks, Task{ID: newID(), BatchID: batch, AccountID: a.ID, AccountName: a.Name, Kind: a.Kind, File: f, Folder: target.Folder, FolderDisplay: display, Status: "queued", Created: now(), Message: "等待上传"})
		}
	}
	if err := m.saveLocked(); err != nil {
		m.tasks = m.tasks[:before]
		return 0, errors.New("任务保存失败，未开始上传")
	}
	m.dispatchLocked()
	return len(m.tasks) - before, nil
}
func (m *Manager) dispatchLocked() {
	if m.closing {
		return
	}
	busy := map[string]bool{}
	for _, t := range m.tasks {
		if _, ok := m.active[t.ID]; ok {
			busy[t.AccountID] = true
		}
	}
	for i := range m.tasks {
		if len(m.active) >= normalizeSettings(m.settings).UploadConcurrency {
			return
		}
		t := &m.tasks[i]
		if t.Status != "queued" || busy[t.AccountID] || m.renaming[t.AccountID] {
			continue
		}
		var a Account
		for _, v := range m.accounts {
			if v.ID == t.AccountID {
				a = v
			}
		}
		ctx, cancel := context.WithCancel(withTransferLimits(context.Background(), m.limits))
		m.active[t.ID] = cancel
		busy[t.AccountID] = true
		t.Status = "running"
		t.LastAttempt = now()
		t.Message = "正在上传"
		copy := *t
		m.workers.Add(1)
		go func() { defer m.workers.Done(); m.run(ctx, copy, a) }()
	}
}
func (m *Manager) run(ctx context.Context, t Task, a Account) {
	ctx = withUploadResumePath(ctx, filepath.Join(filepath.Dir(m.path), "upload-checkpoints", t.ID+".enc"))
	var receipt uploadReceipt
	f, err := inspectUploadEntry(t.File)
	if err == nil && (f.Size != t.File.Size || f.Modified != t.File.Modified) {
		err = errors.New("源文件已修改，请重新选择")
	}
	if err == nil && t.File.RelativePath != "" {
		t.Folder, err = m.prepareUploadPath(ctx, a, t.Folder, t.File)
	}
	if err == nil && !t.File.Directory {
		progress := func(n int64) {
			m.mu.Lock()
			defer m.mu.Unlock()
			for i := range m.tasks {
				if m.tasks[i].ID == t.ID {
					m.tasks[i].Sent = n
					break
				}
			}
		}
		if uploader, ok := m.uploader.(interface {
			uploadRecorded(context.Context, Account, File, string, func(int64)) (uploadReceipt, error)
		}); ok {
			receipt, err = uploader.uploadRecorded(ctx, a, t.File, t.Folder, progress)
		} else {
			err = m.uploader.Upload(ctx, a, t.File, t.Folder, progress)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	wasCanceled := ctx.Err() != nil
	cancel := m.active[t.ID]
	delete(m.active, t.ID)
	if cancel != nil {
		cancel()
	}
	for i := range m.tasks {
		v := &m.tasks[i]
		if v.ID != t.ID {
			continue
		}
		if wasCanceled || v.Status == "paused" {
			v.Status = "paused"
			v.Message = "已停止；继续时尝试恢复分片，请先留意远端是否已产生文件"
		} else if err != nil {
			v.Status = "failed"
			v.Message = err.Error()
		} else {
			v.Status = "success"
			v.SourceSHA256 = receipt.SHA256
			if receipt.Fingerprint != nil {
				m.putFingerprintLocked(*receipt.Fingerprint)
			}
			v.Sent = v.File.Size
			v.Message = "上传完成"
			if receipt.SHA256 != "" && receipt.Fingerprint == nil {
				v.Message = "上传完成；原件摘要已记录，云端关联未确认"
			}
		}
	}
	m.saveLocked()
	m.dispatchLocked()
}
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.ID == id {
			if t.Status != "queued" && t.Status != "running" {
				return nil
			}
			t.Status = "paused"
			t.Message = "已停止；继续时尝试恢复分片，不支持的网盘从头上传"
			if c := m.active[id]; c != nil {
				c()
			}
			return m.saveLocked()
		}
	}
	return errors.New("任务不存在")
}
func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.ID != id {
			continue
		}
		if m.renaming[t.AccountID] {
			return errors.New("目标网盘正在修改网盘文件，请稍后操作")
		}
		if _, active := m.active[id]; active {
			return errors.New("任务正在停止，请稍后重试")
		}
		if t.Status != "paused" && t.Status != "failed" {
			return errors.New("该任务无需重试")
		}
		found := false
		for _, a := range m.accounts {
			if a.ID == t.AccountID {
				found = true
			}
		}
		if !found {
			return errors.New("目标账号已删除，请重新创建任务")
		}
		old := *t
		t.Status = "queued"
		t.Sent = 0
		t.Message = "等待重新上传"
		if err := m.saveLocked(); err != nil {
			*t = old
			return err
		}
		m.dispatchLocked()
		return nil
	}
	return errors.New("任务不存在")
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closing = true
	if m.renewLoopCancel != nil {
		m.renewLoopCancel()
	}
	if m.renewCancel != nil {
		m.renewCancel()
	}
	if m.accountCheck != nil && m.accountCheck.running {
		m.accountCheck.cancel()
	}
	if m.relayCancel != nil {
		m.relayCancel()
	}
	if m.relaySnapshotCancel != nil {
		m.relaySnapshotCancel()
	}
	for i := range m.relays {
		if m.relays[i].Job.State == "queued" {
			m.relays[i].Job.State = "paused"
			m.relays[i].Job.Message = "程序已退出；明确继续后处理"
		}
	}
	if m.purgeCancel != nil {
		m.purgeCancel()
	}
	if m.trashCancel != nil {
		m.trashCancel()
	}
	if m.search != nil && m.search.running {
		m.search.cancel()
	}
	if m.copyCancel != nil {
		m.copyCancel()
	}
	for _, op := range m.recycleRestores {
		if op.cancel != nil {
			op.cancel()
		}
	}
	for _, op := range m.shareRevokes {
		if op.cancel != nil {
			op.cancel()
		}
	}
	for _, op := range m.shares {
		if op.cancel != nil {
			op.cancel()
		}
	}
	for _, cancel := range m.downloadActive {
		cancel()
	}
	for i := range m.downloads {
		if m.downloads[i].Status == "queued" || m.downloads[i].Status == "running" {
			m.downloads[i].Status = "paused"
		}
	}
	for _, c := range m.active {
		c()
	}
	for i := range m.tasks {
		if m.tasks[i].Status == "running" || m.tasks[i].Status == "queued" {
			m.tasks[i].Status = "paused"
			m.tasks[i].Message = "程序已退出；继续时尝试恢复已确认分片，不支持的网盘从头上传"
		}
	}
	m.saveLocked()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

func (m *Manager) validateUploadPlanLocked(files []File, targets []Target) ([]File, error) {
	canonical := make([]File, 0, len(files))
	for _, f := range files {
		current, err := inspectUploadEntry(f)
		if err != nil || current.Size != f.Size || current.Modified != f.Modified {
			return nil, fmt.Errorf("源文件已变化或不可读取：%s，请重新选择", f.Name)
		}
		canonical = append(canonical, current)
	}
	files = canonical
	destinationNames := map[string]bool{}
	for _, f := range files {
		rel := f.RelativePath
		if rel == "" {
			rel = f.Name
		}
		key := strings.ToLower(rel)
		if destinationNames[key] {
			return nil, errors.New("多个本地项目映射到相同上传路径，请拆分或重命名后选择：" + rel)
		}
		destinationNames[key] = true
	}
	seen := map[string]bool{}
	for _, t := range targets {
		if m.renaming[t.AccountID] {
			return nil, errors.New("目标网盘正在修改网盘文件，请稍后开始上传")
		}
		found := false
		for _, a := range m.accounts {
			if a.ID == t.AccountID {
				copy := a
				copy.Folder = t.Folder
				if err := validateAccount(copy); err != nil {
					return nil, err
				}
				for _, f := range files {
					if f.RelativePath != "" {
						if a.Kind == "ctfile" {
							return nil, errors.New("诚通旧OpenAPI不支持目录上传，请改用WebDAV")
						}
						parts := strings.Split(f.RelativePath, "/")
						if !f.Directory {
							parts = parts[:len(parts)-1]
						}
						for _, part := range parts {
							if a.Kind == "lanzou" {
								if e := validateLanzouFolderName(part); e != nil {
									return nil, e
								}
							}
						}
					}
				}
				found = true
			}
		}
		if !found || seen[t.AccountID] {
			return nil, errors.New("目标账号不存在或重复")
		}
		seen[t.AccountID] = true
	}
	return files, nil
}
