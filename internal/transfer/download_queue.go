package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DownloadJob struct {
	ID          string         `json:"id"`
	Input       DownloadInput  `json:"input"`
	Destination string         `json:"destination"`
	Kind        string         `json:"kind"`
	Scope       string         `json:"scope,omitempty"`
	Status      string         `json:"status"`
	Message     string         `json:"message"`
	Received    int64          `json:"received"`
	Total       int64          `json:"total"`
	Created     string         `json:"created"`
	Result      DownloadResult `json:"result"`
}

func downloadScope(a Account) string {
	if a.Kind == "pan123" && a.Endpoint == Pan123API && a.renewal.Scope != "" {
		return a.renewal.Scope
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{a.Kind, a.Endpoint, a.Username, a.Secret}, "\x00")))
	return hex.EncodeToString(sum[:])
}
func (m *Manager) downloadAccountLocked(id string) Account {
	for _, a := range m.accounts {
		if a.ID == id {
			return a
		}
	}
	return Account{}
}
func (m *Manager) QueueDownloads(inputs []DownloadInput, directory string) (int, error) {
	if err := ValidateDownloadBatch(inputs); err != nil {
		return 0, err
	}
	info, err := os.Stat(directory)
	if err != nil || !filepath.IsAbs(directory) || !info.IsDir() {
		return 0, errors.New("保存目录无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	if len(m.downloads)+len(inputs) > 5000 {
		return 0, errors.New("下载记录超过5000项，请先清理已完成记录")
	}
	reserved := map[string]bool{}
	for _, j := range m.downloads {
		if j.Status != "saved" {
			reserved[strings.ToLower(j.Destination)] = true
		}
	}
	jobs := make([]DownloadJob, 0, len(inputs))
	for _, in := range inputs {
		a := m.downloadAccountLocked(in.AccountID)
		if a.ID == "" {
			return 0, errors.New("账号不存在")
		}
		dest := filepath.Join(directory, SuggestedDownloadName(in.Name))
		key := strings.ToLower(dest)
		if _, e := os.Lstat(dest); !errors.Is(e, os.ErrNotExist) || reserved[key] {
			return 0, errors.New("保存目录已有同名项目或任务，请选择其他目录")
		}
		reserved[key] = true
		jobs = append(jobs, DownloadJob{ID: newID(), Input: in, Destination: dest, Kind: a.Kind, Scope: downloadScope(a), Status: "queued", Total: -1, Created: now(), Message: "等待下载"})
	}
	old := len(m.downloads)
	m.downloads = append(m.downloads, jobs...)
	if e := m.saveLocked(); e != nil {
		m.downloads = m.downloads[:old]
		return 0, e
	}
	m.dispatchDownloadsLocked()
	return len(jobs), nil
}
func (m *Manager) dispatchDownloadsLocked() {
	if m.closing || m.downloadImmediate {
		return
	}
	if m.downloadActive == nil {
		m.downloadActive = map[string]context.CancelFunc{}
	}
	for i := range m.downloads {
		if len(m.downloadActive) >= normalizeSettings(m.settings).DownloadConcurrency {
			return
		}
		j := &m.downloads[i]
		if j.Status != "queued" {
			continue
		}
		ctx, cancel := context.WithCancel(withTransferLimits(context.Background(), m.limits))
		m.downloadActive[j.ID] = cancel
		j.Status = "running"
		j.Message = "正在下载"
		copy := *j
		m.workers.Add(1)
		go func() { defer m.workers.Done(); m.runDownloadJob(ctx, copy) }()
	}
}
func (m *Manager) runDownloadJob(ctx context.Context, j DownloadJob) {
	m.mu.Lock()
	a := m.downloadAccountLocked(j.Input.AccountID)
	m.mu.Unlock()
	var result DownloadResult
	var err error
	if a.ID == "" || downloadScope(a) != j.Scope {
		err = errors.New("账号来源或登录信息已变化，请重新创建下载任务")
	} else {
		last := time.Now()
		ctx = withResumePath(ctx, j.Destination+".multipan-"+j.ID+".part")
		result, err = m.downloadFile(ctx, j.Input, j.Destination, func(p DownloadProgress) {
			m.mu.Lock()
			defer m.mu.Unlock()
			for i := range m.downloads {
				if m.downloads[i].ID == j.ID {
					m.downloads[i].Received = p.Received
					m.downloads[i].Total = p.Total
					break
				}
			}
			if time.Since(last) > 2*time.Second {
				_ = m.saveLocked()
				last = time.Now()
			}
		})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.downloadActive[j.ID]; cancel != nil {
		defer cancel()
	}
	delete(m.downloadActive, j.ID)
	for i := range m.downloads {
		v := &m.downloads[i]
		if v.ID != j.ID {
			continue
		}
		v.Result = result
		if err == nil && result.Path != "" {
			v.Status = "saved"
			v.Message = "已保存"
			v.Received = result.Bytes
		} else if ctx.Err() != nil || m.closing {
			v.Status = "paused"
			v.Message = "已暂停；可继续，节点不支持续传时安全重下"
		} else {
			v.Status = "failed"
			if err != nil {
				v.Message = err.Error()
			} else {
				v.Message = "下载未返回保存结果"
			}
		}
		break
	}
	_ = m.saveLocked()
	m.dispatchDownloadsLocked()
}
func (m *Manager) ControlDownload(id string, resume bool) error {
	action := "pause"
	if resume {
		action = "resume"
	}
	results, e := m.BatchDownloadAction([]string{id}, action)
	if e != nil {
		return e
	}
	if len(results) != 1 || !results[0].Applied {
		if len(results) == 1 {
			return errors.New(results[0].Message)
		}
		return errors.New("下载任务不存在")
	}
	return nil
}

// Existing single/batch/folder downloads also enter durable history, without changing their UI contracts.
func (m *Manager) DownloadFile(ctx context.Context, in DownloadInput, destination string, progress func(DownloadProgress)) (DownloadResult, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return DownloadResult{}, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(in.AccountID)
	job := DownloadJob{ID: newID(), Input: in, Destination: destination, Kind: a.Kind, Status: "running", Total: -1, Created: now(), Scope: downloadScope(a)}
	if len(m.downloads) >= 5000 {
		m.mu.Unlock()
		return DownloadResult{}, errors.New("下载历史已满，请清理已完成记录")
	}
	m.downloads = append(m.downloads, job)
	if e := m.saveLocked(); e != nil {
		m.downloads = m.downloads[:len(m.downloads)-1]
		m.mu.Unlock()
		return DownloadResult{}, e
	}
	if len(m.downloadActive) > 0 {
		m.downloads = m.downloads[:len(m.downloads)-1]
		_ = m.saveLocked()
		m.mu.Unlock()
		return DownloadResult{}, errors.New("后台下载队列正在工作，请使用加入队列")
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(withTransferLimits(ctx, m.limits))
	if m.downloadActive == nil {
		m.downloadActive = map[string]context.CancelFunc{}
	}
	m.downloadActive[job.ID] = cancel
	m.downloadImmediate = true
	m.workers.Add(1)
	defer m.workers.Done()
	m.mu.Unlock()
	result, err := m.downloadFile(withResumePath(ctx, destination+".multipan-"+job.ID+".part"), in, destination, progress)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.downloadActive, job.ID)
	m.downloadImmediate = false
	defer cancel()
	defer m.dispatchDownloadsLocked()
	for i := range m.downloads {
		j := &m.downloads[i]
		if j.ID != job.ID {
			continue
		}
		j.Result = result
		if err == nil && result.Path != "" {
			j.Status = "saved"
			j.Received = result.Bytes
			j.Message = "已保存"
		} else {
			j.Status = "failed"
			if ctx.Err() != nil {
				j.Status = "paused"
				j.Message = "已取消，可从队列重新开始"
			} else if err != nil {
				j.Message = err.Error()
			}
		}
		break
	}
	_ = m.saveLocked()
	return result, err
}
func (m *Manager) ClearSavedDownloads() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.downloads
	var keep []DownloadJob
	for _, j := range old {
		if j.Status != "saved" {
			keep = append(keep, j)
		}
	}
	m.downloads = keep
	if e := m.saveLocked(); e != nil {
		m.downloads = old
		return e
	}
	return nil
}

func (m *Manager) RestartUpload(id string) error {
	m.mu.Lock()
	if _, active := m.active[id]; active {
		m.mu.Unlock()
		return errors.New("请先等待当前上传停止")
	}
	found := false
	for _, t := range m.tasks {
		if t.ID == id && (t.Status == "failed" || t.Status == "paused") {
			found = true
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return errors.New("任务无需重新开始")
	}
	e := os.Remove(filepath.Join(filepath.Dir(m.path), "upload-checkpoints", id+".enc"))
	m.mu.Unlock()
	if e != nil && !os.IsNotExist(e) {
		return errors.New("无法清除续传记录")
	}
	return m.Retry(id)
}

type directoryQueueKey struct{}

func (m *Manager) QueueDownloadFolder(ctx context.Context, in DownloadInput, parent string) (int, error) {
	count := 0
	collect := func(plan directoryPlan, root string) (BatchDownloadResult, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closing {
			return BatchDownloadResult{}, errors.New("程序正在退出")
		}
		a := m.downloadAccountLocked(in.AccountID)
		if a.ID == "" {
			return BatchDownloadResult{}, errors.New("账号不存在")
		}
		if len(m.downloads)+len(plan.files) > 5000 {
			return BatchDownloadResult{}, errors.New("下载历史已满，请先清理")
		}
		old := len(m.downloads)
		for _, f := range plan.files {
			m.downloads = append(m.downloads, DownloadJob{ID: newID(), Input: f.input, Destination: filepath.Join(root, f.relative), Kind: a.Kind, Scope: downloadScope(a), Status: "queued", Total: -1, Created: now(), Message: "等待下载"})
		}
		if e := m.saveLocked(); e != nil {
			m.downloads = m.downloads[:old]
			return BatchDownloadResult{}, e
		}
		count = len(plan.files)
		m.dispatchDownloadsLocked()
		return BatchDownloadResult{Directory: root}, nil
	}
	_, err := m.DownloadFolder(context.WithValue(ctx, directoryQueueKey{}, collect), in, parent, nil)
	return count, err
}
