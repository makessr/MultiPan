package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type UploadSkip struct {
	FileIndex   int `json:"fileIndex"`
	TargetIndex int `json:"targetIndex"`
}
type UploadCheck struct {
	FileIndex   int    `json:"fileIndex"`
	TargetIndex int    `json:"targetIndex"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Status      string `json:"status"` // ready, reuse, conflict, unknown
	Message     string `json:"message"`
}
type UploadReview struct {
	Stamp string        `json:"stamp"`
	Items []UploadCheck `json:"items"`
}

// Bind the review to the exact local plan and account source without retaining
// credentials or adding a second persistent plan store. Worker checks remain
// authoritative: remote contents can change after this read-only preview.
func uploadPlanStamp(files []File, targets []Target, accounts []Account) string {
	scopes := []string{}
	for _, target := range targets {
		for _, a := range accounts {
			if a.ID == target.AccountID {
				scopes = append(scopes, downloadScope(a))
				break
			}
		}
	}
	data, _ := json.Marshal(struct {
		Files   []File
		Targets []Target
		Scopes  []string
	}{files, targets, scopes})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) CheckUploadPlan(ctx context.Context, files []File, targets []Target) (UploadReview, error) {
	if len(files) == 0 || len(targets) == 0 || len(files)*len(targets) > 1000 {
		return UploadReview{}, errors.New("请选择本地项目和目标网盘，单批最多1000个任务")
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return UploadReview{}, errors.New("程序正在退出")
	}
	canonical, err := m.validateUploadPlanLocked(files, targets)
	accounts := append([]Account(nil), m.accounts...)
	m.mu.Unlock()
	if err != nil {
		return UploadReview{}, err
	}
	client := NewHTTPUploader().Client
	return reviewUploadPlan(ctx, canonical, targets, accounts, func(ctx context.Context, a Account, parent string) (FilePage, error) {
		return ListAccountFiles(ctx, client, a, parent)
	}), nil
}

type uploadLocation struct {
	id              string
	exists          bool
	status, message string
}
type uploadListing struct {
	page FilePage
	err  error
}

func reviewUploadPlan(ctx context.Context, files []File, targets []Target, accounts []Account, list func(context.Context, Account, string) (FilePage, error)) UploadReview {
	review := UploadReview{Stamp: uploadPlanStamp(files, targets, accounts), Items: []UploadCheck{}}
	for ti, target := range targets {
		var a Account
		for _, candidate := range accounts {
			if candidate.ID == target.AccountID {
				a = candidate
				break
			}
		}
		pages := map[string]uploadListing{}
		locations := map[string]uploadLocation{"": {id: target.Folder, exists: true}}
		read := func(parent string) uploadListing {
			if cached, ok := pages[parent]; ok {
				return cached
			}
			var v uploadListing
			if ctx.Err() != nil {
				v.err = ctx.Err()
			} else {
				v.page, v.err = list(ctx, a, parent)
			}
			pages[parent] = v
			return v
		}
		var locate func([]string) uploadLocation
		locate = func(parts []string) uploadLocation {
			key := strings.Join(parts, "/")
			if v, ok := locations[key]; ok {
				return v
			}
			parent := locate(parts[:len(parts)-1])
			v := parent
			if parent.exists && parent.status == "" {
				page := read(parent.id)
				if page.err != nil {
					v = uploadLocation{status: "unknown", message: "无法确认目标目录，请检查登录、网络或网盘限制后重新检查"}
				} else {
					matches := []CloudEntry{}
					name := parts[len(parts)-1]
					for _, entry := range page.page.Entries {
						if strings.EqualFold(entry.Name, name) {
							matches = append(matches, entry)
						}
					}
					switch {
					case len(matches) == 0:
						v = uploadLocation{}
					case len(matches) == 1 && matches[0].Folder && matches[0].Name == name:
						v = uploadLocation{id: matches[0].ID, exists: true}
					default:
						v = uploadLocation{status: "conflict", message: "路径被同名项目占用、大小写不同或存在多个同名目录，请调整目录后重选"}
					}
				}
			}
			locations[key] = v
			return v
		}
		for fi, f := range files {
			name := f.RelativePath
			if name == "" {
				name = f.Name
			}
			row := UploadCheck{FileIndex: fi, TargetIndex: ti, Name: name, Kind: a.Kind, Destination: target.Folder, Status: "ready", Message: "检查时未发现同名项目；最终结果以网盘实际响应为准"}
			if target.Folder == a.Folder && a.FolderDisplay != "" {
				row.Destination = a.FolderDisplay
			}
			if a.Kind == "ctfile" {
				row.Status = "unknown"
				row.Message = "诚通旧OpenAPI不支持目录列表检查，请确认远端后自行决定是否上传"
				review.Items = append(review.Items, row)
				continue
			}
			parts := strings.Split(name, "/")
			if f.Directory {
				v := locate(parts)
				if v.status != "" {
					row.Status = v.status
					row.Message = v.message
				} else if v.exists {
					row.Status = "reuse"
					row.Message = "将复用唯一同名文件夹，不重新创建"
				}
			} else {
				v := locate(parts[:len(parts)-1])
				if v.status != "" {
					row.Status = v.status
					row.Message = v.message
				} else if v.exists {
					page := read(v.id)
					if page.err != nil {
						row.Status = "unknown"
						row.Message = "无法检查目标列表，请检查登录、网络或网盘限制后重新检查"
					} else {
						for _, entry := range page.page.Entries {
							if strings.EqualFold(entry.Name, parts[len(parts)-1]) {
								row.Status = "conflict"
								row.Message = "目标已有同名项目，默认跳过；不会自动覆盖、改名或把同名当作内容相同"
								break
							}
						}
					}
				}
			}
			review.Items = append(review.Items, row)
		}
	}
	return review
}

type UploadRetryResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

// Only failed tasks are selected; intentional pauses and successful tasks stay
// untouched. Persist all decisions before starting any worker.
func (m *Manager) RetryFailedUploads(ids []string) ([]UploadRetryResult, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, errors.New("每次请选择1–1000个失败任务")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, errors.New("程序正在退出")
	}
	old := append([]Task(nil), m.tasks...)
	index := map[string]int{}
	for i, t := range m.tasks {
		index[t.ID] = i
	}
	seen := map[string]bool{}
	results := []UploadRetryResult{}
	changed := false
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r := UploadRetryResult{ID: id, Message: "任务不存在"}
		if i, ok := index[id]; ok {
			t := &m.tasks[i]
			r.Name = t.File.RelativePath
			if r.Name == "" {
				r.Name = t.File.Name
			}
			_, active := m.active[id]
			switch {
			case t.Status != "failed" || active:
				r.Message = "状态已变化或仍在停止，未重复提交"
			case m.renaming[t.AccountID]:
				r.Message = "目标网盘正在修改文件，请稍后重试"
			case m.downloadAccountLocked(t.AccountID).ID == "":
				r.Message = "目标账号已删除，请重新创建任务"
			default:
				t.Status = "queued"
				t.Sent = 0
				t.Message = "等待重新上传"
				r.Applied = true
				r.Message = "重试请求已提交；请留意远端是否已产生文件"
				changed = true
			}
		}
		results = append(results, r)
	}
	if changed {
		if err := m.saveLocked(); err != nil {
			m.tasks = old
			return nil, err
		}
		m.dispatchLocked()
	}
	return results, nil
}
