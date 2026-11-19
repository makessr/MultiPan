package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type GuangyaRestorePreview struct {
	Location RecycleRestoreLocation `json:"location"`
	Occupied bool                   `json:"occupied"`
}

func (m *Manager) PreviewGuangyaRestore(ctx context.Context, token string) (GuangyaRestorePreview, error) {
	h, err := decodeRecycleRestore(token)
	if err != nil {
		return GuangyaRestorePreview{}, err
	}
	a, err := m.recycleListingAccount(h.AccountID)
	if err != nil || a.Kind != "guangya" || !recycleRestoreAccount(a, h) {
		return GuangyaRestorePreview{}, errors.New("光鸭账号来源已变化，请刷新回收站")
	}

	client := NewHTTPUploader().Client
	page, err := listAccountRecycle(ctx, client, a, h.Page)
	if err != nil {
		return GuangyaRestorePreview{}, err
	}
	match := false
	for _, row := range page.Items {
		if sameRecycleRestoreEntry(row, h.Entry) {
			match = true
			break
		}
	}
	if page.Incomplete || !match {
		return GuangyaRestorePreview{}, errors.New("光鸭回收站原记录已变化或列表不完整，请刷新")
	}
	_, occupied, err := guangyaRestoreOriginal(ctx, client, a, h)
	if err != nil {
		return GuangyaRestorePreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || !recycleRestoreAccount(m.downloadAccountLocked(a.ID), h) {
		return GuangyaRestorePreview{}, errors.New("核对期间光鸭账号来源已变化，请刷新回收站")
	}
	return GuangyaRestorePreview{
		Location: RecycleRestoreLocation{AccountID: a.ID, ParentID: h.Entry.ParentID},
		Occupied: occupied,
	}, nil
}

func validGuangyaRestoreParent(parent, id string) bool {
	return validQuarkFolder(parent) && parent != "0" && parent != id
}

func guangyaRestorePath(parent, name string) string {
	if parent == guangyaRoot {
		return "根目录 / " + name
	}
	return "原目录 ID " + parent + " / " + name
}

func guangyaRestoreOriginal(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource) (bool, bool, error) {
	entry := h.Entry
	c := gyClient{client, a}
	parentID := entry.ParentID
	if parentID == guangyaRoot {
		parentID = ""
	} else {
		var parent struct {
			File struct {
				ID   string `json:"fileId"`
				Type int    `json:"resType"`
			} `json:"fileInfo"`
		}
		if _, err := c.call(ctx, "/userres/v1/file/get_file_detail", map[string]string{"fileId": parentID}, &parent); err != nil {
			return false, false, err
		}
		if parent.File.ID != parentID || parent.File.Type != 2 {
			return false, false, errors.New("光鸭未确认原目录存在")
		}
	}
	seen := map[string]bool{}
	total := -1
	same, occupied := false, false
	for page := 0; page < 50; page++ {
		var data struct {
			List []struct {
				ID     string  `json:"fileId"`
				Name   string  `json:"fileName"`
				Parent *string `json:"parentId"`
				Type   *int    `json:"resType"`
				Size   *int64  `json:"fileSize"`
			} `json:"list"`
			Total *int `json:"total"`
		}
		body := map[string]any{"parentId": parentID, "page": page, "pageSize": 100, "orderBy": 3, "sortType": 1}
		if _, err := c.call(ctx, "/userres/v1/file/get_file_list", body, &data); err != nil {
			return false, false, err
		}
		if data.List == nil || data.Total == nil || *data.Total < 0 || len(data.List) > 100 ||
			total >= 0 && total != *data.Total {
			return false, false, errors.New("光鸭原目录分页无法核对")
		}
		total = *data.Total
		for _, row := range data.List {
			if !validQuarkFolder(row.ID) || row.ID == "0" || row.ID == guangyaRoot ||
				seen[row.ID] || row.Parent == nil || *row.Parent != parentID || strings.TrimSpace(row.Name) == "" ||
				row.Type == nil || (*row.Type != 1 && *row.Type != 2) || row.Size != nil && *row.Size < 0 {
				return false, false, errors.New("光鸭原目录项目无法核对")
			}
			seen[row.ID] = true
			if row.Name == entry.Name {
				if occupied {
					return false, true, errors.New("光鸭原目录存在多个同名项目")
				}
				occupied = true
				isFolder := entry.Kind == "folder"
				same = row.ID == entry.ID && (*row.Type == 2) == isFolder &&
					(isFolder || row.Size != nil && entry.Size != nil && *row.Size == *entry.Size)
			} else if row.ID == entry.ID {
				return false, false, errors.New("光鸭原目录中的同 ID 项目名称已变化")
			}
		}
		if len(seen) == total {
			return same, occupied, nil
		}
		if len(seen) > total || len(data.List) < 100 {
			return false, false, errors.New("光鸭原目录列表不完整")
		}
	}
	return false, false, errors.New("光鸭原目录核对超过5000项，结果不完整")
}

func validGuangyaRestoreTaskID(id string) bool {
	return validQuarkFolder(id) && id != "0"
}

func submitGuangyaRestore(ctx context.Context, client *http.Client, a Account, entry RecycleEntry) (string, error) {
	var data struct {
		TaskID string `json:"taskId"`
	}
	_, err := (gyClient{client, a}).call(ctx, "/userres/v1/file/recycle_file", map[string]any{"fileIds": []string{entry.ID}}, &data)
	if err != nil || !validGuangyaRestoreTaskID(data.TaskID) {
		return "", errors.New("光鸭还原任务凭证未确认")
	}
	return data.TaskID, nil
}

func guangyaFileTaskDone(ctx context.Context, client *http.Client, a Account, taskID string) (bool, error) {
	if !validGuangyaRestoreTaskID(taskID) {
		return false, errors.New("光鸭原任务凭证缺失，请到官网核对")
	}
	var data struct {
		Status *int `json:"status"`
		Detail *struct {
			Code *int `json:"code"`
		} `json:"detail"`
	}
	_, err := (gyClient{client, a}).call(ctx, "/userres/v1/get_task_status", map[string]string{"taskId": taskID}, &data)
	if err != nil {
		return false, err
	}
	if data.Status == nil || *data.Status < 0 || *data.Status >= 3 ||
		data.Detail != nil && data.Detail.Code != nil && *data.Detail.Code != 0 {
		return false, errors.New("光鸭原任务失败或状态未确认，请到官网核对")
	}
	return *data.Status == 2, nil
}
