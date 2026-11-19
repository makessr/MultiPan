package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func revocationKind(h managedShareHandle) string {
	if h.Kind == "" {
		return "quark"
	}
	return h.Kind
}
func supportedRevocationKind(kind string) bool {
	return kind == "quark" || kind == "pan123" || kind == "baidu" || kind == "guangya"
}
func revocationAccountMatches(a Account, h managedShareHandle) bool {
	if a.ID == "" || a.ID != h.AccountID || a.Kind != revocationKind(h) || downloadScope(a) != h.Scope {
		return false
	}
	switch a.Kind {
	case "quark":
		return a.Endpoint == QuarkAPI
	case "pan123":
		return a.Endpoint == Pan123API && valid123Folder(h.ProviderID) && h.ProviderID != "0" && (!h.Paid123 || h.URL != "")
	case "baidu":
		return a.Endpoint == BaiduAPI && valid123Folder(h.ProviderID) && h.ProviderID != "0"
	case "guangya":
		return a.Endpoint == GuangyaAPI && shareToken.MatchString(h.ProviderID) && h.ProviderID != "0" && shareScalar(h.RawID) == h.ProviderID
	}
	return false
}
func revocationMatchesCreation(op *shareOperation, h managedShareHandle) bool {
	if op.scope != h.Scope || op.result.Kind != revocationKind(h) {
		return false
	}
	if h.Kind == "pan123" && op.in.Action == "settings" {
		prior, err := decodeManagedShareHandle(op.in.Handle)
		if err != nil || prior.Paid123 != h.Paid123 {
			return false
		}
		if h.Paid123 {
			return prior.ProviderID == h.ProviderID && prior.URL == h.URL
		}
	} else if h.Kind == "pan123" && (op.in.PaidPriceFen123 > 0 || h.Paid123) {
		return h.Paid123 && op.in.PaidPriceFen123 > 0 && op.saved.Share != "" && op.saved.Share == h.ProviderID
	}
	if op.saved.Share != "" && op.saved.Share == h.ProviderID {
		return true
	}
	if op.result.Kind == "pan123" && (op.saved.Key123 != "" && h.Key123 != "" && op.saved.Key123 == h.Key123 || op.saved.Result.URL != "" && h.URL != "" && op.saved.Result.URL == h.URL) {
		return true
	}
	if op.result.Kind == "guangya" && (op.saved.Share != "" && (op.saved.Share == h.PublicID || h.URL != "" && op.saved.Share == strings.TrimPrefix(h.URL, "https://www.guangyapan.com/s/")) || op.saved.Result.URL != "" && op.saved.Result.URL == h.URL) {
		return true
	}
	return false
}

func submitShareRevocation(ctx context.Context, client *http.Client, a Account, h managedShareHandle) error {
	if !revocationAccountMatches(a, h) {
		return errors.New("撤销账号来源已变化")
	}
	switch a.Kind {
	case "quark":
		_, e := newQuarkClient(client, a).request(ctx, "POST", "/share/delete", nil, map[string]any{"share_ids": []string{h.ProviderID}}, nil)
		return e
	case "pan123":
		paid := 0
		if h.Paid123 {
			paid = 1
		}
		return (pan123Client{client, a}).request(ctx, "POST", "/share/delete", nil, map[string]any{"driveId": 0, "shareInfoList": []map[string]json.RawMessage{{"shareId": json.RawMessage(h.ProviderID)}}, "isPayShare": paid, "event": "shareCancel", "operatePlace": 2}, nil)
	case "baidu":
		c := newBaiduClient(client, a)
		if e := c.session(ctx); e != nil {
			return e
		}
		return c.shareAPI(ctx, "POST", "cancel", nil, url.Values{"shareid_list": {"[" + h.ProviderID + "]"}}, nil)
	case "guangya":
		_, e := (gyClient{client, a}).call(ctx, "/userres/v1/delete_share", map[string]any{"ids": []json.RawMessage{h.RawID}}, nil)
		return e
	}
	return errors.New("此网盘尚未接入撤销")
}

func observeShareRevocation(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (bool, error) {
	if !revocationAccountMatches(a, h) {
		return false, errors.New("撤销账号来源已变化")
	}
	switch a.Kind {
	case "guangya":
		return gyObserveShare(ctx, client, a, h)
	case "quark":
		return quarkObserveShare(ctx, client, a, h)
	case "pan123":
		cursor := "0"
		seen := map[string]bool{"0": true}
		seenPaidIDs := map[string]bool{}
		for page := 0; page < 10; page++ {
			rows, next, e := pan123ManagedRecordsByType(ctx, client, a, cursor, h.Paid123)
			if e != nil {
				return false, e
			}
			for _, row := range rows {
				id := shareScalar(row.ID)
				if h.Paid123 {
					if seenPaidIDs[id] {
						return false, errors.New("123 付费分享核对遇到跨页重复记录")
					}
					seenPaidIDs[id] = true
				}
				if id == h.ProviderID {
					if h.Paid123 {
						if managed123Name(row) != h.Name || managed123ListedLink(row) != h.URL || shareScalar(row.Key) != h.Key123 {
							return false, errors.New("原123付费分享名称、链接或标识已变化")
						}
						return true, nil
					}
					if row.Paid != nil && *row.Paid != 0 || managed123Name(row) != h.Name || h.URL != "" && managed123Link(row) != h.URL {
						return false, errors.New("原123分享名称或链接已变化")
					}
					return true, nil
				}
			}
			if next == "" {
				return false, nil
			}
			if seen[next] {
				return false, errors.New("原123分页发生循环，不能确认撤销结果")
			}
			seen[next] = true
			cursor = next
		}
	case "baidu":
		c := newBaiduClient(client, a)
		if e := c.session(ctx); e != nil {
			return false, e
		}
		for page := 1; page <= 5; page++ {
			rows, e := baiduManagedRecords(ctx, c, page)
			if e != nil {
				return false, e
			}
			for _, row := range rows {
				if shareScalar(row.ID) == h.ProviderID {
					link := ""
					if row.Link != "" {
						link, _ = resolveBaiduShareURL(row.Link)
					}
					if managedBaiduName(row) != h.Name || h.URL != "" && h.URL != link {
						return false, errors.New("原百度分享名称或链接已变化")
					}
					return true, nil
				}
			}
			if len(rows) == 0 {
				return false, nil
			}
		}
	}
	return false, errors.New("原分享检查超过10页/500项，不能确认撤销结果")
}
