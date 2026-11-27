package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ShareInput struct {
	recreateReceiptID      string
	recreateAttemptID      string
	expectedBaiduSources   string
	previousShareURL       string
	expectedLanzouURL      string        // initial checked directory edit only
	expectedLanzouPassword string        // preflight value for the original link, including an empty code
	lanzouReceiptID        string        // resolve the original item while holding the share lock
	SettingsIntent         string        `json:"settingsIntent,omitempty"`
	Handle                 string        `json:"handle,omitempty"`
	AccountID              string        `json:"accountId"`
	ParentID               string        `json:"parentId"`
	EntryID                string        `json:"entryId"`
	Name                   string        `json:"name"`
	Folder                 bool          `json:"folder"`
	Action                 string        `json:"action"` // get, password (Lanzou); create (Quark)
	Password               string        `json:"password"`
	ClearPassword          bool          `json:"clearPassword,omitempty"`
	ActivateBaidu          bool          `json:"activateBaidu,omitempty"`
	NewTitle123            string        `json:"newTitle123,omitempty"`
	TrafficLimit123        *int64        `json:"trafficLimit123,omitempty"`
	TrafficMode123         *int          `json:"trafficMode123,omitempty"`
	Days                   int           `json:"days"`
	PaidPriceFen123        int           `json:"paidPriceFen123,omitempty"`
	PaidDescription123     string        `json:"paidDescription123,omitempty"`
	Targets                []ShareTarget `json:"targets,omitempty"`
}
type ShareTarget struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
}

func shareTargets(in ShareInput) []ShareTarget {
	if len(in.Targets) > 0 {
		return in.Targets
	}
	return []ShareTarget{{ID: in.EntryID, Name: in.Name, Folder: in.Folder}}
}
func shareOverlap(a, b ShareInput) bool {
	for _, x := range shareTargets(a) {
		for _, y := range shareTargets(b) {
			if x.ID == y.ID && x.Folder == y.Folder {
				return true
			}
		}
	}
	return false
}

type ShareResult struct {
	ID       string `json:"id"`
	State    string `json:"state"` // working, ready, unknown, failed
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Password string `json:"password"`
	Days     int    `json:"days"`
	Expiry   string `json:"expiry,omitempty"`
	Message  string `json:"message"`
	CanCheck bool   `json:"canCheck"`
}
type shareOperation struct {
	recreatedFrom       string
	superseded          bool
	settingsCheckpoint  func(managedShareHandle) error
	in                  ShareInput
	scope, key          string
	result              ShareResult
	running             bool
	cancel              context.CancelFunc
	task, share         string // encrypted local state only; never in reports/errors
	key123              string
	quarkGroupVerified  bool
	baiduSource         string
	baiduURL            string
	baiduExpiryPassword *string // nil means the original access settings were never captured
	lanzouURL           string
	created             time.Time
	saved               diskShare // accessed only under Manager.mu; workers never serialize live fields
	checkpoint          func() error
	revoked             bool // terminal local receipt; cannot regain a link after revoke history cleanup
}

// ShareRecord is a local receipt, not a listing of all shares on a provider.
// Provider operation IDs, source hashes and authentication never leave Manager.
type ShareRecord struct {
	AccountID          string                 `json:"accountId"`
	Created            time.Time              `json:"created"`
	Action             string                 `json:"action"`
	PaidPriceFen123    int                    `json:"paidPriceFen123,omitempty"`
	Result             ShareResult            `json:"result"`
	CanLocate          bool                   `json:"canLocate,omitempty"`
	CanOpenSource      bool                   `json:"canOpenSource,omitempty"`
	CanRecreate        bool                   `json:"canRecreate,omitempty"`
	PreviousShareURL   string                 `json:"previousShareUrl,omitempty"`
	PreviousRevocation *ShareRevocationResult `json:"previousRevocation,omitempty"`
	KeepForReplacement bool                   `json:"keepForReplacement,omitempty"`
}

type ShareSourceLocation struct {
	AccountID  string           `json:"accountId"`
	ParentID   string           `json:"parentId"`
	Targets    []ShareTarget    `json:"targets,omitempty"`
	BaiduIDs   []string         `json:"baiduIds,omitempty"`
	Recreation *ShareRecreation `json:"recreation,omitempty"`
}

