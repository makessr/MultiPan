package transfer

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

func sealManagedShare(h managedShareHandle) (string, error) {
	raw, e := json.Marshal(h)
	if e != nil {
		return "", e
	}
	sealed, e := protect(raw)
	if e != nil {
		return "", errors.New("无法生成本机分享读取标识")
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

type managedShareCursor struct {
	AccountID, Scope, Next string
	Page                   int
	Paid123                bool `json:"paid123,omitempty"`
}

func (m *Manager) ManagedPan123PaidShares(ctx context.Context, accountID string) (ManagedSharePage, error) {
	a, err := m.managedShareAccount(accountID)
	if err != nil || a.Kind != "pan123" {
		return ManagedSharePage{}, errors.New("请选择有效的 123 云盘账号")
	}
	result, err := pan123ManagedSharesByType(ctx, NewHTTPUploader().Client, a, "0", 1, true)
	if err != nil {
		return ManagedSharePage{}, err
	}
	if current, err := m.managedShareAccount(accountID); err != nil || downloadScope(current) != downloadScope(a) {
		return ManagedSharePage{}, errors.New("账号来源已变化，请重新读取付费分享")
	}
	return result, nil
}

func (m *Manager) ManagedSharesNext(ctx context.Context, accountID, token string) (ManagedSharePage, error) {
	if len(token) > 16384 {
		return ManagedSharePage{}, errors.New("分页标识无效")
	}
	raw, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil {
		return ManagedSharePage{}, errors.New("分页标识无效")
	}
	raw, e = unprotect(raw)
	if e != nil {
		return ManagedSharePage{}, errors.New("分页标识失效，请刷新第一页")
	}
	var c managedShareCursor
	if json.Unmarshal(raw, &c) != nil || c.AccountID != accountID || c.Page < 2 || c.Page > 1000 || !shareToken.MatchString(c.Next) {
		return ManagedSharePage{}, errors.New("分页标识内容无效")
	}
	a, e := m.managedShareAccount(accountID)
	if e != nil {
		return ManagedSharePage{}, e
	}
	if a.Kind != "pan123" || downloadScope(a) != c.Scope {
		return ManagedSharePage{}, errors.New("分页账号来源已变化，请刷新第一页")
	}
	result, e := pan123ManagedSharesByType(ctx, NewHTTPUploader().Client, a, c.Next, c.Page, c.Paid123)
	if e != nil {
		return result, e
	}
	if current, err := m.managedShareAccount(accountID); err != nil || downloadScope(current) != c.Scope {
		return ManagedSharePage{}, errors.New("分页账号来源已变化")
	}
	return result, nil
}

type managed123Record struct {
	share123Record
	Name          string          `json:"ShareName"`
	Created       string          `json:"CreateAt"`
	Expired       json.RawMessage `json:"Expired"`
	Paid          *int            `json:"isPayShare"`
	PaidAudit     *int            `json:"auditStatus"`
	PriceFen      json.RawMessage `json:"payAmount"`
	EarningsFen   json.RawMessage `json:"amount"`
	NoLoginAmount *int64          `json:"noLoginStdAmount"`
	Traffic       *int            `json:"trafficSwitch"`
	LimitMode     *int            `json:"trafficLimitSwitch"`
	Limit         *int64          `json:"trafficLimit"`
}

func managed123Expired(value json.RawMessage) (bool, error) {
	if len(value) == 0 {
		return false, nil
	}
	switch strings.Trim(string(value), "\"") {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return false, errors.New("123原分享失效状态无法确认")
}
func managed123Link(row managed123Record) string {
	if len(row.Links.List) > 0 {
		return managed123ListedLink(row)
	}
	var raw string
	if json.Unmarshal(row.Key, &raw) == nil {
		if link, e := shareURL("pan123", raw); e == nil {
			return link
		}
	}
	return share123Link(row.share123Record)
}

func managed123ListedLink(row managed123Record) string {
	for _, raw := range row.Links.List {
		if link, err := shareURL("pan123", raw); err == nil {
			return link
		}
	}
	return ""
}

const maxManaged123AlternateLinks = 16
const maxManaged123AlternateBytes = 4096

func managed123AlternateLinks(row managed123Record, primary string) ([]string, bool) {
	seen := map[string]bool{managedShareLinkKey(primary): true}
	var alternates []string
	bytes := 0
	incomplete := false
	for _, raw := range row.Links.List {
		link, err := shareURL("pan123", raw)
		if err != nil {
			incomplete = true
			continue
		}
		key := managedShareLinkKey(link)
		if seen[key] {
			continue
		}
		if len(link) > 512 || bytes+len(link) > maxManaged123AlternateBytes || len(alternates) == maxManaged123AlternateLinks {
			incomplete = true
			continue
		}
		seen[key] = true
		bytes += len(link)
		alternates = append(alternates, link)
	}
	return alternates, incomplete
}

const pan123PaidShareList = "/restful/goapi/v1/share/content/payment/list"

func pan123ManagedRecords(ctx context.Context, client *http.Client, a Account, cursor string) ([]managed123Record, string, error) {
	return pan123ManagedRecordsByType(ctx, client, a, cursor, false)
}

func pan123ManagedRecordsByType(ctx context.Context, client *http.Client, a Account, cursor string, paid bool) ([]managed123Record, string, error) {
	var data struct {
		Rows *[]managed123Record `json:"InfoList"`
		Next json.RawMessage     `json:"Next"`
	}
	endpoint := "/share/list"
	if paid {
		endpoint = pan123PaidShareList
	}
	c := pan123Client{client, a}
	e := c.request(ctx, "GET", endpoint, url.Values{"driveId": {"0"}, "limit": {"50"}, "next": {cursor}, "orderBy": {"share_id"}, "orderDirection": {"desc"}, "SearchData": {""}, "event": {"shareListFile"}}, nil, &data)
	if e != nil {
		return nil, "", e
	}
	if data.Rows == nil || len(*data.Rows) > 50 || len(data.Next) == 0 || string(data.Next) == "null" {
		return nil, "", errors.New("123分享列表或分页字段不完整 [SHARE-LIST/123]")
	}
	next := ""
	if string(data.Next) != "-1" && string(data.Next) != `"-1"` {
		next = shareScalar(data.Next)
		if next == "" {
			return nil, "", errors.New("123分页标识无效")
		}
	}
	if next == "0" {
		next = ""
	}
	if next != "" && (next == cursor || len(*data.Rows) == 0) {
		return nil, "", errors.New("123分页未前进，请刷新第一页")
	}
	seen := map[string]bool{}
	for _, row := range *data.Rows {
		id := shareScalar(row.ID)
		if id == "" || id == "0" || seen[id] || len(row.Name) > 1024 || len(row.Expiration) > 128 {
			return nil, "", errors.New("123分享标识重复或格式无效")
		}
		if paid && row.Paid != nil && *row.Paid != 1 {
			return nil, "", errors.New("123 付费分享列表出现类型不一致的记录")
		}
		seen[id] = true
	}
	return *data.Rows, next, nil
}
func managed123Name(row managed123Record) string {
	if strings.TrimSpace(row.Name) == "" {
		return "未命名分享"
	}
	return row.Name
}
func managed123Deadline(value string) (time.Time, error) {
	deadline, e := time.Parse(time.RFC3339, value)
	if e != nil {
		deadline, e = time.ParseInLocation("2006-01-02 15:04:05", value, time.FixedZone("CST", 8*3600))
	}
	return deadline, e
}

func pan123ManagedShares(ctx context.Context, client *http.Client, a Account, cursor string, page int) (ManagedSharePage, error) {
	return pan123ManagedSharesByType(ctx, client, a, cursor, page, false)
}

func pan123ManagedSharesByType(ctx context.Context, client *http.Client, a Account, cursor string, page int, paid bool) (ManagedSharePage, error) {
	rows, next, e := pan123ManagedRecordsByType(ctx, client, a, cursor, paid)
	if e != nil {
		return ManagedSharePage{}, e
	}
	result := ManagedSharePage{Items: []ManagedShare{}, Page: page, HasMore: next != "" && page < 1000, pan123Next: next}
	if result.HasMore {
		raw, _ := json.Marshal(managedShareCursor{AccountID: a.ID, Scope: downloadScope(a), Next: next, Page: page + 1, Paid123: paid})
		sealed, e := protect(raw)
		if e != nil {
			return result, e
		}
		result.Next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	for _, row := range rows {
		link := managed123Link(row)
		if paid {
			link = managed123ListedLink(row)
		}
		if len(row.Links.List) > 0 && link == "" {
			return result, errors.New("123分享列表返回异常链接，未展示")
		}
		h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: shareScalar(row.ID), Name: managed123Name(row), URL: link, Kind: "pan123", Page: page, Cursor: cursor, Key123: shareScalar(row.Key), Paid123: paid}
		h.AlternateLinks123, h.AlternateLinksIncomplete = managed123AlternateLinks(row, link)
		if link != "" && pan123EditableShare(row, time.Now(), paid) {
			h.Settings123 = pan123ShareSettingsDigest(row)
			h.Deadline123 = row.Expiration
			h.TrafficMode123 = row.Traffic
			if *row.Traffic != 1 {
				h.TrafficCap123 = row.Limit
			}
		}
		token, e := sealManagedShare(h)
		if e != nil {
			return result, e
		}
		expiry := "有效期以网盘显示为准"
		status := "有效性待核对"
		deadline, deadlineErr := managed123Deadline(row.Expiration)
		if d, e := deadline, deadlineErr; e == nil {
			expiry = "到期：" + d.Format(time.RFC3339)
			if !d.After(time.Now()) {
				status = "已到期"
			}
		}
		if len(row.Violations) > 0 {
			status = "分享内容受限"
		}
		expired, expiredErr := managed123Expired(row.Expired)
		if expired || expiredErr != nil {
			status = "已失效或状态未确认"
		}
		item := ManagedShare{ID: token, Name: h.Name, Status: status, Expiry: expiry, CanRevoke: !paid && (row.Paid == nil || *row.Paid == 0), CanEdit: h.Settings123 != "", CanLimitTraffic123: h.TrafficCap123 != nil,
			replacementAvailable: deadlineErr == nil && deadline.After(time.Now()) && expiredErr == nil && !expired && len(row.Violations) == 0 && (!paid || row.PaidAudit != nil && *row.PaidAudit == 1), SelectionKey: managedShareSelectionKey(h)}
		if paid {
			item.PriceFen, item.EarningsFen = managed123Amount(row.PriceFen), managed123Amount(row.EarningsFen)
			item.CanRevoke = revocationAccountMatches(a, h) && page <= 10
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func managed123Amount(raw json.RawMessage) *int64 {
	amount, err := strconv.ParseInt(shareScalar(raw), 10, 64)
	if err != nil || amount < 0 || amount > 1<<50 {
		return nil
	}
	return &amount
}

func pan123ManagedShareLink(ctx context.Context, client *http.Client, a Account, h managedShareHandle, keys []string) (ManagedShareLink, error) {
	if h.Page < 1 || h.Page > 1000 || !shareToken.MatchString(h.Cursor) {
		return ManagedShareLink{}, errors.New("123原分页信息无效，请刷新列表")
	}
	rows, _, e := pan123ManagedRecordsByType(ctx, client, a, h.Cursor, h.Paid123)
	if e != nil {
		return ManagedShareLink{}, e
	}
	for _, row := range rows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		link := managed123Link(row)
		if h.Paid123 {
			link = managed123ListedLink(row)
			if h.URL == "" || h.Key123 != shareScalar(row.Key) {
				return ManagedShareLink{}, errors.New("123 原付费分享链接或标识无法确认")
			}
		}
		expired, stateErr := managed123Expired(row.Expired)
		deadline, e := managed123Deadline(row.Expiration)
		if managed123Name(row) != h.Name || link == "" || h.URL != "" && link != h.URL || e != nil || !deadline.After(time.Now()) || expired || stateErr != nil || len(row.Violations) > 0 || row.Password == nil {
			return ManagedShareLink{}, errors.New("123原分享已变化、过期或访问设置未确认，未展示链接")
		}
		if *row.Password != "" && (len(*row.Password) != 4 || !shareCode.MatchString(*row.Password)) {
			return ManagedShareLink{}, errors.New("123原分享提取码格式未确认")
		}
		if len(keys) != 0 {
			previous := append([]string{h.URL}, h.AlternateLinks123...)
			alternates, _ := managed123AlternateLinks(row, link)
			current := append([]string{link}, alternates...)
			matched := ""
			for _, key := range keys {
				if i := slices.IndexFunc(current, func(value string) bool { return managedShareLinkKey(value) == key }); i >= 0 &&
					slices.ContainsFunc(previous, func(value string) bool { return managedShareLinkKey(value) == key }) {
					matched = current[i]
					break
				}
			}
			if matched == "" {
				return ManagedShareLink{}, errors.New("查找命中的链接已变化，请重新搜索")
			}
			link = matched
		}
		result := ManagedShareLink{Name: h.Name, URL: link, Password: *row.Password, Expiry: "到期：" + deadline.Format(time.RFC3339), Message: "已读取同一原分享，没有新建、修改或延长期限"}
		if pan123EditableShare(row, time.Now(), h.Paid123) && pan123ShareSettingsDigest(row) == h.Settings123 {
			result.TrafficCap123 = h.TrafficCap123
			result.TrafficMode123 = h.TrafficMode123
		}
		return result, nil
	}
	return ManagedShareLink{}, errors.New("原分享已不在原页，请刷新云端列表后重选")
}

type managedBaiduRecord struct {
	ID        json.RawMessage   `json:"shareId"`
	Sources   []json.RawMessage `json:"fsIds"`
	Path      string            `json:"typicalPath"`
	Link      string            `json:"shortlink"`
	Expiry    *int              `json:"expiredType"`
	Remaining *int64            `json:"expiredTime"`
	ExpiresAt *int64            `json:"lastExpireTime"`
	Public    *int              `json:"public"`
	Channel   *int              `json:"channel"`
	Status    *int              `json:"status"`
	Tag       *int              `json:"tag"`
}

const baiduSharePageSize = 100

func baiduManagedRecords(ctx context.Context, c *baiduClient, page int) ([]managedBaiduRecord, error) {
	var response struct {
		List json.RawMessage `json:"list"`
	}
	if e := c.shareAPI(ctx, "GET", "record", url.Values{"page": {strconv.Itoa(page)}, "num": {strconv.Itoa(baiduSharePageSize)}, "order": {"time"}, "desc": {"1"}}, nil, &response); e != nil {
		return nil, e
	}
	if strings.TrimSpace(string(response.List)) == "{}" {
		return []managedBaiduRecord{}, nil
	}
	var rows []managedBaiduRecord
	if json.Unmarshal(response.List, &rows) != nil || rows == nil || len(rows) > baiduSharePageSize {
		shape := "other"
		raw := strings.TrimSpace(string(response.List))
		switch {
		case raw == "":
			shape = "missing"
		case raw == "null":
			shape = "null"
		case strings.HasPrefix(raw, "["):
			shape = "array"
		case strings.HasPrefix(raw, "{"):
			shape = "object"
		case strings.HasPrefix(raw, `"`):
			shape = "string"
		}
		return nil, fmt.Errorf("百度分享列表格式不完整 [SHARE-LIST/BD] shape=%s count=%d", shape, len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		id := shareScalar(r.ID)
		if id == "" || id == "0" || !valid123Folder(id) || seen[id] || len(r.Path) > 4096 {
			return nil, errors.New("百度分享标识重复或格式无效")
		}
		seen[id] = true
		if r.Link != "" {
			if _, e := resolveBaiduShareURL(r.Link); e != nil {
				return nil, errors.New("百度分享列表链接格式无效")
			}
		}
	}
	return rows, nil
}
func managedBaiduName(row managedBaiduRecord) string {
	if row.Path == "" || row.Path == "/" {
		return "未命名分享"
	}
	return path.Base(row.Path)
}

func managedBaiduExpiry(row managedBaiduRecord) string {
	switch {
	case row.Expiry != nil && *row.Expiry == -1:
		return "已过期"
	case row.Expiry != nil && *row.Expiry == 1 && row.Remaining != nil && *row.Remaining == 0:
		return "永久"
	case row.Expiry != nil && *row.Expiry == 0 && row.ExpiresAt != nil && *row.ExpiresAt > 0 && *row.ExpiresAt <= 253402300799:
		return "到期：" + time.Unix(*row.ExpiresAt, 0).Format(time.RFC3339)
	default:
		return "有效期以网盘显示为准"
	}
}

func baiduManagedShares(ctx context.Context, client *http.Client, a Account, page int) (ManagedSharePage, error) {
	c := newBaiduClient(client, a)
	if e := c.session(ctx); e != nil {
		return ManagedSharePage{}, e
	}
	rows, e := baiduManagedRecords(ctx, c, page)
	if e != nil {
		return ManagedSharePage{}, e
	}
	result := ManagedSharePage{Items: []ManagedShare{}, Page: page, HasMore: len(rows) == baiduSharePageSize && page < 1000}
	for _, row := range rows {
		link := ""
		if row.Link != "" {
			link, _ = resolveBaiduShareURL(row.Link)
		}
		h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: shareScalar(row.ID), Name: managedBaiduName(row), URL: link, Kind: "baidu", Page: page}
		frozen := row.Status != nil && *row.Status == baiduShareFrozen
		if frozen && link != "" && validBaiduPath(row.Path) && row.Path != "/" {
			h.ActivationBaidu = baiduActivationDeadline(row, time.Now())
			h.PathBaidu = row.Path
		}
		token, e := sealManagedShare(h)
		if e != nil {
			return result, e
		}
		status := "有效性待核对"
		if row.Expiry != nil && *row.Expiry < 0 || row.Remaining != nil && *row.Remaining < 0 {
			status = "已失效或过期"
		}
		if frozen {
			status = "已被冻结"
		}
		result.Items = append(result.Items, ManagedShare{ID: token, Name: h.Name, Status: status, Expiry: managedBaiduExpiry(row), CanEdit: !frozen && h.URL != "" && row.Expiry != nil && row.Remaining != nil && *row.Expiry >= 0 && *row.Remaining >= 0, CanRevoke: true, CanActivate: h.ActivationBaidu != "", replacementAvailable: !frozen && row.Expiry != nil && row.Remaining != nil && *row.Expiry >= 0 && *row.Remaining >= 0, SelectionKey: managedShareSelectionKey(h)})
	}
	return result, nil
}
func baiduManagedShareLink(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (ManagedShareLink, error) {
	link, _, err := readBaiduManagedShare(ctx, client, a, h)
	return link, err
}

func readBaiduManagedShare(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (ManagedShareLink, managedBaiduRecord, error) {
	if h.Page < 1 || h.Page > 1000 {
		return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原页码无效，请刷新列表")
	}
	c := newBaiduClient(client, a)
	if e := c.session(ctx); e != nil {
		return ManagedShareLink{}, managedBaiduRecord{}, e
	}
	rows, e := baiduManagedRecords(ctx, c, h.Page)
	if e != nil {
		return ManagedShareLink{}, managedBaiduRecord{}, e
	}
	return readBaiduManagedShareFromPage(ctx, c, h, rows)
}

func readBaiduManagedShareFromPage(ctx context.Context, c *baiduClient, h managedShareHandle, rows []managedBaiduRecord) (ManagedShareLink, managedBaiduRecord, error) {
	for _, row := range rows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		if managedBaiduName(row) != h.Name || row.Expiry == nil || row.Remaining == nil || *row.Expiry < 0 || *row.Remaining < 0 {
			return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原分享已变化或期限无法确认")
		}
		var detail struct {
			Password *string `json:"pwd"`
			URL      string  `json:"shorturl"`
		}
		sum := md5.Sum([]byte(h.ProviderID + "_sharesurlinfo!@#"))
		if e := c.shareAPI(ctx, "GET", "surlinfoinrecord", url.Values{"shareid": {h.ProviderID}, "sign": {hex.EncodeToString(sum[:])}}, nil, &detail); e != nil {
			return ManagedShareLink{}, managedBaiduRecord{}, e
		}
		if detail.Password == nil {
			return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原分享提取码未返回，未展示链接")
		}
		password := *detail.Password
		if password == "0" {
			password = ""
		}
		if password != "" && (len(password) != 4 || !shareCode.MatchString(password)) {
			return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原提取码格式未确认")
		}
		if row.Public == nil && row.Channel == nil || row.Public != nil && *row.Public != 0 && *row.Public != 1 || row.Channel != nil && *row.Channel != 4 || password == "" && (row.Public == nil || *row.Public != 1) || password != "" && row.Public != nil && *row.Public != 0 {
			return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原分享访问模式未确认，未展示链接")
		}
		link, e := resolveBaiduShareURL(detail.URL, row.Link, h.URL)
		if e != nil || h.URL != "" && link != h.URL {
			return ManagedShareLink{}, managedBaiduRecord{}, errors.New("百度原链接已变化或格式无效，请刷新列表")
		}
		return ManagedShareLink{Name: h.Name, URL: link, Password: password, Expiry: managedBaiduExpiry(row), Message: "已按同一原分享读取链接和提取码，没有新建或延长期限"}, row, nil
	}
	return ManagedShareLink{}, managedBaiduRecord{}, errors.New("原分享已不在原页，请刷新云端列表后重选")
}
