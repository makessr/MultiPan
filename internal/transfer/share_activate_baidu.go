package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const baiduShareFrozen = 19

func baiduActivationDeadline(row managedBaiduRecord, now time.Time) string {
	if row.Expiry == nil || row.Remaining == nil || row.Tag != nil && *row.Tag == 7 {
		return ""
	}
	if *row.Expiry == 1 && *row.Remaining == 0 {
		return "-1"
	}
	if *row.Expiry == 0 && *row.Remaining > 0 && row.ExpiresAt != nil &&
		*row.ExpiresAt > now.Unix() && *row.ExpiresAt <= 253402300799 {
		return strconv.FormatInt(*row.ExpiresAt, 10)
	}
	return ""
}

func baiduFrozenShareMatches(h managedShareHandle, row managedBaiduRecord) bool {
	return row.Status != nil && *row.Status == baiduShareFrozen &&
		row.Path == h.PathBaidu && baiduActivationDeadline(row, time.Now()) == h.ActivationBaidu
}

func validateBaiduActivationSources(ctx context.Context, client *http.Client, a Account, sources []managedShareHandle) error {
	c := newBaiduClient(client, a)
	if err := c.session(ctx); err != nil {
		return errors.New("无法读取百度原分享状态")
	}
	pages := make(map[int][]managedBaiduRecord)
	for _, h := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if h.Page < 1 || h.Page > 1000 {
			return errors.New("百度原页码无效，请刷新列表")
		}
		rows, found := pages[h.Page]
		if !found {
			var err error
			rows, err = baiduManagedRecords(ctx, c, h.Page)
			if err != nil {
				return fmt.Errorf("无法核对百度分享第 %d 页", h.Page)
			}
			pages[h.Page] = rows
		}
		if _, row, err := readBaiduManagedShareFromPage(ctx, c, h, rows); err != nil || !baiduFrozenShareMatches(h, row) {
			return errors.New(h.Name + "：原分享状态、链接或有效期无法确认")
		}
	}
	return nil
}

func shareBaiduActivation(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	unknown := func(reason string) ShareResult {
		return ShareResult{State: "unknown", Message: reason + "；请只读检查原回执 [SHARE-ACTIVATE/BD]"}
	}
	h, err := decodeManagedShareHandle(op.in.Handle)
	if err != nil || h.Kind != "baidu" || h.AccountID != a.ID || h.Scope != downloadScope(a) ||
		h.URL == "" || h.ActivationBaidu == "" || !validBaiduPath(h.PathBaidu) || h.PathBaidu == "/" {
		return unknown("原账号或分享标识无法确认")
	}
	if !checkOnly {
		link, row, err := readBaiduManagedShare(ctx, client, a, h)
		if err != nil || !baiduFrozenShareMatches(h, row) {
			return ShareResult{State: "failed", Message: "原分享状态、链接或有效期无法确认，未提交激活"}
		}
		op.baiduExpiryPassword = &link.Password
		if op.settingsCheckpoint != nil {
			if err = op.settingsCheckpoint(h); err != nil {
				op.baiduExpiryPassword = nil
				return unknown("本机回执保存失败，未发送激活请求")
			}
		}
		// The web action POSTs the share ID list and target status as query parameters.
		_ = newBaiduClient(client, a).shareAPI(ctx, http.MethodPost, "resetstatus",
			url.Values{"sid_list": {"[" + h.ProviderID + "]"}, "status": {"0"}}, nil, nil)
	}
	if op.baiduExpiryPassword == nil {
		return unknown("回执缺少原分享访问设置")
	}
	link, row, err := readBaiduManagedShare(ctx, client, a, h)
	if err != nil || row.Status == nil || *row.Status != 0 || row.Path != h.PathBaidu ||
		baiduActivationDeadline(row, time.Now()) != h.ActivationBaidu || link.Password != *op.baiduExpiryPassword {
		return unknown("原分享的激活状态、链接、期限或提取码尚未核对")
	}
	return ShareResult{State: "ready", URL: link.URL, Password: link.Password, Expiry: link.Expiry, Message: "已核对同一分享恢复为有效状态，原链接、提取码和有效期保留"}
}
