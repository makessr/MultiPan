package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type RenameInput struct {
	AccountID     string `json:"accountId"`
	ParentID      string `json:"parentId"`
	EntryID       string `json:"entryId"`
	OldName       string `json:"oldName"`
	NewName       string `json:"newName"`
	Folder        bool   `json:"folder"`
	ParentDisplay string `json:"parentDisplay"`
}

func RenameAccountEntry(ctx context.Context, client *http.Client, a Account, in RenameInput) (CloudEntry, error) {
	switch a.Kind {
	case "lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav":
	default:
		return CloudEntry{}, errors.New("此网盘暂不支持重命名")
	}
	if e := validateCloudFolderName(in.NewName); e != nil {
		return CloudEntry{}, e
	}
	if a.Kind == "lanzou" {
		if e := validateLanzouFolderName(in.NewName); e != nil {
			return CloudEntry{}, e
		}
	}
	if in.ParentID == "" || in.EntryID == "" || in.OldName == "" {
		return CloudEntry{}, errors.New("重命名目标不完整，请刷新列表")
	}
	before, e := ListAccountFiles(ctx, client, a, in.ParentID)
	if e != nil {
		return CloudEntry{}, e
	}
	var source CloudEntry
	count := 0
	for _, f := range before.Entries {
		if f.ID == in.EntryID && f.Folder == in.Folder {
			source = f
			count++
		} else if f.Name == in.NewName {
			return CloudEntry{}, errors.New("当前目录已有同名项目，请使用其他名称")
		}
	}
	if count != 1 || source.Name != in.OldName {
		return CloudEntry{}, errors.New("目标已变化或不在当前目录，请刷新后重试")
	}
	if source.Name == in.NewName {
		return source, nil
	}
	uncertain := errors.New("重命名结果未确认，请先刷新当前目录核对，再决定是否重试 [RENAME/0.12.0]")
	targetID := source.ID
	switch a.Kind {
	case "lanzou":
		e = renameLanzou(ctx, client, a, source, in.NewName)
	case "quark":
		_, e = newQuarkClient(client, a).request(ctx, "POST", "/file/rename", nil, map[string]any{"fid": source.ID, "file_name": in.NewName}, nil)
	case "pan123":
		e = (pan123Client{client, a}).request(ctx, "POST", "/file/rename", nil, map[string]any{"driveId": 0, "fileId": source.ID, "fileName": in.NewName}, nil)
	case "guangya":
		_, e = (gyClient{client, a}).call(ctx, "/userres/v1/file/rename", map[string]any{"fileId": source.ID, "newName": in.NewName}, nil)
	case "baidu":
		c := newBaiduClient(client, a)
		if e = c.session(ctx); e != nil {
			return CloudEntry{}, e
		}
		items, _ := json.Marshal([]map[string]string{{"path": source.ID, "newname": in.NewName}})
		e = c.api(ctx, "POST", "filemanager", url.Values{"opera": {"rename"}}, url.Values{"async": {"0"}, "ondup": {"fail"}, "filelist": {string(items)}}, nil)
		targetID = path.Join(in.ParentID, in.NewName)
	case "ctweb":
		e = ctWebRename(ctx, client, a, source.ID, in.NewName)
	case "ctdav", "webdav":
		base, _ := url.Parse(a.Endpoint)
		prefix := strings.TrimRight(base.Path, "/")
		src := *base
		src.Path = prefix + source.ID
		src.RawPath = ""
		dst := *base
		targetID = path.Join(in.ParentID, in.NewName)
		dst.Path = prefix + targetID
		dst.RawPath = ""
		if source.Folder {
			src.Path += "/"
			dst.Path += "/"
		}
		req, err := http.NewRequestWithContext(ctx, "MOVE", src.String(), nil)
		if err != nil {
			return CloudEntry{}, errors.New("WebDAV重命名地址无效")
		}
		req.SetBasicAuth(a.Username, a.Secret)
		req.Header.Set("Destination", dst.String())
		req.Header.Set("Overwrite", "F")
		resp, err := client.Do(req)
		if err != nil {
			return CloudEntry{}, uncertain
		}
		resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 204 {
			return CloudEntry{}, fmt.Errorf("%w HTTP=%d", uncertain, resp.StatusCode)
		}
	}
	if e != nil {
		return CloudEntry{}, fmt.Errorf("%w；%v", uncertain, e)
	}
	for attempt := 0; attempt < 6; attempt++ {
		after, e := ListAccountFiles(ctx, client, a, in.ParentID)
		if e != nil {
			return CloudEntry{}, fmt.Errorf("%w；核验：%v", uncertain, e)
		}
		var matches []CloudEntry
		oldRemains := false
		for _, f := range after.Entries {
			if f.ID == source.ID && f.Name == source.Name && f.Folder == source.Folder {
				oldRemains = true
			}
			if f.ID == targetID && f.Name == in.NewName && f.Folder == source.Folder {
				matches = append(matches, f)
			}
		}
		if len(matches) == 1 && !oldRemains {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return CloudEntry{}, uncertain
		}
		if attempt < 5 {
			select {
			case <-ctx.Done():
				return CloudEntry{}, uncertain
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return CloudEntry{}, uncertain
}

func lanzouEditRequest(ctx context.Context, client *http.Client, a Account, form url.Values, out any) error {
	req, e := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/doupload.php", strings.NewReader(form.Encode()))
	if e != nil {
		return errors.New("蓝奏编辑地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", userAgent(a))
	req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("蓝奏编辑请求连接失败")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil || resp.StatusCode != 200 {
		return errors.New("蓝奏编辑接口请求失败")
	}
	var result struct {
		ZT json.RawMessage `json:"zt"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return errors.New("蓝奏编辑响应格式无效")
	}
	if strings.Trim(string(result.ZT), "\"") != "1" {
		return errors.New("蓝奏未接受编辑请求，请核对登录及账号权限；文件重命名可能需要会员")
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("蓝奏编辑信息无法识别")
	}
	return nil
}

func renameLanzou(ctx context.Context, client *http.Client, a Account, entry CloudEntry, name string) error {
	if entry.Folder {
		var detail struct {
			Info struct {
				Name        string  `json:"name"`
				Description *string `json:"des"`
			} `json:"info"`
		}
		if e := lanzouEditRequest(ctx, client, a, url.Values{"task": {"18"}, "folder_id": {entry.ID}}, &detail); e != nil {
			return e
		}
		if html.UnescapeString(detail.Info.Name) != entry.Name || detail.Info.Description == nil {
			return errors.New("无法确认蓝奏文件夹原描述，未发送重命名")
		}
		return lanzouEditRequest(ctx, client, a, url.Values{"task": {"4"}, "folder_id": {entry.ID}, "folder_name": {name}, "folder_description": {*detail.Info.Description}}, nil)
	}
	ext := path.Ext(entry.Name)
	if path.Ext(name) != ext {
		return errors.New("蓝奏文件重命名需保留原扩展名")
	}
	var detail struct {
		Info *string `json:"info"`
	}
	if e := lanzouEditRequest(ctx, client, a, url.Values{"task": {"46"}, "type": {"1"}, "file_id": {entry.ID}}, &detail); e != nil {
		return e
	}
	if detail.Info == nil {
		return errors.New("无法读取蓝奏文件可编辑名称，未发送重命名")
	}
	editable := html.UnescapeString(*detail.Info)
	submitted := name
	if editable != entry.Name {
		if editable != strings.TrimSuffix(entry.Name, ext) {
			return errors.New("蓝奏文件名称格式不兼容，未发送重命名")
		}
		submitted = strings.TrimSuffix(name, ext)
	}
	return lanzouEditRequest(ctx, client, a, url.Values{"task": {"46"}, "type": {"2"}, "file_id": {entry.ID}, "file_name": {submitted}}, nil)
}
