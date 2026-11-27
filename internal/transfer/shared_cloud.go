package transfer

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ManagedShare contains a protected local handle, never a raw provider ID.
type ManagedShare struct {
	replacementAvailable bool
	PriceFen             *int64 `json:"priceFen,omitempty"`
	EarningsFen          *int64 `json:"earningsFen,omitempty"`
	CanLimitTraffic123   bool   `json:"canLimitTraffic123,omitempty"`
	QuarkAccess          string `json:"quarkAccess,omitempty"`
	QuarkRestricted      int64  `json:"quarkRestricted,omitempty"`
	CanActivate          bool   `json:"canActivate,omitempty"`
	CanEdit              bool   `json:"canEdit"`
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Status               string `json:"status"`
	Created              string `json:"created"`
	Expiry               string `json:"expiry"`
	CanRevoke            bool   `json:"canRevoke"`
	SelectionKey         string `json:"selectionKey"`
}
type ManagedSharePage struct {
	Items   []ManagedShare `json:"items"`
	Page    int            `json:"page"`
	HasMore bool           `json:"hasMore"`
	Next    string         `json:"next"`

	quarkTotal *int64
	quarkRows  []quarkShareRecord
	pan123Next string
}
type ManagedShareLink struct {
	TrafficMode123 *int   `json:"trafficMode123,omitempty"`
	TrafficCap123  *int64 `json:"trafficCap123,omitempty"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Password       string `json:"password"`
	Expiry         string `json:"expiry"`
	Message        string `json:"message"`
}
type managedShareHandle struct {
	AccountID                string          `json:"accountId"`
	Scope                    string          `json:"scope"`
	ProviderID               string          `json:"providerId"`
	Name                     string          `json:"name"`
	URL                      string          `json:"url"`
	Kind                     string          `json:"kind,omitempty"`
	Page                     int             `json:"page,omitempty"`
	Cursor                   string          `json:"cursor,omitempty"`
	QuarkFilter              string          `json:"quarkFilter,omitempty"`
	QuarkLinkKey             string          `json:"quarkLinkKey,omitempty"`
	Key123                   string          `json:"key123,omitempty"`
	AlternateLinks123        []string        `json:"alternateLinks123,omitempty"`
	AlternateLinksIncomplete bool            `json:"alternateLinksIncomplete,omitempty"`
	Paid123                  bool            `json:"paid123,omitempty"`
	RawID                    json.RawMessage `json:"rawId,omitempty"`
	PublicID                 string          `json:"publicId,omitempty"`
	Settings123              string          `json:"settings123,omitempty"`
	Deadline123              string          `json:"deadline123,omitempty"`
	TrafficCap123            *int64          `json:"trafficCap123,omitempty"`
	TrafficMode123           *int            `json:"trafficMode123,omitempty"`
	ActivationBaidu          string          `json:"activationBaidu,omitempty"`
	PathBaidu                string          `json:"pathBaidu,omitempty"`
}

var managedShareSelectionSecret = func() [32]byte {
	var key [32]byte
	rand.Read(key[:])
	return key
}()

// This identifies one account's share across list reads without exposing the
// provider ID or replacing the protected handle used to read the link.
func managedShareSelectionKey(h managedShareHandle) string {
	mac := hmac.New(sha256.New, managedShareSelectionSecret[:])
	mac.Write([]byte(strings.Join([]string{h.AccountID, h.Kind, h.Scope, h.ProviderID, strconv.FormatBool(h.Paid123)}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func managedShareExpiry(kind *int, expires int64) string {
	if expires > 0 {
		return "到期：" + managedShareTime(expires)
	}
	if kind != nil && *kind == 1 {
		return "永久"
	}
	return "有效期以网盘显示为准"
}
func managedShareTime(value int64) string {
	if value <= 0 {
		return ""
	}
	if value >= 1000000000000 {
		return time.UnixMilli(value).Format(time.RFC3339)
	}
	return time.Unix(value, 0).Format(time.RFC3339)
}

func (m *Manager) managedShareAccount(id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.downloadAccountLocked(id)
	if m.closing || a.ID == "" {
		return Account{}, errors.New("程序正在退出或账号不存在")
	}
	if !(a.Kind == "quark" && a.Endpoint == QuarkAPI || a.Kind == "pan123" && a.Endpoint == Pan123API || a.Kind == "baidu" && a.Endpoint == BaiduAPI || a.Kind == "guangya" && a.Endpoint == GuangyaAPI) {
		return Account{}, errors.New("云端分享列表当前支持夸克、123、百度和光鸭")
	}
	return a, nil
}

func (m *Manager) ManagedShares(ctx context.Context, accountID string, page int) (ManagedSharePage, error) {
	return m.managedShares(ctx, accountID, page, "")
}

func (m *Manager) ManagedQuarkShares(ctx context.Context, accountID string, page int, filter string) (ManagedSharePage, error) {
	if filter == "" || !validQuarkShareFilter(filter) {
		return ManagedSharePage{}, errors.New("夸克分享筛选条件无效")
	}
	return m.managedShares(ctx, accountID, page, filter)
}

func (m *Manager) managedShares(ctx context.Context, accountID string, page int, quarkFilter string) (ManagedSharePage, error) {
	if page < 1 || page > 1000 {
		return ManagedSharePage{}, errors.New("分享列表页码无效")
	}
	a, err := m.managedShareAccount(accountID)
	if err != nil {
		return ManagedSharePage{}, err
	}
	if quarkFilter != "" && a.Kind != "quark" {
		return ManagedSharePage{}, errors.New("此筛选仅用于夸克分享")
	}
	var result ManagedSharePage
	client := NewHTTPUploader().Client
	switch a.Kind {
	case "pan123":
		if page != 1 {
			return result, errors.New("123翻页需使用原分页标识，请刷新第一页")
		}
		result, err = pan123ManagedShares(ctx, client, a, "0", 1)
	case "baidu":
		result, err = baiduManagedShares(ctx, client, a, page)
	case "guangya":
		result, err = gyManagedShares(ctx, client, a, page)
	default:
		result, err = quarkManagedSharesFiltered(ctx, client, a, page, quarkFilter)
	}
	if err != nil {
		return ManagedSharePage{}, err
	}
	if current, e := m.managedShareAccount(accountID); e != nil || downloadScope(current) != downloadScope(a) {
		return ManagedSharePage{}, errors.New("账号来源已变化，请重新读取云端分享")
	}
	return result, nil
}

func quarkManagedShares(ctx context.Context, client *http.Client, a Account, page int) (ManagedSharePage, error) {
	return quarkManagedSharesFiltered(ctx, client, a, page, "")
}

func validQuarkShareFilter(filter string) bool {
	switch filter {
	case "", "all", "valid", "violated", "expired", "failed", "private":
		return true
	default:
		return false
	}
}

func quarkManagedSharesFiltered(ctx context.Context, client *http.Client, a Account, page int, filter string) (ManagedSharePage, error) {
	return quarkManagedSharesFromClient(ctx, newQuarkClient(client, a), page, filter)
}

type quarkShareRecord struct {
	ID          json.RawMessage `json:"share_id"`
	Title       string          `json:"title"`
	URL         string          `json:"share_url"`
	Key         json.RawMessage `json:"pwd_id"`
	Status      *int            `json:"status"`
	Mode        json.RawMessage `json:"url_type"`
	Created     int64           `json:"created_at"`
	Expires     int64           `json:"expired_at"`
	ExpiryType  *int            `json:"expired_type"`
	Audit       json.RawMessage `json:"audit_status"`
	Partial     json.RawMessage `json:"partial_violation"`
	Restricted  json.RawMessage `json:"violation_cnt"`
	DetailToken json.RawMessage `json:"stoken"`
}

func (row quarkShareRecord) unavailableStatus(now time.Time) string {
	if row.Status != nil && *row.Status == 3 {
		return "源文件已删除"
	}
	if string(row.Audit) == "2" || string(row.Audit) == "3" {
		return "分享违规"
	}
	if row.Expires > 0 && (row.ExpiryType == nil || *row.ExpiryType != 1) {
		deadline := time.Unix(row.Expires, 0)
		if row.Expires >= 1000000000000 {
			deadline = time.UnixMilli(row.Expires)
		}
		if !deadline.After(now) {
			return "分享已过期"
		}
	}
	return ""
}

func (row quarkShareRecord) accessMode() string {
	switch string(row.Mode) {
	case "1":
		return "public"
	case "2":
		return "protected"
	}
	return ""
}

func quarkManagedSharesFromClient(ctx context.Context, c *quarkClient, page int, filter string) (ManagedSharePage, error) {
	if !validQuarkShareFilter(filter) {
		return ManagedSharePage{}, errors.New("夸克分享筛选条件无效")
	}
	const pageSize = 50
	a := c.account
	var data struct {
		List *[]quarkShareRecord `json:"list"`
	}
	query := url.Values{"_page": {strconv.Itoa(page)}, "_size": {strconv.Itoa(pageSize)}, "_order_field": {"created_at"}, "_order_type": {"desc"}, "_fetch_total": {"1"}}
	if filter != "" {
		query.Set("_filter", filter)
		query.Set("_share_type", "0,1")
	}
	meta, err := c.request(ctx, "GET", "/share/mypage/detail", query, nil, &data)
	if err != nil {
		return ManagedSharePage{}, err
	}
	if data.List == nil || len(*data.List) > pageSize {
		return ManagedSharePage{}, errors.New("夸克分享列表格式不完整，请到官网核对 [SHARE-LIST/QK]")
	}
	var metadata struct {
		Total *int64 `json:"_total"`
	}
	if len(meta) != 0 && json.Unmarshal(meta, &metadata) != nil {
		return ManagedSharePage{}, errors.New("夸克分享分页信息无效，请刷新列表 [SHARE-LIST/QK]")
	}
	rowCount := len(*data.List)
	result := ManagedSharePage{Page: page, HasMore: rowCount == pageSize, Items: []ManagedShare{}, quarkTotal: metadata.Total, quarkRows: *data.List}
	if metadata.Total != nil {
		total := *metadata.Total
		offset := int64(page-1) * pageSize
		if total < 0 || int64(rowCount) != min(max(total-offset, 0), pageSize) {
			return ManagedSharePage{}, errors.New("夸克分享列表条数与总数不一致，请刷新列表 [SHARE-LIST/QK]")
		}
		result.HasMore = total > offset+pageSize
	}
	seen := map[string]bool{}
	now := time.Now()
	for _, row := range *data.List {
		id := shareScalar(row.ID)
		if id == "" || id == "0" || seen[id] || len(row.Title) > 1024 {
			return ManagedSharePage{}, errors.New("夸克分享列表标识无效或重复 [SHARE-LIST/QK]")
		}
		seen[id] = true
		link := row.URL
		if link == "" {
			if key := shareScalar(row.Key); key != "" {
				link = "https://pan.quark.cn/s/" + key
			}
		}
		if link != "" {
			link, err = shareURL("quark", link)
			if err != nil {
				return ManagedSharePage{}, errors.New("夸克分享列表返回异常链接，未展示 [SHARE-LIST/QK]")
			}
		}
		name := strings.TrimSpace(row.Title)
		if name == "" {
			name = "未命名分享"
		}
		handle := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: id, Name: name, URL: link, Kind: "quark", Page: page, QuarkFilter: filter, QuarkLinkKey: shareScalar(row.Key)}
		raw, _ := json.Marshal(handle)
		sealed, e := protect(raw)
		if e != nil {
			return ManagedSharePage{}, errors.New("无法生成本机分享读取标识")
		}
		status := row.unavailableStatus(now)
		restricted, _ := row.restriction(now)
		if status == "" {
			status = "有效性待核对"
			if restricted > 0 {
				status = "部分文件受限"
			}
		}
		expiry := managedShareExpiry(row.ExpiryType, row.Expires)
		if row.ExpiryType != nil && *row.ExpiryType == 1 {
			expiry = "永久"
		}
		result.Items = append(result.Items, ManagedShare{
			ID: base64.RawURLEncoding.EncodeToString(sealed), Name: name, Status: status,
			Created: managedShareTime(row.Created), Expiry: expiry, CanRevoke: true, SelectionKey: managedShareSelectionKey(handle),
			QuarkRestricted: restricted, QuarkAccess: row.accessMode(), replacementAvailable: status == "有效性待核对",
		})
	}
	return result, nil
}

func decodeManagedShareHandle(handle string) (managedShareHandle, error) {
	if len(handle) > 16384 {
		return managedShareHandle{}, errors.New("本机分享读取标识无效")
	}
	sealed, e := base64.RawURLEncoding.DecodeString(handle)
	if e != nil {
		return managedShareHandle{}, errors.New("本机分享读取标识无效")
	}
	raw, e := unprotect(sealed)
	if e != nil {
		return managedShareHandle{}, errors.New("无法读取本机分享标识，请刷新云端列表")
	}
	var h managedShareHandle
	if json.Unmarshal(raw, &h) != nil || !shareToken.MatchString(h.ProviderID) || h.ProviderID == "0" || h.AccountID == "" || h.Scope == "" || h.Name == "" || len(h.Name) > 1024 {
		return managedShareHandle{}, errors.New("本机分享读取标识不完整")
	}
	if h.Kind == "" {
		h.Kind = "quark"
	} // 0.58/0.59 handles were Quark-only.
	if h.Kind != "quark" && h.Kind != "pan123" && h.Kind != "baidu" && h.Kind != "guangya" {
		return h, errors.New("本机分享网盘类型无效")
	}
	if h.QuarkFilter != "" && (h.Kind != "quark" || !validQuarkShareFilter(h.QuarkFilter) || h.Page < 1 || h.Page > 1000) {
		return h, errors.New("夸克分享筛选或页码无效")
	}
	if (h.Kind == "pan123" || h.Kind == "baidu") && !valid123Folder(h.ProviderID) || h.Kind != "quark" && (h.Page < 1 || h.Page > 1000) {
		return h, errors.New("原分享标识或页码无效")
	}
	if h.Kind == "guangya" && shareScalar(h.RawID) != h.ProviderID {
		return h, errors.New("光鸭原记录标识格式无效")
	}
	if h.URL != "" {
		if _, err := shareURL(h.Kind, h.URL); err != nil {
			return managedShareHandle{}, errors.New("本机分享链接记录无效")
		}
	}
	if len(h.AlternateLinks123) > maxManaged123AlternateLinks ||
		(h.Kind != "pan123" || h.URL == "") && (len(h.AlternateLinks123) != 0 || h.AlternateLinksIncomplete) {
		return managedShareHandle{}, errors.New("本机分享备用链接记录无效")
	}
	for _, alternate := range h.AlternateLinks123 {
		if _, err := shareURL("pan123", alternate); err != nil || len(alternate) > 512 {
			return managedShareHandle{}, errors.New("本机分享备用链接记录无效")
		}
	}
	return h, nil
}

type ManagedShareLinkMatches struct {
	Keys    []string `json:"keys"`
	Unknown int      `json:"unknown"`
}

// MatchManagedShareLinkKeys compares list links and verified 123 alternates, and fills in one batch of missing
// Quark links. The caller only receives selection keys and the unresolved count.
func (m *Manager) MatchManagedShareLinkKeys(ctx context.Context, accountID string, handles, keys []string) (ManagedShareLinkMatches, error) {
	return m.matchManagedShareLinkKeys(ctx, NewHTTPUploader().Client, accountID, handles, keys)
}

func (m *Manager) matchManagedShareLinkKeys(ctx context.Context, client *http.Client, accountID string, handles, keys []string) (ManagedShareLinkMatches, error) {
	if len(handles) > 500 || len(keys) < 1 || len(keys) > 20 {
		return ManagedShareLinkMatches{}, errors.New("分享链接搜索超出单次核对范围")
	}
	account, err := m.managedShareAccount(accountID)
	if err != nil {
		return ManagedShareLinkMatches{}, err
	}
	wanted := make(map[string]bool, len(keys))
	quarkLookup := false
	for _, key := range keys {
		if key == "" || len(key) > 2048 {
			return ManagedShareLinkMatches{}, errors.New("分享链接搜索格式无效")
		}
		wanted[key] = true
		if token, ok := strings.CutPrefix(key, "pan.quark.cn/s/"); account.Kind == "quark" && ok && shareToken.MatchString(token) {
			quarkLookup = true
		}
	}
	result := ManagedShareLinkMatches{Keys: []string{}}
	var missing []managedShareHandle
	missingIDs := make(map[string]bool)
	scope := downloadScope(account)
	for _, token := range handles {
		handle, err := decodeManagedShareHandle(token)
		if err != nil || handle.AccountID != account.ID || handle.Scope != scope || handle.Kind != account.Kind {
			return ManagedShareLinkMatches{}, errors.New("分享列表或账号来源已变化，请重新搜索")
		}
		if handle.URL == "" {
			result.Unknown++
			if quarkLookup && len(missing) < quarkManagedShareReadLimit && !missingIDs[handle.ProviderID] && !m.managedShareRevoking(handle) {
				missingIDs[handle.ProviderID] = true
				missing = append(missing, handle)
			}
			continue
		}
		matches := wanted[managedShareLinkKey(handle.URL)]
		for _, alternate := range handle.AlternateLinks123 {
			matches = matches || wanted[managedShareLinkKey(alternate)]
		}
		if matches {
			result.Keys = append(result.Keys, managedShareSelectionKey(handle))
		} else if handle.AlternateLinksIncomplete {
			result.Unknown++
		}
	}
	if len(missing) != 0 {
		links, err := quarkManagedShareLinks(ctx, client, account, missing)
		if err == nil {
			for _, handle := range missing {
				link, found := links[handle.ProviderID]
				if !found || m.managedShareRevoking(handle) {
					continue
				}
				result.Unknown--
				if wanted[managedShareLinkKey(link.URL)] {
					result.Keys = append(result.Keys, managedShareSelectionKey(handle))
				}
			}
		}
	}
	current, err := m.managedShareAccount(accountID)
	if err != nil || downloadScope(current) != scope {
		return ManagedShareLinkMatches{}, errors.New("账号来源已变化，请重新搜索")
	}
	return result, nil
}

func managedShareLinkKey(value string) string {
	link, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(link.Hostname()) + strings.TrimSuffix(link.EscapedPath(), "/")
}

func (m *Manager) ManagedShareLink(ctx context.Context, handle string) (ManagedShareLink, error) {
	return m.managedShareLink(ctx, handle, nil)
}

func (m *Manager) Managed123MatchedShareLink(ctx context.Context, handle string, keys []string) (ManagedShareLink, error) {
	if len(keys) == 0 || len(keys) > 20 {
		return ManagedShareLink{}, errors.New("分享链接查找条件无效")
	}
	for _, key := range keys {
		if key == "" || len(key) > 2048 {
			return ManagedShareLink{}, errors.New("分享链接查找条件无效")
		}
	}
	return m.managedShareLink(ctx, handle, keys)
}

func (m *Manager) managedShareLink(ctx context.Context, handle string, keys []string) (ManagedShareLink, error) {
	h, e := decodeManagedShareHandle(handle)
	if e != nil {
		return ManagedShareLink{}, e
	}
	a, e := m.managedShareAccount(h.AccountID)
	if e != nil {
		return ManagedShareLink{}, e
	}
	if downloadScope(a) != h.Scope {
		return ManagedShareLink{}, errors.New("账号来源已变化，请重新读取云端分享")
	}
	if a.Kind != h.Kind || len(keys) != 0 && a.Kind != "pan123" {
		return ManagedShareLink{}, errors.New("分享网盘来源不一致")
	}
	if m.managedShareRevoking(h) {
		return ManagedShareLink{}, errors.New("原分享已进入撤销流程，请检查原撤销结果；未展示链接")
	}
	var result ManagedShareLink
	switch a.Kind {
	case "pan123":
		result, e = pan123ManagedShareLink(ctx, NewHTTPUploader().Client, a, h, keys)
	case "baidu":
		result, e = baiduManagedShareLink(ctx, NewHTTPUploader().Client, a, h)
	case "guangya":
		result, e = gyManagedShareLink(ctx, NewHTTPUploader().Client, a, h)
	default:
		result, e = quarkManagedShareLink(ctx, NewHTTPUploader().Client, a, h)
	}
	if e != nil {
		return ManagedShareLink{}, e
	}
	if current, err := m.managedShareAccount(h.AccountID); err != nil || downloadScope(current) != h.Scope {
		return ManagedShareLink{}, errors.New("账号来源已变化，请重新读取云端分享")
	}
	if m.managedShareRevoking(h) {
		return ManagedShareLink{}, errors.New("原分享已进入撤销流程，请检查原撤销结果；未展示链接")
	}
	return result, nil
}

type quarkShareDetails struct {
	ID         json.RawMessage `json:"share_id"`
	URL        string          `json:"share_url"`
	Key        json.RawMessage `json:"pwd_id"`
	Password   *string         `json:"passcode"`
	Mode       *int            `json:"url_type"`
	Status     *int            `json:"status"`
	Audit      *int            `json:"audit_status"`
	Expires    int64           `json:"expired_at"`
	ExpiryType *int            `json:"expired_type"`
}

func quarkManagedShareLink(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (ManagedShareLink, error) {
	var row quarkShareDetails
	_, e := newQuarkClient(client, a).request(ctx, "POST", "/share/password", nil, map[string]string{"share_id": h.ProviderID}, &row)
	if e != nil {
		return ManagedShareLink{}, e
	}
	return verifyQuarkManagedShareLink(h, row)
}

func verifyQuarkManagedShareLink(h managedShareHandle, row quarkShareDetails) (ManagedShareLink, error) {
	if row.ID != nil && shareScalar(row.ID) != h.ProviderID {
		return ManagedShareLink{}, errors.New("原分享标识不一致，未展示链接 [SHARE-LINK/QK]")
	}
	if row.Status != nil && *row.Status == 3 || row.Audit != nil && (*row.Audit == 2 || *row.Audit == 3) {
		return ManagedShareLink{}, errors.New("原分享已失效或未通过审核，未展示链接")
	}
	if row.Expires > 0 {
		deadline := time.Unix(row.Expires, 0)
		if row.Expires >= 1000000000000 {
			deadline = time.UnixMilli(row.Expires)
		}
		if !deadline.After(time.Now()) {
			return ManagedShareLink{}, errors.New("原分享已过期，未展示链接")
		}
	}
	password := ""
	if row.Password != nil {
		password = *row.Password
	}
	if row.Mode == nil || *row.Mode != 1 && *row.Mode != 2 || *row.Mode == 1 && password != "" || *row.Mode == 2 && (len(password) != 4 || !shareCode.MatchString(password)) {
		return ManagedShareLink{}, errors.New("原分享访问设置未确认，未展示链接 [SHARE-LINK/QK/settings]")
	}
	link := row.URL
	if link == "" {
		if key := shareScalar(row.Key); key != "" {
			link = "https://pan.quark.cn/s/" + key
		}
	}
	link, e := shareURL("quark", link)
	if e != nil || h.URL != "" && h.URL != link {
		return ManagedShareLink{}, errors.New("原分享链接已变化或格式无效，请刷新云端列表")
	}
	return ManagedShareLink{Name: h.Name, URL: link, Password: password, Expiry: managedShareExpiry(row.ExpiryType, row.Expires), Message: "已读取原分享链接；没有新建或修改分享，当前有效性以网盘显示为准"}, nil
}
