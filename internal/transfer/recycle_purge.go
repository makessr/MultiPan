package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

type recyclePurgeSource struct {
	Purpose             string       `json:"purpose"`
	AccountID           string       `json:"accountId"`
	Kind                string       `json:"kind"`
	Scope               string       `json:"scope"`
	Entry               RecycleEntry `json:"entry"`
	FolderFileIDs       []string     `json:"folderFileIds,omitempty"`
	FolderInventoryHash string       `json:"folderInventoryHash,omitempty"`
}
type RecyclePurgePrepared struct {
	Name        string   `json:"name"`
	Handle      string   `json:"handle"`
	FolderFiles []string `json:"folderFiles,omitempty"`
}
type RecyclePurgeSelection struct {
	Page  int          `json:"page"`
	Entry RecycleEntry `json:"entry"`
}

const recyclePurgePageLimit = 50

type diskRecyclePurge struct {
	Source       recyclePurgeSource `json:"source"`
	Receipt      TrashReceipt       `json:"receipt"`
	Submitted    bool               `json:"submitted"`
	Acknowledged bool               `json:"acknowledged"`
	TaskID       string             `json:"taskId,omitempty"`
}
type recyclePurgeOperation struct {
	saved   diskRecyclePurge
	running bool
}

// Caller holds Manager.mu. Unknown opposite operations must be resolved before
// the same recycle record can be used for a contradictory mutation.
func (m *Manager) pendingRestoreLocked(scope, id string) bool {
	for _, op := range m.recycleRestores {
		if op.saved.Source.Scope == scope && op.saved.Source.Entry.ID == id && op.saved.Submitted && (op.running || op.saved.Result.State == "unknown") {
			return true
		}
	}
	return false
}
func (m *Manager) pendingPurgeLocked(scope, id string) bool {
	for _, op := range m.recyclePurges {
		source := op.saved.Source
		matches := source.Entry.ID == id || strings.HasPrefix(id, "file:") && slices.Contains(source.FolderFileIDs, strings.TrimPrefix(id, "file:"))
		if source.Scope == scope && matches && op.saved.Submitted && (op.running || op.saved.Receipt.State == "unknown") {
			return true
		}
	}
	return false
}

