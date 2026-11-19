package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
)

const pan123ShareUpdate = "/restful/goapi/v1/share/update"
const pan123ShareTrafficInfo = "/share/traffic-info"
const maxPan123ShareTraffic = 1 << 50

func validPan123ExpiryHandle(h managedShareHandle) bool {
	digest, err := hex.DecodeString(h.Settings123)
	_, expiryErr := managed123Deadline(h.Deadline123)
	return err == nil && len(digest) == sha256.Size && expiryErr == nil &&
		(!h.Paid123 || h.URL != "" && h.Key123 != "") &&
		valid123Folder(h.ProviderID) && h.ProviderID != "0" && shareToken.MatchString(h.Cursor)
}

func validPan123ShareTitle(title string) bool {
	return strings.TrimSpace(title) != "" && len([]rune(title)) <= 255 &&
		!strings.ContainsAny(title, "<>|?:/\\") && strings.IndexFunc(title, unicode.IsControl) < 0
}

func pan123EditableShare(row managed123Record, now time.Time, paid bool) bool {
	deadline, expiryErr := managed123Deadline(row.Expiration)
	created, createdErr := managed123Deadline(row.Created)
	expired, stateErr := managed123Expired(row.Expired)
	id := shareScalar(row.ID)
	if expiryErr != nil || createdErr != nil || !deadline.After(now) || created.After(now) ||
		stateErr != nil || expired || len(row.Violations) != 0 || !valid123Folder(id) || id == "0" {
		return false
	}
	if row.Password == nil || *row.Password != "" && (len(*row.Password) != 4 || !shareCode.MatchString(*row.Password)) ||
		!validPan123ShareTitle(row.Name) {
		return false
	}
	// The website submits the traffic configuration with the new expiry.
	// Edit only where that configuration is explicit and can be preserved.
	if row.Paid == nil || row.NoLoginAmount != nil && *row.NoLoginAmount < 0 {
		return false
	}
	if paid {
		if *row.Paid != 1 || row.PaidAudit == nil || *row.PaidAudit != 1 ||
			managed123Amount(row.PriceFen) == nil || shareScalar(row.Key) == "" || !deadline.Equal(share123Expiry(0, now)) ||
			row.NoLoginAmount != nil && *row.NoLoginAmount != 0 {
			return false
		}
	} else if *row.Paid != 0 {
		return false
	}
	return row.Traffic != nil && *row.Traffic >= 1 && *row.Traffic <= 4 &&
		row.LimitMode != nil && row.Limit != nil &&
		(*row.LimitMode == 1 && *row.Limit == 0 || *row.LimitMode == 2 && *row.Traffic != 1 && *row.Limit > 0 && *row.Limit <= maxPan123ShareTraffic)
}

