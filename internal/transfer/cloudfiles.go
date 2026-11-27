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
	"time"
)

// CloudEntry contains display metadata only, never a token or download URL.
type CloudEntry struct {
	revision string
	ID       string `json:"id"`
	ObjectID string `json:"objectId,omitempty"` // Stable provider identity for path-addressed destructive operations.
	Name     string `json:"name"`
	Folder   bool   `json:"folder"`
	Size     *int64 `json:"size"`
	SizeText string `json:"sizeText"`
}
type FilePage struct {
	CurrentID   string        `json:"currentId"`
	Entries     []CloudEntry  `json:"entries"`
	Breadcrumbs []CloudFolder `json:"breadcrumbs"`
}

func cloudRoot(kind string) string {
	switch kind {
	case "lanzou":
		return "-1"
	case "quark", "pan123":
		return "0"
	case "ctfile", "ctweb":
		return "d0"
	case "guangya":
		return "root"
	default:
		return "/"
	}
}

func filePage(a Account, parent string) FilePage {
	root := cloudRoot(a.Kind)
	p := FilePage{CurrentID: parent, Entries: []CloudEntry{}, Breadcrumbs: []CloudFolder{{ID: root, Name: "根目录"}}}
	if parent != root {
		if a.Kind == "baidu" || a.Kind == "ctdav" || a.Kind == "webdav" {
			current := ""
			for _, part := range strings.Split(strings.TrimPrefix(parent, "/"), "/") {
				current += "/" + part
				p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: current, Name: part})
			}
		} else {
			name := "目录 " + parent
			if parent == a.Folder && a.FolderDisplay != "" {
				name = a.FolderDisplay
			}
			p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: parent, Name: name})
		}
	}
	return p
}

func (m *Manager) ListFiles(ctx context.Context, id, parent string) (FilePage, error) {
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
		return FilePage{}, errors.New("账号不存在，请先保存账号")
	}
	return ListAccountFiles(ctx, NewHTTPUploader().Client, account, parent)
}

func ListAccountFiles(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	if parent == "" {
		parent = cloudRoot(a.Kind)
	}
	p := filePage(a, parent)
	switch a.Kind {
	case "lanzou":
		folders, e := ListLanzouFolders(ctx, client, a, parent)
		if e != nil {
			return p, e
		}
		p.Breadcrumbs = folders.Breadcrumbs
		for _, f := range folders.Folders {
			p.Entries = append(p.Entries, CloudEntry{ID: f.ID, Name: f.Name, Folder: true})
		}
		files, e := listLanzouFiles(ctx, client, a, parent)
		if e != nil {
			return FilePage{}, e
		}
		p.Entries = append(p.Entries, files...)
	case "quark":
		entries, e := newQuarkClient(client, a).list(ctx, parent)
		if e != nil {
			return p, e
		}
		for _, f := range entries {
			p.Entries = append(p.Entries, CloudEntry{ID: f.FID, Name: f.Name, Folder: f.isDir(), Size: f.Size, revision: string(f.UpdatedAt)})
		}
	case "pan123":
		entries, e := (pan123Client{client, a}).list(ctx, parent)
		if e != nil {
			return p, e
		}
		for _, f := range entries {
			size := f.Size
			p.Entries = append(p.Entries, CloudEntry{ID: string(f.ID), Name: f.Name, Folder: f.Type == 1, Size: &size, revision: f.MD5})
		}
	case "baidu":
		entries, e := newBaiduClient(client, a).list(ctx, parent)
		if e != nil {
			return p, e
		}
		for _, f := range entries {
			p.Entries = append(p.Entries, CloudEntry{ID: f.Path, ObjectID: string(f.ID), Name: f.Name, Folder: *f.Dir == 1, Size: f.Size, revision: string(f.ID) + ":" + f.MD5})
		}
	case "guangya":
		entries, e := (gyClient{client, a}).list(ctx, parent)
		if e != nil {
			return p, e
		}
		for _, f := range entries {
			size := f.Size
			p.Entries = append(p.Entries, CloudEntry{ID: f.ID, Name: f.Name, Folder: f.Type == 2, Size: &size})
		}
	case "ctdav", "webdav":
		return ListDAVFiles(ctx, client, a, parent)
	case "ctweb":
		folders, e := newCtWebClient(client, a).list(ctx, parent)
		if e != nil {
			return p, e
		}
		p.Entries = folders.Entries
	case "ctfile":
		c := ctClient{client, a}
		folders, e := c.folders(ctx, parent)
		if e != nil {
			return p, e
		}
		files, e := c.files(ctx, parent)
		if e != nil {
			return p, e
		}
		seen := map[string]bool{}
		for _, f := range append(folders, files...) {
			if seen[f.Key] {
				continue
			}
			seen[f.Key] = true
			var size *int64
			if n, e := f.Size.Int64(); e == nil && n >= 0 {
				size = &n
			}
			p.Entries = append(p.Entries, CloudEntry{ID: f.Key, Name: f.Name, Folder: validCtFolder(f.Key), Size: size})
		}
	default:
		return p, errors.New("此账号暂不支持文件浏览")
	}
	return p, nil
}

