package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// All fields stay inside encrypted account storage, never public account JSON.
// Scope may remain stable only after the server has verified the same UID on
// both credentials. Ordinary manual credential changes reset this proof.
type diskSessionRenewal struct {
	Scope     string    `json:"scope,omitempty"`
	UID       string    `json:"uid,omitempty"`
	Enabled   bool      `json:"enabled,omitempty"`
	Pending   bool      `json:"pending,omitempty"`
	Next      time.Time `json:"next,omitempty"`
	Last      time.Time `json:"last,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	Candidate string    `json:"candidate,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}
type SessionRenewalView struct {
	AccountID string    `json:"accountId"`
	Enabled   bool      `json:"enabled"`
	Pending   bool      `json:"pending"`
	Busy      bool      `json:"busy"`
	Next      time.Time `json:"next"`
	Last      time.Time `json:"last"`
	Outcome   string    `json:"outcome"`
	CanCheck  bool      `json:"canCheck"`
	CheckedAt time.Time `json:"checkedAt"`
}

func valid123UID(s string) bool {
	n, e := strconv.ParseUint(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == s
}
func validSessionRenewal(a Account, r diskSessionRenewal) bool {
	if a.Kind != "pan123" || a.Endpoint != Pan123API {
		return false
	}
	if r.Candidate != "" && (!r.Pending || !valid123Credential(r.Candidate)) {
		return false
	}
	if r.Scope != "" {
		b, e := hex.DecodeString(r.Scope)
		if e != nil || len(b) != 32 || r.Scope != strings.ToLower(r.Scope) || r.UID != "" && !valid123UID(r.UID) {
			return false
		}
	} else if r.UID != "" || r.Pending {
		return false
	}
	if r.UID == "" {
		switch r.Outcome {
		case "", "enabled", "disabled", "read_failed", "manual_reset":
			return !r.Pending
		default:
			return false
		}
	}
	switch r.Outcome {
	case "", "enabled", "disabled", "confirmed", "unconfirmed", "read_failed", "identity_changed", "candidate_mismatch":
		return true
	}
	return false
}
func freshSessionOrigin() diskSessionRenewal {
	sum := sha256.Sum256([]byte("manual-origin\x00" + newID()))
	return diskSessionRenewal{Scope: hex.EncodeToString(sum[:]), Outcome: "manual_reset"}
}
func renewalView(a Account, busy bool) SessionRenewalView {
	r := a.renewal
	return SessionRenewalView{AccountID: a.ID, Enabled: r.Enabled, Pending: r.Pending, Busy: busy, Next: r.Next, Last: r.Last, Outcome: r.Outcome, CanCheck: r.Pending && r.Candidate != "" && validSessionRenewal(a, r), CheckedAt: r.CheckedAt}
}
func (m *Manager) SessionRenewals() []SessionRenewalView {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []SessionRenewalView{}
	for _, a := range m.accounts {
		if a.Kind == "pan123" && a.Endpoint == Pan123API {
			rows = append(rows, renewalView(a, m.renewingID == a.ID))
		}
	}
	return rows
}
func (m *Manager) SetSessionAutoRenew(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, a := range m.accounts {
		if a.ID != id {
			continue
		}
		if m.closing || a.Kind != "pan123" || a.Endpoint != Pan123API || m.renewingID == id {
			return errors.New("当前不能修改此账号的登录维护设置")
		}
		if enabled && a.renewal.Pending {
			return errors.New("上次令牌更新未确认，请先手动重新登录")
		}
		before := a.renewal
		m.accounts[i].renewal.Enabled = enabled
		if !a.renewal.Pending {
			m.accounts[i].renewal.Outcome = "disabled"
		}
		if enabled {
			m.accounts[i].renewal.Next = time.Now()
			m.accounts[i].renewal.Outcome = "enabled"
		}
		if e := m.saveLocked(); e != nil {
			m.accounts[i].renewal = before
			return errors.New("设置未保存，自动更新偏好未改变")
		}
		return nil
	}
	return errors.New("账号不存在")
}
func read123UID(ctx context.Context, client *http.Client, a Account) (string, error) {
	var data struct {
		UID json.Number `json:"UID"`
	}
	if e := (pan123Client{client, a}).request(ctx, "GET", "/user/info", nil, nil, &data); e != nil {
		return "", e
	}
	if !valid123UID(string(data.UID)) {
		return "", errors.New("identity unavailable")
	}
	return string(data.UID), nil
}

// The PC refresh route is separate from the signed /b/api web read route.
// Fixed origin, no redirects, no retry, and no token leaves this function.
func refresh123Token(ctx context.Context, client *http.Client, a Account) (string, error) {
	if a.Kind != "pan123" || a.Endpoint != Pan123API {
		return "", errors.New("invalid origin")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.123278.com/api/user/refresh_token", nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+pan123Token(a.Secret))
	req.Header.Set("platform", "pc")
	req.Header.Set("app-version", "3.2.7")
	req.Header.Set("User-Agent", "123pan/3.2.7")
	req.Header.Set("Origin", "https://yun.123pan.cn")
	req.Header.Set("Referer", "https://yun.123pan.cn/")
	req.Header.Set("Accept", "application/json")
	public := *client
	public.Jar = nil
	public.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := public.Do(req)
	if e != nil {
		return "", errors.New("refresh unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("refresh rejected")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if e != nil || len(raw) > 64<<10 {
		return "", errors.New("invalid response")
	}
	var data struct {
		Code *int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Code == nil || *data.Code != 0 {
		return "", errors.New("invalid response")
	}
	token := strings.TrimSpace(data.Data.Token)
	if len(token) >= 7 && strings.EqualFold(token[:7], "Bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if !valid123Credential(token) {
		return "", errors.New("invalid credential")
	}
	return token, nil
}
func valid123Credential(token string) bool {
	if len(token) == 0 || len(token) > 16384 {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (m *Manager) renewalBusyLocked(id string) bool {
	if m.downloadImmediate {
		return true
	}
	for _, d := range m.downloads {
		if d.Input.AccountID == id && (d.Status == "queued" || d.Status == "running") {
			return true
		}
	}
	if m.accountCheck != nil && m.accountCheck.running && m.accountCheck.scopes[id] != "" {
		return true
	}
	if m.search != nil && m.search.running && m.search.scopes[id] != "" {
		return true
	}
	return false
}
func (m *Manager) renewalMatchIndexLocked(a Account) int {
	for i, current := range m.accounts {
		if current.ID == a.ID && current.Kind == a.Kind && current.Endpoint == a.Endpoint && current.Username == a.Username && current.UserAgent == a.UserAgent && current.Folder == a.Folder && current.Secret == a.Secret && downloadScope(current) == downloadScope(a) {
			return i
		}
	}
	return -1
}
func (m *Manager) RenewAccountSession(ctx context.Context, id string) (SessionRenewalView, error) {
	return m.renewAccountSession(ctx, id, false)
}
func (m *Manager) renewAccountSession(ctx context.Context, id string, automatic bool) (SessionRenewalView, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	index := -1
	var a Account
	for i, r := range m.accounts {
		if r.ID == id {
			index = i
			a = r
			break
		}
	}
	if index < 0 || m.closing || ctx.Err() != nil || a.Kind != "pan123" || a.Endpoint != Pan123API || a.renewal.Pending || m.renewalBusyLocked(id) || automatic && (!a.renewal.Enabled || a.renewal.Next.After(time.Now())) {
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("当前不能更新：请先完成或停止账号任务；未确认的更新需手动重新登录")
	}
	if e := m.reserveShareLocked(id); e != nil {
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("账号正在处理其他任务，请稍后更新")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	m.renewingID = id
	m.renewCancel = cancel
	m.workers.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.renaming, id)
		m.renewingID = ""
		m.renewCancel = nil
		m.mu.Unlock()
		m.workers.Done()
	}()
	client := NewHTTPUploader().Client
	uid, e := read123UID(ctx, client, a)
	m.mu.Lock()
	index = m.renewalMatchIndexLocked(a)
	if index < 0 || m.closing || ctx.Err() != nil {
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("账号配置变化或操作取消，未发送令牌更新")
	}
	if e != nil || a.renewal.UID != "" && uid != a.renewal.UID {
		m.accounts[index].renewal.Enabled = false
		m.accounts[index].renewal.Last = time.Now()
		m.accounts[index].renewal.Next = time.Now().Add(12 * time.Hour)
		m.accounts[index].renewal.Outcome = "read_failed"
		if e == nil {
			generation := sha256.Sum256([]byte(downloadScope(a) + "\x00" + uid + "\x00" + newID()))
			m.accounts[index].renewal = diskSessionRenewal{Scope: hex.EncodeToString(generation[:]), UID: uid, Outcome: "identity_changed", Last: time.Now()}
			m.dropFingerprintsLocked(id, "")
		}
		_ = m.saveLocked()
		view := renewalView(m.accounts[index], false)
		m.mu.Unlock()
		return view, errors.New("未能确认原账号身份，未发送令牌更新；请检查网络或手动登录")
	}
	// Persist uncertainty before the refresh can invalidate the old token.
	before := m.accounts[index].renewal
	r := before
	r.Scope = downloadScope(a)
	r.UID = uid
	r.Pending = true
	r.Last = time.Now()
	r.Next = time.Now().Add(12 * time.Hour)
	r.Outcome = "unconfirmed"
	m.accounts[index].renewal = r
	if e = m.saveLocked(); e != nil {
		m.accounts[index].renewal = before
		m.accounts[index].renewal.Next = time.Now().Add(12 * time.Hour)
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("无法保存更新检查点，未发送令牌更新")
	}
	m.mu.Unlock()
	token, e := refresh123Token(ctx, client, a)
	mismatch := false
	if e == nil {
		// Persist the received credential before any second network request.
		// A later read/save failure can then be recovered without refreshing again.
		m.mu.Lock()
		index = m.renewalMatchIndexLocked(a)
		if index < 0 {
			m.mu.Unlock()
			return SessionRenewalView{}, errors.New("账号配置已变化，未采用返回的令牌")
		}
		prior := m.accounts[index].renewal
		m.accounts[index].renewal.Candidate = token
		if err := m.saveLocked(); err != nil {
			m.accounts[index].renewal = prior
			m.accounts[index].renewal.Enabled = false
			view := renewalView(m.accounts[index], false)
			m.mu.Unlock()
			return view, errors.New("已收到的令牌未能保存，请手动重新登录；不会重复请求更新")
		}
		m.mu.Unlock()
		candidate := a
		candidate.Secret = token
		var current string
		current, e = read123UID(ctx, client, candidate)
		if e == nil && current != uid {
			mismatch = true
			e = errors.New("identity changed")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index = m.renewalMatchIndexLocked(a)
	if index < 0 {
		return SessionRenewalView{}, errors.New("账号配置已变化，未采用返回的令牌")
	}
	current := m.accounts[index]
	if token != "" {
		m.accounts[index].renewal.CheckedAt = time.Now()
	}
	if current.ID != a.ID || current.Kind != a.Kind || current.Endpoint != a.Endpoint || current.Username != a.Username || current.UserAgent != a.UserAgent || current.Secret != a.Secret || downloadScope(current) != downloadScope(a) {
		return SessionRenewalView{}, errors.New("账号配置已变化，未采用返回的令牌")
	}
	if e != nil || m.closing || ctx.Err() != nil {
		m.accounts[index].renewal.Enabled = false
		if mismatch {
			m.accounts[index].renewal.Candidate = ""
			m.accounts[index].renewal.Outcome = "candidate_mismatch"
		}
		_ = m.saveLocked()
		return renewalView(m.accounts[index], false), errors.New("令牌更新结果未确认，自动更新已暂停；有已保存令牌可只读核对，否则请手动重新登录")
	}
	beforeAccount := current
	m.accounts[index].Secret = token
	m.accounts[index].renewal.Pending = false
	m.accounts[index].renewal.Candidate = ""
	m.accounts[index].renewal.Outcome = "confirmed"
	if e = m.saveLocked(); e != nil {
		m.accounts[index] = beforeAccount
		m.accounts[index].renewal.Enabled = false
		return renewalView(m.accounts[index], false), errors.New("更新结果未能保存，旧回执保留未确认状态；请手动重新登录")
	}
	return renewalView(m.accounts[index], false), nil
}

// CheckSessionRenewal only reads the credential already received and durably
// bound to the original server identity. It never calls the refresh endpoint.
func (m *Manager) CheckSessionRenewal(ctx context.Context, id string) (SessionRenewalView, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	var a Account
	for _, row := range m.accounts {
		if row.ID == id {
			a = row
			break
		}
	}
	if a.ID == "" || m.closing || ctx.Err() != nil || !renewalView(a, false).CanCheck || m.renewalBusyLocked(id) {
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("当前无法核对：需要原账号已保存的待确认令牌，且账号任务已停止")
	}
	if e := m.reserveShareLocked(id); e != nil {
		m.mu.Unlock()
		return SessionRenewalView{}, errors.New("账号正在处理其他任务，请稍后核对")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	m.renewingID = id
	m.renewCancel = cancel
	m.workers.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.renaming, id)
		m.renewingID = ""
		m.renewCancel = nil
		m.mu.Unlock()
		m.workers.Done()
	}()
	candidate := a
	candidate.Secret = a.renewal.Candidate
	uid, e := read123UID(ctx, NewHTTPUploader().Client, candidate)
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.renewalMatchIndexLocked(a)
	if index < 0 {
		return SessionRenewalView{}, errors.New("账号配置已变化，未采用待确认令牌")
	}
	current := m.accounts[index]
	if !current.renewal.Pending || current.renewal.Candidate != a.renewal.Candidate || current.renewal.UID != a.renewal.UID {
		return SessionRenewalView{}, errors.New("原待确认记录已变化，请刷新本机状态")
	}
	m.accounts[index].renewal.Enabled = false
	m.accounts[index].renewal.CheckedAt = time.Now()
	if e != nil || ctx.Err() != nil || m.closing {
		m.accounts[index].renewal.Outcome = "unconfirmed"
		if err := m.saveLocked(); err != nil {
			return renewalView(m.accounts[index], false), errors.New("核对与状态保存未完成，原待确认记录保留")
		}
		return renewalView(m.accounts[index], false), errors.New("本次未能核对身份，原待确认令牌保留；可稍后再次只读核对")
	}
	if uid != a.renewal.UID {
		m.accounts[index].renewal.Candidate = ""
		m.accounts[index].renewal.Outcome = "candidate_mismatch"
		if err := m.saveLocked(); err != nil {
			return renewalView(m.accounts[index], false), errors.New("身份不一致，未采用令牌；状态未能保存，请手动重新登录")
		}
		return renewalView(m.accounts[index], false), errors.New("待确认令牌身份不一致，已弃用；请手动重新登录")
	}
	m.accounts[index].Secret = candidate.Secret
	m.accounts[index].renewal.Candidate = ""
	m.accounts[index].renewal.Pending = false
	m.accounts[index].renewal.Outcome = "confirmed"
	if err := m.saveLocked(); err != nil {
		m.accounts[index] = current
		m.accounts[index].renewal.Enabled = false
		return renewalView(m.accounts[index], false), errors.New("核对结果未能保存，原待确认记录保留；可稍后再次只读核对")
	}
	return renewalView(m.accounts[index], false), nil
}
func (m *Manager) startSessionRenewalLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	m.renewLoopCancel = cancel
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.renewDueSessions(ctx, time.Now())
			}
		}
	}()
}
func (m *Manager) renewDueSessions(ctx context.Context, now time.Time) {
	m.mu.Lock()
	ids := []string{}
	for _, a := range m.accounts {
		if a.Kind == "pan123" && a.renewal.Enabled && !a.renewal.Pending && !a.renewal.Next.After(now) {
			ids = append(ids, a.ID)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		_, _ = m.renewAccountSession(ctx, id, true)
	}
}