func pan123ShareSettingsDigest(row managed123Record) string {
	data, _ := json.Marshal(struct {
		Password *string
		Traffic  *int
		Mode     *int
		Limit    *int64
		Created  string
	}{row.Password, row.Traffic, row.LimitMode, row.Limit, row.Created})
	if row.Paid != nil && *row.Paid == 1 {
		data, _ = json.Marshal(struct {
			Settings json.RawMessage
			Price    *int64
			Audit    *int
		}{data, managed123Amount(row.PriceFen), row.PaidAudit})
	} else if row.NoLoginAmount != nil && *row.NoLoginAmount > 0 {
		data, _ = json.Marshal(struct {
			Settings      json.RawMessage
			NoLoginAmount int64
		}{data, *row.NoLoginAmount})
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readPan123ExpiryShare(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (managed123Record, error) {
	return readPan123SettingsShare(ctx, client, a, h, h.Name, nil, nil)
}

func pan123TrafficLimitMode(limit int64) int {
	if limit == 0 {
		return 1
	}
	return 2
}

func pan123SettingsMatch(row managed123Record, h managedShareHandle, targetLimit *int64, targetMode *int) bool {
	if targetLimit != nil && targetMode != nil {
		if h.TrafficMode123 == nil || *row.Traffic != *targetMode || *row.Limit != *targetLimit ||
			*row.LimitMode != pan123TrafficLimitMode(*targetLimit) {
			return false
		}
		originalLimit := int64(0)
		if h.TrafficCap123 != nil {
			originalLimit = *h.TrafficCap123
		} else if *h.TrafficMode123 != 1 {
			return false
		}
		originalMode := pan123TrafficLimitMode(originalLimit)
		row.Traffic, row.Limit, row.LimitMode = h.TrafficMode123, &originalLimit, &originalMode
		return pan123ShareSettingsDigest(row) == h.Settings123
	}
	if targetLimit != nil {
		if h.TrafficCap123 == nil || *row.Traffic == 1 || *row.Limit != *targetLimit || *row.LimitMode != pan123TrafficLimitMode(*targetLimit) {
			return false
		}
		// Compare the settings that were meant to stay in place using the
		// original limit from the protected handle.
		mode := pan123TrafficLimitMode(*h.TrafficCap123)
		row.LimitMode, row.Limit = &mode, h.TrafficCap123
	}
	if targetMode != nil {
		if h.TrafficMode123 == nil || *row.Traffic != *targetMode {
			return false
		}
		originalLimit := int64(0)
		if h.TrafficCap123 != nil {
			originalLimit = *h.TrafficCap123
		}
		expectedLimit := originalLimit
		if *targetMode == 1 {
			expectedLimit = 0
		}
		if *row.Limit != expectedLimit || *row.LimitMode != pan123TrafficLimitMode(expectedLimit) {
			return false
		}
		originalMode := pan123TrafficLimitMode(originalLimit)
		row.Traffic, row.LimitMode, row.Limit = h.TrafficMode123, &originalMode, &originalLimit
	}
	return pan123ShareSettingsDigest(row) == h.Settings123
}

func readPan123SettingsShare(ctx context.Context, client *http.Client, a Account, h managedShareHandle, expectedName string, targetLimit *int64, targetMode *int) (managed123Record, error) {
	rows, _, err := pan123ManagedRecordsByType(ctx, client, a, h.Cursor, h.Paid123)
	if err != nil {
		return managed123Record{}, err
	}
	return findPan123SettingsShare(rows, h, expectedName, targetLimit, targetMode)
}

func findPan123SettingsShare(rows []managed123Record, h managedShareHandle, expectedName string, targetLimit *int64, targetMode *int) (managed123Record, error) {
	for _, row := range rows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		link := managed123Link(row)
		if h.Paid123 {
			link = managed123ListedLink(row)
		}
		if row.Name == expectedName && link == h.URL && shareScalar(row.Key) == h.Key123 &&
			pan123EditableShare(row, time.Now(), h.Paid123) && pan123SettingsMatch(row, h, targetLimit, targetMode) {
			return row, nil
		}
		return managed123Record{}, errors.New("原链接、访问设置或定价已变化，或分享已失效")
	}
	return managed123Record{}, errors.New("原分享不在原列表页，请刷新云端分享")
}

func checkPan123ShareTrafficBalance(ctx context.Context, pan pan123Client) error {
	var balance struct {
		Tip *string `json:"shareTrafficBalanceTip"`
	}
	err := pan.request(ctx, http.MethodGet, pan123ShareTrafficInfo, nil, nil, &balance)
	if err != nil || balance.Tip == nil {
		return errors.New("无法确认 123 分享流量包余额")
	}
	if *balance.Tip != "" {
		return errors.New("123 提示分享流量包不足")
	}
	return nil
}

func validatePan123TrafficBatchSources(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle, change ShareInput) error {
	pages := make(map[string][]managed123Record)
	checkBalance := false
	for _, h := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, found := pages[h.Cursor]
		if !found {
			var err error
			rows, _, err = pan123ManagedRecordsByType(ctx, client, a, h.Cursor, h.Paid123)
			if err != nil {
				return err
			}
			pages[h.Cursor] = rows
		}
		row, err := findPan123SettingsShare(rows, h, h.Name, nil, nil)
		if err != nil {
			return errors.New(h.Name + "：" + err.Error())
		}
		deadline, _ := managed123Deadline(h.Deadline123)
		if err = checkPan123ExpiryTarget(row, h, 0, deadline, time.Now()); err != nil {
			return errors.New(h.Name + "：" + err.Error())
		}
		if change.TrafficLimit123 != nil && !matchesPan123OriginalLimit(row, h, change.TrafficMode123 != nil) {
			return errors.New(h.Name + "：原下载流量设置无法确认")
		}
		if change.TrafficMode123 != nil {
			if h.TrafficMode123 == nil || *h.TrafficMode123 != *row.Traffic {
				return errors.New(h.Name + "：原下载权限无法确认")
			}
			checkBalance = checkBalance || *change.TrafficMode123 > 1 && *change.TrafficMode123 != *row.Traffic
		}
	}
	if checkBalance {
		return checkPan123ShareTrafficBalance(ctx, pan123Client{client, a})
	}
	return nil
}

func matchesPan123OriginalLimit(row managed123Record, h managedShareHandle, editingMode bool) bool {
	if *row.Traffic == 1 {
		return editingMode && *row.Limit == 0
	}
	return h.TrafficCap123 != nil && *h.TrafficCap123 == *row.Limit
}

func checkPan123ExpiryTarget(row managed123Record, h managedShareHandle, days int, target, now time.Time) error {
	original, _ := managed123Deadline(h.Deadline123)
	current, _ := managed123Deadline(row.Expiration)
	if !original.Equal(current) {
		return errors.New("原有效期已变化，请刷新后重选")
	}
	created, _ := managed123Deadline(row.Created)
	if days > 0 && !current.Equal(target) && created.AddDate(0, 0, days).Before(now) {
		return errors.New("按原分享创建时间，此选项已不可用，请选择更长的有效期")
	}
	return nil
}

func sharePan123Settings(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	unknown := func(message string) ShareResult {
		return ShareResult{State: "unknown", Message: message + "；请检查原结果，不会重复提交"}
	}
	h, err := decodeManagedShareHandle(op.in.Handle)
	if err != nil || h.Kind != "pan123" || h.AccountID != a.ID || h.Scope != downloadScope(a) || !validPan123ExpiryHandle(h) {
		return unknown("123 原账号或分享标识无法核对")
	}
	deadline := share123Expiry(op.in.Days, op.created).Truncate(time.Second)
	renaming := op.in.NewTitle123 != ""
	targetLimit := op.in.TrafficLimit123
	targetMode := op.in.TrafficMode123
	if h.Paid123 && (op.in.Days != 0 || !renaming && targetLimit == nil && targetMode == nil) {
		return ShareResult{State: "failed", Message: "123 付费分享只支持修改主题和下载设置"}
	}
	name := h.Name
	if renaming {
		name = op.in.NewTitle123
	}
	if renaming || targetLimit != nil || targetMode != nil {
		deadline, _ = managed123Deadline(h.Deadline123)
	}
	if !checkOnly {
		row, err := readPan123ExpiryShare(ctx, client, a, h)
		if err != nil {
			return ShareResult{State: "failed", Message: "未提交 123 分享设置：" + err.Error()}
		}
		if err := checkPan123ExpiryTarget(row, h, op.in.Days, deadline, time.Now()); err != nil {
			return ShareResult{State: "failed", Message: "未提交 123 分享设置：" + err.Error()}
		}
		current, _ := managed123Deadline(row.Expiration)
		if renaming && name == h.Name {
			return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: current.Format(time.RFC3339Nano),
				Message: "123 分享主题与原主题一致，无需修改"}
		}
		if !renaming && targetLimit == nil && targetMode == nil && current.Equal(deadline) {
			return ShareResult{State: "ready", URL: h.URL, Password: *row.Password,
				Message: "123 原分享已是目标有效期，无需修改。到期时间：" + current.Format(time.RFC3339)}
		}
		mode, limit := *row.LimitMode, *row.Limit
		traffic := *row.Traffic
		pan := pan123Client{client, a}
		if targetLimit != nil {
			if !matchesPan123OriginalLimit(row, h, targetMode != nil) {
				return ShareResult{State: "failed", Message: "原下载流量设置无法确认，未提交"}
			}
			if *targetLimit == limit && targetMode == nil {
				return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: current.Format(time.RFC3339Nano),
					Message: "123 下载流量上限与原设置一致，无需修改"}
			}
			limit, mode = *targetLimit, pan123TrafficLimitMode(*targetLimit)
		}
		if targetMode != nil {
			if h.TrafficMode123 == nil || *h.TrafficMode123 != traffic {
				return ShareResult{State: "failed", Message: "原下载权限无法确认，未提交"}
			}
			if traffic == *targetMode && (targetLimit == nil || limit == *row.Limit) {
				return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: current.Format(time.RFC3339Nano),
					Message: "123 下载权限与原设置一致，无需修改"}
			}
			traffic = *targetMode
			if traffic == 1 {
				limit, mode = 0, 1
			} else if traffic != *row.Traffic {
				if err := checkPan123ShareTrafficBalance(ctx, pan); err != nil {
					return ShareResult{State: "failed", Message: err.Error() + "，未提交"}
				}
			}
		}
		if op.settingsCheckpoint != nil {
			if err = op.settingsCheckpoint(h); err != nil {
				return unknown("本机回执保存失败，未发送 123 修改请求")
			}
		}
		body := map[string]any{
			"shareId": h.ProviderID, "expiration": deadline.Format(time.RFC3339Nano), "shareName": name,
			"trafficSwitch": traffic, "trafficLimitSwitch": mode, "trafficLimit": limit,
		}
		if !h.Paid123 && row.NoLoginAmount != nil && *row.NoLoginAmount > 0 {
			body["noLoginStdAmount"] = *row.NoLoginAmount
		}
		// Even if the response is lost, only a read of the original share follows.
		_ = pan.request(ctx, http.MethodPut, pan123ShareUpdate, nil, body, nil)
	}
	row, err := readPan123SettingsShare(ctx, client, a, h, name, targetLimit, targetMode)
	if err != nil {
		return unknown("无法核对 123 原分享当前设置")
	}
	actual, _ := managed123Deadline(row.Expiration)
	if !actual.Equal(deadline) {
		return unknown("123 原分享到期时间尚未符合目标")
	}
	if renaming {
		if h.Paid123 {
			return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: actual.Format(time.RFC3339Nano),
				Message: "已核对新主题，原链接、提取码、定价、有效期及流量设置一致"}
		}
		return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: actual.Format(time.RFC3339Nano),
			Message: "已核对新主题，原链接、提取码、有效期及流量设置一致"}
	}
	if targetLimit != nil && targetMode != nil {
		return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: actual.Format(time.RFC3339Nano),
			Message: "已核对下载权限和流量上限"}
	}
	if targetLimit != nil {
		return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: actual.Format(time.RFC3339Nano),
			Message: "已核对下载流量上限，原主题、链接、提取码、有效期及下载权限一致"}
	}
	if targetMode != nil {
		return ShareResult{State: "ready", URL: h.URL, Password: *row.Password, Expiry: actual.Format(time.RFC3339Nano),
			Message: "已核对下载权限、流量上限及原主题、链接、提取码和有效期"}
	}
	return ShareResult{State: "ready", URL: h.URL, Password: *row.Password,
		Message: "已核对 123 原链接、提取码、流量设置及新的到期时间：" + actual.Format(time.RFC3339)}
}
