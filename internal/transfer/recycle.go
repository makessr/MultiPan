package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// RecycleEntry is display metadata only. It is not a restore/delete handle.
type RecycleEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Size          *int64 `json:"size"`
	OriginalPath  string `json:"originalPath"`
	RestoreHandle string `json:"restoreHandle"`
	ParentID      string `json:"parentId,omitempty"`
	FileID        string `json:"fileId,omitempty"` // Quark record_id is distinct from the original fid.
}
type RecyclePage struct {
	Total      *int64         `json:"total,omitempty"`
	Incomplete bool           `json:"incomplete"`
	BrowseOnly bool           `json:"browseOnly,omitempty"`
	Items      []RecycleEntry `json:"items"`
	Page       int            `json:"page"`
	HasMore    bool           `json:"hasMore"`
}

const recyclePageSize = 100

func (m *Manager) ListRecycle(ctx context.Context, accountID string, page int) (RecyclePage, error) {
	a, e := m.recycleListingAccount(accountID)
	if e != nil {
		return RecyclePage{}, e
	}
	result, e := listAccountRecyclePage(ctx, NewHTTPUploader().Client, a, page, true)
	if e != nil {
		return RecyclePage{}, e
	}
	current, e := m.recycleListingAccount(accountID)
	if e != nil || downloadScope(current) != downloadScope(a) {
		return RecyclePage{}, errors.New("账号来源已变化，请重新读取回收站")
	}
	if a.Kind == "lanzou" {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) {
			return RecyclePage{}, errors.New("账号来源已变化，请重新读取回收站")
		}
		for i, original := range result.Items {
			row, ok := m.lanzouRestoreOriginLocked(a, original)
			if !ok && !result.Incomplete {
				row, ok = lanzouListedRestoreFile(original)
			}
			if ok {
				row.RestoreHandle, e = sealRecycleRestore(a, page, row)
				if e == nil {
					result.Items[i] = row
				}
			}
		}
	}
	if !result.Incomplete && !result.BrowseOnly && (a.Kind == "baidu" || a.Kind == "pan123" || a.Kind == "quark" || a.Kind == "guangya") {
		for i := range result.Items {
			handle, e := sealRecycleRestore(a, page, result.Items[i])
			if e == nil {
				result.Items[i].RestoreHandle = handle
			}
		}
	}
	return result, nil
}

func (m *Manager) recycleListingAccount(id string) (Account, error) {
	m.mu.Lock()
	a := m.downloadAccountLocked(id)
	closing := m.closing
	m.mu.Unlock()
	if !closing && a.ID != "" && a.Kind == "lanzou" && a.Endpoint == lanzouRecycleOrigin {
		return a, nil
	}
	return m.managedShareAccount(id)
}

func pan123RecycleFieldKind(raw json.RawMessage) string {
	switch string(raw) {
	case "":
		return "missing"
	case "null":
		return "null"
	case "0":
		return "zero"
	case "-1", `"-1"`:
		return "end"
	case "true", "false":
		return "boolean"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	default:
		return "number"
	}
}

func pan123RecycleFormatError(reason string, data, list, total, next, length json.RawMessage) error {
	return fmt.Errorf("123 回收站返回的列表格式无法识别。可复制诊断后反馈。 [RECYCLE/pan123/list] RECYCLE123 data=%s list=%s total=%s next=%s len=%s reason=%s",
		pan123RecycleFieldKind(data), pan123RecycleFieldKind(list), pan123RecycleFieldKind(total), pan123RecycleFieldKind(next), pan123RecycleFieldKind(length), reason)
}

func listAccountRecycle(ctx context.Context, client *http.Client, a Account, page int) (RecyclePage, error) {
	return listAccountRecyclePage(ctx, client, a, page, false)
}