func recyclePurgeProvider(a Account) bool {
	return trashProviderV1(a) || a.Kind == "quark" && a.Endpoint == QuarkAPI || a.Kind == "guangya" && a.Endpoint == GuangyaAPI
}
func validRecyclePurgeSource(s recyclePurgeSource) bool {
	e := s.Entry
	if s.Kind == "lanzou" {
		if s.Purpose == lanzouListedFilePurgePurpose {
			row, ok := lanzouListedRestoreFile(e)
			return s.AccountID != "" && s.Scope != "" && ok && sameRecycleRestoreEntry(row, e) && validLanzouPurgeFileIDs(s)
		}
		if s.Purpose == lanzouListedEmptyFolderPurgePurpose {
			row, ok := lanzouListedRecycleEntry(e, "folder")
			return s.AccountID != "" && s.Scope != "" && ok && sameRecycleRestoreEntry(row, e) &&
				len(s.FolderFileIDs) == 0 && s.FolderInventoryHash == ""
		}
		if s.Purpose == lanzouListedFolderPurgePurpose {
			row, ok := lanzouListedRecycleEntry(e, "folder")
			return s.AccountID != "" && s.Scope != "" && ok && sameRecycleRestoreEntry(row, e) &&
				len(s.FolderFileIDs) != 0 && validLanzouPurgeFileIDs(s)
		}
		typesMatch := s.Purpose == lanzouPurgePurpose && e.Kind == "file" ||
			s.Purpose == lanzouFolderPurgePurpose && e.Kind == "folder"
		return s.AccountID != "" && s.Scope != "" && typesMatch && validLanzouRestoreEntry(e) && validLanzouPurgeFileIDs(s)
	}
	if len(s.FolderFileIDs) != 0 || s.FolderInventoryHash != "" {
		return false
	}
	if s.Purpose == guangyaEmptyPurpose {
		return s.AccountID != "" && s.Scope != "" && s.Kind == "guangya" && validQuarkFolder(e.ID) &&
			e == (RecycleEntry{ID: e.ID, Kind: "all", Name: guangyaEmptyName})
	}
	if s.Purpose == guangyaPurgePurpose {
		return s.AccountID != "" && s.Scope != "" && s.Kind == "guangya" &&
			validQuarkFolder(e.ID) && e.ID != "0" && e.ID != guangyaRoot && relayName(e.Name) &&
			(e.Kind == "file" || e.Kind == "folder") && e.FileID == "" && e.RestoreHandle == "" &&
			(e.Size == nil || *e.Size >= 0)
	}
	validID := valid123Folder(e.ID)
	if s.Kind == "quark" {
		validID = validQuarkFolder(e.ID)
	}
	return s.Purpose == "recycle-purge-v1" && s.AccountID != "" && s.Scope != "" && (s.Kind == "quark" || s.Kind == "pan123" || s.Kind == "baidu") && validID && e.ID != "0" && e.Name != "" && len(e.Name) <= 4096 && !strings.ContainsAny(e.Name, "/\\\x00\r\n") && (e.Kind == "file" || e.Kind == "folder") && e.RestoreHandle == "" && (e.Size == nil || *e.Size >= 0)
}
func purgeKey(s recyclePurgeSource) string { return s.Scope + "\x00" + s.Entry.ID }
func purgeAccount(a Account, s recyclePurgeSource) bool {
	return (recyclePurgeProvider(a) || lanzouRecyclePurgeAccount(a)) && a.ID == s.AccountID && a.Kind == s.Kind && downloadScope(a) == s.Scope
}
func decodeRecyclePurge(handle string) (recyclePurgeSource, error) {
	var s recyclePurgeSource
	if len(handle) > 32768 {
		return s, errors.New("永久删除标识无效，请刷新")
	}
	b, e := base64.RawURLEncoding.DecodeString(handle)
	if e == nil {
		b, e = unprotect(b)
	}
	if e != nil || json.Unmarshal(b, &s) != nil || !validRecyclePurgeSource(s) {
		return s, errors.New("永久删除标识无效，请刷新")
	}
	return s, nil
}

// A complete bounded scan is required to prove absence. Finding an exact ID
// proves presence early, but no failed or truncated list can prove removal.
func findRecyclePurge(ctx context.Context, client *http.Client, a Account, entry RecycleEntry) (bool, error) {
	seen := map[string]bool{}
	var expectedTotal *int64
	for page := 1; page <= recyclePurgePageLimit; page++ {
		p, e := listAccountRecycle(ctx, client, a, page)
		if e != nil {
			return false, e
		}
		if p.Incomplete {
			return false, errors.New("回收站列表与总数不符，核对不完整")
		}
		if page == 1 {
			expectedTotal = p.Total
		} else if (expectedTotal == nil) != (p.Total == nil) || expectedTotal != nil && p.Total != nil && *p.Total != *expectedTotal {
			return false, errors.New("回收站分页期间总数变化，请稍后核对")
		}
		if p.HasMore && len(p.Items) < 100 {
			return false, errors.New("回收站分页提前结束，核对不完整")
		}
		for _, r := range p.Items {
			if seen[r.ID] {
				return false, errors.New("回收站分页重复，核对不完整")
			}
			seen[r.ID] = true
			if r.ID == entry.ID {
				if !sameRecycleRestoreEntry(r, entry) {
					return true, errors.New("原回收站同ID记录已变化")
				}
				return true, nil
			}
		}
		if !p.HasMore {
			if expectedTotal != nil && int64(len(seen)) != *expectedTotal {
				return false, errors.New("回收站分页累计数量与总数不符，核对不完整")
			}
			return false, nil
		}
	}
	return false, errors.New("回收站核对超过5000项，结果不完整")
}

