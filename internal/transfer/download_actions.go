package transfer

import "errors"

type DownloadActionResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

// One locked state transition prevents resume dispatch from racing a batch pause.
// Removing records never deletes final or partial files.
func (m *Manager) BatchDownloadAction(ids []string, action string) ([]DownloadActionResult, error) {
	return m.batchDownloadAction(ids, action, 200)
}

func (m *Manager) batchDownloadAction(ids []string, action string, maxItems int) ([]DownloadActionResult, error) {
	if len(ids) == 0 || len(ids) > maxItems {
		return nil, errors.New("每次选择1–200个下载任务")
	}
	if action != "pause" && action != "resume" && action != "remove" {
		return nil, errors.New("不支持的下载操作")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, errors.New("程序正在退出")
	}
	old := append([]DownloadJob(nil), m.downloads...)
	seen := map[string]bool{}
	remove := map[string]bool{}
	cancelIDs := []string{}
	results := []DownloadActionResult{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result := DownloadActionResult{ID: id, Message: "任务不存在或已被清理"}
		for i := range m.downloads {
			j := &m.downloads[i]
			if j.ID != id {
				continue
			}
			result.Name = j.Input.Name
			_, active := m.downloadActive[id]
			switch action {
			case "pause":
				if j.Status == "queued" {
					j.Status = "paused"
					j.Message = "已暂停"
					result.Applied = true
					result.Message = "已暂停"
				} else if active {
					cancelIDs = append(cancelIDs, id)
					result.Applied = true
					result.Message = "暂停请求已提交"
				} else {
					result.Message = "当前状态无需暂停"
				}
			case "resume":
				if active {
					result.Message = "任务正在运行或停止，请稍后"
				} else if j.Status == "paused" || j.Status == "failed" {
					j.Status = "queued"
					j.Message = "等待继续下载"
					result.Applied = true
					result.Message = "继续请求已提交"
				} else {
					result.Message = "当前状态无需继续"
				}
			case "remove":
				if active || j.Status == "queued" || j.Status == "running" {
					result.Message = "请先暂停任务，等待停止后再移除记录"
				} else {
					remove[id] = true
					result.Applied = true
					result.Message = "已移除记录，未删除已下载或临时文件"
				}
			}
			break
		}
		results = append(results, result)
	}
	kept := make([]DownloadJob, 0, len(m.downloads))
	for _, j := range m.downloads {
		if !remove[j.ID] {
			kept = append(kept, j)
		}
	}
	m.downloads = kept
	if e := m.saveLocked(); e != nil {
		m.downloads = old
		return nil, errors.New("下载状态保存失败，本次批量操作未提交")
	}
	for _, id := range cancelIDs {
		if cancel := m.downloadActive[id]; cancel != nil {
			cancel()
		}
	}
	if action == "resume" {
		m.dispatchDownloadsLocked()
	}
	return results, nil
}

func (m *Manager) ControlAllDownloads(action string) ([]DownloadActionResult, error) {
	if action != "pause" && action != "resume" {
		return nil, errors.New("全局操作仅支持暂停或继续")
	}
	m.mu.Lock()
	ids := []string{}
	for _, j := range m.downloads {
		if action == "pause" && (j.Status == "queued" || j.Status == "running") {
			ids = append(ids, j.ID)
		} else if action == "resume" && (j.Status == "paused" || j.Status == "failed") {
			ids = append(ids, j.ID)
		}
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return []DownloadActionResult{}, nil
	}
	return m.batchDownloadAction(ids, action, 5000)
}
