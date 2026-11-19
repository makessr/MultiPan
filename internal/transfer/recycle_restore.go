package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

type recycleRestoreSource struct {
	Purpose       string       `json:"purpose"`
	AccountID     string       `json:"accountId"`
	Scope         string       `json:"scope"`
	Page          int          `json:"page"`
	Entry         RecycleEntry `json:"entry"`
	DestinationID string       `json:"destinationId,omitempty"`
}
type RecycleRestoreResult struct {
	ID            string    `json:"id"`
	EntryID       string    `json:"entryId"`
	Name          string    `json:"name"`
	State         string    `json:"state"`
	Message       string    `json:"message"`
	Created       time.Time `json:"created"`
	CanCheck      bool      `json:"canCheck"`
	CanMove       bool      `json:"canMove,omitempty"`
	CanMoveFolder bool      `json:"canMoveFolder,omitempty"`
}

type RecycleRestoreLocation struct {
	AccountID    string               `json:"accountId"`
	ParentID     string               `json:"parentId"`
	MoveFile     *RestoredFileForMove `json:"moveFile,omitempty"`
	MoveFolderID string               `json:"moveFolderId,omitempty"`
}

type RestoredFileForMove struct {
	ID        string `json:"id"`
	ObjectID  string `json:"objectId,omitempty"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	ReceiptID string `json:"receiptId"`
}

func restoredFileForMove(source recycleRestoreSource) *RestoredFileForMove {
	e := source.Entry
	if source.DestinationID != "" || e.Kind != "file" || e.Size == nil ||
		*e.Size < 0 || *e.Size > 1<<53-1 {
		return nil
	}
	file := &RestoredFileForMove{Name: e.Name, Size: *e.Size}
	switch source.Purpose {
	case "pan123-recycle-restore-v1", "guangya-recycle-restore-v1":
		file.ID = e.ID
	case "quark-recycle-restore-v1":
		file.ID = e.FileID
	case "baidu-recycle-restore-v1":
		file.ID, file.ObjectID = e.OriginalPath, e.ID
	default:
		return nil
	}
	return file
}

// LocateRecycleRestore reads the live parent of a confirmed receipt without
// replaying the restore or changing its saved status.
func (m *Manager) LocateRecycleRestore(ctx context.Context, accountID, receiptID string) (RecycleRestoreLocation, error) {
	m.mu.Lock()
	op := m.recycleRestores[receiptID]
	a := m.downloadAccountLocked(accountID)
	if m.closing || op == nil || op.running || op.saved.Result.State != "confirmed" || !recycleRestoreAccount(a, op.saved.Source) {
		m.mu.Unlock()
		return RecycleRestoreLocation{}, errors.New("此账号没有可定位的已确认还原记录")
	}
	source := op.saved.Source
	m.mu.Unlock()

	match, _, err := recycleRestoreOriginal(ctx, NewHTTPUploader().Client, a, source)
	if err != nil {
		return RecycleRestoreLocation{}, errors.New("无法重新读取还原目录，请检查登录或网络")
	}
	if !match {
		return RecycleRestoreLocation{}, errors.New("还原项目已移动、改名或无法在还原位置确认")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.recycleRestores[receiptID] != op || op.running || op.saved.Result.State != "confirmed" || !recycleRestoreAccount(m.downloadAccountLocked(accountID), source) {
		return RecycleRestoreLocation{}, errors.New("账号来源或还原记录已变化，请刷新后重试")
	}
	file := restoredFileForMove(source)
	if file != nil {
		file.ReceiptID = receiptID
	}
	return RecycleRestoreLocation{AccountID: accountID, ParentID: recycleRestoreParent(source), MoveFile: file, MoveFolderID: restoredFolderID(source)}, nil
}

func recycleRestoreParent(source recycleRestoreSource) string {
	if source.DestinationID != "" {
		return source.DestinationID
	}
	switch source.Purpose {
	case lanzouRestorePurpose, lanzouListedFileRestorePurpose:
		return lanzouRestoreParent(source.Entry)
	case "baidu-recycle-restore-v1":
		return path.Dir(source.Entry.OriginalPath)
	default:
		return source.Entry.ParentID
	}
}

type diskRecycleRestore struct {
	Source                    recycleRestoreSource  `json:"source"`
	Result                    RecycleRestoreResult  `json:"result"`
	Pan123Acknowledged        bool                  `json:"pan123Acknowledged,omitempty"`
	Submitted                 bool                  `json:"submitted"`
	TaskID                    string                `json:"taskId,omitempty"`
	LanzouAcknowledged        bool                  `json:"lanzouAcknowledged,omitempty"`
	LanzouContents            *lanzouFolderContents `json:"lanzouContents,omitempty"`
	LanzouContentsFromRecycle bool                  `json:"lanzouContentsFromRecycle,omitempty"`
}
type recycleRestore struct {
	saved   diskRecycleRestore
	running bool
	cancel  context.CancelFunc
}

func validRecycleRestoreSource(h recycleRestoreSource) bool {
	e := h.Entry
	if h.DestinationID != "" && (h.Purpose != "pan123-recycle-restore-v1" ||
		!valid123Folder(h.DestinationID) || h.DestinationID == e.ParentID || h.DestinationID == e.ID) {
		return false
	}
	if h.Purpose == lanzouRestorePurpose {
		return h.AccountID != "" && h.Scope != "" && h.Page >= 1 && h.Page <= 1000 && validLanzouRestoreEntry(e)
	}
	if h.Purpose == lanzouListedFileRestorePurpose {
		row, ok := lanzouListedRestoreFile(e)
		return ok && row.FileID == e.FileID && h.AccountID != "" && h.Scope != "" && h.Page >= 1 && h.Page <= 1000
	}
	if h.Purpose == "guangya-recycle-restore-v1" {
		return h.AccountID != "" && h.Scope != "" && h.Page >= 1 && h.Page <= 1000 &&
			validQuarkFolder(e.ID) && e.ID != "0" && e.ID != guangyaRoot && validGuangyaRestoreParent(e.ParentID, e.ID) &&
			e.OriginalPath == guangyaRestorePath(e.ParentID, e.Name) && relayName(e.Name) &&
			(e.Kind == "folder" || e.Kind == "file" && e.Size != nil) &&
			(e.Size == nil || *e.Size >= 0) && e.FileID == "" && e.RestoreHandle == ""
	}
	if h.Purpose == "quark-recycle-restore-v1" {
		return h.AccountID != "" && h.Scope != "" && h.Page >= 1 && h.Page <= 1000 && validQuarkFolder(e.ID) && e.ID != "0" && validQuarkFolder(e.FileID) && e.FileID != "0" && validQuarkFolder(e.ParentID) && e.ParentID != e.FileID && e.Name != "" && e.Name != "." && e.Name != ".." && len(e.Name) <= 4096 && !strings.ContainsAny(e.Name, "/\\\x00\r\n") && e.OriginalPath == "原目录 ID "+e.ParentID+" / "+e.Name && (e.Kind == "file" || e.Kind == "folder") && (e.Kind == "folder" || e.Size != nil) && (e.Size == nil || *e.Size >= 0) && e.RestoreHandle == ""
	}
	origin := h.Purpose == "baidu-recycle-restore-v1" && validBaiduPath(e.OriginalPath) && e.OriginalPath != "/" && path.Base(e.OriginalPath) == e.Name
	if h.Purpose == "pan123-recycle-restore-v1" {
		origin = valid123Folder(e.ParentID) && e.ParentID != e.ID && !strings.ContainsAny(e.Name, "/\\\x00") && e.OriginalPath == "原目录 ID "+e.ParentID+" / "+e.Name && (e.Kind == "folder" || e.Size != nil)
	}
	return origin && h.AccountID != "" && h.Scope != "" && h.Page >= 1 && h.Page <= 1000 && valid123Folder(e.ID) && e.ID != "0" && e.Name != "" && len(e.Name) <= 4096 && (e.Kind == "file" || e.Kind == "folder") && (e.Size == nil || *e.Size >= 0) && e.RestoreHandle == ""
}
func sealRecycleRestore(a Account, page int, entry RecycleEntry) (string, error) {
	h := recycleRestoreSource{Purpose: "baidu-recycle-restore-v1", AccountID: a.ID, Scope: downloadScope(a), Page: page, Entry: entry}
	if a.Kind == "pan123" {
		h.Purpose = "pan123-recycle-restore-v1"
	}
	if a.Kind == "quark" {
		h.Purpose = "quark-recycle-restore-v1"
	}
	if a.Kind == "guangya" {
		h.Purpose = "guangya-recycle-restore-v1"
	}
	if a.Kind == "lanzou" {
		h.Purpose = lanzouRestorePurpose
		if entry.ParentID == "" {
			h.Purpose = lanzouListedFileRestorePurpose
		}
	}
	if !recycleRestoreAccount(a, h) || !validRecycleRestoreSource(h) {
		return "", errors.New("原位置无法确认，暂不支持程序内还原")
	}
	b, e := json.Marshal(h)
	if e != nil {
		return "", e
	}
	b, e = protect(b)
	if e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func decodeRecycleRestore(token string) (recycleRestoreSource, error) {
	var h recycleRestoreSource
	if len(token) > 32768 {
		return h, errors.New("还原标识无效，请刷新回收站")
	}
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e == nil {
		b, e = unprotect(b)
	}
	if e != nil || json.Unmarshal(b, &h) != nil || h.DestinationID != "" || !validRecycleRestoreSource(h) {
		return h, errors.New("还原标识失效，请刷新回收站")
	}
	return h, nil
}
func recycleRestoreAccount(a Account, h recycleRestoreSource) bool {
	provider := a.Kind == "baidu" && a.Endpoint == BaiduAPI && h.Purpose == "baidu-recycle-restore-v1" ||
		a.Kind == "pan123" && a.Endpoint == Pan123API && h.Purpose == "pan123-recycle-restore-v1" ||
		a.Kind == "quark" && a.Endpoint == QuarkAPI && h.Purpose == "quark-recycle-restore-v1" ||
		a.Kind == "guangya" && a.Endpoint == GuangyaAPI && h.Purpose == "guangya-recycle-restore-v1" ||
		a.Kind == "lanzou" && a.Endpoint == lanzouRecycleOrigin && isLanzouRestorePurpose(h.Purpose)
	return a.ID == h.AccountID && provider && downloadScope(a) == h.Scope
}
func sameRecycleRestoreEntry(a, b RecycleEntry) bool {
	return a.ID == b.ID && a.FileID == b.FileID && a.Name == b.Name && a.Kind == b.Kind && a.ParentID == b.ParentID && a.OriginalPath == b.OriginalPath && (a.Size == nil && b.Size == nil || a.Size != nil && b.Size != nil && *a.Size == *b.Size)
}

func (m *Manager) restoreRecycleReceipts(rows []diskRecycleRestore) error {
	if len(rows) > 1000 {
		return errors.New("本机还原回执数量异常")
	}
	m.recycleRestores = map[string]*recycleRestore{}
	seen := map[string]bool{}
	for _, d := range rows {
		key := d.Source.Scope + "/" + d.Source.Entry.ID
		if !validRecycleRestoreSource(d.Source) || d.Result.ID == "" || d.Result.Created.IsZero() || d.Result.Name != d.Source.Entry.Name || d.Result.EntryID != d.Source.Entry.ID || m.recycleRestores[d.Result.ID] != nil || seen[key] {
			return errors.New("本机还原回执不完整或重复，未覆盖原数据")
		}
		direct := d.Source.DestinationID != ""
		if d.Pan123Acknowledged && (!direct || !d.Submitted) || direct && d.Result.State == "confirmed" && !d.Pan123Acknowledged {
			return errors.New("123 指定目录还原的本机回执状态不一致")
		}
		guangya := d.Source.Purpose == "guangya-recycle-restore-v1"
		if d.TaskID != "" && (!guangya || !d.Submitted || !validGuangyaRestoreTaskID(d.TaskID)) ||
			guangya && d.Result.State == "confirmed" && d.TaskID == "" {
			return errors.New("本机还原任务凭证与回执状态不一致")
		}
		lanzou := isLanzouRestorePurpose(d.Source.Purpose)
		if d.LanzouAcknowledged && (!lanzou || !d.Submitted) || lanzou && d.Result.State == "confirmed" && !d.LanzouAcknowledged {
			return errors.New("蓝奏还原回执与本机状态不一致")
		}
		if d.LanzouContents != nil && (!lanzou || d.Source.Entry.Kind != "folder" || !validLanzouFolderContents(d.LanzouContents)) {
			return errors.New("蓝奏还原回执的原文件清单无效")
		}
		if d.LanzouContentsFromRecycle && (!d.Submitted || d.LanzouContents == nil) {
			return errors.New("蓝奏还原回执的清单来源无效")
		}
		seen[key] = true
		switch d.Result.State {
		case "working", "unknown":
			if d.Submitted {
				d.Result.State = "unknown"
				d.Result.Message = "上次还原结果待确认，只检查原结果，不会重新提交"
			} else {
				d.Result.State = "not_sent"
				d.Result.Message = "上次退出前尚未提交还原请求"
			}
		case "confirmed":
			if !d.Submitted {
				return errors.New("还原回执状态矛盾")
			}
		case "not_sent":
			if d.Submitted {
				return errors.New("还原回执状态矛盾")
			}
		default:
			return errors.New("还原回执状态无效")
		}
		d.Result.CanCheck = d.Submitted && d.Result.State == "unknown"
		m.recycleRestores[d.Result.ID] = &recycleRestore{saved: d}
	}
	return nil
}
func (m *Manager) RecycleRestoreHistory(accountID string) []RecycleRestoreResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []RecycleRestoreResult{}
	a := m.downloadAccountLocked(accountID)
	for _, op := range m.recycleRestores {
		if op.saved.Source.AccountID != accountID {
			continue
		}
		r := op.saved.Result
		if !recycleRestoreAccount(a, op.saved.Source) {
			r.CanCheck = false
			r.State = "unknown"
			r.Message = "账号来源已变化，请在原账号官网核对还原结果"
		}
		r.CanMove = r.State == "confirmed" && !op.running && restoredFileForMove(op.saved.Source) != nil
		r.CanMoveFolder = r.State == "confirmed" && !op.running && restoredFolderID(op.saved.Source) != ""
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
func (m *Manager) RestoreRecycle(ctx context.Context, token string) (RecycleRestoreResult, error) {
	return m.restoreRecycle(ctx, token, nil)
}

func (m *Manager) RestoreRecycleTo(ctx context.Context, token string, trail []CloudFolder) (RecycleRestoreResult, error) {
	if len(trail) == 0 || len(trail) > 64 || !valid123Folder(trail[len(trail)-1].ID) {
		return RecycleRestoreResult{}, errors.New("还原目标目录无效，请重新选择")
	}
	return m.restoreRecycle(ctx, token, trail)
}

func (m *Manager) restoreRecycle(ctx context.Context, token string, trail []CloudFolder) (RecycleRestoreResult, error) {
	h, e := decodeRecycleRestore(token)
	if e != nil {
		return RecycleRestoreResult{}, e
	}
	if len(trail) > 0 {
		h.DestinationID = trail[len(trail)-1].ID
		if !validRecycleRestoreSource(h) {
			return RecycleRestoreResult{}, errors.New("此项目无法还原到所选目录")
		}
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(h.AccountID)
	if m.closing || !recycleRestoreAccount(a, h) {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("账号来源已变化，请刷新回收站，不提交还原")
	}
	if a.Kind == "lanzou" && !m.lanzouCanRestoreLocked(a, h) {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("蓝奏原移入记录无法确认或仍待核对，请刷新列表")
	}
	if m.pendingPurgeLocked(h.Scope, h.Entry.ID) || m.pendingGuangyaEmptyLocked(h.Scope) {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("原项目永久删除结果待核对，暂不能提交还原")
	}
	for _, op := range m.recycleRestores {
		if op.saved.Source.Scope == h.Scope && op.saved.Source.Entry.ID == h.Entry.ID {
			if op.saved.Source.DestinationID != h.DestinationID {
				m.mu.Unlock()
				return RecycleRestoreResult{}, errors.New("项目已有另一还原位置的回执，请先核对")
			}
			r := op.saved.Result
			m.mu.Unlock()
			return r, nil
		}
	}
	if len(m.recycleRestores) >= 1000 {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("还原回执已达1000条，请清理已完成回执")
	}
	if e = m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return RecycleRestoreResult{}, e
	}
	ctx, cancel := context.WithCancel(ctx)
	op := &recycleRestore{saved: diskRecycleRestore{Source: h, Result: RecycleRestoreResult{ID: newID(), EntryID: h.Entry.ID, Name: h.Entry.Name, Created: time.Now(), State: "working", Message: "正在核对原项目，尚未提交还原"}}, running: true, cancel: cancel}
	if h.Purpose == lanzouRestorePurpose && h.Entry.Kind == "folder" {
		op.saved.LanzouContents = m.lanzouFolderInventoryLocked(a, h.Entry.ID, h.Entry.Name).LanzouContents
	}
	if m.recycleRestores == nil {
		m.recycleRestores = map[string]*recycleRestore{}
	}
	m.recycleRestores[op.saved.Result.ID] = op
	if e = m.saveLocked(); e != nil {
		delete(m.recycleRestores, op.saved.Result.ID)
		delete(m.renaming, a.ID)
		cancel()
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("无法保存还原回执，未发送请求")
	}
	m.workers.Add(1)
	m.mu.Unlock()
	return m.runRecycleRestore(ctx, a, op, false, trail), nil
}
func (m *Manager) CheckRecycleRestore(ctx context.Context, id string) (RecycleRestoreResult, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.recycleRestores[id]
	if m.closing || op == nil {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("原还原回执不存在或程序正在退出")
	}
	a := m.downloadAccountLocked(op.saved.Source.AccountID)
	if !recycleRestoreAccount(a, op.saved.Source) {
		m.mu.Unlock()
		return RecycleRestoreResult{}, errors.New("账号来源已变化，请到原账号官网核对")
	}
	if op.running || !op.saved.Result.CanCheck {
		r := op.saved.Result
		m.mu.Unlock()
		return r, nil
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return RecycleRestoreResult{}, e
	}
	ctx, cancel := context.WithCancel(ctx)
	op.running = true
	op.cancel = cancel
	m.workers.Add(1)
	m.mu.Unlock()
	return m.runRecycleRestore(ctx, a, op, true, nil), nil
}
func recycleRestoreOriginal(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource) (bool, bool, error) {
	if a.Kind == "quark" {
		return quarkRestoreOriginal(ctx, client, a, h)
	}
	if a.Kind == "pan123" {
		return pan123RestoreOriginal(ctx, client, a, h)
	}
	if a.Kind == "guangya" {
		return guangyaRestoreOriginal(ctx, client, a, h)
	}
	if a.Kind == "lanzou" {
		return lanzouRestoreLocation(ctx, client, a, h)
	}
	c := newBaiduClient(client, a)
	if e := c.session(ctx); e != nil {
		return false, false, e
	}
	rows, e := c.list(ctx, recycleRestoreParent(h))
	if e != nil {
		return false, false, e
	}
	for _, r := range rows {
		if r.Path != h.Entry.OriginalPath {
			continue
		}
		same := string(r.ID) == h.Entry.ID && r.Name == h.Entry.Name && r.Dir != nil && (*r.Dir == 1) == (h.Entry.Kind == "folder") && (h.Entry.Kind == "folder" || h.Entry.Size == nil || r.Size != nil && *r.Size == *h.Entry.Size)
		return same, true, nil
	}
	return false, false, nil
}
func (m *Manager) runRecycleRestore(ctx context.Context, a Account, op *recycleRestore, checkOnly bool, trail []CloudFolder) RecycleRestoreResult {
	defer m.workers.Done()
	m.mu.Lock()
	d := op.saved
	m.mu.Unlock()
	client := NewHTTPUploader().Client
	r := d.Result
	r.State = "unknown"
	r.CanCheck = true
	r.Message = "还原结果待确认，请检查原结果或到官网核对，不会重新提交"
	if !checkOnly {
		var lanzouForm url.Values
		rows, e := listAccountRecycle(ctx, client, a, d.Source.Page)
		match := false
		if e == nil {
			for _, entry := range rows.Items {
				if sameRecycleRestoreRow(entry, d.Source.Entry) {
					match = true
				}
			}
		}
		var targetErr error
		var occupied bool
		if match && e == nil && !rows.Incomplete {
			if d.Source.DestinationID != "" {
				targetErr = verify123RestoreDestination(ctx, client, a, d.Source, trail)
			}
			if targetErr == nil {
				_, occupied, targetErr = recycleRestoreOriginal(ctx, client, a, d.Source)
			}
		}
		if a.Kind == "lanzou" && match && e == nil && !rows.Incomplete && targetErr == nil && !occupied {
			if d.Source.Entry.Kind == "folder" && d.LanzouContents == nil {
				d.LanzouContents = captureLanzouRecycleRestoreContents(ctx, client, a, d.Source.Entry)
				d.LanzouContentsFromRecycle = d.LanzouContents != nil
			}
			lanzouForm, targetErr = readLanzouRecycleConfirmation(ctx, client, a, d.Source.Entry.ID, lanzouRestore)
			if targetErr != nil {
				r.Message = "未提交还原：" + targetErr.Error()
			}
		}
		if !match || e != nil || rows.Incomplete || targetErr != nil || occupied {
			r.State = "not_sent"
			r.CanCheck = false
			if !strings.HasPrefix(r.Message, "未提交还原") {
				r.Message = "未提交还原：原记录已变化、还原位置无法读取或已有同名项目，请刷新或到官网核对"
			}
		} else {
			m.mu.Lock()
			op.saved.Submitted = true
			op.saved.Result = r
			if d.LanzouContentsFromRecycle {
				op.saved.LanzouContents = d.LanzouContents
				op.saved.LanzouContentsFromRecycle = true
			}
			e = m.saveLocked()
			if e != nil {
				op.saved.Submitted = false
				if d.LanzouContentsFromRecycle {
					op.saved.LanzouContents = nil
					op.saved.LanzouContentsFromRecycle = false
				}
			}
			m.mu.Unlock()
			if e != nil {
				r.State = "not_sent"
				r.CanCheck = false
				r.Message = "提交检查点保存失败，未发送还原请求"
			} else {
				if a.Kind == "lanzou" {
					if submitLanzouRestore(ctx, client, a, lanzouForm) == nil {
						m.mu.Lock()
						op.saved.LanzouAcknowledged = true
						if m.saveLocked() == nil {
							d.LanzouAcknowledged = true
						} else {
							op.saved.LanzouAcknowledged = false
						}
						m.mu.Unlock()
					}
				} else if a.Kind == "quark" {
					_, _ = newQuarkClient(client, a).request(ctx, "POST", "/file/recycle/recover", nil, map[string]any{"select_mode": 2, "record_list": []string{d.Source.Entry.ID}}, nil)
				} else if d.Source.DestinationID != "" {
					if submit123RestoreTo(ctx, client, a, d.Source) == nil {
						m.mu.Lock()
						op.saved.Pan123Acknowledged = true
						if m.saveLocked() == nil {
							d.Pan123Acknowledged = true
						} else {
							op.saved.Pan123Acknowledged = false
						}
						m.mu.Unlock()
					}
				} else if a.Kind == "pan123" {
					_ = (pan123Client{client, a}).request(ctx, "POST", "/file/trash", nil, map[string]any{"driveId": 0, "operation": false, "fileTrashInfoList": []map[string]json.Number{{"FileId": json.Number(d.Source.Entry.ID)}}}, nil)
				} else if a.Kind == "guangya" {
					if id, err := submitGuangyaRestore(ctx, client, a, d.Source.Entry); err == nil {
						m.mu.Lock()
						op.saved.TaskID = id
						if err = m.saveLocked(); err == nil {
							d.TaskID = id
						} else {
							op.saved.TaskID = ""
						}
						m.mu.Unlock()
					}
				} else {
					c := newBaiduClient(client, a)
					if e = c.session(ctx); e == nil {
						_ = c.api(ctx, "POST", "recycle/restore", url.Values{"async": {"1"}}, url.Values{"fidlist": {"[" + d.Source.Entry.ID + "]"}}, nil)
					}
				}
				// Even an ambiguous HTTP response is followed only by reads, never a replay.
				checkOnly = true
			}
		}
	}
	if checkOnly {
		delay := 350 * time.Millisecond
		if a.Kind == "guangya" {
			delay = time.Second
		}
		for attempt := 0; attempt < 3; attempt++ {
			same, e := checkRecycleRestored(ctx, client, a, d)
			if e == nil && same {
				r.State = "confirmed"
				r.CanCheck = false
				r.Message = "已在原位置核对同一文件ID、原位置、类型和可用大小，还原结果已确认"
				if d.Source.DestinationID != "" {
					r.Message = "已确认项目还原到所选目录，并从回收站移除"
					if d.Source.Entry.Kind == "folder" {
						r.Message += "。请打开还原目录核对文件夹内容"
					}
				}
				if a.Kind == "lanzou" {
					switch {
					case d.Source.Entry.Kind == "file":
						r.Message = "已在蓝奏云根目录核对同一文件 ID 和名称，还原结果已确认"
					case d.LanzouContentsFromRecycle:
						r.Message = "已核对蓝奏文件夹回到原位置，目录中的文件 ID 和名称与还原前回收站网页清单一致"
					case d.LanzouContents != nil:
						r.Message = "已核对蓝奏文件夹回到原位置，目录中的文件 ID 和完整名称与移入前清单一致"
					default:
						r.Message = "已在原位置核对蓝奏文件夹 ID 和名称。此记录没有移入前清单，请在文件夹中核对内容"
					}
				}
				break
			}
			if e != nil {
				if a.Kind == "guangya" || a.Kind == "lanzou" {
					r.Message = "还原结果待确认：" + e.Error() + "；不会重复提交"
				}
				if !errors.Is(e, errLanzouRestoreContentsPending) {
					break
				}
			}
			if attempt == 2 {
				break
			}
			select {
			case <-ctx.Done():
				attempt = 2
			case <-time.After(delay):
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
		if op.saved.Submitted {
			r.State = "unknown"
			r.CanCheck = true
		}
		r.Message += "；本机回执保存失败，请勿重复提交"
		op.saved.Result = r
	}
	return r
}

func checkRecycleRestored(ctx context.Context, client *http.Client, a Account, d diskRecycleRestore) (bool, error) {
	if d.Source.DestinationID != "" && !d.Pan123Acknowledged {
		return false, errors.New("123 未保存明确的还原成功回执，请到官网核对")
	}
	if a.Kind == "lanzou" && !d.LanzouAcknowledged {
		return false, errors.New("蓝奏还原回执未能确认，请到官网核对")
	}
	if a.Kind == "guangya" {
		done, err := guangyaFileTaskDone(ctx, client, a, d.TaskID)
		if err != nil || !done {
			return false, err
		}
	}
	same, _, err := recycleRestoreOriginal(ctx, client, a, d.Source)
	if a.Kind == "lanzou" && err == nil && same && d.LanzouContents != nil {
		err = checkLanzouRestoredContents(ctx, client, a, d.Source.Entry, d.LanzouContents)
		return err == nil, err
	}
	if (a.Kind != "guangya" && d.Source.DestinationID == "") || err != nil || !same {
		return same, err
	}
	present, err := findRecyclePurge(ctx, client, a, d.Source.Entry)
	return err == nil && !present, err
}
func (m *Manager) ClearRecycleRestores(accountID string) (int, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(accountID)
	removed := map[string]*recycleRestore{}
	for id, op := range m.recycleRestores {
		if recycleRestoreAccount(a, op.saved.Source) && !op.running && (op.saved.Result.State == "confirmed" || op.saved.Result.State == "not_sent") {
			removed[id] = op
			delete(m.recycleRestores, id)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if m.saveLocked() != nil {
		for id, op := range removed {
			m.recycleRestores[id] = op
		}
		return 0, errors.New("清理保存失败，原回执已保留")
	}
	return len(removed), nil
}