func (m *Manager) PrepareRecyclePurge(ctx context.Context, accountID string, page int, entries []RecycleEntry) ([]RecyclePurgePrepared, error) {
	selections := make([]RecyclePurgeSelection, len(entries))
	for i, entry := range entries {
		selections[i] = RecyclePurgeSelection{Page: page, Entry: entry}
	}
	return m.prepareRecyclePurge(ctx, accountID, selections, false)
}

func (m *Manager) PrepareRecyclePurgeAcrossPages(ctx context.Context, accountID string, selections []RecyclePurgeSelection) ([]RecyclePurgePrepared, error) {
	return m.prepareRecyclePurge(ctx, accountID, selections, true)
}

func (m *Manager) prepareRecyclePurge(ctx context.Context, accountID string, selections []RecyclePurgeSelection, acrossPages bool) ([]RecyclePurgePrepared, error) {
	if len(selections) < 1 || len(selections) > 50 {
		return nil, errors.New("每次永久删除请选择1至50项")
	}
	for _, selection := range selections {
		if acrossPages && (selection.Page < 1 || selection.Page > recyclePurgePageLimit) {
			return nil, errors.New("所选项目超出前 50 页核对范围，整批未提交")
		}
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(accountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || !recyclePurgeProvider(a) && (acrossPages || !lanzouRecyclePurgeAccount(a)) {
		return nil, errors.New("永久删除账号不可用")
	}
	client := NewHTTPUploader().Client
	pages := make(map[int]RecyclePage)
	listed := make(map[string]bool)
	var total *int64
	for _, selection := range selections {
		page := selection.Page
		if _, ok := pages[page]; ok {
			continue
		}
		var current RecyclePage
		var err error
		if page == 0 && a.Kind == "lanzou" {
			// Blue's verified coverage preview reads one webpage.
			current.Items, err = readLanzouRecycleRows(ctx, client, a)
		} else {
			current, err = listAccountRecycle(ctx, client, a, page)
		}
		if err != nil {
			return nil, err
		}
		if current.Incomplete {
			return nil, errors.New("回收站列表不完整，整批未提交")
		}
		if acrossPages {
			if len(pages) > 0 && ((total == nil) != (current.Total == nil) || total != nil && *total != *current.Total) {
				return nil, errors.New("回收站分页总数发生变化，整批未提交")
			}
			total = current.Total
			for _, row := range current.Items {
				if listed[row.ID] {
					return nil, errors.New("回收站跨页出现重复条目，整批未提交")
				}
				listed[row.ID] = true
			}
		}
		pages[page] = current
	}
	result := []RecyclePurgePrepared{}
	var lanzouMembers map[string]bool
	seen := map[string]bool{}
	folderChildren := map[string]bool{}
	for _, selection := range selections {
		entry := selection.Entry
		entry.RestoreHandle = ""
		s := recyclePurgeSource{Purpose: "recycle-purge-v1", AccountID: a.ID, Kind: a.Kind, Scope: downloadScope(a), Entry: entry}
		if a.Kind == "guangya" {
			s.Purpose = guangyaPurgePurpose
		}
		var inventory *lanzouFolderContents
		if a.Kind == "lanzou" {
			s.Purpose = lanzouPurgePurpose
			if entry.Kind == "folder" {
				s.Purpose = lanzouFolderPurgePurpose
			}
			m.mu.Lock()
			original, ok := m.lanzouRestoreOriginLocked(a, entry)
			if !ok && selection.Page == 0 {
				switch entry.Kind {
				case "file":
					original, ok = lanzouListedRestoreFile(entry)
					s.Purpose = lanzouListedFilePurgePurpose
				case "folder":
					original, ok = lanzouListedRecycleEntry(entry, "folder")
					s.Purpose = lanzouListedEmptyFolderPurgePurpose
				}
				ok = ok && !m.lanzouPendingMoveLocked(s.Scope, original)
			}
			if entry.Kind == "folder" {
				inventory = m.lanzouFolderInventoryLocked(a, entry.ID, entry.Name).LanzouContents
			}
			m.mu.Unlock()
			if !ok || !sameRecycleRestoreEntry(original, entry) {
				return nil, errors.New("蓝奏项目缺少可核对的来源，请重新预览清理范围；整批未提交")
			}
			if s.Purpose == lanzouListedFilePurgePurpose {
				if lanzouMembers == nil {
					var err error
					lanzouMembers, err = readLanzouPurgeFolderMembers(ctx, client, a, pages[0].Items, nil)
					if err != nil {
						return nil, err
					}
				}
				if lanzouMembers[entry.FileID] {
					return nil, errors.New("蓝奏原文件属于回收站文件夹，请到官网处理")
				}
			}
		}
		if !validRecyclePurgeSource(s) || seen[entry.ID] {
			return nil, errors.New("永久删除所选项目无效或重复；整批未提交")
		}
		seen[entry.ID] = true
		match := false
		for _, r := range pages[selection.Page].Items {
			if sameRecycleRestoreRow(r, entry) {
				match = true
				break
			}
		}
		if !match {
			return nil, errors.New("原回收站记录已变化，请刷新；整批未提交")
		}
		prepared := RecyclePurgePrepared{Name: entry.Name}
		if a.Kind == "lanzou" {
			_, contents, e := readLanzouPurgeForm(ctx, client, a, s, inventory, true)
			if e != nil {
				return nil, e
			}
			prepared.FolderFiles = contents.names
			s.FolderFileIDs = contents.ids
			s.FolderInventoryHash = contents.hash
			if s.Purpose == lanzouListedEmptyFolderPurgePurpose && len(contents.ids) != 0 {
				s.Purpose = lanzouListedFolderPurgePurpose
			}
			if !validRecyclePurgeSource(s) {
				return nil, errors.New("蓝奏清理清单无效，整批未提交")
			}
			for _, id := range contents.ids {
				key := "file:" + id
				if folderChildren[key] {
					return nil, errors.New("蓝奏文件夹清单包含重复文件；整批未提交")
				}
				folderChildren[key] = true
			}
		}
		b, _ := json.Marshal(s)
		b, e := protect(b)
		if e != nil {
			return nil, e
		}
		prepared.Handle = base64.RawURLEncoding.EncodeToString(b)
		result = append(result, prepared)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || !purgeAccount(m.downloadAccountLocked(a.ID), recyclePurgeSource{AccountID: a.ID, Kind: a.Kind, Scope: downloadScope(a)}) {
		return nil, errors.New("账号来源已变化，整批未提交")
	}
	for _, selection := range selections {
		entry := selection.Entry
		if m.pendingRestoreLocked(downloadScope(a), entry.ID) || m.pendingGuangyaEmptyLocked(downloadScope(a)) {
			return nil, errors.New("有待核对的还原或清空请求，整批未提交")
		}
		for _, op := range m.recyclePurges {
			if op.saved.Source.Scope == downloadScope(a) && op.saved.Source.Entry.ID == entry.ID {
				return nil, errors.New("所选项目已有永久删除记录，请先核对原结果；整批未提交")
			}
		}
	}
	for id := range folderChildren {
		if seen[id] || m.pendingRestoreLocked(downloadScope(a), id) || m.pendingPurgeLocked(downloadScope(a), id) {
			return nil, errors.New("所选文件夹包含已单选或有待核对操作的文件；整批未提交")
		}
	}
	return result, nil
}

func (m *Manager) restoreRecyclePurges(rows []diskRecyclePurge) error {
	m.recyclePurges = map[string]*recyclePurgeOperation{}
	seen := map[string]bool{}
	if len(rows) > 1000 {
		return errors.New("永久删除记录数量异常")
	}
	for _, d := range rows {
		r := d.Receipt
		if !validRecyclePurgeSource(d.Source) || r.ID == "" || r.Created.IsZero() || r.AccountID != d.Source.AccountID || r.Name != d.Source.Entry.Name || r.Original != d.Source.Entry.OriginalPath || seen[purgeKey(d.Source)] || m.recyclePurges[r.ID] != nil || d.Acknowledged && !d.Submitted {
			return errors.New("永久删除记录损坏或重复，未覆盖原数据")
		}
		if d.Source.Purpose == guangyaEmptyPurpose || d.Source.Purpose == guangyaPurgePurpose {
			if d.Acknowledged != validGuangyaRestoreTaskID(d.TaskID) || d.TaskID != "" && !d.Acknowledged {
				return errors.New("光鸭删除任务凭证与回执不一致")
			}
		} else if d.TaskID != "" {
			return errors.New("永久删除任务凭证与网盘不一致")
		}
		seen[purgeKey(d.Source)] = true
		switch r.State {
		case "working", "unknown":
			if d.Submitted {
				r.State = "unknown"
				r.Message = "上次永久删除结果待核对，只检查原结果，不会再次发送"
			} else {
				r.State = "not_sent"
				r.Message = "上次未发送永久删除请求"
			}
		case "confirmed":
			if !d.Submitted || !d.Acknowledged {
				return errors.New("永久删除确认状态矛盾")
			}
		case "not_sent":
			if d.Submitted {
				return errors.New("永久删除未提交状态矛盾")
			}
		default:
			return errors.New("永久删除记录状态无效")
		}
		r.CanCheck = false
		d.Receipt = r
		m.recyclePurges[r.ID] = &recyclePurgeOperation{saved: d}
	}
	return nil
}

// Sends one native recycle-only operation, never a live-file delete or clear-all.
func submitRecyclePurge(ctx context.Context, client *http.Client, a Account, entry RecycleEntry) error {
	switch a.Kind {
	case "quark":
		_, e := newQuarkClient(client, a).request(ctx, "POST", "/file/recycle/remove", nil, map[string]any{"select_mode": 2, "record_list": []string{entry.ID}}, nil)
		return e
	case "pan123":
		return (pan123Client{client, a}).request(ctx, "POST", "/file/delete", nil, map[string]any{"fileIdList": []map[string]any{{"fileId": json.Number(entry.ID)}}, "event": "recycleDelete", "operatePlace": 1, "RequestSource": nil}, nil)
	case "baidu":
		c := newBaiduClient(client, a)
		if e := c.session(ctx); e != nil {
			return e
		}
		return c.api(ctx, "POST", "recycle/delete", url.Values{"async": {"1"}}, url.Values{"fidlist": {"[" + entry.ID + "]"}}, nil)
	}
	return errors.New("不支持的回收站操作")
}
func checkRecyclePurge(ctx context.Context, client *http.Client, a Account, d diskRecyclePurge) (string, string) {
	if d.Source.Kind == "lanzou" {
		return checkLanzouPurge(ctx, client, a, d)
	}
	if d.Source.Purpose == guangyaEmptyPurpose {
		return checkGuangyaEmpty(ctx, client, a, d)
	}
	if d.Source.Purpose == guangyaPurgePurpose {
		return checkGuangyaPurge(ctx, client, a, d)
	}
	present, e := findRecyclePurge(ctx, client, a, d.Source.Entry)
	if e != nil {
		return "unknown", "原结果待核对：" + e.Error()
	}
	if present {
		return "unknown", "原记录仍在回收站，请稍后检查；不会再次提交"
	}
	if !d.Acknowledged {
		return "unknown", "回收站已无原记录，但未保存服务端受理回执，无法确认永久删除是否执行；请到官网核对，不要重发"
	}
	return "confirmed", "已核对服务端受理回执且回收站原记录已移除；不代表底层内容擦除证明"
}
func (m *Manager) PurgeRecycle(ctx context.Context, handle, confirmation string) (TrashReceipt, error) {
	if confirmation != "永久删除" && confirmation != "清空回收站" {
		return TrashReceipt{}, errors.New("请输入“永久删除”并明确确认不可还原后再提交")
	}
	s, e := decodeRecyclePurge(handle)
	if e != nil {
		return TrashReceipt{}, e
	}
	nativeEmpty := s.Purpose == guangyaEmptyPurpose
	if nativeEmpty != (confirmation == "清空回收站") {
		return TrashReceipt{}, errors.New("确认文字与回收站操作不匹配")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(s.AccountID)
	if m.closing || !purgeAccount(a, s) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("账号来源已变化或不可用")
	}
	var lanzouInventory *lanzouFolderContents
	if s.Kind == "lanzou" {
		if s.Purpose == lanzouListedFilePurgePurpose || s.Purpose == lanzouListedEmptyFolderPurgePurpose || s.Purpose == lanzouListedFolderPurgePurpose {
			if m.lanzouPendingMoveLocked(s.Scope, s.Entry) {
				m.mu.Unlock()
				return TrashReceipt{}, errors.New("蓝奏项目有待核对的移入操作，未提交永久删除")
			}
		} else if original, ok := m.lanzouRestoreOriginLocked(a, s.Entry); !ok || !sameRecycleRestoreEntry(original, s.Entry) {
			m.mu.Unlock()
			return TrashReceipt{}, errors.New("蓝奏原项目移入记录已变化，未提交永久删除")
		}
		if s.Entry.Kind == "folder" {
			lanzouInventory = m.lanzouFolderInventoryLocked(a, s.Entry.ID, s.Entry.Name).LanzouContents
		}
		if s.Purpose == lanzouFolderPurgePurpose && len(s.FolderFileIDs) != 0 &&
			(lanzouInventory == nil || lanzouPurgeInventoryHash(lanzouInventory) != s.FolderInventoryHash) {
			m.mu.Unlock()
			return TrashReceipt{}, errors.New("蓝奏移入前清单已变化，请重新预览")
		}
		for _, id := range s.FolderFileIDs {
			if m.pendingRestoreLocked(s.Scope, "file:"+id) || m.pendingPurgeLocked(s.Scope, "file:"+id) {
				m.mu.Unlock()
				return TrashReceipt{}, errors.New("文件夹内部有待核对的操作，未提交永久删除")
			}
		}
	}
	if s.Purpose == guangyaPurgePurpose && m.pendingGuangyaEmptyLocked(s.Scope) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("光鸭清空结果待核对，暂不能永久删除指定项目")
	}
	var snapshot *recycleEmptySnapshot
	if nativeEmpty {
		snapshot = m.recycleEmpty
		if snapshot == nil || snapshot.token != s.Entry.ID || snapshot.scope != s.Scope ||
			snapshot.accountID != s.AccountID || !snapshot.used || snapshot.executed ||
			len(snapshot.items) == 0 || !time.Now().Before(snapshot.expires) {
			m.mu.Unlock()
			return TrashReceipt{}, errors.New("光鸭清空预览已失效，请重新读取清单")
		}
		if err := m.emptyEligibleLocked(a, snapshot.items); err != nil {
			m.mu.Unlock()
			return TrashReceipt{}, err
		}
		snapshot.executed = true
	}
	if m.pendingRestoreLocked(s.Scope, s.Entry.ID) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("原项目还原结果待核对，不能同时永久删除")
	}
	if m.pendingPurgeLocked(s.Scope, s.Entry.ID) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("原项目或包含它的目录有待核对的永久删除记录")
	}
	for _, op := range m.recyclePurges {
		if purgeKey(op.saved.Source) == purgeKey(s) {
			m.mu.Unlock()
			return TrashReceipt{}, errors.New("已有原项目永久删除记录，请先检查原结果，不会重复提交")
		}
	}
	if len(m.recyclePurges) >= 1000 {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("永久删除记录已达1000条，请清理已完成记录")
	}
	if e = m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return TrashReceipt{}, e
	}
	op := &recyclePurgeOperation{running: true, saved: diskRecyclePurge{Source: s, Receipt: TrashReceipt{ID: newID(), AccountID: a.ID, Name: s.Entry.Name, Original: s.Entry.OriginalPath, State: "working", Message: "正在检查原记录，尚未提交", Created: time.Now()}}}
	if m.recyclePurges == nil {
		m.recyclePurges = map[string]*recyclePurgeOperation{}
	}
	m.recyclePurges[op.saved.Receipt.ID] = op
	if m.saveLocked() != nil {
		delete(m.recyclePurges, op.saved.Receipt.ID)
		delete(m.renaming, a.ID)
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("初始永久删除记录保存失败，未发送请求")
	}
	ctx, cancel := context.WithCancel(ctx)
	m.purgeCancel = cancel
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	client := NewHTTPUploader().Client
	var err error
	var lanzouForm url.Values
	if nativeEmpty {
		var rows []RecycleEntry
		rows, err = scanRecycleEmpty(ctx, client, a)
		if err == nil && !sameRecycleEmptyItems(snapshot.items, rows) {
			err = errors.New("回收站清单已变化，未提交光鸭清空")
		}
	} else if s.Kind == "lanzou" {
		var present bool
		present, err = findLanzouPurgeEntry(ctx, client, a, s.Entry, nil, s.Purpose)
		if err == nil && !present {
			err = errors.New("蓝奏原项目已不在回收站，未提交")
		}
		if err == nil {
			var contents lanzouPurgeContents
			lanzouForm, contents, err = readLanzouPurgeForm(ctx, client, a, s, lanzouInventory, false)
			if err == nil && !slices.Equal(contents.ids, s.FolderFileIDs) {
				err = errors.New("蓝奏文件夹内容已变化，请重新预览；未提交永久删除")
			}
		}
	} else {
		present, findErr := findRecyclePurge(ctx, client, a, s.Entry)
		err = findErr
		if err == nil && !present {
			err = errors.New("原记录已不在回收站，未提交")
		}
	}
	if err == nil {
		m.mu.Lock()
		if m.closing || ctx.Err() != nil || !purgeAccount(m.downloadAccountLocked(a.ID), s) ||
			nativeEmpty && !time.Now().Before(snapshot.expires) {
			err = errors.New("操作取消或账号来源已变化")
		} else {
			op.saved.Submitted = true
			op.saved.Receipt.State = "unknown"
			op.saved.Receipt.Message = "写前检查点已保存，结果待核对"
			if m.saveLocked() != nil {
				op.saved.Submitted = false
				err = errors.New("提交检查点保存失败，未发送请求")
			}
		}
		m.mu.Unlock()
	}
	if err == nil {
		var responseErr error
		var taskID string
		if nativeEmpty {
			taskID, responseErr = submitGuangyaEmpty(ctx, client, a)
		} else if s.Purpose == guangyaPurgePurpose {
			taskID, responseErr = submitGuangyaPurge(ctx, client, a, s.Entry.ID)
		} else if s.Kind == "lanzou" {
			responseErr = submitLanzouPurge(ctx, client, a, lanzouForm)
		} else {
			responseErr = submitRecyclePurge(ctx, client, a, s.Entry)
		}
		m.mu.Lock()
		op.saved.Acknowledged = responseErr == nil
		op.saved.TaskID = taskID
		ackSaved := m.saveLocked() == nil
		if !ackSaved {
			op.saved.Acknowledged = false
			op.saved.TaskID = ""
		}
		d := op.saved
		m.mu.Unlock()
		state, msg := checkRecyclePurge(ctx, client, a, d)
		m.mu.Lock()
		op.saved.Receipt.State = state
		op.saved.Receipt.Message = msg
		if responseErr != nil {
			op.saved.Receipt.Message += "；请求回执未确认：" + responseErr.Error()
		}
		if !ackSaved {
			op.saved.Receipt.Message += "；受理回执保存失败"
		}
		m.mu.Unlock()
	} else {
		m.mu.Lock()
		if op.saved.Submitted {
			op.saved.Receipt.State = "unknown"
		} else {
			op.saved.Receipt.State = "not_sent"
		}
		op.saved.Receipt.Message = err.Error()
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel()
	m.purgeCancel = nil
	op.running = false
	delete(m.renaming, a.ID)
	if m.saveLocked() != nil {
		if op.saved.Submitted {
			op.saved.Receipt.State = "unknown"
		}
		op.saved.Receipt.Message = "最终记录保存失败，请只核对原结果，不要重复提交"
	}
	r := op.saved.Receipt
	r.CanCheck = op.saved.Submitted && r.State == "unknown"
	return r, nil
}
func (m *Manager) RecyclePurgeHistory(accountID string) []TrashReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.downloadAccountLocked(accountID)
	result := []TrashReceipt{}
	for _, op := range m.recyclePurges {
		if op.saved.Source.AccountID != accountID {
			continue
		}
		r := op.saved.Receipt
		r.CanCheck = !op.running && op.saved.Submitted && r.State == "unknown" && purgeAccount(a, op.saved.Source)
		if !purgeAccount(a, op.saved.Source) {
			r.State = "unknown"
			r.Message = "账号来源已变化，请在原账号官网核对"
		}
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	return result
}
func (m *Manager) CheckRecyclePurge(ctx context.Context, id string) (TrashReceipt, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.recyclePurges[id]
	if m.closing || op == nil {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("原永久删除记录不可用")
	}
	a := m.downloadAccountLocked(op.saved.Source.AccountID)
	if !purgeAccount(a, op.saved.Source) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("账号来源已变化")
	}
	if !op.saved.Submitted || op.saved.Receipt.State != "unknown" {
		r := op.saved.Receipt
		m.mu.Unlock()
		return r, nil
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return TrashReceipt{}, e
	}
	ctx, cancel := context.WithCancel(ctx)
	m.purgeCancel = cancel
	op.running = true
	m.workers.Add(1)
	d := op.saved
	m.mu.Unlock()
	defer m.workers.Done()
	state, msg := checkRecyclePurge(ctx, NewHTTPUploader().Client, a, d)
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel()
	m.purgeCancel = nil
	op.running = false
	delete(m.renaming, a.ID)
	op.saved.Receipt.State = state
	op.saved.Receipt.Message = msg + "；本次只有读取，没有重发请求"
	if m.saveLocked() != nil {
		op.saved.Receipt.State = "unknown"
		op.saved.Receipt.Message = "核对保存失败，请勿重发永久删除"
	}
	r := op.saved.Receipt
	r.CanCheck = r.State == "unknown"
	return r, nil
}
func (m *Manager) ClearRecyclePurgeHistory(accountID string) (int, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(accountID)
	removed := map[string]*recyclePurgeOperation{}
	for id, op := range m.recyclePurges {
		if op.saved.Source.AccountID == accountID && purgeAccount(a, op.saved.Source) && !op.running && (op.saved.Receipt.State == "confirmed" || op.saved.Receipt.State == "not_sent") {
			removed[id] = op
			delete(m.recyclePurges, id)
		}
	}
	if len(removed) > 0 && m.saveLocked() != nil {
		for id, op := range removed {
			m.recyclePurges[id] = op
		}
		return 0, errors.New("清理保存失败，原记录保留")
	}
	return len(removed), nil
}
