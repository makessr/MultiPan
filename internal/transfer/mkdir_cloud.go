package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func validateCloudFolderName(name string) error {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 50 || !utf8.ValidString(name) {
		return errors.New("文件夹名称须为 1–50 个字符，首尾不能是空白，不能为 . 或 ..")
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune("/\\:*?\"<>|", r) {
			return errors.New("文件夹名称包含不支持的字符")
		}
	}
	return nil
}

func CreateAccountFolder(ctx context.Context, client *http.Client, a Account, parent, name string) (CloudFolder, error) {
	if a.Kind == "lanzou" {
		return CreateLanzouFolder(ctx, client, a, parent, name)
	}
	switch a.Kind {
	case "quark", "pan123", "baidu", "guangya", "ctdav", "webdav", "ctweb":
	default:
		return CloudFolder{}, errors.New("此网盘暂不支持新建文件夹")
	}
	if e := validateCloudFolderName(name); e != nil {
		return CloudFolder{}, e
	}
	if parent == "" {
		parent = cloudRoot(a.Kind)
	}
	before, e := ListAccountFiles(ctx, client, a, parent)
	if e != nil {
		return CloudFolder{}, e
	}
	seen := map[string]bool{}
	for _, f := range before.Entries {
		seen[f.ID] = true
		if f.Name == name {
			return CloudFolder{}, errors.New("当前目录已有同名文件或文件夹，请使用其他名称")
		}
	}
	// Once mutation starts, never replay it automatically, even when the
	// response is lost. Confirm only with read-only parent listings.
	uncertain := errors.New("网盘创建结果未确认，请先刷新当前目录核对，再决定是否重试 [CLOUD-MKDIR/0.11.0]")
	switch a.Kind {
	case "quark":
		_, e = newQuarkClient(client, a).request(ctx, "POST", "/file", nil, map[string]any{"pdir_fid": parent, "file_name": name, "dir_path": "", "dir_init_lock": false}, nil)
	case "pan123":
		e = (pan123Client{client, a}).request(ctx, "POST", "/file/upload_request", nil, map[string]any{"driveId": 0, "parentFileId": parent, "fileName": name, "type": 1, "size": 0, "etag": "", "duplicate": 1}, nil)
	case "guangya":
		pid := parent
		if pid == guangyaRoot {
			pid = ""
		}
		_, e = (gyClient{client, a}).call(ctx, "/userres/v1/file/create_dir", map[string]any{"parentId": pid, "dirName": name, "failIfNameExist": true}, nil)
	case "baidu":
		c := newBaiduClient(client, a)
		// Fetch the web CSRF token before sending the one create request.
		if e = c.session(ctx); e != nil {
			return CloudFolder{}, e
		}
		target := path.Join(parent, name)
		e = c.api(ctx, "POST", "create", url.Values{"a": {"commit"}}, url.Values{"path": {target}, "isdir": {"1"}, "size": {"0"}, "block_list": {"[]"}, "rtype": {"0"}}, nil)
	case "ctweb":
		_, e = ctWebMkdir(ctx, client, a, parent, name)
	case "ctdav", "webdav":
		base, parseErr := url.Parse(a.Endpoint)
		if parseErr != nil {
			return CloudFolder{}, errors.New("WebDAV地址无效")
		}
		base.Path = strings.TrimRight(base.Path, "/") + path.Join(parent, name)
		base.RawPath = ""
		if !strings.HasSuffix(base.Path, "/") {
			base.Path += "/"
		}
		req, err := http.NewRequestWithContext(ctx, "MKCOL", base.String(), nil)
		if err != nil {
			return CloudFolder{}, errors.New("WebDAV目录地址无效")
		}
		req.SetBasicAuth(a.Username, a.Secret)
		resp, err := client.Do(req)
		if err != nil {
			return CloudFolder{}, uncertain
		}
		resp.Body.Close()
		if resp.StatusCode != 201 {
			return CloudFolder{}, fmt.Errorf("%w HTTP=%d", uncertain, resp.StatusCode)
		}
	}
	if e != nil {
		return CloudFolder{}, fmt.Errorf("%w；%v", uncertain, e)
	}
	for attempt := 0; attempt < 6; attempt++ {
		after, e := ListAccountFiles(ctx, client, a, parent)
		if e != nil {
			return CloudFolder{}, fmt.Errorf("%w；核验：%v", uncertain, e)
		}
		found := []CloudFolder{}
		for _, f := range after.Entries {
			if f.Folder && f.Name == name && !seen[f.ID] {
				found = append(found, CloudFolder{ID: f.ID, Name: f.Name})
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return CloudFolder{}, uncertain
		}
		if attempt < 5 {
			select {
			case <-ctx.Done():
				return CloudFolder{}, uncertain
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return CloudFolder{}, uncertain
}