func listLanzouFiles(ctx context.Context, client *http.Client, a Account, parent string) ([]CloudEntry, error) {
	return readLanzouFiles(ctx, client, a, parent, false)
}

func readLanzouFiles(ctx context.Context, client *http.Client, a Account, parent string, strict bool) ([]CloudEntry, error) {
	entries := []CloudEntry{}
	seen := map[string]bool{}
	maxPages := 10000
	if strict {
		maxPages = 50
	}
	for page := 1; page <= maxPages; page++ {
		form := url.Values{"task": {"5"}, "folder_id": {parent}, "pg": {strconv.Itoa(page)}}
		req, _ := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/doupload.php", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", a.Secret)
		req.Header.Set("User-Agent", userAgent(a))
		req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
		resp, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("连接蓝奏云失败，请检查网络")
		}
		var result struct {
			ZT   json.RawMessage `json:"zt"`
			Text json.RawMessage `json:"text"`
			Info json.RawMessage `json:"info"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, statusError(resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, errors.New("蓝奏云文件列表无法识别 [LZ-LIST/0.9.0]")
		}
		code := strings.Trim(string(result.ZT), "\"")
		if code == "9" {
			return nil, errors.New("蓝奏云登录已失效，请重新登录")
		}
		if code != "1" && code != "2" {
			return nil, errors.New("蓝奏云未返回有效文件列表 [LZ-LIST/0.9.0]")
		}
		var files []struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name_all"`
			Size string          `json:"size"`
		}
		text := strings.TrimSpace(string(result.Text))
		if text != "" && text != "null" && text != "\"\"" {
			if json.Unmarshal(result.Text, &files) != nil {
				return nil, errors.New("蓝奏云文件列表格式不兼容 [LZ-LIST/0.9.0]")
			}
		} else if strict && code != "2" && strings.Trim(string(result.Info), "\"") != "0" {
			return nil, errors.New("蓝奏未明确返回文件列表，无法核对原目录")
		}
		if strict && (len(files) > 18 || code == "2" && len(files) != 0) {
			return nil, errors.New("蓝奏返回的文件分页条数不一致")
		}
		added := 0
		for _, f := range files {
			id, e := folderID(f.ID)
			if e != nil || id == "-1" {
				return nil, errors.New("蓝奏云文件标识无效")
			}
			if seen[id] {
				if strict {
					return nil, errors.New("蓝奏文件分页出现重复标识，无法核对原目录")
				}
				continue
			}
			name := html.UnescapeString(f.Name)
			if strict && (!valid123Folder(id) || id == "0" || !relayName(name)) {
				return nil, errors.New("蓝奏文件列表包含无效条目")
			}
			seen[id] = true
			added++
			entries = append(entries, CloudEntry{ID: id, Name: name, SizeText: f.Size})
		}
		if len(files) < 18 {
			return entries, nil
		}
		if added == 0 {
			return nil, errors.New("蓝奏云文件分页未推进，请刷新重试")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, errors.New("蓝奏云文件页数超出限制")
}