func listAccountRecyclePage(ctx context.Context, client *http.Client, a Account, page int, forBrowsing bool) (RecyclePage, error) {
	result := RecyclePage{Items: []RecycleEntry{}, Page: page}
	if page < 1 || page > 1000 {
		return RecyclePage{}, errors.New("回收站页码无效")
	}
	if a.Kind == "lanzou" {
		return readLanzouRecycle(ctx, client, a, page)
	}
	var rows json.RawMessage
	var total *int64
	var next json.RawMessage
	var pan123Payload, pan123Count, pan123Length json.RawMessage
	var invalidPan123Total bool
	switch {
	case a.Kind == "quark" && a.Endpoint == QuarkAPI:
		var data struct {
			List  json.RawMessage `json:"list"`
			Total *int64          `json:"total"`
		}
		meta, e := newQuarkClient(client, a).request(ctx, "GET", "/file/recycle/list", url.Values{"_page": {strconv.Itoa(page)}, "_size": {"100"}, "_fetch_total": {"1"}}, nil, &data)
		if e != nil {
			return RecyclePage{}, e
		}
		var metadata struct {
			Total *int64 `json:"_total"`
		}
		if len(meta) > 0 && string(meta) != "null" {
			if e = json.Unmarshal(meta, &metadata); e != nil {
				return RecyclePage{}, errors.New("夸克回收站分页信息无效")
			}
		}
		total = metadata.Total
		if data.Total != nil {
			if total != nil && *total != *data.Total {
				return RecyclePage{}, errors.New("夸克回收站总数矛盾")
			}
			total = data.Total
		}
		rows = data.List
	case a.Kind == "pan123" && a.Endpoint == Pan123API:
		var data struct {
			List   json.RawMessage `json:"InfoList"`
			Total  json.RawMessage `json:"Total"`
			Next   json.RawMessage `json:"Next"`
			Length json.RawMessage `json:"Len"`
		}
		q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {"0"}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "parentFileId": {"0"}, "trashed": {"true"}, "SearchData": {""}, "Page": {strconv.Itoa(page)}, "OnlyLookAbnormalFile": {"0"}, "inDirectSpace": {"false"}}
		if e := (pan123Client{client, a}).request(ctx, "GET", "/file/list/new", q, nil, &pan123Payload); e != nil {
			// The official recycle page explicitly treats business code 5007 as empty.
			var business *pan123BusinessError
			if forBrowsing && page == 1 && errors.As(e, &business) && business.code == 5007 {
				empty := int64(0)
				result.Total = &empty
				return result, nil
			}
			return RecyclePage{}, e
		}
		if e := json.Unmarshal(pan123Payload, &data); e != nil {
			return RecyclePage{}, pan123RecycleFormatError("data", pan123Payload, nil, nil, nil, nil)
		}
		rows, next, pan123Count, pan123Length = data.List, data.Next, data.Total, data.Length
		invalidPan123Total = len(data.Total) != 0 && json.Unmarshal(data.Total, &total) != nil
		if invalidPan123Total {
			total = nil
		}
		// A terminal marker is enough to display a missing first page when
		// count metadata agrees or is omitted. Keep write verification strict.
		marker := strings.Trim(string(next), "\"")
		consistentLength := len(data.Length) == 0 || string(data.Length) == "null" || string(data.Length) == "0"
		explicitEmptyCount := total != nil && *total == 0 && (len(next) == 0 || string(next) == "null" || marker == "-1")
		if forBrowsing && page == 1 && !invalidPan123Total && consistentLength && (len(rows) == 0 || string(rows) == "null") &&
			(explicitEmptyCount || total == nil && marker == "-1") {
			empty := int64(0)
			result.Total = &empty
			return result, nil
		}
	case a.Kind == "baidu" && a.Endpoint == BaiduAPI:
		c := newBaiduClient(client, a)
		if e := c.session(ctx); e != nil {
			return RecyclePage{}, e
		}
		var data struct {
			List  json.RawMessage `json:"list"`
			Total *int64          `json:"total"`
		}
		if e := c.api(ctx, "GET", "recycle/list", url.Values{"start": {strconv.Itoa((page - 1) * recyclePageSize)}, "page": {strconv.Itoa(page)}, "num": {"100"}, "dir": {"/"}, "order": {"time"}, "desc": {"1"}}, nil, &data); e != nil {
			return RecyclePage{}, e
		}
		rows = data.List
		total = data.Total
	case a.Kind == "guangya" && a.Endpoint == GuangyaAPI:
		var data struct {
			List  json.RawMessage `json:"list"`
			Total *int64          `json:"total"`
		}
		// The website uses the regular listing endpoint with the recycle view selected.
		body := struct {
			ParentID string `json:"parentId"`
			Page     int    `json:"page"`
			PageSize int    `json:"pageSize"`
			DirType  int    `json:"dirType"`
			OrderBy  int    `json:"orderBy"`
			SortType int    `json:"sortType"`
		}{ParentID: "", Page: page - 1, PageSize: recyclePageSize, DirType: 4, OrderBy: 12, SortType: 1}
		if _, e := (gyClient{client, a}).call(ctx, "/userres/v1/file/get_file_list", body, &data); e != nil {
			return RecyclePage{}, e
		}
		if data.Total == nil {
			return RecyclePage{}, errors.New("光鸭未返回回收站总数，请刷新或到官网核对")
		}
		rows = data.List
		total = data.Total
	default:
		return RecyclePage{}, errors.New("回收站浏览当前支持夸克、123、百度、蓝奏和光鸭")
	}
	var records []json.RawMessage
	listErr := json.Unmarshal(rows, &records)
	if a.Kind == "pan123" && forBrowsing && page == 1 && pan123RecycleFieldKind(next) == "end" && listErr == nil && len(records) > 0 && len(records) <= 1000 {
		result.BrowseOnly = !invalidPan123Total && len(records) > recyclePageSize || invalidPan123Total && pan123RecycleFieldKind(pan123Count) == "number"
	}
	if invalidPan123Total && !result.BrowseOnly {
		return RecyclePage{}, pan123RecycleFormatError("total", pan123Payload, rows, pan123Count, next, pan123Length)
	}
	if listErr != nil || records == nil || len(records) > recyclePageSize && !result.BrowseOnly {
		if a.Kind == "pan123" {
			return RecyclePage{}, pan123RecycleFormatError("list", pan123Payload, rows, pan123Count, next, pan123Length)
		}
		return RecyclePage{}, fmt.Errorf("回收站列表格式未确认 [RECYCLE/%s/list]，请刷新或到官网核对", a.Kind)
	}
	seen := map[string]bool{}
	for _, raw := range records {
		var entry RecycleEntry
		var e error
		switch a.Kind {
		case "quark":
			var r struct {
				ID       json.RawMessage `json:"record_id"`
				FID      string          `json:"fid"`
				Name     string          `json:"file_name"`
				Dir      *bool           `json:"dir"`
				Size     *int64          `json:"size"`
				Parent   json.RawMessage `json:"pdir_fid"`
				FileType *int            `json:"file_type"`
			}
			e = json.Unmarshal(raw, &r)
			entry = RecycleEntry{ID: shareScalar(r.ID), FileID: r.FID, ParentID: shareScalar(r.Parent), Name: html.UnescapeString(r.Name), Size: r.Size, Kind: quarkRecycleKind(r.Dir, r.FileType)}
			if validQuarkFolder(entry.ParentID) {
				entry.OriginalPath = "原目录 ID " + entry.ParentID + " / " + entry.Name
			}
		case "pan123":
			var r struct {
				Parent json.RawMessage `json:"ParentFileId"`
				ID     json.RawMessage `json:"FileId"`
				Name   string          `json:"FileName"`
				Type   *int            `json:"Type"`
				Size   *int64          `json:"Size"`
			}
			e = json.Unmarshal(raw, &r)
			entry = RecycleEntry{ID: shareScalar(r.ID), Name: r.Name, Size: r.Size, Kind: "unknown"}
			entry.ParentID = shareScalar(r.Parent)
			if entry.ParentID != "" && valid123Folder(entry.ParentID) {
				entry.OriginalPath = "原目录 ID " + entry.ParentID + " / " + entry.Name
			}
			if r.Type != nil {
				if *r.Type == 0 {
					entry.Kind = "file"
				} else if *r.Type == 1 {
					entry.Kind = "folder"
				} else {
					e = errors.New("unknown type")
				}
			}
		case "baidu":
			var r struct {
				ID   json.RawMessage `json:"fs_id"`
				Name string          `json:"server_filename"`
				Dir  *int            `json:"isdir"`
				Size *int64          `json:"size"`
				Path string          `json:"path"`
			}
			e = json.Unmarshal(raw, &r)
			entry = RecycleEntry{ID: shareScalar(r.ID), Name: r.Name, Size: r.Size, Kind: "unknown"}
			if r.Dir != nil {
				if *r.Dir == 0 {
					entry.Kind = "file"
				} else if *r.Dir == 1 {
					entry.Kind = "folder"
				} else {
					e = errors.New("unknown type")
				}
			}
			if r.Path != "" {
				if !validBaiduPath(r.Path) {
					e = errors.New("invalid original path")
				} else {
					entry.OriginalPath = r.Path
				}
			}
		case "guangya":
			var r struct {
				ID     string  `json:"fileId"`
				Name   string  `json:"fileName"`
				Type   *int    `json:"resType"`
				Size   *int64  `json:"fileSize"`
				Parent *string `json:"parentId"`
			}
			e = json.Unmarshal(raw, &r)
			entry = RecycleEntry{ID: r.ID, Name: r.Name, Size: r.Size, Kind: "unknown"}
			if r.Type != nil {
				switch *r.Type {
				case 1:
					entry.Kind = "file"
				case 2:
					entry.Kind = "folder"
				default:
					e = errors.New("unknown type")
				}
			}
			if !validQuarkFolder(r.ID) || r.ID == guangyaRoot || strings.ContainsAny(r.Name, "\x00\r\n") {
				e = errors.New("invalid recycle entry")
			}
			if r.Parent != nil && (*r.Parent == "" || *r.Parent != guangyaRoot && validGuangyaRestoreParent(*r.Parent, r.ID)) {
				entry.ParentID = *r.Parent
				if entry.ParentID == "" {
					entry.ParentID = guangyaRoot
				}
				entry.OriginalPath = guangyaRestorePath(entry.ParentID, r.Name)
			}
		}
		if e != nil || entry.ID == "" || entry.ID == "0" || !shareToken.MatchString(entry.ID) || seen[entry.ID] || strings.TrimSpace(entry.Name) == "" || len(entry.Name) > 4096 || entry.Size != nil && *entry.Size < 0 {
			return RecyclePage{}, fmt.Errorf("回收站条目格式未确认 [RECYCLE/%s/entry]，请到官网核对", a.Kind)
		}
		seen[entry.ID] = true
		result.Items = append(result.Items, entry)
	}
	if result.BrowseOnly {
		return result, nil
	}
	result.HasMore = len(records) == recyclePageSize
	result.Total = total
	if total != nil {
		if *total < 0 {
			return RecyclePage{}, errors.New("回收站总数无效")
		}
		end := int64((page-1)*recyclePageSize + len(records))
		result.HasMore = end < *total
		result.Incomplete = end > *total || len(records) < recyclePageSize && result.HasMore
	}
	if a.Kind == "pan123" && len(next) > 0 && string(next) != "null" {
		value := strings.Trim(string(next), "\"")
		if value != "-1" && !valid123Folder(value) {
			return RecyclePage{}, errors.New("123回收站分页信息无效")
		}
		if value == "-1" {
			if result.HasMore && total != nil {
				result.Incomplete = true
			}
			result.HasMore = false
		} else if len(records) > 0 && total == nil {
			result.HasMore = true
		}
	}
	if len(records) == 0 {
		result.HasMore = false
	}
	if page >= 1000 {
		if result.HasMore {
			result.Incomplete = true
		}
		result.HasMore = false
	}
	return result, nil
}
