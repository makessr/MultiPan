package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
)

type MoveBatchEntry struct {
	Input  MoveFileInput `json:"input"`
	Folder bool          `json:"folder"`
}

// Preflight is read-only. Each subsequent move still rechecks its own source and
// destination, since a remote client can change the directory after this check.
func (m *Manager) ValidateMoveBatch(ctx context.Context, entries []MoveBatchEntry) error {
	if len(entries) == 0 || len(entries) > 50 {
		return errors.New("每次批量移动请选择1至50项")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(entries[0].Input.AccountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || a.ID == "" {
		return errors.New("账号不可用，请刷新后重试")
	}
	if err := validateAccountMoveBatch(ctx, NewHTTPUploader().Client, a, entries); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) {
		return errors.New("账号来源已变化，请重新选择移动项目")
	}
	return nil
}

func validateAccountMoveBatch(ctx context.Context, client *http.Client, a Account, entries []MoveBatchEntry) error {
	if len(entries) == 0 || len(entries) > 50 {
		return errors.New("每次批量移动请选择1至50项")
	}
	first := entries[0].Input
	if first.AccountID != a.ID || first.ParentID == "" || first.DestinationID == "" || first.ParentID == first.DestinationID {
		return errors.New("批量移动来源或目标无效")
	}
	switch a.Kind {
	case "webdav", "ctdav", "baidu", "quark", "pan123", "guangya":
	default:
		return errors.New("此网盘暂不支持文件夹批量移动")
	}
	source, e := ListAccountFiles(ctx, client, a, first.ParentID)
	if e != nil {
		return e
	}
	dest, e := ListAccountFiles(ctx, client, a, first.DestinationID)
	if e != nil {
		return e
	}
	seen, names := map[string]bool{}, map[string]bool{}
	for _, row := range entries {
		in := row.Input
		key := fmt.Sprintf("%t:%s", row.Folder, in.EntryID)
		if in.AccountID != a.ID || in.ParentID != first.ParentID || in.DestinationID != first.DestinationID || seen[key] || names[in.Name] || in.EntryID == "" || in.Name == "" || in.Name == "." || in.Name == ".." || strings.ContainsAny(in.Name, "/\\\x00\r\n") {
			return errors.New("批量项目重复、同名或来源不一致，未发送移动请求")
		}
		seen[key] = true
		names[in.Name] = true
		matches := 0
		for _, f := range source.Entries {
			if f.ID == in.EntryID && f.Name == in.Name && f.Folder == row.Folder {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s：原项目已变化，整批未提交", in.Name)
		}
		for _, f := range dest.Entries {
			if f.Name == in.Name {
				return fmt.Errorf("%s：目标已有同名项目，整批未提交", in.Name)
			}
		}
		if row.Folder {
			if a.Kind == "quark" || a.Kind == "pan123" || a.Kind == "guangya" {
				if _, _, _, e := verifiedMoveTrails(ctx, client, a, in); e != nil {
					return e
				}
			} else {
				if !validBaiduPath(in.EntryID) || path.Dir(in.EntryID) != in.ParentID || path.Base(in.EntryID) != in.Name || in.DestinationID == in.EntryID || strings.HasPrefix(in.DestinationID, in.EntryID+"/") {
					return errors.New("不能将文件夹移入自身或子目录，整批未提交")
				}
			}
		}
	}
	return nil
}
