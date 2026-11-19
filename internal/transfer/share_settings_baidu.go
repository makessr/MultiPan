package transfer

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func shareBaiduSettings(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	unknown := func(reason string) ShareResult {
		return ShareResult{State: "unknown", Message: reason + "；请检查原结果，不重复提交 [SHARE-EDIT/BD]"}
	}
	h, e := decodeManagedShareHandle(op.in.Handle)
	if e != nil || h.Kind != "baidu" || h.AccountID != a.ID || h.Scope != downloadScope(a) || h.URL == "" {
		return unknown("原账号来源已变化")
	}
	if !checkOnly {
		original, e := baiduManagedShareLink(ctx, client, a, h)
		if e != nil {
			return ShareResult{State: "failed", Message: "未提交修改：无法核对原分享链接、提取码或状态"}
		}
		op.baiduExpiryPassword = &original.Password
		if op.settingsCheckpoint != nil {
			if e = op.settingsCheckpoint(h); e != nil {
				op.baiduExpiryPassword = nil
				return unknown("本机回执保存失败，未发送修改请求")
			}
		}
		days := op.in.Days
		deadline := int64(-1)
		if days == 0 {
			days = -1
		} else {
			deadline = op.created.Unix() + int64(days)*86400
		}
		// This web endpoint uses Cookie authentication without bdstoken. A fresh
		// client has no session token; only original-share reads acquire one.
		e = newBaiduClient(client, a).shareAPI(ctx, "POST", "mexpire", nil, url.Values{"share_id": {h.ProviderID}, "expire_time": {strconv.FormatInt(deadline, 10)}, "op_expire_days": {strconv.Itoa(days)}}, nil)
		if e != nil {
			var apiErr *baiduAPIError
			if errors.As(e, &apiErr) {
				return ShareResult{State: "failed", Message: "百度拒绝期限修改；未重试，请核对登录、原分享及自动转存限制 [SHARE-EDIT/BD]"}
			}
		}
	}
	if op.baiduExpiryPassword == nil {
		return unknown("旧回执缺少原分享访问设置，请到官网核对")
	}
	detail, row, e := readBaiduManagedShare(ctx, client, a, h)
	if e != nil {
		return unknown("无法核对原分享当前状态")
	}
	if detail.Password != *op.baiduExpiryPassword {
		return unknown("原分享提取码或访问方式已变化")
	}
	if op.in.Days == 0 {
		if *row.Expiry != 1 || *row.Remaining != 0 {
			return unknown("永久期限尚未确认")
		}
	} else {
		if *row.Expiry != 0 || row.ExpiresAt == nil || *row.ExpiresAt <= time.Now().Unix() {
			return unknown("原分享未返回可核对的将来到期时间")
		}
		expected := op.created.Unix() + int64(op.in.Days)*86400
		delta := *row.ExpiresAt - expected
		if delta < -120 || delta > 120 {
			return unknown("原分享到期时间尚未符合目标")
		}
	}
	return ShareResult{State: "ready", URL: detail.URL, Password: detail.Password, Expiry: managedBaiduExpiry(row), Message: "已核对原分享链接、提取码和新期限。"}
}
