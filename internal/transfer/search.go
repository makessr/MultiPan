package transfer

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

type CloudSearchInput struct {
	AccountIDs           []string `json:"accountIds"`
	Query                string   `json:"query"`
	IncludePath          bool     `json:"includePath"`
	FirstMatchPerAccount bool     `json:"firstMatchPerAccount"`
	ListLanzouCandidates bool     `json:"listLanzouCandidates,omitempty"`
}
type CloudSearchResult struct {
	AccountID string        `json:"accountId"`
	Entry     CloudEntry    `json:"entry"`
	ParentID  string        `json:"parentId"`
	Path      string        `json:"path"`
	Trail     []CloudFolder `json:"trail"`
}
type CloudSearchStatus struct {
	ID               string              `json:"id"`
	State            string              `json:"state"`
	Message          string              `json:"message"`
	Scanned          int                 `json:"scanned"`
	Directories      int                 `json:"directories"`
	Current          string              `json:"current"`
	CurrentAccountID string              `json:"currentAccountId"`
	Items            []CloudSearchResult `json:"items"`
	Issues           []string            `json:"issues"`
}
type cloudSearch struct {
	status  CloudSearchStatus
	cancel  context.CancelFunc
	running bool
	scopes  map[string]string
}

var errSearchSourceChanged = errors.New("搜索账号来源已变化，请重新搜索")

// Root is depth 0. Read only root, its children, and grandchildren directories.
const cloudSearchMaxDepth = 2

func supportsCloudSearch(kind string) bool {
	switch kind {
	case "lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav", "ctfile", "ctweb":
		return true
	}
	return false
}

func (m *Manager) StartCloudSearch(in CloudSearchInput) (CloudSearchStatus, error) {
	in.Query = strings.TrimSpace(in.Query)
	if in.ListLanzouCandidates {
		if len(in.AccountIDs) != 1 || in.Query != "" || in.IncludePath || in.FirstMatchPerAccount {
			return CloudSearchStatus{}, errors.New("蓝奏候选清单只接受一个指定账号")
		}
	} else if len([]rune(in.Query)) < 1 || len([]rune(in.Query)) > 100 || len(in.AccountIDs) == 0 || len(in.AccountIDs) > 20 {
		return CloudSearchStatus{}, errors.New("请输入1至100字搜索词，选择1至20个账号")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return CloudSearchStatus{}, errors.New("程序正在退出")
	}
	if m.search != nil && m.search.running {
		return CloudSearchStatus{}, errors.New("上次搜索仍在运行或停止中，请稍后再试")
	}
	accounts := []Account{}
	seen := map[string]bool{}
	scopes := map[string]string{}
	for _, id := range in.AccountIDs {
		a := m.downloadAccountLocked(id)
		if seen[id] || a.ID == "" || !supportsCloudSearch(a.Kind) || in.ListLanzouCandidates && a.Kind != "lanzou" {
			return CloudSearchStatus{}, errors.New("搜索账号无效、重复或不支持浏览")
		}
		seen[id] = true
		accounts = append(accounts, a)
		scopes[id] = downloadScope(a)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	job := &cloudSearch{status: CloudSearchStatus{ID: newID(), State: "running", Message: "正在逐目录扫描", Items: []CloudSearchResult{}, Issues: []string{}}, cancel: cancel, running: true, scopes: scopes}
	m.search = job
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		client := NewHTTPUploader().Client
		list := func(ctx context.Context, a Account, id string) (FilePage, error) {
			m.mu.Lock()
			valid := downloadScope(m.downloadAccountLocked(a.ID)) == scopes[a.ID]
			m.mu.Unlock()
			if !valid {
				return FilePage{}, errSearchSourceChanged
			}
			return ListAccountFiles(ctx, client, a, id)
		}
		result := scanCloudSearch(ctx, accounts, in, list, func(s CloudSearchStatus) { m.mu.Lock(); defer m.mu.Unlock(); s.ID = job.status.ID; job.status = s })
		m.mu.Lock()
		defer m.mu.Unlock()
		for id, scope := range scopes {
			if downloadScope(m.downloadAccountLocked(id)) != scope {
				result.State = "invalidated"
				result.Message = errSearchSourceChanged.Error()
				result.Items = []CloudSearchResult{}
			}
		}
		result.ID = job.status.ID
		job.status = result
		job.running = false
	}()
	return cloneCloudSearch(job.status), nil
}
func cloneCloudSearch(s CloudSearchStatus) CloudSearchStatus {
	s.Items = append([]CloudSearchResult{}, s.Items...)
	for i := range s.Items {
		s.Items[i].Trail = append([]CloudFolder{}, s.Items[i].Trail...)
		if s.Items[i].Entry.Size != nil {
			n := *s.Items[i].Entry.Size
			s.Items[i].Entry.Size = &n
		}
	}
	s.Issues = append([]string{}, s.Issues...)
	return s
}
func (m *Manager) CloudSearchStatus(id string) (CloudSearchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.search == nil || m.search.status.ID != id {
		return CloudSearchStatus{}, errors.New("搜索会话已变化，请重新开始")
	}
	for accountID, scope := range m.search.scopes {
		if downloadScope(m.downloadAccountLocked(accountID)) != scope {
			m.search.cancel()
			s := cloneCloudSearch(m.search.status)
			s.State = "invalidated"
			s.Message = errSearchSourceChanged.Error()
			s.Items = []CloudSearchResult{}
			return s, nil
		}
	}
	return cloneCloudSearch(m.search.status), nil
}
func (m *Manager) CancelCloudSearch(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.search != nil && m.search.status.ID == id && m.search.running {
		m.search.cancel()
	}
	return nil
}

