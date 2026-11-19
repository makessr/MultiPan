package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

func relayDirectoryProvider(a Account) bool {
	return relayProvider(a) || a.Kind == "pan123" && a.Endpoint == Pan123API || a.Kind == "baidu" && a.Endpoint == BaiduAPI ||
		a.Kind == "quark" && a.Endpoint == QuarkAPI || a.Kind == "guangya" && a.Endpoint == GuangyaAPI
}
func relayDirectorySavedAccount(kind string) Account {
	a := Account{Kind: kind}
	if kind == "pan123" {
		a.Endpoint = Pan123API
	}
	if kind == "baidu" {
		a.Endpoint = BaiduAPI
	}
	if kind == "guangya" {
		a.Endpoint = GuangyaAPI
	}
	if kind == "quark" {
		a.Endpoint = QuarkAPI
	}
	return a
}
func relayDirectoryStamp(kind string, row CloudEntry) string {
	if !row.Folder {
		return ""
	}
	switch kind {
	case "quark", "guangya":
		if validGuangyaCopyFileID(row.ID) {
			return "directory-" + kind + "-v1"
		}
	case "pan123":
		if valid123Folder(row.ID) && row.ID != "0" {
			return "directory-123-v1"
		}
	case "baidu":
		if valid123Folder(row.ObjectID) && row.ObjectID != "0" {
			return "directory-baidu-v1:" + row.ObjectID
		}
	case "webdav", "ctdav":
		return "directory-v1"
	}
	return ""
}
func validRelayDirectoryStamp(kind, stamp string) bool {
	switch kind {
	case "quark", "guangya":
		return stamp == "directory-"+kind+"-v1"
	case "pan123":
		return stamp == "directory-123-v1"
	case "baidu":
		id := strings.TrimPrefix(stamp, "directory-baidu-v1:")
		return stamp != "" && id != stamp && valid123Folder(id) && id != "0"
	case "webdav", "ctdav":
		return stamp == "directory-v1"
	}
	return false
}