func canLocateCreatedShare(a Account, op *shareOperation) bool {
	return op != nil && !op.running && (op.result.State == "ready" || op.result.State == "failed") &&
		op.in.Action == "create" && op.in.AccountID == a.ID && a.ID != "" && op.result.Kind == a.Kind &&
		downloadScope(a) == op.scope && validateShare(a, op.in) == nil
}

// CreatedShareSourceLocation reads the directory saved with a local creation receipt.
func (m *Manager) CreatedShareSourceLocation(accountID, receiptID string) (ShareSourceLocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.shares[receiptID]
	if m.closing || !canLocateCreatedShare(m.downloadAccountLocked(accountID), op) {
		return ShareSourceLocation{}, errors.New("分享回执或账号来源已变化，无法打开原目录")
	}
	if result, overlaid := m.shareRevocationOverlayLocked(op); overlaid && result.State == "unknown" {
		return ShareSourceLocation{}, errors.New("原分享结果待核对，暂不能定位")
	}
	location := ShareSourceLocation{AccountID: accountID, ParentID: op.in.ParentID}
	if op.result.Kind == "baidu" {
		location.BaiduIDs = baiduReceiptSourceIDs(op)
		if len(location.BaiduIDs) == 0 {
			return location, nil
		}
	}
	location.Targets = append([]ShareTarget(nil), shareTargets(op.in)...)
	location.Recreation = &ShareRecreation{
		ReceiptID: receiptID, AttemptID: newID(), Protected: op.in.Password != "", Days: op.in.Days,
		PaidPriceFen123: op.in.PaidPriceFen123,
	}
	return location, nil
}

type LanzouShareLocation struct {
	AccountID string     `json:"accountId"`
	ParentID  string     `json:"parentId"`
	Entry     CloudEntry `json:"entry"`
}

func canLocateLanzouShare(a Account, op *shareOperation) bool {
	return op != nil && !op.running && op.result.State == "ready" && op.result.Kind == "lanzou" &&
		a.ID != "" && a.Kind == "lanzou" && op.in.AccountID == a.ID && downloadScope(a) == op.scope &&
		validateShare(a, op.in) == nil
}

func (m *Manager) LanzouShareLocation(accountID, receiptID string) (LanzouShareLocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.shares[receiptID]
	if m.closing || !canLocateLanzouShare(m.downloadAccountLocked(accountID), op) {
		return LanzouShareLocation{}, errors.New("蓝奏分享回执或账号来源已变化")
	}
	if _, blocked := m.shareRevocationOverlayLocked(op); blocked {
		return LanzouShareLocation{}, errors.New("原分享结果待核对，暂不能定位")
	}
	return LanzouShareLocation{AccountID: accountID, ParentID: op.in.ParentID,
		Entry: CloudEntry{ID: op.in.EntryID, Name: op.in.Name, Folder: op.in.Folder}}, nil
}

func (m *Manager) ShareHistory() []ShareRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ShareRecord, 0, len(m.shares))
	retained := m.shareSourcesInUseLocked()
	for _, op := range m.shares {
		result := op.result
		if overlaid, blocked := m.shareRevocationOverlayLocked(op); blocked {
			result = overlaid
		}
		a := m.downloadAccountLocked(op.in.AccountID)
		if a.ID == "" || downloadScope(a) != op.scope {
			result.State = "unknown"
			result.CanCheck = false
			result.Message = "账号已移除或登录来源已变化，请到原账号官网核对；没有重新创建分享"
		}
		if result.State != "ready" {
			result.URL = ""
			result.Password = ""
		}
		action := ""
		switch op.in.Action {
		case "create", "get", "password", "settings":
			action = op.in.Action
		}
		row := ShareRecord{AccountID: op.in.AccountID, Created: op.created, Action: action, Result: result,
			CanLocate:          result.State == "ready" && canLocateLanzouShare(a, op),
			CanOpenSource:      result.State != "unknown" && canLocateCreatedShare(a, op),
			KeepForReplacement: retained[op.result.ID]}
		row.PreviousRevocation = m.previousShareRevocationLocked(a, op)
		if previous, _, ok := m.previousShareLocked(a, op); ok && row.PreviousRevocation == nil {
			row.PreviousShareURL = previous.url
		}
		row.CanRecreate = row.CanOpenSource && (a.Kind != "baidu" || len(baiduReceiptSourceIDs(op)) > 0)
		if action == "create" && result.Kind == "pan123" {
			row.PaidPriceFen123 = op.in.PaidPriceFen123
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Created.Equal(rows[j].Created) {
			return rows[i].Result.ID < rows[j].Result.ID
		}
		return rows[i].Created.After(rows[j].Created)
	})
	return rows
}

