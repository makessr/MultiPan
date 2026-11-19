package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

func quarkRecycleKind(dir *bool, typ *int) string {
	if typ != nil && *typ != 0 && *typ != 1 {
		return "unknown"
	}
	if dir == nil && typ == nil || dir != nil && typ != nil && *dir != (*typ == 0) {
		return "unknown"
	}
	if dir != nil && *dir || typ != nil && *typ == 0 {
		return "folder"
	}
	return "file"
}

// Match the live fid, never the recycle record_id. Read the entire original
// parent so duplicate names or malformed pagination cannot establish success.
func quarkRestoreOriginal(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource) (bool, bool, error) {
	seen := map[string]bool{}
	same, occupied := false, false
	for page := 1; page <= 50; page++ {
		var data struct {
			List []struct {
				FID    string          `json:"fid"`
				Parent json.RawMessage `json:"pdir_fid"`
				Name   string          `json:"file_name"`
				Dir    *bool           `json:"dir"`
				Type   *int            `json:"file_type"`
				Size   *int64          `json:"size"`
			} `json:"list"`
		}
		q := url.Values{"pdir_fid": {h.Entry.ParentID}, "_page": {strconv.Itoa(page)}, "_size": {"100"}, "_fetch_total": {"1"}, "_fetch_sub_dirs": {"0"}, "_sort": {"file_name:asc"}}
		meta, e := newQuarkClient(client, a).request(ctx, "GET", "/file/sort", q, nil, &data)
		if e != nil {
			return false, false, e
		}
		if data.List == nil || len(data.List) > 100 {
			return false, false, errors.New("夸克原目录列表格式未确认")
		}
		var metadata struct {
			Total *int `json:"_total"`
		}
		if len(meta) > 0 && string(meta) != "null" && json.Unmarshal(meta, &metadata) != nil {
			return false, false, errors.New("夸克原目录分页信息无效")
		}
		for _, r := range data.List {
			kind := quarkRecycleKind(r.Dir, r.Type)
			name := html.UnescapeString(r.Name)
			if !validQuarkFolder(r.FID) || r.FID == "0" || seen[r.FID] || shareScalar(r.Parent) != h.Entry.ParentID || kind == "unknown" || name == "" || strings.ContainsAny(name, "/\\\x00\r\n") || r.Size != nil && *r.Size < 0 {
				return false, false, errors.New("夸克原目录条目来源、类型或分页未确认")
			}
			seen[r.FID] = true
			if name == h.Entry.Name {
				if occupied {
					return false, true, errors.New("夸克原目录存在多个同名项目")
				}
				occupied = true
				same = r.FID == h.Entry.FileID && kind == h.Entry.Kind && (kind == "folder" || r.Size != nil && h.Entry.Size != nil && *r.Size == *h.Entry.Size)
			}
		}
		if metadata.Total != nil && (*metadata.Total < 0 || *metadata.Total < len(seen)) {
			return false, false, errors.New("夸克原目录总数矛盾")
		}
		if metadata.Total != nil && len(seen) == *metadata.Total || len(data.List) < 100 && metadata.Total == nil {
			return same, occupied, nil
		}
		if len(data.List) < 100 {
			return false, false, errors.New("夸克原目录列表不完整")
		}
	}
	return false, false, errors.New("夸克原目录核对超过5000项，结果不完整")
}

// Preflight the whole selection without writes. Execution still validates each
// row again, because other clients can mutate the cloud after this check.
func (m *Manager) ValidateRecycleRestoreBatch(ctx context.Context, handles []string) error {
	return m.validateRecycleRestoreBatch(ctx, handles, nil)
}

func (m *Manager) ValidateRecycleRestoreBatchTo(ctx context.Context, handles []string, trail []CloudFolder) error {
	if len(trail) == 0 || len(trail) > 64 {
		return errors.New("还原目标目录无效，请重新选择")
	}
	return m.validateRecycleRestoreBatch(ctx, handles, trail)
}

