package transfer

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type AccountCheckRow struct {
	AccountID string    `json:"accountId"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checkedAt"`
}
type AccountCheckStatus struct {
	ID           string            `json:"id"`
	State        string            `json:"state"`
	Rows         []AccountCheckRow `json:"rows"`
	PreviousRows []AccountCheckRow `json:"previousRows"`
}
type accountCheck struct {
	status   AccountCheckStatus
	scopes   map[string]string
	previous map[string]scopedAccountCheckRow
	cancel   context.CancelFunc
	running  bool
}

type scopedAccountCheckRow struct {
	row   AccountCheckRow
	scope string
}

func checkAccountRowScope(row AccountCheckRow, scope string, account Account) AccountCheckRow {
	if accountCheckScope(account) != scope {
		row.State, row.Message = "changed", "账号来源已变化，请重新检查"
	}
	return row
}

func (m *Manager) accountCheckSnapshotLocked(job *accountCheck) AccountCheckStatus {
	s := job.status
	s.Rows = make([]AccountCheckRow, 0, len(job.status.Rows))
	for _, row := range job.status.Rows {
		s.Rows = append(s.Rows, checkAccountRowScope(row, job.scopes[row.AccountID], m.downloadAccountLocked(row.AccountID)))
	}
	s.PreviousRows = make([]AccountCheckRow, 0, len(job.previous))
	for _, account := range m.accounts {
		if prior, ok := job.previous[account.ID]; ok {
			s.PreviousRows = append(s.PreviousRows, checkAccountRowScope(prior.row, prior.scope, account))
		}
	}
	return s
}

// Only authenticated reads. Refreshed cookies stay within this verification;
// checking availability must not replace credentials or revive write tasks.
func checkAccountRead(ctx context.Context, client *http.Client, a Account) error {
	if a.Kind == "quark" && a.Endpoint != QuarkAPI {
		return errors.New("invalid origin")
	}
	switch a.Kind {
	case "lanzou":
		if a.Endpoint != "https://up.woozooo.com" {
			return errors.New("invalid origin")
		}
		return VerifyLanzouSession(ctx, client, a)
	case "quark":
		return VerifyQuarkSession(ctx, client, a)
	case "pan123":
		return Verify123Session(ctx, client, a)
	case "baidu":
		_, e := CheckBaiduSession(ctx, client, a)
		return e
	case "guangya":
		return VerifyGuangyaSession(ctx, client, a)
	case "ctfile":
		return VerifyCtSession(ctx, client, a)
	case "ctweb":
		return CheckCtWebSession(ctx, client, a)
	case "ctdav", "webdav":
		// A configured DAV collection may be the only permitted root.
		_, e := ListDAVFolders(ctx, client, a, a.Folder)
		return e
	}
	return errors.New("unsupported account")
}
func accountCheckScope(a Account) string {
	return downloadScope(a) + "\x00" + a.UserAgent + "\x00" + a.Folder
}
func (m *Manager) StartAccountCheck(ids []string) (AccountCheckStatus, error) {
	if len(ids) == 0 || len(ids) > 20 {
		return AccountCheckStatus{}, errors.New("请选择1至20个账号")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return AccountCheckStatus{}, errors.New("程序正在退出")
	}
	if m.accountCheck != nil && m.accountCheck.running {
		return AccountCheckStatus{}, errors.New("上次检查仍在运行或停止中")
	}
	accounts := []Account{}
	scopes := map[string]string{}
	rows := []AccountCheckRow{}
	for _, id := range ids {
		a := m.downloadAccountLocked(id)
		if a.ID == "" || scopes[id] != "" || !supportsCloudSearch(a.Kind) {
			return AccountCheckStatus{}, errors.New("账号不存在、重复或不支持检查")
		}
		accounts = append(accounts, a)
		scopes[id] = accountCheckScope(a)
		rows = append(rows, AccountCheckRow{AccountID: id, State: "queued", Message: "等待检查"})
	}
	previous := map[string]scopedAccountCheckRow{}
	if last := m.accountCheck; last != nil {
		for _, row := range last.status.Rows {
			last.previous[row.AccountID] = scopedAccountCheckRow{row: row, scope: last.scopes[row.AccountID]}
		}
		for _, account := range m.accounts {
			if prior, ok := last.previous[account.ID]; ok && scopes[account.ID] == "" {
				previous[account.ID] = prior
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	job := &accountCheck{
		status:   AccountCheckStatus{ID: newID(), State: "running", Rows: rows},
		scopes:   scopes,
		previous: previous,
		cancel:   cancel,
		running:  true,
	}
	m.accountCheck = job
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		client := NewHTTPUploader().Client
		for i, a := range accounts {
			m.mu.Lock()
			if ctx.Err() != nil {
				m.mu.Unlock()
				break
			}
			if accountCheckScope(m.downloadAccountLocked(a.ID)) != scopes[a.ID] {
				job.status.Rows[i].State, job.status.Rows[i].Message = "changed", "账号来源已变化，请重新检查"
				m.mu.Unlock()
				continue
			}
			job.status.Rows[i].State, job.status.Rows[i].Message = "checking", "正在验证读取权限"
			m.mu.Unlock()
			attempt, stop := context.WithTimeout(ctx, 15*time.Second)
			e := checkAccountRead(attempt, client, a)
			stop()
			m.mu.Lock()
			r := &job.status.Rows[i]
			r.CheckedAt = time.Now()
			switch {
			case accountCheckScope(m.downloadAccountLocked(a.ID)) != scopes[a.ID]:
				r.State, r.Message = "changed", "账号来源已变化，请重新检查"
			case ctx.Err() != nil:
				r.State, r.Message = "cancelled", "检查已停止，未确认当前可用性"
			case e != nil:
				r.State, r.Message = "unconfirmed", "未能确认读取可用；请检查网络、权限或更新登录后再试"
			default:
				r.State, r.Message = "available", "本次认证读取通过；不代表写入权限或登录永久有效"
			}
			m.mu.Unlock()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		job.running = false
		job.status.State = "complete"
		if ctx.Err() != nil {
			job.status.State = "stopped"
		}
		for i := range job.status.Rows {
			r := &job.status.Rows[i]
			if r.State == "queued" || r.State == "checking" {
				r.State, r.Message = "cancelled", "检查已停止，此项未完成"
			}
		}
	}()
	return m.accountCheckSnapshotLocked(job), nil
}
func (m *Manager) AccountCheckStatus(id string) (AccountCheckStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.accountCheck
	if j == nil {
		return AccountCheckStatus{State: "idle", Rows: []AccountCheckRow{}, PreviousRows: []AccountCheckRow{}}, nil
	}
	if id != "" && id != j.status.ID {
		return AccountCheckStatus{}, errors.New("检查会话已变化，请刷新")
	}
	return m.accountCheckSnapshotLocked(j), nil
}
func (m *Manager) CancelAccountCheck(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accountCheck != nil && m.accountCheck.status.ID == id && m.accountCheck.running {
		m.accountCheck.cancel()
	}
	return nil
}
