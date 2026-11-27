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
	"strconv"
	"strings"
	"time"
)

type TrashInput struct {
	AccountID string     `json:"accountId"`
	ParentID  string     `json:"parentId"`
	Entry     CloudEntry `json:"entry"`
}
type TrashPrepared struct {
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
	Handle string `json:"handle"`
}
type trashSource struct {
	Purpose  string       `json:"purpose"`
	Scope    string       `json:"scope"`
	Input    TrashInput   `json:"input"`
	Original RecycleEntry `json:"original"`
}
type TrashReceipt struct {
	ID        string    `json:"id"`
	AccountID string    `json:"accountId"`
	Name      string    `json:"name"`
	Original  string    `json:"original"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	Created   time.Time `json:"created"`
	CanCheck  bool      `json:"canCheck"`
}
type diskTrash struct {
	Source         trashSource           `json:"source"`
	Receipt        TrashReceipt          `json:"receipt"`
	Submitted      bool                  `json:"submitted"`
	LanzouContents *lanzouFolderContents `json:"lanzouContents,omitempty"`
}
type trashOperation struct {
	saved   diskTrash
	running bool
}

func submitPan123Trash(ctx context.Context, client *http.Client, a Account, id string) error {
	return (pan123Client{client, a}).request(ctx, "POST", "/file/trash", nil, map[string]any{
		"driveId": 0, "fileTrashInfoList": []map[string]any{{"FileId": json.Number(id)}}, "operation": true,
	}, nil)
}

func (c *baiduClient) submitTrash(ctx context.Context, filePath string) error {
	payload, _ := json.Marshal([]string{filePath})
	return c.api(ctx, "POST", "filemanager", url.Values{"opera": {"delete"}, "async": {"2"}, "newVerify": {"1"}}, url.Values{"filelist": {string(payload)}}, nil)
}

func trashProvider(a Account) bool {
	return trashProviderV1(a) || a.Kind == "lanzou" && a.Endpoint == lanzouRecycleOrigin || a.Kind == "quark" && a.Endpoint == QuarkAPI
}
func trashProviderV1(a Account) bool {
	return a.Kind == "baidu" && a.Endpoint == BaiduAPI || a.Kind == "pan123" && a.Endpoint == Pan123API
}
func trashDirectoryPath(parent, name string) string {
	return "原目录 ID " + parent + " / " + name
}

func trashAccount(a Account, s trashSource) bool {
	if a.ID != s.Input.AccountID || downloadScope(a) != s.Scope {
		return false
	}
	switch s.Purpose {
	case lanzouTrashPurpose, lanzouFolderTrashPurpose:
		return a.Kind == "lanzou" && a.Endpoint == lanzouRecycleOrigin
	case quarkTrashPurpose:
		return a.Kind == "quark" && a.Endpoint == QuarkAPI
	default:
		return trashProviderV1(a)
	}
}
func validTrashSource(s trashSource) bool {
	in, e := s.Input, s.Original
	if s.Purpose == quarkTrashPurpose {
		return validQuarkTrashSource(s)
	}
	if s.Purpose == lanzouTrashPurpose || s.Purpose == lanzouFolderTrashPurpose {
		return s.Scope != "" && in.AccountID != "" && in.Entry.Folder == (s.Purpose == lanzouFolderTrashPurpose) &&
			valid123Folder(in.Entry.ID) && in.Entry.ID != "0" &&
			(!in.Entry.Folder || in.Entry.ID != in.ParentID) &&
			validLanzouTrashParent(in.ParentID) &&
			relayName(in.Entry.Name) && e == lanzouTrashEntry(in.ParentID, in.Entry)
	}
	kind := "file"
	if in.Entry.Folder {
		kind = "folder"
	}
	valid := s.Purpose == "trash-v1" && s.Scope != "" && in.AccountID != "" && in.Entry.ID != "" && in.Entry.Name != "" && len(in.Entry.Name) <= 4096 && !strings.ContainsAny(in.Entry.Name, "/\\\x00\r\n") && in.Entry.Name != "." && in.Entry.Name != ".." && e.Name == in.Entry.Name && e.Kind == kind && valid123Folder(e.ID) && e.ID != "0" && e.RestoreHandle == "" && (e.Kind == "folder" || e.Size != nil && *e.Size >= 0)
	if !valid {
		return false
	}
	if strings.HasPrefix(in.Entry.ID, "/") {
		return validBaiduPath(in.Entry.ID) && in.Entry.ID != "/" && path.Dir(in.Entry.ID) == in.ParentID && path.Base(in.Entry.ID) == in.Entry.Name && e.OriginalPath == in.Entry.ID && e.ParentID == "" && in.Entry.ObjectID == e.ID
	}
	return valid123Folder(in.ParentID) && valid123Folder(in.Entry.ID) && in.Entry.ID != "0" && in.Entry.ID != in.ParentID && e.ID == in.Entry.ID && e.ParentID == in.ParentID && e.OriginalPath == "原目录 ID "+in.ParentID+" / "+in.Entry.Name
}

// Read the entire parent within a fixed 5000-entry budget. Never interpret an
// incomplete or malformed listing as an absent source. IDs remain decimal strings.
func trashLive(ctx context.Context, client *http.Client, a Account, parent string) ([]RecycleEntry, error) {
	rows := []RecycleEntry{}
	seen := map[string]bool{}
	if !trashProvider(a) {
		return nil, errors.New("移入回收站当前支持百度、123、蓝奏和夸克")
	}
	if a.Kind == "quark" {
		return readQuarkTrashParent(ctx, client, a, parent)
	}
	if a.Kind == "lanzou" {
		if !validLanzouTrashParent(parent) || a.Secret == "" {
			return nil, errors.New("蓝奏原目录或登录无效")
		}
		files, err := readLanzouFiles(ctx, client, a, parent, true)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			rows = append(rows, lanzouTrashEntry(parent, file))
		}
		return rows, nil
	}
	if a.Kind == "baidu" && !validBaiduPath(parent) || a.Kind == "pan123" && !valid123Folder(parent) {
		return nil, errors.New("原目录无效")
	}
	c := newBaiduClient(client, a)
	for page := 1; page <= 50; page++ {
		chunk := []RecycleEntry{}
		done := false
		if a.Kind == "baidu" {
			var data struct {
				List []baiduEntry `json:"list"`
			}
			if e := c.api(ctx, "GET", "list", url.Values{"dir": {parent}, "page": {strconv.Itoa(page)}, "num": {"100"}, "order": {"name"}, "desc": {"0"}, "showempty": {"0"}}, nil, &data); e != nil {
				return nil, e
			}
			if data.List == nil || len(data.List) > 100 {
				return nil, errors.New("原目录列表格式未确认")
			}
			for _, r := range data.List {
				if r.Dir == nil || (*r.Dir != 0 && *r.Dir != 1) || !validBaiduPath(r.Path) || path.Dir(r.Path) != parent || path.Base(r.Path) != r.Name {
					return nil, errors.New("原目录条目来源未确认")
				}
				kind := "file"
				if *r.Dir == 1 {
					kind = "folder"
				}
				chunk = append(chunk, RecycleEntry{ID: string(r.ID), Name: r.Name, Kind: kind, Size: r.Size, OriginalPath: r.Path})
			}
			done = len(data.List) < 100
		} else {
			var data struct {
				Rows []struct {
					ID      json.Number `json:"FileId"`
					Parent  json.Number `json:"ParentFileId"`
					Name    string      `json:"FileName"`
					Type    *int        `json:"Type"`
					Size    *int64      `json:"Size"`
					Trashed *bool       `json:"Trashed"`
				} `json:"InfoList"`
				Next  json.RawMessage `json:"Next"`
				Total *int            `json:"Total"`
			}
			q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {"0"}, "parentFileId": {parent}, "trashed": {"false"}, "Page": {strconv.Itoa(page)}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "SearchData": {""}, "inDirectSpace": {"false"}}
			if e := (pan123Client{client, a}).request(ctx, "GET", "/file/list/new", q, nil, &data); e != nil {
				return nil, e
			}
			if data.Rows == nil || len(data.Rows) > 100 {
				return nil, errors.New("原目录列表格式未确认")
			}
			for _, r := range data.Rows {
				if string(r.Parent) != parent || r.Type == nil || (*r.Type != 0 && *r.Type != 1) || r.Trashed != nil && *r.Trashed {
					return nil, errors.New("原目录条目来源未确认")
				}
				kind := "file"
				if *r.Type == 1 {
					kind = "folder"
				}
				chunk = append(chunk, RecycleEntry{ID: string(r.ID), Name: r.Name, Kind: kind, Size: r.Size, ParentID: parent, OriginalPath: "原目录 ID " + parent + " / " + r.Name})
			}
			done = len(chunk) == 0 || strings.Trim(string(data.Next), "\"") == "-1" || data.Total != nil && *data.Total >= 0 && len(rows)+len(chunk) >= *data.Total
		}
		for _, r := range chunk {
			if !valid123Folder(r.ID) || r.ID == "0" || seen[r.ID] || r.Name == "" || strings.ContainsAny(r.Name, "/\\\x00\r\n") || r.Size != nil && *r.Size < 0 {
				return nil, errors.New("原目录条目重复或无效")
			}
			seen[r.ID] = true
			rows = append(rows, r)
		}
		if done {
			return rows, nil
		}
	}
	return nil, errors.New("原目录超过5000项，列表不完整，未提交操作")
}

func (m *Manager) PrepareTrash(ctx context.Context, inputs []TrashInput) ([]TrashPrepared, error) {
	if len(inputs) < 1 || len(inputs) > 50 {
		return nil, errors.New("请选择1至50项移入回收站")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(inputs[0].AccountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || !trashProvider(a) {
		return nil, errors.New("此账号不可用或不支持移入回收站")
	}
	client := NewHTTPUploader().Client
	live, e := trashBatchLive(ctx, client, a, inputs)
	if e != nil {
		return nil, e
	}
	result := []TrashPrepared{}
	seen := map[string]bool{}
	for _, in := range inputs {
		key := in.Entry.ID
		if a.Kind == "lanzou" && in.Entry.Folder {
			key = "folder:" + key
		}
		if in.AccountID != a.ID || in.ParentID != inputs[0].ParentID || seen[key] {
			return nil, errors.New("项目重复或来源不一致")
		}
		seen[key] = true
		var original RecycleEntry
		for _, r := range live {
			id := r.ID
			if a.Kind == "baidu" {
				id = r.OriginalPath
			}
			if id == in.Entry.ID && r.Name == in.Entry.Name && (r.Kind == "folder") == in.Entry.Folder {
				original = r
			}
		}
		s := trashSource{Purpose: "trash-v1", Scope: downloadScope(a), Input: in, Original: original}
		if a.Kind == "lanzou" {
			s.Purpose = lanzouTrashPurpose
			if in.Entry.Folder {
				s.Purpose = lanzouFolderTrashPurpose
			}
		}
		if a.Kind == "quark" {
			s.Purpose = quarkTrashPurpose
		}
		if !validTrashSource(s) || a.Kind != "lanzou" && !in.Entry.Folder && (in.Entry.Size == nil || original.Size == nil || *in.Entry.Size != *original.Size) {
			return nil, errors.New("项目已变化，请刷新目录重新选择；未发送删除请求")
		}
		if s.Purpose == lanzouFolderTrashPurpose {
			if e = checkLanzouLeafFolder(ctx, client, a, s); e != nil {
				return nil, e
			}
		}
		b, _ := json.Marshal(s)
		b, e = protect(b)
		if e != nil {
			return nil, e
		}
		result = append(result, TrashPrepared{Name: in.Entry.Name, Folder: in.Entry.Folder, Handle: base64.RawURLEncoding.EncodeToString(b)})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) {
		return nil, errors.New("账号来源已变化")
	}
	return result, nil
}
func decodeTrash(handle string) (trashSource, error) {
	var s trashSource
	if len(handle) > 32768 {
		return s, errors.New("操作标识无效")
	}
	b, e := base64.RawURLEncoding.DecodeString(handle)
	if e == nil {
		b, e = unprotect(b)
	}
	if e != nil || json.Unmarshal(b, &s) != nil || !validTrashSource(s) {
		return s, errors.New("操作标识无效，请重新选择")
	}
	return s, nil
}
func trashKey(s trashSource) string {
	id := s.Original.ID
	if s.Purpose == lanzouFolderTrashPurpose {
		id = "folder:" + id
	}
	return s.Scope + "\x00" + id
}
func (m *Manager) restoreTrash(rows []diskTrash) error {
	m.trashes = map[string]*trashOperation{}
	seen := map[string]bool{}
	if len(rows) > 1000 {
		return errors.New("回收站操作记录过多")
	}
	for _, d := range rows {
		r := d.Receipt
		if !validTrashSource(d.Source) || r.ID == "" || r.Created.IsZero() || r.AccountID != d.Source.Input.AccountID || r.Name != d.Source.Original.Name || r.Original != d.Source.Original.OriginalPath || m.trashes[r.ID] != nil || seen[trashKey(d.Source)] {
			return errors.New("回收站操作记录损坏或重复，未覆盖原数据")
		}
		if d.LanzouContents != nil && (!d.Submitted || d.Source.Purpose != lanzouFolderTrashPurpose || !validLanzouFolderContents(d.LanzouContents)) {
			return errors.New("蓝奏文件夹的本机原清单记录无效，未覆盖原数据")
		}
		seen[trashKey(d.Source)] = true
		switch r.State {
		case "working", "unknown":
			if d.Submitted {
				r.State = "unknown"
				r.Message = "上次结果待核对，不会再次提交"
			} else {
				r.State = "not_sent"
				r.Message = "上次尚未发送请求"
			}
		case "confirmed":
			if !d.Submitted {
				return errors.New("回收站操作记录矛盾")
			}
		case "not_sent":
			if d.Submitted {
				return errors.New("回收站操作记录矛盾")
			}
		default:
			return errors.New("回收站操作状态无效")
		}
		r.CanCheck = false
		d.Receipt = r
		m.trashes[r.ID] = &trashOperation{saved: d}
	}
	return nil
}

func checkTrash(ctx context.Context, client *http.Client, a Account, s trashSource) error {
	live, e := trashSourceLive(ctx, client, a, s)
	if e != nil {
		return e
	}
	for _, r := range live {
		if r.ID == s.Original.ID {
			return errors.New("原项目仍在原目录")
		}
	}
	if a.Kind == "lanzou" {
		return checkLanzouTrash(ctx, client, a, s)
	}
	if a.Kind == "quark" {
		return checkQuarkTrash(ctx, client, a, s)
	}
	seen := map[string]bool{}
	for page := 1; page <= 50; page++ {
		p, e := listAccountRecycle(ctx, client, a, page)
		if e != nil {
			return e
		}
		for _, r := range p.Items {
			if seen[r.ID] {
				return errors.New("回收站分页未推进")
			}
			seen[r.ID] = true
			if r.ID == s.Original.ID {
				if sameRecycleRestoreEntry(r, s.Original) {
					return nil
				}
				return errors.New("回收站同ID项目元数据与原项目不一致")
			}
		}
		if !p.HasMore {
			return errors.New("尚未在回收站核对到原ID项目")
		}
	}
	return errors.New("回收站核对超过5000项，结果不完整")
}
func (m *Manager) TrashEntry(ctx context.Context, handle string) (TrashReceipt, error) {
	s, e := decodeTrash(handle)
	if e != nil {
		return TrashReceipt{}, e
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(s.Input.AccountID)
	if m.closing || !trashAccount(a, s) {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("账号来源已变化或不可用")
	}
	for _, op := range m.trashes {
		if trashKey(op.saved.Source) == trashKey(s) {
			m.mu.Unlock()
			return TrashReceipt{}, errors.New("已有原项目操作记录，请先检查原结果；不会重复提交")
		}
	}
	if len(m.trashes) >= 1000 {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("操作记录已达1000条，请清理已完成记录")
	}
	if e = m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return TrashReceipt{}, e
	}
	op := &trashOperation{running: true, saved: diskTrash{Source: s, Receipt: TrashReceipt{ID: newID(), AccountID: a.ID, Name: s.Original.Name, Original: s.Original.OriginalPath, State: "working", Message: "正在预检查，尚未提交", Created: time.Now()}}}
	if m.trashes == nil {
		m.trashes = map[string]*trashOperation{}
	}
	m.trashes[op.saved.Receipt.ID] = op
	if m.saveLocked() != nil {
		delete(m.trashes, op.saved.Receipt.ID)
		delete(m.renaming, a.ID)
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("初始记录保存失败，未提交")
	}
	ctx, cancel := context.WithCancel(ctx)
	m.trashCancel = cancel
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	client := NewHTTPUploader().Client
	live, err := trashSourceLive(ctx, client, a, s)
	matched := false
	for _, r := range live {
		if sameRecycleRestoreEntry(r, s.Original) {
			matched = true
		}
	}
	if err == nil && !matched {
		err = errors.New("原项目身份或元数据已变化，请刷新后重选")
	}
	if err == nil && s.Purpose == lanzouFolderTrashPurpose {
		err = checkLanzouLeafFolder(ctx, client, a, s)
	}
	var contents *lanzouFolderContents
	if err == nil && s.Purpose == lanzouFolderTrashPurpose {
		contents = captureLanzouFolderContents(ctx, client, a, s.Original.ID)
	}
	if err == nil {
		m.mu.Lock()
		if m.closing || ctx.Err() != nil || !trashAccount(m.downloadAccountLocked(a.ID), s) {
			err = errors.New("操作取消或账号来源已变化")
		} else {
			op.saved.Submitted = true
			op.saved.LanzouContents = contents
			op.saved.Receipt.State = "unknown"
			op.saved.Receipt.Message = "提交检查点已保存，结果待核对"
			if m.saveLocked() != nil {
				op.saved.Submitted = false
				op.saved.LanzouContents = nil
				err = errors.New("提交检查点保存失败，未发送请求")
			}
		}
		m.mu.Unlock()
	}
	if err == nil {
		if a.Kind == "lanzou" {
			form := url.Values{"task": {"6"}, "file_id": {s.Original.ID}}
			if s.Purpose == lanzouFolderTrashPurpose {
				form = url.Values{"task": {"3"}, "folder_id": {s.Original.ID}}
			}
			_ = lanzouEditRequest(ctx, client, a, form, nil)
		} else if a.Kind == "pan123" {
			_ = submitPan123Trash(ctx, client, a, s.Original.ID)
		} else if a.Kind == "quark" {
			submitQuarkTrash(ctx, client, a, s.Original.ID)
		} else {
			c := newBaiduClient(client, a)
			if err = c.session(ctx); err == nil {
				_ = c.submitTrash(ctx, s.Input.Entry.ID)
			}
		}
		// Acknowledgements never establish success; only the same ID in recycle does.
		err = checkTrash(ctx, client, a, s)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel()
	m.trashCancel = nil
	delete(m.renaming, a.ID)
	op.running = false
	if err == nil {
		op.saved.Receipt.State = "confirmed"
		op.saved.Receipt.Message = "原目录已无原ID，回收站同ID、名称、位置、类型及可用大小已核对"
		if a.Kind == "lanzou" {
			op.saved.Receipt.Message = "原目录已无原项目，已在蓝奏回收站核对到同一类型、ID 和名称"
		}
	} else if op.saved.Submitted {
		op.saved.Receipt.State = "unknown"
		op.saved.Receipt.Message = "结果待核对，不要重复提交：" + err.Error()
	} else {
		op.saved.Receipt.State = "not_sent"
		op.saved.Receipt.Message = "未提交：" + err.Error()
	}
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
func (m *Manager) TrashHistory(accountID string) []TrashReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.downloadAccountLocked(accountID)
	rows := []TrashReceipt{}
	for _, op := range m.trashes {
		if op.saved.Receipt.AccountID != accountID {
			continue
		}
		r := op.saved.Receipt
		r.CanCheck = !op.running && op.saved.Submitted && r.State == "unknown" && downloadScope(a) == op.saved.Source.Scope
		if downloadScope(a) != op.saved.Source.Scope {
			r.State = "unknown"
			r.Message = "账号来源已改变，请在原账号官网核对"
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
	return rows
}
func (m *Manager) CheckTrash(ctx context.Context, id string) (TrashReceipt, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.trashes[id]
	if m.closing || op == nil {
		m.mu.Unlock()
		return TrashReceipt{}, errors.New("原操作记录不可用")
	}
	a := m.downloadAccountLocked(op.saved.Receipt.AccountID)
	if !trashAccount(a, op.saved.Source) {
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
	m.trashCancel = cancel
	op.running = true
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	err := checkTrash(ctx, NewHTTPUploader().Client, a, op.saved.Source)
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel()
	m.trashCancel = nil
	op.running = false
	delete(m.renaming, a.ID)
	if err == nil {
		op.saved.Receipt.State = "confirmed"
		op.saved.Receipt.Message = "已只读核对原项目在回收站，没有重新发送请求"
	} else {
		op.saved.Receipt.Message = "原结果仍未确认：" + err.Error()
	}
	if m.saveLocked() != nil {
		op.saved.Receipt.State = "unknown"
		op.saved.Receipt.Message = "核对记录保存失败，请勿重复提交"
	}
	r := op.saved.Receipt
	r.CanCheck = r.State == "unknown"
	return r, nil
}
func (m *Manager) ClearTrashHistory(accountID string) (int, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(accountID)
	removed := map[string]*trashOperation{}
	for id, op := range m.trashes {
		if op.saved.Receipt.AccountID == accountID && op.saved.Source.Scope == downloadScope(a) && !op.running && (op.saved.Receipt.State == "confirmed" || op.saved.Receipt.State == "not_sent") {
			removed[id] = op
			delete(m.trashes, id)
		}
	}
	if len(removed) > 0 && m.saveLocked() != nil {
		for id, op := range removed {
			m.trashes[id] = op
		}
		return 0, errors.New("清理保存失败，已保留原记录")
	}
	return len(removed), nil
}