var shareCode = regexp.MustCompile(`^[A-Za-z0-9]{1,6}$`)
var shareToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var lanzouShareHost = regexp.MustCompile(`(^|\.)lanzou[a-z]?\.com$`)

func validateShare(a Account, in ShareInput) error {
	if in.PaidPriceFen123 != 0 || in.PaidDescription123 != "" {
		if a.Kind != "pan123" || in.Action != "create" || in.Days != 0 || in.Password != "" ||
			in.PaidPriceFen123 < 100 || in.PaidPriceFen123 > 999900 ||
			len([]rune(in.PaidDescription123)) > 200 || strings.ContainsRune(in.PaidDescription123, 0) ||
			!validPan123ShareTitle(in.Name) || len(in.Targets) != 0 {
			return errors.New("123 付费分享需要单个文件或文件夹、永久有效和 1 至 9999 元的价格")
		}
	}
	if in.TrafficMode123 != nil && (a.Kind != "pan123" || in.Action != "settings" || in.Days != 0 ||
		in.NewTitle123 != "" || *in.TrafficMode123 < 1 || *in.TrafficMode123 > 4 ||
		in.TrafficLimit123 != nil && *in.TrafficMode123 == 1) {
		return errors.New("123 分享下载权限或操作无效")
	}
	if in.TrafficLimit123 != nil && (a.Kind != "pan123" || in.Action != "settings" || in.Days != 0 ||
		in.NewTitle123 != "" || *in.TrafficLimit123 < 0 || *in.TrafficLimit123 > maxPan123ShareTraffic) {
		return errors.New("123 下载流量上限或操作无效")
	}
	if in.NewTitle123 != "" && (a.Kind != "pan123" || in.Action != "settings" || in.Days != 0 || !validPan123ShareTitle(in.NewTitle123)) {
		return errors.New("123 分享主题或操作无效")
	}
	if in.ActivateBaidu && (a.Kind != "baidu" || in.Action != "settings" || in.Password != "" || in.Days != 0) {
		return errors.New("百度激活操作或参数无效")
	}
	if in.ClearPassword && (a.Kind != "lanzou" || in.Action != "password" || in.Password != "") {
		return errors.New("取消提取码操作或参数无效")
	}
	if in.Action == "settings" {
		h, e := decodeManagedShareHandle(in.Handle)
		if e != nil || !(a.Kind == "guangya" && a.Endpoint == GuangyaAPI || a.Kind == "baidu" && a.Endpoint == BaiduAPI || a.Kind == "pan123" && a.Endpoint == Pan123API) || h.Kind != a.Kind || h.AccountID != a.ID || h.Scope != downloadScope(a) || in.AccountID != a.ID || in.ParentID != "managed-share" || in.EntryID != h.ProviderID || in.Name != h.Name || len(in.Targets) > 0 || h.URL == "" {
			return errors.New("原分享设置标识或来源无效，请刷新列表")
		}
		if a.Kind == "baidu" {
			if in.ActivateBaidu && (h.ActivationBaidu == "" || !validBaiduPath(h.PathBaidu) || h.PathBaidu == "/") {
				return errors.New("百度原分享未提供可核对的激活信息，请刷新列表")
			}
			if in.Password != "" || in.Days != 0 && in.Days != 1 && in.Days != 3 && in.Days != 7 && in.Days != 30 {
				return errors.New("百度仅支持修改期限，不修改提取码")
			}
			return nil
		}
		if a.Kind == "pan123" {
			if h.Paid123 && (in.Days != 0 || in.NewTitle123 == "" && in.TrafficMode123 == nil && in.TrafficLimit123 == nil) {
				return errors.New("123 付费分享只支持修改主题和下载设置")
			}
			if in.TrafficMode123 != nil && h.TrafficMode123 == nil {
				return errors.New("原分享未提供可编辑的下载权限，请刷新列表")
			}
			if in.TrafficLimit123 != nil && h.TrafficCap123 == nil &&
				(in.TrafficMode123 == nil || h.TrafficMode123 == nil || *h.TrafficMode123 != 1) {
				return errors.New("原分享未提供可编辑的下载流量设置，请刷新列表")
			}
			if in.Password != "" || !validPan123ExpiryHandle(h) || in.Days != 0 && in.Days != 1 && in.Days != 7 && in.Days != 30 {
				return errors.New("123 仅支持更改可核对的原分享有效期，请刷新列表")
			}
			return nil
		}
		if in.Days != 0 && in.Days != 1 && in.Days != 7 && in.Days != 30 || in.Password != "" && (len(in.Password) != 4 || !shareCode.MatchString(in.Password)) {
			return errors.New("有效期或提取码无效")
		}
		return nil
	}
	if len(in.Targets) > 0 {
		if (a.Kind != "baidu" && a.Kind != "pan123" && a.Kind != "guangya" && a.Kind != "quark") ||
			in.Action != "create" || len(in.Targets) < 2 || len(in.Targets) > 50 {
			return errors.New("此网盘合并分享范围无效（2–50项）")
		}
		seen := map[string]bool{}
		primary := false
		for _, target := range in.Targets {
			if seen[target.ID] {
				return errors.New("合并分享包含重复项目")
			}
			seen[target.ID] = true
			single := in
			single.Targets = nil
			single.EntryID = target.ID
			single.Name = target.Name
			single.Folder = target.Folder
			if err := validateShare(a, single); err != nil {
				return err
			}
			if target.ID == in.EntryID && target.Name == in.Name && target.Folder == in.Folder {
				primary = true
			}
		}
		if !primary {
			return errors.New("合并分享首项目不一致")
		}
	}
	if in.ParentID == "" || in.EntryID == "" || in.Name == "" || len(in.Name) > 1024 {
		return errors.New("分享目标不完整，请刷新文件列表")
	}
	if a.Kind == "lanzou" {
		if a.Endpoint != "https://up.woozooo.com" || !validFolderID(in.ParentID) || !validFolderID(in.EntryID) || in.EntryID == "-1" {
			return errors.New("蓝奏分享目标无效")
		}
		if in.Action != "get" && in.Action != "password" {
			return errors.New("蓝奏当前支持读取现有分享和设置提取码")
		}
		if in.Days != 0 {
			return errors.New("蓝奏分享沿用原有有效期")
		}
		if in.Action == "password" && !in.ClearPassword && !shareCode.MatchString(in.Password) {
			return errors.New("蓝奏提取码请使用1–6位字母或数字")
		}
		if in.Action == "get" && in.Password != "" {
			return errors.New("读取现有链接不会修改提取码")
		}
		return nil
	}
	if a.Kind == "quark" {
		if a.Endpoint != QuarkAPI || !validQuarkFolder(in.ParentID) || !validQuarkFolder(in.EntryID) || in.EntryID == "0" || in.Action != "create" {
			return errors.New("夸克分享目标或操作无效")
		}
		if in.Days != 0 && in.Days != 1 && in.Days != 7 && in.Days != 30 {
			return errors.New("夸克有效期支持1、7、30天或永久")
		}
		if in.Password != "" && (!shareCode.MatchString(in.Password) || len(in.Password) != 4) {
			return errors.New("夸克提取码须为4位字母或数字")
		}
		return nil
	}
	if a.Kind == "pan123" {
		if a.Endpoint != Pan123API || !valid123Folder(in.ParentID) || !valid123Folder(in.EntryID) || in.EntryID == "0" || in.Action != "create" {
			return errors.New("123 分享目标或操作无效")
		}
		if in.Days != 0 && in.Days != 1 && in.Days != 7 && in.Days != 30 {
			return errors.New("123 有效期支持1、7、30天或永久")
		}
		if in.Password != "" && (!shareCode.MatchString(in.Password) || len(in.Password) != 4) {
			return errors.New("123 提取码须为4位字母或数字")
		}
		return nil
	}
	if a.Kind == "baidu" {
		if a.Endpoint != BaiduAPI || !validBaiduPath(in.ParentID) || !validBaiduPath(in.EntryID) || in.EntryID == "/" || path.Dir(in.EntryID) != in.ParentID || path.Base(in.EntryID) != in.Name || in.Action != "create" {
			return errors.New("百度分享目标或操作无效")
		}
		if in.Days != 0 && in.Days != 1 && in.Days != 7 {
			return errors.New("百度分享有效期支持1天、7天或永久")
		}
		if len(in.Password) != 4 || !shareCode.MatchString(in.Password) {
			return errors.New("百度分享请设置4位字母或数字提取码")
		}
		return nil
	}
	if a.Kind == "guangya" {
		if a.Endpoint != GuangyaAPI || !validQuarkFolder(in.ParentID) || !validQuarkFolder(in.EntryID) || in.EntryID == guangyaRoot || in.Action != "create" {
			return errors.New("光鸭分享目标或操作无效")
		}
		if in.Days != 0 && in.Days != 1 && in.Days != 7 && in.Days != 30 {
			return errors.New("光鸭有效期支持1、7、30天或永久")
		}
		if in.Password != "" && (len(in.Password) != 4 || !shareCode.MatchString(in.Password)) {
			return errors.New("光鸭提取码须为4位字母或数字")
		}
		return nil
	}
	return errors.New("此网盘尚未接入分享；WebDAV地址不等于公开分享链接")
}

