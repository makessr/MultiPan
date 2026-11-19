package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type CloudFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type FolderPage struct {
	CurrentID   string        `json:"currentId"`
	Folders     []CloudFolder `json:"folders"`
	Breadcrumbs []CloudFolder `json:"breadcrumbs"`
}

func validFolderID(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && (n == -1 || n > 0)
}
func folderID(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	if !validFolderID(s) {
		return "", errors.New("网盘返回了无效的目录 ID")
	}
	return s, nil
}

func ListLanzouFolders(ctx context.Context, client *http.Client, a Account, parent string) (FolderPage, error) {
	return listLanzouFolders(ctx, client, a, parent, false)
}

func listLanzouFolders(ctx context.Context, client *http.Client, a Account, parent string, strict bool) (FolderPage, error) {
	page := FolderPage{CurrentID: parent, Folders: []CloudFolder{}, Breadcrumbs: []CloudFolder{{ID: "-1", Name: "根目录"}}}
	if !validFolderID(parent) {
		return page, errors.New("目标目录无效")
	}
	values := url.Values{"task": {"47"}, "folder_id": {parent}}
	req, e := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/doupload.php", strings.NewReader(values.Encode()))
	if e != nil {
		return page, errors.New("目录请求地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", userAgent(a))
	req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return page, errors.New("读取目录超时或已取消，请重试")
		}
		return page, errors.New("连接蓝奏云失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return page, statusError(resp.StatusCode)
	}
	var result struct {
		ZT   json.RawMessage `json:"zt"`
		Text json.RawMessage `json:"text"`
		Info json.RawMessage `json:"info"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result) != nil {
		return page, errors.New("目录数据无法识别，登录可能已失效")
	}
	code := strings.Trim(string(result.ZT), "\"")
	if code == "9" {
		return page, errors.New("蓝奏云登录已失效，请重新登录")
	}
	if code != "1" && code != "2" {
		return page, errors.New("无法读取此目录，请确认目录仍存在且账号有访问权限")
	}
	var folders []struct {
		ID   json.RawMessage `json:"fol_id"`
		Name string          `json:"name"`
	}
	if strict && len(result.Text) == 0 {
		return page, errors.New("蓝奏未返回明确的子文件夹清单")
	}
	if len(result.Text) > 0 && string(result.Text) != "null" && string(result.Text) != "\"\"" {
		if json.Unmarshal(result.Text, &folders) != nil {
			return page, errors.New("文件夹列表格式不兼容，请刷新或更新客户端")
		}
	}
	if strict && code == "2" && len(folders) != 0 {
		return page, errors.New("蓝奏子文件夹清单的状态与内容不一致")
	}
	seen := map[string]bool{}
	for _, f := range folders {
		id, e := folderID(f.ID)
		if e != nil || id == "-1" || id == parent || seen[id] {
			if strict {
				return page, errors.New("蓝奏子文件夹 ID 缺失、重复或无效")
			}
			continue
		}
		name := html.UnescapeString(f.Name)
		if strict && (!valid123Folder(id) || id == "0" || !relayName(name)) {
			return page, errors.New("蓝奏子文件夹标识无法核对")
		}
		seen[id] = true
		page.Folders = append(page.Folders, CloudFolder{ID: id, Name: name})
	}
	var crumbs []struct {
		ID   json.RawMessage `json:"folderid"`
		Name string          `json:"name"`
	}
	if len(result.Info) > 0 && string(result.Info) != "null" && json.Unmarshal(result.Info, &crumbs) == nil {
		for _, c := range crumbs {
			id, e := folderID(c.ID)
			if strict && (e != nil || id != "-1" && (!valid123Folder(id) || id == "0" || !relayName(html.UnescapeString(c.Name)))) {
				return page, errors.New("蓝奏目录路径无法核对")
			}
			if e == nil && id != "-1" {
				page.Breadcrumbs = append(page.Breadcrumbs, CloudFolder{ID: id, Name: html.UnescapeString(c.Name)})
			}
		}
	} else if strict {
		return page, errors.New("蓝奏未返回可核对的目录路径")
	}
	if strict && (parent == "-1" && len(page.Breadcrumbs) != 1 ||
		parent != "-1" && (len(page.Breadcrumbs) == 1 || page.Breadcrumbs[len(page.Breadcrumbs)-1].ID != parent)) {
		return page, errors.New("蓝奏返回的目录与请求不一致")
	}
	if parent != "-1" && (len(page.Breadcrumbs) == 1 || page.Breadcrumbs[len(page.Breadcrumbs)-1].ID != parent) {
		page.Breadcrumbs = append(page.Breadcrumbs, CloudFolder{ID: parent, Name: "目录 " + parent})
	}
	return page, nil
}
func (m *Manager) ListFolders(ctx context.Context, id, parent string) (FolderPage, error) {
	m.mu.Lock()
	var account Account
	for _, a := range m.accounts {
		if a.ID == id {
			account = a
			break
		}
	}
	m.mu.Unlock()
	if account.ID == "" {
		return FolderPage{}, errors.New("账号不存在，请先保存账号")
	}
	if account.Kind == "quark" {
		return ListQuarkFolders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind == "pan123" {
		return List123Folders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind == "ctdav" || account.Kind == "webdav" {
		return ListDAVFolders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind == "guangya" {
		return ListGuangyaFolders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind == "baidu" {
		return ListBaiduFolders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind == "ctfile" {
		return ListCtFolders(ctx, NewHTTPUploader().Client, account, parent)
	}
	if account.Kind != "lanzou" {
		return FolderPage{}, errors.New("此目录浏览器暂时支持蓝奏云")
	}
	return ListLanzouFolders(ctx, NewHTTPUploader().Client, account, parent)
}
func (m *Manager) SetDefaultFolder(id, folder, display string) error {
	display = strings.TrimSpace(display)
	if display == "" || len(display) > 4096 {
		return errors.New("目录路径无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.renaming[id] {
		return errors.New("此网盘正在修改网盘文件，请稍后设置目录")
	}
	for i := range m.accounts {
		a := &m.accounts[i]
		if a.ID != id {
			continue
		}
		if a.Kind != "lanzou" && a.Kind != "quark" && a.Kind != "pan123" && a.Kind != "baidu" && a.Kind != "ctfile" && a.Kind != "ctdav" && a.Kind != "webdav" && a.Kind != "guangya" {
			return errors.New("此操作暂时支持蓝奏云")
		}
		if (a.Kind == "lanzou" && !validFolderID(folder)) || (a.Kind == "quark" && !validQuarkFolder(folder)) {
			return errors.New("目标目录无效")
		}
		if a.Kind == "pan123" && !valid123Folder(folder) {
			return errors.New("目标目录无效")
		}
		if (a.Kind == "ctdav" || a.Kind == "webdav") && !validBaiduPath(folder) {
			return errors.New("WebDAV目标目录无效")
		}
		if a.Kind == "guangya" && !validQuarkFolder(folder) {
			return errors.New("光鸭目标目录无效")
		}
		old := *a
		if a.Kind == "ctfile" && !validCtFolder(folder) {
			return errors.New("诚通目标目录无效")
		}
		if a.Kind == "baidu" && !validBaiduPath(folder) {
			return errors.New("百度目标目录路径无效")
		}
		a.Folder = folder
		a.FolderDisplay = display
		if e := m.saveLocked(); e != nil {
			*a = old
			return errors.New("无法保存默认目录，请检查本机数据目录")
		}
		return nil
	}
	return errors.New("账号不存在")
}
