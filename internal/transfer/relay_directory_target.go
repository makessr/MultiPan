package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func relayDirectoryTargetProvider(a Account) bool {
	return relayProvider(a) || a.Kind == "baidu" && a.Endpoint == BaiduAPI || a.Kind == "pan123" && a.Endpoint == Pan123API || a.Kind == "guangya" && a.Endpoint == GuangyaAPI || a.Kind == "quark" && a.Endpoint == QuarkAPI
}

func relayIDTreeTarget(kind string) bool {
	return kind == "pan123" || relayTargetNeedsFileID(kind)
}

func relayTargetNeedsFileID(kind string) bool {
	return kind == "guangya" || kind == "quark"
}

func relayPlannedChildTarget(d diskRelay) string {
	if relayIDTreeTarget(d.TargetKind) {
		return path.Join("/", d.Input.DestinationID, d.Input.Name)
	}
	return path.Join(d.Input.DestinationID, d.Input.Name)
}
func validRelayPlannedTarget(b Account, d diskRelay) bool {
	if relayTargetNeedsFileID(b.Kind) {
		return b.Endpoint == relayDirectorySavedAccount(b.Kind).Endpoint && d.TargetKind == b.Kind && validRelaySavedTarget(d)
	}
	if b.Kind == "pan123" && d.ParentJobID != "" {
		return b.Endpoint == Pan123API && d.TargetKind == "pan123" && validRelaySavedTarget(d)
	}
	return validRelayTarget(b, d.Input.DestinationID)
}
func relayTargetParent(d diskRelay) string {
	if relayIDTreeTarget(d.TargetKind) && d.targetParent != "" {
		return d.targetParent
	}
	return d.Input.DestinationID
}
func (m *Manager) resolveRelayTarget(d diskRelay) (diskRelay, error) {
	if !relayIDTreeTarget(d.TargetKind) {
		return d, nil
	}
	if d.ParentJobID == "" {
		d.targetParent = d.Input.DestinationID
		return d, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.relayByIDLocked(d.ParentJobID)
	if p == nil || !m.relayParentReadyLocked(d) || p.TargetKind != d.TargetKind || !validRelayTargetFolderID(d.TargetKind, p.TargetFolderID) || relayPlannedChildTarget(*p) != d.Input.DestinationID {
		return d, errors.New("目标父目录缺少已确认ID，未继续子项")
	}
	d.targetParent = p.TargetFolderID
	return d, nil
}

// Read preparation happens before the durable write checkpoint. The returned
// closure performs exactly one creation request, with no retry or fallback.
func prepareRelayDirectoryCreate(ctx context.Context, client *http.Client, b Account, d diskRelay) (func() (string, error), error) {
	if b.Kind == "pan123" {
		parent := relayTargetParent(d)
		if !valid123Folder(parent) {
			return nil, errors.New("123目标父目录ID无效，未创建")
		}
		return func() (string, error) {
			var response pan123Pre
			err := (pan123Client{client, b}).request(ctx, "POST", "/file/upload_request", nil, map[string]any{"driveId": 0, "parentFileId": parent, "fileName": d.Input.Name, "type": 1, "size": 0, "etag": "", "duplicate": 1}, &response)
			id := string(response.FileID)
			if response.Info != nil {
				r := response.Info
				if r.Name != d.Input.Name || string(r.ParentID) != parent || r.Type != 1 || r.Size != 0 || id != "" && id != string(r.ID) {
					return "", errors.New("123目录创建受理信息不符，保持待核对")
				}
				id = string(r.ID)
			}
			if err != nil || response.Reuse || !valid123Folder(id) || id == "0" || id == parent {
				return "", errors.New("123目录创建缺少准确ID受理凭据，保持待核对，不重发或合并")
			}
			return id, nil
		}, nil
	}
	if b.Kind == "quark" {
		parent := relayTargetParent(d)
		if !validRelayTarget(b, parent) {
			return nil, errors.New("夸克目标父目录 ID 无效，未创建")
		}
		prior, err := readRelayNativeTargetIDs(ctx, client, b, parent, d.Input.Name)
		if err != nil {
			return nil, err
		}
		return func() (string, error) {
			var response struct {
				ID string `json:"fid"`
			}
			_, err := newQuarkClient(client, b).request(ctx, http.MethodPost, "/file", nil,
				map[string]any{"pdir_fid": parent, "file_name": d.Input.Name, "dir_path": "", "dir_init_lock": false}, &response)
			if err != nil || !validRelayTargetFolderID(b.Kind, response.ID) || response.ID == parent || prior[response.ID] ||
				d.Input.SourceAccountID == d.Input.TargetAccountID && response.ID == d.Input.EntryID {
				return "", errors.New("夸克目录创建缺少明确受理及新目录 ID，保持待核对")
			}
			return response.ID, nil
		}, nil
	}
	if b.Kind == "guangya" {
		parent := relayTargetParent(d)
		if parent == guangyaRoot {
			parent = ""
		} else if !validGuangyaCopyFileID(parent) {
			return nil, errors.New("光鸭目标父目录 ID 无效，未创建")
		}
		return func() (string, error) {
			var response struct {
				ID string `json:"fileId"`
			}
			_, err := (gyClient{client, b}).call(ctx, "/userres/v1/file/create_dir",
				map[string]any{"parentId": parent, "dirName": d.Input.Name, "failIfNameExist": true}, &response)
			if err != nil || !validGuangyaCopyFileID(response.ID) || response.ID == parent {
				return "", errors.New("光鸭目录创建缺少明确受理及目录 ID，保持待核对")
			}
			return response.ID, nil
		}, nil
	}
	if b.Kind == "baidu" {
		c := newBaiduClient(client, b)
		if e := c.session(ctx); e != nil {
			return nil, errors.New("百度目标登录读取未完成，未发送目录创建")
		}
		return func() (string, error) {
			target := path.Join(d.Input.DestinationID, d.Input.Name)
			var response struct {
				ID   json.Number `json:"fs_id"`
				Path string      `json:"path"`
				Dir  *int        `json:"isdir"`
			}
			e := c.api(ctx, "POST", "create", url.Values{"a": {"commit"}}, url.Values{"path": {target}, "isdir": {"1"}, "size": {"0"}, "block_list": {"[]"}, "rtype": {"0"}}, &response)
			if e != nil || response.Path != target || response.Dir == nil || *response.Dir != 1 || !valid123Folder(string(response.ID)) || response.ID == "0" {
				return "", errors.New("百度目录创建缺少准确路径/类型/ID受理凭据，保持待核对，不重发或合并")
			}
			return string(response.ID), nil
		}, nil
	}
	return func() (string, error) {
		resp, e := relayRequest(ctx, client, b, "MKCOL", path.Join(d.Input.DestinationID, d.Input.Name), nil, map[string]string{"If-None-Match": "*"}, 0)
		if resp != nil {
			resp.Body.Close()
		}
		if e != nil || resp == nil || resp.StatusCode != 201 {
			return "", errors.New("目录创建未获明确受理，保持待核对，不重复创建或合并")
		}
		return "", nil
	}, nil
}
func relayDirectoryTarget(ctx context.Context, client *http.Client, b Account, d diskRelay) error {
	if !d.CreatedAck || (b.Kind == "baidu" || relayIDTreeTarget(b.Kind)) && d.TargetFolderID == "" {
		return errors.New("目录缺少已保存的创建受理依据，保持待核对")
	}
	page, e := relayDirectoryPage(ctx, client, b, relayTargetParent(d))
	if e != nil {
		return e
	}
	count := 0
	matched := false
	for _, r := range page.Entries {
		if strings.EqualFold(r.Name, d.Input.Name) {
			count++
			matched = r.Name == d.Input.Name && r.ID == path.Join(d.Input.DestinationID, d.Input.Name) && r.Folder && (b.Kind != "baidu" || r.ObjectID == d.TargetFolderID)
			if relayIDTreeTarget(b.Kind) {
				matched = r.Name == d.Input.Name && r.Folder && r.ID == d.TargetFolderID
			}
		}
	}
	if count != 1 || !matched {
		return errors.New("目标目录路径、类型或原目录ID不匹配，保持待核对")
	}
	return nil
}
func (m *Manager) relayTargetDirectoryAncestors(ctx context.Context, client *http.Client, b Account, d diskRelay) error {
	if b.Kind != "baidu" && !relayIDTreeTarget(b.Kind) {
		return nil
	}
	m.mu.Lock()
	next := d.ParentJobID
	chain := []diskRelay{}
	for next != "" {
		p := m.relayByIDLocked(next)
		if p == nil || !p.Folder || !p.CreatedAck || len(chain) >= 8 {
			m.mu.Unlock()
			return errors.New("目标父目录受理依据不完整，未继续")
		}
		chain = append(chain, *p)
		next = p.ParentJobID
	}
	m.mu.Unlock()
	for i := len(chain) - 1; i >= 0; i-- {
		resolved, e := m.resolveRelayTarget(chain[i])
		if e != nil {
			return e
		}
		if e := relayDirectoryTarget(ctx, client, b, resolved); e != nil {
			return e
		}
	}
	return nil
}