func scanCloudSearch(ctx context.Context, accounts []Account, in CloudSearchInput, list func(context.Context, Account, string) (FilePage, error), progress func(CloudSearchStatus)) CloudSearchStatus {
	s := CloudSearchStatus{State: "running", Items: []CloudSearchResult{}, Issues: []string{}}
	needle := strings.ToLower(strings.TrimSpace(in.Query))
	firstPerAccount := in.FirstMatchPerAccount || len(accounts) > 1
	hadIssue := false
	issue := func(text string) {
		hadIssue = true
		if len(s.Issues) < 20 {
			s.Issues = append(s.Issues, text)
		}
	}
	type directory struct {
		id    string
		trail []CloudFolder
		depth int
	}
accountLoop:
	for _, a := range accounts {
		s.CurrentAccountID = a.ID
		root := cloudRoot(a.Kind)
		queue := []directory{{id: root, trail: []CloudFolder{{ID: root, Name: "根目录"}}}}
		seen := map[string]bool{root: true}
		for len(queue) > 0 {
			if e := ctx.Err(); e != nil {
				s.State = "cancelled"
				s.Message = "搜索已停止，以下为已扫描部分，不代表完整结果"
				if errors.Is(e, context.DeadlineExceeded) {
					s.Message = "搜索已达到2分钟时限，以下为部分结果"
				}
				return s
			}
			if s.Directories >= 500 {
				s.State = "limited"
				s.Message = "达到500个目录上限，未扫描完整"
				return s
			}
			d := queue[0]
			queue = queue[1:]
			names := []string{}
			for _, c := range d.trail {
				names = append(names, c.Name)
			}
			s.Current = strings.Join(names, " / ")
			s.Directories++
			progress(cloneCloudSearch(s))
			p, e := list(ctx, a, d.id)
			if errors.Is(e, errSearchSourceChanged) {
				s.State = "invalidated"
				s.Message = e.Error()
				s.Items = []CloudSearchResult{}
				return s
			}
			if e != nil {
				if ctx.Err() != nil {
					continue
				}
				issue(fmt.Sprintf("账号 %s / %s：%v", a.ID, s.Current, e))
				continue
			}
			if p.CurrentID != d.id {
				issue("目录来源不一致，已跳过")
				continue
			}
			entries := map[string]bool{}
			for _, entry := range p.Entries {
				if ctx.Err() != nil {
					break
				}
				if s.Scanned >= 5000 || len(s.Items) >= 1000 {
					s.State = "limited"
					s.Message = "达到5000个项目或1000条匹配上限，未扫描完整"
					return s
				}
				s.Scanned++
				key := fmt.Sprintf("%t:%s", entry.Folder, entry.ID)
				if entry.ID == "" || entry.Name == "" || len(entry.Name) > 4096 || entries[key] {
					issue("发现不完整或重复项目，已跳过")
					continue
				}
				entries[key] = true
				if (a.Kind == "baidu" || a.Kind == "ctdav" || a.Kind == "webdav") && (!validBaiduPath(entry.ID) || path.Dir(entry.ID) != d.id || path.Base(entry.ID) != entry.Name) {
					issue("项目路径不属于当前目录，已跳过")
					continue
				}
				display := s.Current + " / " + entry.Name
				// A deeper directory's name can be present in this listing, but it
				// is outside the requested search scope and must not be a result.
				if entry.Folder && d.depth >= cloudSearchMaxDepth {
					continue
				}
				text := entry.Name
				if in.IncludePath {
					text = display + " " + strings.ReplaceAll(display, " / ", "/")
				}
				if in.ListLanzouCandidates || strings.Contains(strings.ToLower(text), needle) {
					s.Items = append(s.Items, CloudSearchResult{AccountID: a.ID, Entry: entry, ParentID: d.id, Path: display, Trail: append([]CloudFolder{}, d.trail...)})
					if firstPerAccount {
						progress(cloneCloudSearch(s))
						continue accountLoop
					}
				}
				if entry.Folder {
					if seen[entry.ID] {
						issue("检测到目录循环或重复，已跳过")
						continue
					}
					seen[entry.ID] = true
					queue = append(queue, directory{id: entry.ID, trail: append(append([]CloudFolder{}, d.trail...), CloudFolder{ID: entry.ID, Name: entry.Name}), depth: d.depth + 1})
				}
			}
			progress(cloneCloudSearch(s))
		}
	}
	if ctx.Err() != nil {
		s.State = "cancelled"
		s.Message = "搜索已停止，以下为已扫描部分，不代表完整结果"
	} else if hadIssue {
		s.State = "partial"
		s.Message = "扫描结束，部分目录读取失败或被跳过，结果不完整"
	} else {
		s.State = "completed"
		s.Message = "已完成根目录及两级子目录范围扫描；更深目录不搜索，结果反映扫描时状态"
		if firstPerAccount {
			s.Message = "逐盘查找结束：每个网盘找到首个匹配即切换，只保留每盘首个匹配，不包含全部匹配项"
		}
	}
	return s
}