func shareURL(kind, raw string) (string, error) {
	u, err := safePublicDownloadURL(raw, nil)
	if err != nil || u.RawQuery != "" || u.Port() != "" {
		return "", errors.New("网盘返回的分享链接格式无效")
	}
	host := strings.ToLower(u.Hostname())
	if kind == "quark" {
		if host != "pan.quark.cn" || !strings.HasPrefix(u.Path, "/s/") || !shareToken.MatchString(strings.TrimPrefix(u.Path, "/s/")) {
			return "", errors.New("夸克分享链接格式无效")
		}
	} else if kind == "guangya" {
		if host != "www.guangyapan.com" || !strings.HasPrefix(u.Path, "/s/") || !shareToken.MatchString(strings.TrimPrefix(u.Path, "/s/")) {
			return "", errors.New("光鸭分享链接格式无效")
		}
	} else if kind == "baidu" {
		if host != "pan.baidu.com" || !strings.HasPrefix(u.Path, "/s/") || !shareToken.MatchString(strings.TrimPrefix(u.Path, "/s/")) {
			return "", errors.New("百度分享链接格式无效")
		}
	} else if kind == "pan123" {
		legacy := (host == "www.123pan.com" || host == "www.123pan.cn" || host == "www.123865.com" || host == "www.123684.com") && strings.HasPrefix(u.Path, "/s/") && shareToken.MatchString(strings.TrimPrefix(u.Path, "/s/"))
		modernHost := strings.HasSuffix(host, ".share.123pan.cn") || strings.HasSuffix(host, ".mshare.123pan.cn")
		modern := modernHost && strings.HasPrefix(u.Path, "/123pan/") && shareToken.MatchString(strings.TrimPrefix(u.Path, "/123pan/"))
		if !legacy && !modern {
			return "", errors.New("123 分享链接格式无效，请到官网核对")
		}
	} else if kind == "lanzou" {
		if !lanzouShareHost.MatchString(host) || !shareToken.MatchString(strings.Trim(u.Path, "/")) || strings.Count(strings.Trim(u.Path, "/"), "/") != 0 {
			return "", errors.New("蓝奏分享链接格式无效，请到官网核对")
		}
	} else {
		return "", errors.New("分享网盘类型无效")
	}
	u.RawPath = ""
	return u.String(), nil
}