func (m *Manager) validateRecycleRestoreBatch(ctx context.Context, handles []string, trail []CloudFolder) error {
	if len(handles) < 1 || len(handles) > 50 {
		return errors.New("每次还原请选择1至50项")
	}
	sources := []recycleRestoreSource{}
	seen, names, files := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, token := range handles {
		h, e := decodeRecycleRestore(token)
		if e != nil {
			return e
		}
		if len(trail) > 0 {
			h.DestinationID = trail[len(trail)-1].ID
			if h.DestinationID == "" || h.Purpose != "pan123-recycle-restore-v1" || !validRecycleRestoreSource(h) {
				return errors.New("有项目无法还原到所选目录，整批未提交")
			}
			for _, folder := range trail {
				if folder.ID == h.Entry.ID {
					return errors.New("不能将项目还原到自身或其子目录，整批未提交")
				}
			}
			for _, previous := range sources {
				if strings.EqualFold(previous.Entry.Name, h.Entry.Name) {
					return errors.New("所选项目在目标目录会发生同名冲突，整批未提交")
				}
				if previous.Entry.ID == h.Entry.ParentID || h.Entry.ID == previous.Entry.ParentID {
					return errors.New("请将文件夹与其直接子项分开还原，整批未提交")
				}
			}
		}
		nameKey := h.Entry.OriginalPath
		if h.Purpose == "baidu-recycle-restore-v1" {
			nameKey = strings.ToLower(nameKey)
		}
		if isLanzouRestorePurpose(h.Purpose) {
			for _, previous := range sources {
				if lanzouRestoreParent(previous.Entry) == lanzouRestoreParent(h.Entry) && strings.EqualFold(previous.Entry.Name, h.Entry.Name) {
					return errors.New("所选蓝奏项目的还原位置存在同名冲突，整批未提交")
				}
			}
		}
		fileKey := h.Entry.ID
		if h.Entry.FileID != "" && !isLanzouRestorePurpose(h.Purpose) {
			fileKey = h.Entry.FileID
		}
		samePurpose := len(sources) == 0 || h.Purpose == sources[0].Purpose || isLanzouRestorePurpose(h.Purpose) && isLanzouRestorePurpose(sources[0].Purpose)
		if seen[h.Entry.ID] || nameKey != "" && names[nameKey] || files[fileKey] || !samePurpose || len(sources) > 0 && (h.AccountID != sources[0].AccountID || h.Scope != sources[0].Scope) {
			return errors.New("还原项目重复、目标同名或账号来源不一致")
		}
		seen[h.Entry.ID] = true
		files[fileKey] = true
		names[nameKey] = true
		sources = append(sources, h)
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(sources[0].AccountID)
	closing := m.closing
	for _, h := range sources {
		if a.Kind == "lanzou" && !m.lanzouCanRestoreLocked(a, h) {
			m.mu.Unlock()
			return errors.New("蓝奏原移入记录无法确认或仍待核对，整批还原未提交")
		}
		if m.pendingPurgeLocked(h.Scope, h.Entry.ID) || m.pendingGuangyaEmptyLocked(h.Scope) {
			m.mu.Unlock()
			return errors.New("所选项目永久删除结果待核对，整批还原未提交")
		}
		for _, op := range m.recycleRestores {
			if op.saved.Source.Scope == h.Scope && op.saved.Source.Entry.ID == h.Entry.ID {
				m.mu.Unlock()
				return errors.New("所选项目已有本机还原记录，请先核对原结果")
			}
		}
	}
	m.mu.Unlock()
	if closing || !recycleRestoreAccount(a, sources[0]) {
		return errors.New("账号来源已变化或不可用")
	}
	pages := map[int]RecyclePage{}
	parents := map[string][]RecycleEntry{}
	client := NewHTTPUploader().Client
	if len(trail) > 0 {
		if err := validate123RestoreBatchDestination(ctx, client, a, sources, trail); err != nil {
			return err
		}
	}
	for _, h := range sources {
		p, ok := pages[h.Page]
		if !ok {
			var e error
			p, e = listAccountRecycle(ctx, client, a, h.Page)
			if e != nil {
				return e
			}
			if p.Incomplete {
				return errors.New("回收站列表不完整，整批还原未提交")
			}
			pages[h.Page] = p
		}
		match := false
		for _, r := range p.Items {
			if sameRecycleRestoreRow(r, h.Entry) {
				match = true
			}
		}
		if !match {
			return errors.New("所选原记录已变化，请刷新回收站；整批未提交")
		}
		if len(trail) > 0 {
			continue
		}
		occupied := false
		var e error
		if a.Kind == "quark" || a.Kind == "guangya" || a.Kind == "lanzou" {
			_, occupied, e = recycleRestoreOriginal(ctx, client, a, h)
		} else {
			parent := h.Entry.ParentID
			if a.Kind == "baidu" {
				parent = path.Dir(h.Entry.OriginalPath)
			}
			live, ok := parents[parent]
			if !ok {
				live, e = trashLive(ctx, client, a, parent)
				if e == nil {
					parents[parent] = live
				}
			}
			for _, r := range live {
				if r.Name == h.Entry.Name || a.Kind == "baidu" && strings.EqualFold(r.Name, h.Entry.Name) {
					occupied = true
				}
			}
		}
		if e != nil || occupied {
			return errors.New("有项目的还原位置无法完整读取或已有同名项目；整批未提交")
		}
		if a.Kind == "lanzou" {
			if e = checkLanzouRecycleConfirmation(ctx, client, a, h.Entry.ID, lanzouRestore); e != nil {
				return e
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || !recycleRestoreAccount(m.downloadAccountLocked(a.ID), sources[0]) {
		return errors.New("账号来源已变化，整批未提交")
	}
	return nil
}
