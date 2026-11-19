package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (m *Manager) UpdateManagedShare(ctx context.Context, handle, password string, days int) (ShareResult, error) {
	h, e := decodeManagedShareHandle(handle)
	if e != nil {
		return ShareResult{}, e
	}
	input := managedShareSettingsInput(h, handle, password, days)
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

func (m *Manager) ActivateManagedBaiduShare(ctx context.Context, handle string) (ShareResult, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareResult{}, err
	}
	input := managedShareSettingsInput(h, handle, "", 0)
	input.ActivateBaidu = true
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

func (m *Manager) RenameManagedPan123Share(ctx context.Context, handle, title string) (ShareResult, error) {
	title = strings.TrimSpace(title)
	if !validPan123ShareTitle(title) {
		return ShareResult{}, errors.New("123 分享主题应为 1 至 255 个字符，且不含控制字符或 <>|?:/\\")
	}
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareResult{}, err
	}
	input := managedShareSettingsInput(h, handle, "", 0)
	input.NewTitle123 = title
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

func managedShareSettingsInput(h managedShareHandle, handle, password string, days int) ShareInput {
	return ShareInput{Handle: handle, AccountID: h.AccountID, ParentID: "managed-share", EntryID: h.ProviderID, Name: h.Name, Action: "settings", Password: password, Days: days}
}

func (m *Manager) SetManagedPan123ShareTrafficLimit(ctx context.Context, handle string, limit int64) (ShareResult, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareResult{}, err
	}
	input := managedShareSettingsInput(h, handle, "", 0)
	input.TrafficLimit123 = &limit
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

func (m *Manager) SetManagedPan123ShareTrafficMode(ctx context.Context, handle string, mode int) (ShareResult, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareResult{}, err
	}
	input := managedShareSettingsInput(h, handle, "", 0)
	input.TrafficMode123 = &mode
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

func (m *Manager) SetManagedPan123ShareDownloadSettings(ctx context.Context, handle string, mode int, limit int64) (ShareResult, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return ShareResult{}, err
	}
	input := managedShareSettingsInput(h, handle, "", 0)
	input.TrafficMode123, input.TrafficLimit123 = &mode, &limit
	input.SettingsIntent = newID()
	return m.ShareEntry(ctx, input)
}

type managedShareBatchMode uint8

const (
	managedShareExpiryBatch managedShareBatchMode = iota
	guangyaShareSettingsBatch
	baiduShareActivationBatch
	pan123ShareTrafficBatch
)

// ValidateManagedShareExpiryBatch checks account, selection and original settings
// without recording an operation or calling a provider's write endpoint.
func (m *Manager) ValidateManagedShareExpiryBatch(ctx context.Context, handles []string, days int) error {
	return m.validateManagedShareSettingsBatch(ctx, handles, ShareInput{Days: days}, managedShareExpiryBatch)
}

func (m *Manager) ValidateGuangyaShareSettingsBatch(ctx context.Context, handles []string, password string, days int) error {
	return m.validateManagedShareSettingsBatch(ctx, handles, ShareInput{Password: password, Days: days}, guangyaShareSettingsBatch)
}

func (m *Manager) ValidateBaiduShareActivationBatch(ctx context.Context, handles []string) error {
	return m.validateManagedShareSettingsBatch(ctx, handles, ShareInput{ActivateBaidu: true}, baiduShareActivationBatch)
}

func (m *Manager) ValidatePan123ShareTrafficBatch(ctx context.Context, handles []string, permission int, limit int64) error {
	var change ShareInput
	if permission == 0 {
		change.TrafficLimit123 = &limit
	} else if limit == 0 {
		change.TrafficMode123 = &permission
	} else {
		return errors.New("请单独选择批量下载权限或流量上限")
	}
	return m.validateManagedShareSettingsBatch(ctx, handles, change, pan123ShareTrafficBatch)
}

func (m *Manager) ValidatePan123ShareDownloadSettingsBatch(ctx context.Context, handles []string, permission int, limit int64) error {
	return m.validateManagedShareSettingsBatch(ctx, handles, ShareInput{TrafficMode123: &permission, TrafficLimit123: &limit}, pan123ShareTrafficBatch)
}