func shareSource(ctx context.Context, client *http.Client, a Account, in ShareInput) error {
	if in.expectedLanzouURL != "" || in.ClearPassword {
		return checkLanzouDirectoryEntry(ctx, client, a, in)
	}
	page, err := ListAccountFiles(ctx, client, a, in.ParentID)
	if err != nil {
		return err
	}
	for _, target := range shareTargets(in) {
		matches := 0
		for _, e := range page.Entries {
			if e.ID == target.ID && e.Name == target.Name && e.Folder == target.Folder {
				matches++
			}
		}
		if matches != 1 {
			return errors.New("分享目标已变化或不在当前目录，请刷新后重选")
		}
	}
	return nil
}

func (m *Manager) ShareEntry(ctx context.Context, in ShareInput) (ShareResult, error) {
	return m.shareEntry(ctx, in, false)
}

// ShareBatchEntry protects a batch reopened with different settings from
// creating another share while the original target's outcome is unknown.
func (m *Manager) ShareBatchEntry(ctx context.Context, in ShareInput) (ShareResult, error) {
	return m.shareEntry(ctx, in, true)
}

// ValidateShareCreationBatch checks one directory snapshot before the first write.
// Submission still checks every original entry again.
func (m *Manager) ValidateShareCreationBatch(ctx context.Context, inputs []ShareInput) error {
	return m.validateShareCreationBatch(ctx, NewHTTPUploader().Client, inputs)
}

