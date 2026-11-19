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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func validateLanzouFolderName(name string) error {
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 50 || !utf8.ValidString(name) {
		return errors.New("文件夹名称须为 1–50 个字符，不能为 . 或 ..")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || strings.ContainsRune("()", r) {
			return errors.New("蓝奏文件夹名称不能包含空格或英文括号，请使用下划线")
		}
		if unicode.IsControl(r) || strings.ContainsRune("/\\:*?\"<>|", r) {
			return errors.New("文件夹名称包含不支持的字符")
		}
	}
	return nil
}

func (m *Manager) CreateFolder(ctx context.Context, id, parent, name string) (CloudFolder, error) {
	// Serialize create/check operations without holding the task/account lock
	// during network requests. A second local request rechecks the updated list.
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return CloudFolder{}, err
	}
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
		return CloudFolder{}, errors.New("账号不存在，请先保存账号")
	}
	return CreateAccountFolder(ctx, NewHTTPUploader().Client, account, parent, name)
}

func CreateLanzouFolder(ctx context.Context, client *http.Client, a Account, parent, name string) (CloudFolder, error) {
	return createLanzouFolder(ctx, client, a, parent, name, true)
}

func createLanzouFolder(ctx context.Context, client *http.Client, a Account, parent, name string, configureShare bool) (CloudFolder, error) {
	if !validFolderID(parent) {
		return CloudFolder{}, errors.New("蓝奏父目录 ID 无效")
	}
	if e := validateLanzouFolderName(name); e != nil {
		return CloudFolder{}, e
	}
	before, e := ListLanzouFolders(ctx, client, a, parent)
	if e != nil {
		return CloudFolder{}, e
	}
	seen := map[string]bool{}
	for _, f := range before.Folders {
		seen[f.ID] = true
		if f.Name == name {
			return CloudFolder{}, errors.New("当前目录已有同名文件夹，请使用其他名称")
		}
	}
	form := url.Values{"task": {"2"}, "parent_id": {parent}, "folder_name": {name}, "folder_description": {""}}
	req, e := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/doupload.php", strings.NewReader(form.Encode()))
	if e != nil {
		return CloudFolder{}, errors.New("蓝奏创建请求地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", userAgent(a))
	req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
	uncertain := errors.New("蓝奏创建结果未确认，请先刷新当前目录核对，再决定是否重试 [LZ-MKDIR/0.10.0]")
	resp, e := client.Do(req)
	if e != nil {
		return CloudFolder{}, uncertain
	}
	var result struct {
		ZT json.RawMessage `json:"zt"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	resp.Body.Close()
	if resp.StatusCode != 200 || decodeErr != nil {
		return CloudFolder{}, uncertain
	}
	code := strings.Trim(string(result.ZT), "\"")
	if code == "9" {
		return CloudFolder{}, errors.New("蓝奏云登录已失效，请重新登录")
	}
	if code == "0" {
		return CloudFolder{}, errors.New("蓝奏拒绝创建文件夹，请核对名称、目录权限及账号限制 [LZ-MKDIR/0.10.0]")
	}
	if code != "1" {
		return CloudFolder{}, uncertain
	}
	// Do not replay a mutating request if the response or later verification is
	// ambiguous. Only read the selected parent to confirm a uniquely new ID.
	for attempt := 0; attempt < 4; attempt++ {
		after, e := ListLanzouFolders(ctx, client, a, parent)
		if e != nil {
			return CloudFolder{}, uncertain
		}
		var found []CloudFolder
		for _, f := range after.Folders {
			if f.Name == name && !seen[f.ID] {
				found = append(found, f)
			}
		}
		if len(found) == 1 {
			if configureShare {
				if e := makeNewLanzouFolderPublic(ctx, client, a, found[0]); e != nil {
					return CloudFolder{}, e
				}
			}
			return found[0], nil
		}
		if len(found) > 1 {
			return CloudFolder{}, uncertain
		}
		if attempt < 3 {
			select {
			case <-ctx.Done():
				return CloudFolder{}, uncertain
			case <-time.After(400 * time.Millisecond):
			}
		}
	}
	return CloudFolder{}, uncertain
}

// Only called for a uniquely new folder, never an existing upload destination.
func makeNewLanzouFolderPublic(ctx context.Context, client *http.Client, a Account, folder CloudFolder) error {
	pending := func(reason string) error {
		return fmt.Errorf("文件夹「%s」已创建（ID：%s），但%s；请刷新后在官网核对，不要重复创建 [LZ-MKDIR/password]", folder.Name, folder.ID, reason)
	}
	read := func() (string, error) {
		var result struct {
			Info struct {
				Name *string         `json:"name"`
				On   json.RawMessage `json:"onof"`
			} `json:"info"`
		}
		if e := lanzouEditRequest(ctx, client, a, url.Values{"task": {"18"}, "folder_id": {folder.ID}}, &result); e != nil {
			return "", e
		}
		if result.Info.Name == nil || html.UnescapeString(*result.Info.Name) != folder.Name {
			return "", errors.New("新目录身份未确认")
		}
		on := strings.Trim(string(result.Info.On), "\"")
		if on != "0" && on != "1" {
			return "", errors.New("提取码开关未返回")
		}
		return on, nil
	}
	on, e := read()
	if e != nil {
		return pending("无法读取提取码状态")
	}
	if on == "0" {
		return nil
	}
	// A lost acknowledgement does not justify resending this write. Read back the
	// same new folder even when the update response is missing or rejected.
	_ = lanzouEditRequest(ctx, client, a, url.Values{"task": {"16"}, "folder_id": {folder.ID}, "shows": {"0"}, "shownames": {""}}, nil)
	on, e = read()
	if e != nil {
		return pending("关闭提取码的结果待确认")
	}
	if on != "0" {
		return pending("蓝奏仍开启提取码，可能受账号权限限制")
	}
	return nil
}