func (m *Manager) validateManagedShareSettingsBatch(ctx context.Context, handles []string, change ShareInput, mode managedShareBatchMode) error {
	if len(handles) == 0 || len(handles) > 50 {
		return errors.New("请选择 1 至 50 条原分享")
	}
	sources := make([]managedShareHandle, 0, len(handles))
	inputs := make([]ShareInput, 0, len(handles))
	seen := make(map[string]bool, len(handles))
	for _, handle := range handles {
		h, err := decodeManagedShareHandle(handle)
		if err != nil {
			return err
		}
		if mode == guangyaShareSettingsBatch && h.Kind != "guangya" ||
			mode == baiduShareActivationBatch && h.Kind != "baidu" ||
			mode == pan123ShareTrafficBatch && h.Kind != "pan123" ||
			mode == managedShareExpiryBatch && h.Kind != "baidu" && h.Kind != "pan123" {
			return errors.New("所选网盘不支持此批量设置操作")
		}
		if seen[h.ProviderID] || len(sources) > 0 && (h.AccountID != sources[0].AccountID || h.Scope != sources[0].Scope || h.Kind != sources[0].Kind || h.Paid123 != sources[0].Paid123) {
			return errors.New("请选择同一账号、同一分享类型下不重复的原分享")
		}
		seen[h.ProviderID] = true
		sources = append(sources, h)
		input := managedShareSettingsInput(h, handle, change.Password, change.Days)
		input.ActivateBaidu = change.ActivateBaidu
		input.TrafficLimit123, input.TrafficMode123 = change.TrafficLimit123, change.TrafficMode123
		inputs = append(inputs, input)
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(sources[0].AccountID)
	if m.closing || a.ID == "" {
		m.mu.Unlock()
		return errors.New("原分享账号已变化，请刷新列表")
	}
	for _, input := range inputs {
		if err := validateShare(a, input); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	scope := sources[0].Scope
	paid := sources[0].Paid123
	for _, op := range m.shareRevokes {
		h := op.saved.Source
		if op.saved.Submitted && h.Paid123 == paid && h.AccountID == a.ID && h.Scope == scope && seen[h.ProviderID] {
			m.mu.Unlock()
			return errors.New("所选分享已有撤销操作，请先核对原结果")
		}
	}
	for _, op := range m.shares {
		if op.in.AccountID == a.ID && op.scope == scope && op.in.Action == "settings" && seen[op.in.EntryID] && (op.running || op.result.State == "unknown" || op.result.State == "working") {
			if h, err := decodeManagedShareHandle(op.in.Handle); err == nil && h.Paid123 != paid {
				continue
			}
			m.mu.Unlock()
			return errors.New("所选分享已有待核对的设置操作，请先核对原结果")
		}
	}
	m.mu.Unlock()
	client := NewHTTPUploader().Client
	var err error
	if mode == pan123ShareTrafficBatch {
		err = validatePan123TrafficBatchSources(ctx, client, a, sources, change)
	} else {
		err = validateManagedShareSettingsSources(ctx, client, a, sources, change.Days, mode)
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != scope {
		return errors.New("账号来源已变化，请刷新列表")
	}
	return nil
}

func validateManagedShareSettingsSources(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle, days int, mode managedShareBatchMode) error {
	if mode == baiduShareActivationBatch {
		return validateBaiduActivationSources(ctx, client, a, sources)
	}
	for _, h := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.Kind == "baidu" {
			if _, err := baiduManagedShareLink(ctx, client, a, h); err != nil {
				return errors.New(h.Name + "：无法核对原分享链接、提取码或状态")
			}
			continue
		}
		if a.Kind == "guangya" {
			if _, err := checkGuangyaSettingsSource(ctx, client, a, h); err != nil {
				return errors.New(h.Name + "：无法核对原分享链接、访问设置或状态")
			}
			continue
		}
		row, err := readPan123ExpiryShare(ctx, client, a, h)
		if err == nil {
			now := time.Now()
			err = checkPan123ExpiryTarget(row, h, days, share123Expiry(days, now).Truncate(time.Second), now)
		}
		if err != nil {
			return errors.New(h.Name + "：" + err.Error())
		}
	}
	return nil
}

// Supersede old local copies before the write. They must never advertise an old
// password after a settings request whose outcome may be unknown.
func (m *Manager) prepareShareSettings(op *shareOperation, h managedShareHandle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	type previousShare struct {
		superseded bool
		result     ShareResult
		saved      diskShare
	}
	previous := map[*shareOperation]previousShare{}
	for _, prior := range m.shares {
		if prior == op || prior.scope != op.scope || prior.in.AccountID != op.in.AccountID || prior.result.Kind != op.result.Kind {
			continue
		}
		if op.result.Kind == "lanzou" {
			if prior.result.State != "ready" || !shareOverlap(prior.in, op.in) {
				continue
			}
		} else if prior.result.URL != h.URL && prior.share != h.ProviderID && (h.PublicID == "" || prior.share != h.PublicID) {
			continue
		}
		if h.Kind == "pan123" {
			priorPaid := prior.in.PaidPriceFen123 > 0
			if prior.in.Action == "settings" {
				savedHandle, err := decodeManagedShareHandle(prior.in.Handle)
				if err != nil {
					continue
				}
				priorPaid = savedHandle.Paid123
			}
			if priorPaid != h.Paid123 {
				continue
			}
		}
		previous[prior] = previousShare{prior.superseded, prior.result, prior.saved}
		prior.superseded = true
		prior.result.State = "failed"
		prior.result.URL = ""
		prior.result.Password = ""
		prior.result.CanCheck = false
		prior.result.Message = "此分享已进入设置修改流程，请使用最新设置回执核对"
		prior.saved = snapshotShare(prior)
	}
	before := op.saved
	op.saved = snapshotShare(op)
	if err := m.saveLocked(); err != nil {
		op.saved = before
		for prior, state := range previous {
			prior.superseded, prior.result, prior.saved = state.superseded, state.result, state.saved
		}
		return err
	}
	return nil
}

func readGuangyaSettingsRecord(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (managedGYRecord, error) {
	rows, _, err := gyManagedRecords(ctx, client, a, h.Page)
	if err != nil {
		return managedGYRecord{}, err
	}
	for _, row := range rows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		link, err := shareURL("guangya", row.URL)
		if err != nil || link != h.URL || gyManagedName(row) != h.Name || row.Status == nil || *row.Status != 1 || shareScalar(row.ShareID) != h.PublicID || string(row.ID) != string(h.RawID) {
			return row, errors.New("原分享目标或状态已变化")
		}
		return row, nil
	}
	return managedGYRecord{}, errors.New("原分享不在原页，请到官网核对")
}

func checkGuangyaSettingsSource(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (managedGYRecord, error) {
	if _, err := gyManagedShareLink(ctx, client, a, h); err != nil {
		return managedGYRecord{}, err
	}
	return readGuangyaSettingsRecord(ctx, client, a, h)
}

func shareGuangyaSettings(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	unknown := func(reason string) ShareResult {
		return ShareResult{State: "unknown", Message: reason + "；请检查原设置结果，不要重复提交 [SHARE-EDIT/GY]"}
	}
	h, e := decodeManagedShareHandle(op.in.Handle)
	if e != nil || h.Scope != downloadScope(a) {
		return unknown("原账号来源或分享标识已变化")
	}
	if !checkOnly {
		row, err := checkGuangyaSettingsSource(ctx, client, a, h)
		if err != nil {
			return ShareResult{State: "failed", Message: "未提交设置修改：原分享已变化或无法读取"}
		}
		body := map[string]any{}
		for _, key := range []string{"title", "autoFillCode", "downloadType", "trafficLimit", "maxRestoreCount", "enableShareCode", "shareCode", "shareTemplate"} {
			if raw, ok := row.Fields[key]; ok {
				body[key] = raw
			}
		}
		if _, ok := body["downloadType"]; !ok {
			body["downloadType"] = 0
		}
		if _, ok := body["maxRestoreCount"]; !ok {
			body["maxRestoreCount"] = 0
		}
		body["id"] = h.RawID
		body["validateDuration"] = int64(op.in.Days) * 86400
		body["code"] = op.in.Password
		body["shareType"] = 0
		if op.in.Password != "" {
			body["shareType"] = 2
		} else {
			body["autoFillCode"] = false
		}
		if op.checkpoint != nil {
			if e = op.settingsCheckpoint(h); e != nil {
				return unknown("本机回执无法保存，未发送修改请求")
			}
		}
		// A successful write may return no data. Its acknowledgement is never the
		// success criterion; only the same original record can confirm the settings.
		_, _ = (gyClient{client, a}).call(ctx, "/userres/v1/update_share", body, nil)
	}
	row, e := readGuangyaSettingsRecord(ctx, client, a, h)
	if e != nil {
		return unknown("无法核对原设置记录")
	}
	code, accessErr := gyShareAccess(row.gyShareRecord)
	if accessErr != nil || code != op.in.Password {
		return unknown("原分享提取码或访问模式尚未与目标设置一致")
	}
	if row.Left == nil {
		return unknown("原分享未返回期限")
	}
	if op.in.Days == 0 {
		if *row.Left != -1 {
			return unknown("原分享永久期限尚未确认")
		}
	} else {
		expected := int64(op.in.Days)*86400 - int64(time.Since(op.created).Seconds())
		delta := *row.Left - expected
		if *row.Left <= 0 || delta < -120 || delta > 120 || row.Duration != nil && *row.Duration != int64(op.in.Days)*86400 {
			return unknown("原分享到期时间尚未与目标设置一致")
		}
	}
	return ShareResult{State: "ready", URL: h.URL, Password: op.in.Password, Message: "已核对同一原分享的提取码、访问模式和期限；原链接保留，期限从本次修改起计算。"}
}

func (r *managedGYRecord) UnmarshalJSON(data []byte) error {
	type plain managedGYRecord
	var value plain
	if e := json.Unmarshal(data, &value); e != nil {
		return e
	}
	*r = managedGYRecord(value)
	return json.Unmarshal(data, &r.Fields)
}