func (m *Manager) validateShareCreationBatch(ctx context.Context, client *http.Client, inputs []ShareInput) error {
	if len(inputs) == 0 || len(inputs) > 50 {
		return errors.New("每批请选择 1 至 50 个分享项目")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	first := inputs[0]
	m.mu.Lock()
	account := m.downloadAccountLocked(first.AccountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || account.ID == "" {
		return errors.New("程序正在退出或账号不存在")
	}
	seen := make(map[string]bool)
	targets := make([]ShareTarget, 0, len(inputs))
	for _, input := range inputs {
		if input.AccountID != first.AccountID || input.ParentID != first.ParentID || input.Action != "create" {
			return errors.New("批量创建只支持同一账号、同一目录的项目")
		}
		if err := validateShare(account, input); err != nil {
			return err
		}
		for _, target := range shareTargets(input) {
			if seen[target.ID] || len(targets) >= 50 {
				return errors.New("分享项目重复或超过 50 项")
			}
			seen[target.ID] = true
			targets = append(targets, target)
		}
	}
	first.Targets = targets
	scope := downloadScope(account)
	m.mu.Lock()
	for _, op := range m.shares {
		if pendingShareOverlap(op, first, scope) {
			m.mu.Unlock()
			return errors.New("所选项目有待确认的原操作，请先到分享记录核对")
		}
	}
	m.mu.Unlock()
	if err := shareSource(ctx, client, account, first); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(first.AccountID)) != scope {
		return errors.New("账号来源已变化，请刷新后重选")
	}
	return nil
}

func pendingShareOverlap(op *shareOperation, in ShareInput, scope string) bool {
	sameSource := op.in.ParentID == in.ParentID ||
		op.in.Action == "create" && (op.in.PaidPriceFen123 > 0 || in.PaidPriceFen123 > 0)
	return op.scope == scope && op.in.AccountID == in.AccountID && sameSource &&
		(op.running || op.result.State == "unknown" || op.result.State == "working") && shareOverlap(op.in, in)
}

func (m *Manager) shareEntry(ctx context.Context, in ShareInput, strictTarget bool) (ShareResult, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(in.AccountID)
	if m.closing || a.ID == "" {
		m.mu.Unlock()
		return ShareResult{}, errors.New("程序正在退出或账号不存在")
	}
	if in.lanzouReceiptID != "" {
		original := m.shares[in.lanzouReceiptID]
		if !canLocateLanzouShare(a, original) {
			m.mu.Unlock()
			return ShareResult{}, errors.New("原蓝奏回执或账号来源已变化，未修改提取码")
		}
		link, err := shareURL("lanzou", original.result.URL)
		if _, blocked := m.shareRevocationOverlayLocked(original); blocked || err != nil || link != in.expectedLanzouURL {
			m.mu.Unlock()
			return ShareResult{}, errors.New("原蓝奏分享链接无法确认，未修改提取码")
		}
		in.ParentID, in.EntryID, in.Name, in.Folder = original.in.ParentID, original.in.EntryID, original.in.Name, original.in.Folder
	}
	if in.recreateReceiptID != "" {
		if err := m.bindShareRecreationLocked(a, &in); err != nil {
			m.mu.Unlock()
			return ShareResult{}, err
		}
	}
	if err := validateShare(a, in); err != nil {
		m.mu.Unlock()
		return ShareResult{}, err
	}
	if len(in.Targets) > 0 {
		in.Targets = append([]ShareTarget(nil), in.Targets...)
		sort.Slice(in.Targets, func(i, j int) bool { return in.Targets[i].ID < in.Targets[j].ID })
		in.EntryID, in.Name, in.Folder = in.Targets[0].ID, in.Targets[0].Name, in.Targets[0].Folder
	}
	var settingsSource managedShareHandle
	if in.Action == "settings" {
		settingsSource, _ = decodeManagedShareHandle(in.Handle) // validated above
		if r := m.revocationForLocked(settingsSource); r != nil && r.saved.Submitted {
			m.mu.Unlock()
			return ShareResult{}, errors.New("原分享已进入撤销流程，请先核对撤销结果，未修改设置")
		}
	}
	keyInput := in
	if in.Action == "settings" {
		keyInput.Handle = ""
	}
	raw, _ := json.Marshal(keyInput)
	if in.recreateReceiptID != "" {
		raw, _ = json.Marshal([]string{"recreate", in.recreateReceiptID, in.recreateAttemptID})
	}
	sum := sha256.Sum256(append([]byte(downloadScope(a)), raw...))
	key := hex.EncodeToString(sum[:])
	if in.recreateReceiptID != "" {
		for _, op := range m.shares {
			if op.key != key {
				continue
			}
			result := op.result
			if overlaid, ok := m.shareRevocationOverlayLocked(op); ok {
				result = overlaid
			}
			m.mu.Unlock()
			return result, nil
		}
	}
	for _, op := range m.shares {
		if a.Kind == "lanzou" && op.superseded {
			continue // A fresh read or edit must use the current cloud settings.
		}
		if a.Kind == "lanzou" && in.Action == "password" && op.in.Action == "password" && op.in.AccountID == in.AccountID && op.scope == downloadScope(a) &&
			op.in.EntryID == in.EntryID && op.in.Folder == in.Folder && (op.running || op.result.State == "unknown" || op.result.State == "working") {
			r := op.result
			m.mu.Unlock()
			return r, nil
		}
		if in.Action == "settings" && op.in.Action == "settings" && op.scope == downloadScope(a) && op.in.AccountID == in.AccountID && op.in.EntryID == in.EntryID && (op.running || op.result.State == "unknown" || op.result.State == "working") {
			previous, err := decodeManagedShareHandle(op.in.Handle)
			if err != nil || previous.Paid123 == settingsSource.Paid123 {
				r := op.result
				m.mu.Unlock()
				return r, nil
			}
		}
		if op.key == key && op.superseded {
			r := op.result
			m.mu.Unlock()
			return r, nil
		}
		if (strictTarget || len(in.Targets) > 0 || len(op.in.Targets) > 0 || in.PaidPriceFen123 > 0 || op.in.PaidPriceFen123 > 0) &&
			in.Action == "create" && pendingShareOverlap(op, in, downloadScope(a)) {
			result := op.result
			result.URL = ""
			result.Password = ""
			result.Message = "同一目标已有待确认操作，本次未提交新创建；请检查原操作结果"
			m.mu.Unlock()
			return result, nil
		}
		if op.key == key {
			if r, blocked := m.shareRevocationOverlayLocked(op); blocked {
				m.mu.Unlock()
				return r, nil
			}
		}
		if op.key == key && in.Action != "get" && (op.running || op.result.State == "unknown" || (in.Action == "create" || in.Action == "settings") && op.result.State == "ready") {
			if (in.Action == "create" || in.Action == "settings") && op.result.State == "ready" && !op.running {
				checkCtx, err := m.beginShareCheckLocked(ctx, a, op)
				if err != nil {
					m.mu.Unlock()
					return ShareResult{}, err
				}
				m.mu.Unlock()
				return m.runShare(checkCtx, a, op, true), nil
			}
			result := op.result
			m.mu.Unlock()
			return result, nil
		}
	}
	if len(m.shares) >= maxShareRecords {
		m.mu.Unlock()
		return ShareResult{}, errors.New("本机分享记录已达1000项，请在分享记录页清理已确认记录；未确认记录不会自动删除")
	}
	if err := m.reserveShareLocked(a.ID); err != nil {
		m.mu.Unlock()
		return ShareResult{}, err
	}
	if m.shares == nil {
		m.shares = map[string]*shareOperation{}
	}
	ctx, cancel := context.WithCancel(ctx)
	op := &shareOperation{recreatedFrom: in.recreateReceiptID, in: in, scope: downloadScope(a), key: key, created: time.Now(), running: true, cancel: cancel, result: ShareResult{ID: newID(), Kind: a.Kind, Name: in.Name, Days: in.Days, State: "working", Message: "正在处理分享，请勿重复提交"}}
	if in.Action == "settings" {
		op.share = in.EntryID
	}
	m.shares[op.result.ID] = op
	m.attachShareCheckpoint(op)
	op.saved = snapshotShare(op)
	if err := m.saveLocked(); err != nil {
		delete(m.shares, op.result.ID)
		delete(m.renaming, a.ID)
		cancel()
		m.mu.Unlock()
		return ShareResult{}, errors.New("无法保存分享回执，未发送创建或修改请求；请检查磁盘空间和权限")
	}
	m.workers.Add(1)
	m.mu.Unlock()
	return m.runShare(ctx, a, op, false), nil
}

func (m *Manager) reserveShareLocked(accountID string) error {
	if m.renaming[accountID] {
		return errors.New("此网盘正在修改文件或处理分享，请稍后")
	}
	for _, t := range m.tasks {
		if t.AccountID == accountID && (t.Status == "queued" || t.Status == "running" || m.active[t.ID] != nil) {
			return errors.New("请先完成或停止此网盘的上传任务，再分享")
		}
	}
	if m.renaming == nil {
		m.renaming = map[string]bool{}
	}
	m.renaming[accountID] = true
	return nil
}

func (m *Manager) CheckShare(ctx context.Context, id string) (ShareResult, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.shares[id]
	if op == nil || m.closing {
		m.mu.Unlock()
		return ShareResult{}, errors.New("本次分享记录已失效，请到官网核对")
	}
	if op.superseded {
		r := op.result
		m.mu.Unlock()
		return r, nil
	}
	a := m.downloadAccountLocked(op.in.AccountID)
	if a.ID == "" || downloadScope(a) != op.scope {
		m.mu.Unlock()
		return ShareResult{}, errors.New("账号登录或来源已变化，请到原账号官网核对分享，不要重复创建")
	}
	if r, blocked := m.shareRevocationOverlayLocked(op); blocked {
		m.mu.Unlock()
		return r, nil
	}
	if op.running || !op.result.CanCheck {
		v := op.result
		m.mu.Unlock()
		return v, nil
	}
	ctx, err := m.beginShareCheckLocked(ctx, a, op)
	if err != nil {
		m.mu.Unlock()
		return ShareResult{}, err
	}
	m.mu.Unlock()
	return m.runShare(ctx, a, op, true), nil
}

func (m *Manager) runShare(ctx context.Context, a Account, op *shareOperation, checkOnly bool) ShareResult {
	defer m.workers.Done()
	client := NewHTTPUploader().Client
	result := op.result
	result.URL = ""
	result.Password = ""
	if op.in.Action == "settings" {
		switch a.Kind {
		case "baidu":
			if op.in.ActivateBaidu {
				result = shareBaiduActivation(ctx, client, a, op, checkOnly)
			} else {
				result = shareBaiduSettings(ctx, client, a, op, checkOnly)
			}
		case "pan123":
			result = sharePan123Settings(ctx, client, a, op, checkOnly)
		default:
			result = shareGuangyaSettings(ctx, client, a, op, checkOnly)
		}
	} else if checkOnly && op.in.PaidPriceFen123 > 0 {
		result = share123Paid(ctx, client, a, op, true)
	} else if err := shareSource(ctx, client, a, op.in); err != nil {
		result.State = "failed"
		result.Message = "无法确认分享源，请刷新文件列表并检查登录或网络；未发送新的创建或修改请求"
		if checkOnly {
			result.State = "unknown"
			result.Message = "无法再次确认分享源，请在官网核对原操作，不要重复创建"
		}
	} else if a.Kind == "lanzou" {
		result = shareLanzou(ctx, client, a, op, checkOnly)
	} else if a.Kind == "pan123" {
		if op.in.PaidPriceFen123 > 0 {
			result = share123Paid(ctx, client, a, op, false)
		} else {
			result = share123(ctx, client, a, op, checkOnly)
		}
	} else if a.Kind == "baidu" {
		result = shareBaidu(ctx, client, a, op, checkOnly)
	} else if a.Kind == "guangya" {
		result = shareGuangya(ctx, client, a, op, checkOnly)
	} else {
		result = shareQuark(ctx, client, a, op, checkOnly)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result.ID = op.result.ID
	result.Name = op.in.Name
	if result.State == "ready" && op.in.NewTitle123 != "" {
		result.Name = op.in.NewTitle123
	}
	if len(op.in.Targets) > 1 {
		result.Name += " 等" + fmt.Sprint(len(op.in.Targets)) + "项（合并分享）"
	}
	if !checkOnly && op.in.previousShareURL != "" && result.State == "ready" && result.URL == op.in.previousShareURL {
		result.Message = "网盘返回了原分享链接，请以此链接和当前显示的设置为准"
	}
	result.Kind = a.Kind
	result.Days = op.in.Days
	result.CanCheck = a.Kind == "lanzou" || op.task != "" || op.share != "" || op.key123 != ""
	op.result = result
	op.running = false
	op.cancel()
	op.cancel = nil
	delete(m.renaming, a.ID)
	op.saved = snapshotShare(op)
	if m.saveLocked() != nil {
		result.Message += "；本机回执保存失败，请勿重复创建，恢复磁盘后检查原结果"
		op.result = result
	}
	return result
}

// A returned link is a public share URL, not an authenticated download node.
func shareScalar(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
		if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return ""
		}
	}
	if !shareToken.MatchString(value) {
		return ""
	}
	return value
}