// These readers use existing read protocols, with a required explicit list and
// strict page accounting. Ordinary browsing and file transfer remain unchanged.
func relayDirectoryPage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	if relayProvider(a) {
		return listDAVFiles(ctx, client, a, parent, true)
	}
	if a.Kind == "guangya" && a.Endpoint == GuangyaAPI {
		return readGuangyaRelayPage(ctx, client, a, parent, true)
	}
	if a.Kind == "quark" && a.Endpoint == QuarkAPI {
		return relayQuarkDirectoryPage(ctx, client, a, parent)
	}
	result := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	seen, names := map[string]bool{}, map[string]bool{}
	objects := map[string]bool{}
	add := func(row CloudEntry) error {
		key := strings.ToLower(row.Name)
		if !relayName(row.Name) || seen[row.ID] || names[key] || row.ObjectID != "" && objects[row.ObjectID] {
			return errors.New("源目录有无效或重复项目，预检不完整")
		}
		seen[row.ID] = true
		if row.ObjectID != "" {
			objects[row.ObjectID] = true
		}
		names[key] = true
		result.Entries = append(result.Entries, row)
		return nil
	}
	if a.Kind == "pan123" && a.Endpoint == Pan123API && valid123Folder(parent) {
		totalKnown := -1
		for page := 1; page <= 5; page++ {
			var data struct {
				Next  json.RawMessage `json:"Next"`
				Total *int            `json:"Total"`
				Rows  *[]struct {
					ID     json.Number `json:"FileId"`
					Parent json.Number `json:"ParentFileId"`
					Name   string      `json:"FileName"`
					Type   *int        `json:"Type"`
					Size   *int64      `json:"Size"`
					MD5    string      `json:"Etag"`
				} `json:"InfoList"`
			}
			q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {"0"}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "parentFileId": {parent}, "trashed": {"false"}, "SearchData": {""}, "Page": {strconv.Itoa(page)}, "OnlyLookAbnormalFile": {"0"}, "event": {"homeListFile"}, "operateType": {"4"}, "inDirectSpace": {"false"}}
			if e := (pan123Client{client, a}).request(ctx, "GET", "/file/list/new", q, nil, &data); e != nil {
				return FilePage{}, e
			}
			if data.Rows == nil || len(*data.Rows) > 100 {
				return FilePage{}, errors.New("123目录缺少明确清单或超出分页大小，预检不完整")
			}
			if data.Total != nil {
				if *data.Total < 0 || totalKnown >= 0 && *data.Total != totalKnown {
					return FilePage{}, errors.New("123分页总数变化，预检不完整")
				}
				totalKnown = *data.Total
			}
			for _, r := range *data.Rows {
				if !valid123Folder(string(r.ID)) || r.ID == "0" || r.ID == json.Number(parent) || string(r.Parent) != parent || r.Type == nil || (*r.Type != 0 && *r.Type != 1) {
					return FilePage{}, errors.New("123目录缺少可靠ID/父目录/类型，预检不完整")
				}
				if e := add(CloudEntry{ID: string(r.ID), Name: r.Name, Folder: *r.Type == 1, Size: r.Size, revision: strings.ToLower(r.MD5)}); e != nil {
					return FilePage{}, e
				}
			}
			next := strings.Trim(string(data.Next), "\"")
			count := len(result.Entries)
			if totalKnown >= 0 && count > totalKnown {
				return FilePage{}, errors.New("123分页项目数矛盾，预检不完整")
			}
			if next == "-1" || totalKnown >= 0 && count == totalKnown {
				if totalKnown >= 0 && count != totalKnown {
					return FilePage{}, errors.New("123分页提前结束，预检不完整")
				}
				return result, nil
			}
			if len(*data.Rows) == 0 || next != "0" {
				return FilePage{}, errors.New("123分页没有完整结束依据，预检不完整")
			}
			select {
			case <-ctx.Done():
				return FilePage{}, ctx.Err()
			case <-time.After(700 * time.Millisecond):
			}
		}
		return FilePage{}, errors.New("123单目录预检超过500条，预检不完整")
	}
	if a.Kind == "baidu" && a.Endpoint == BaiduAPI && validBaiduPath(parent) {
		c := newBaiduClient(client, a)
		for page := 1; page <= 2; page++ {
			var data struct {
				Rows *[]baiduEntry `json:"list"`
			}
			q := url.Values{"dir": {parent}, "page": {strconv.Itoa(page)}, "num": {"1000"}, "order": {"name"}, "desc": {"0"}, "showempty": {"0"}}
			if e := c.api(ctx, "GET", "list", q, nil, &data); e != nil {
				return FilePage{}, e
			}
			if data.Rows == nil || len(*data.Rows) > 1000 {
				return FilePage{}, errors.New("百度目录缺少明确清单或超出分页大小，预检不完整")
			}
			for _, r := range *data.Rows {
				if !validBaiduPath(r.Path) || r.Path == parent || path.Dir(r.Path) != parent || r.Name != path.Base(r.Path) || r.Dir == nil || (*r.Dir != 0 && *r.Dir != 1) || !valid123Folder(string(r.ID)) || r.ID == "0" {
					return FilePage{}, errors.New("百度目录缺少可靠路径/ID/类型，预检不完整")
				}
				if e := add(CloudEntry{ID: r.Path, ObjectID: string(r.ID), Name: r.Name, Folder: *r.Dir == 1, Size: r.Size, revision: string(r.ID) + ":" + strings.ToLower(r.MD5)}); e != nil {
					return FilePage{}, e
				}
			}
			if len(*data.Rows) < 1000 {
				return result, nil
			}
			select {
			case <-ctx.Done():
				return FilePage{}, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
		return FilePage{}, errors.New("百度单目录预检超过2000条，预检不完整")
	}
	return FilePage{}, errors.New("此来源不支持目录中转")
}

// Re-read every captured ancestor identity before transfer/creation and after
// target readback. A Baidu pathname alone is not a directory identity.
func (m *Manager) relayDirectoryAncestors(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	m.mu.Lock()
	chain := []diskRelay{}
	next := d.ParentJobID
	for next != "" {
		p := m.relayByIDLocked(next)
		if p == nil || !p.Folder || len(chain) >= 8 {
			m.mu.Unlock()
			return errors.New("目录依赖不完整，未继续")
		}
		chain = append(chain, *p)
		next = p.ParentJobID
	}
	m.mu.Unlock()
	for i := len(chain) - 1; i >= 0; i-- {
		if e := relayDirectorySource(ctx, client, a, chain[i]); e != nil {
			return e
		}
	}
	return nil
}
