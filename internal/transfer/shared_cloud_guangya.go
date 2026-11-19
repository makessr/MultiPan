package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type managedGYRecord struct {
	Fields map[string]json.RawMessage `json:"-"`
	gyShareRecord
	Title   string `json:"title"`
	Created int64  `json:"createTime"`
}

func gyManagedName(row managedGYRecord) string {
	if strings.TrimSpace(row.Title) == "" {
		return "未命名分享"
	}
	return row.Title
}
func gyManagedRecords(ctx context.Context, client *http.Client, a Account, page int) ([]managedGYRecord, bool, error) {
	var data struct {
		Rows  *[]managedGYRecord `json:"list"`
		Total *int               `json:"total"`
	}
	_, e := (gyClient{client, a}).call(ctx, "/userres/v1/get_share_list", map[string]any{"page": page - 1, "pageSize": 50, "orderType": 1, "sortType": 1}, &data)
	if e != nil {
		return nil, false, e
	}
	if data.Rows == nil || len(*data.Rows) > 50 {
		return nil, false, errors.New("光鸭分享列表格式不完整 [SHARE-LIST/GY]")
	}
	more := len(*data.Rows) == 50
	if data.Total != nil {
		offset := (page - 1) * 50
		if *data.Total < 0 || len(*data.Rows) > 0 && *data.Total < offset+len(*data.Rows) || len(*data.Rows) < 50 && *data.Total > offset+len(*data.Rows) {
			return nil, false, errors.New("光鸭分享分页数量不一致，未确认结果")
		}
		more = offset+len(*data.Rows) < *data.Total
	}
	seen := map[string]bool{}
	for _, row := range *data.Rows {
		id := shareScalar(row.ID)
		if id == "" || id == "0" || seen[id] || len(row.Title) > 1024 {
			return nil, false, errors.New("光鸭原记录标识无效或重复")
		}
		seen[id] = true
		if row.URL != "" {
			if _, e := shareURL("guangya", row.URL); e != nil {
				return nil, false, errors.New("光鸭分享列表链接格式无效")
			}
		}
	}
	return *data.Rows, more, nil
}
func gyManagedExpiry(left *int64) string {
	if left == nil {
		return "有效期以网盘显示为准"
	}
	if *left == -1 {
		return "永久"
	}
	if *left <= 0 {
		return "已到期或期限异常"
	}
	if *left >= 86400 {
		return "剩余约 " + strconv.FormatInt(*left/86400, 10) + " 天"
	}
	if *left >= 3600 {
		return "剩余约 " + strconv.FormatInt(*left/3600, 10) + " 小时"
	}
	if *left >= 60 {
		return "剩余约 " + strconv.FormatInt(*left/60, 10) + " 分钟"
	}
	return "剩余不足1分钟"
}
func gyManagedShares(ctx context.Context, client *http.Client, a Account, page int) (ManagedSharePage, error) {
	rows, more, e := gyManagedRecords(ctx, client, a, page)
	if e != nil {
		return ManagedSharePage{}, e
	}
	result := ManagedSharePage{Items: []ManagedShare{}, Page: page, HasMore: more && page < 1000}
	for _, row := range rows {
		link := ""
		if row.URL != "" {
			link, _ = shareURL("guangya", row.URL)
		}
		h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: shareScalar(row.ID), Name: gyManagedName(row), URL: link, Kind: "guangya", Page: page, RawID: row.ID, PublicID: shareScalar(row.ShareID)}
		handle, e := sealManagedShare(h)
		if e != nil {
			return result, e
		}
		status := "有效性待核对"
		if row.Status != nil {
			status = map[int]string{0: "状态未知", 1: "网盘显示正常", 2: "已过期", 3: "已删除", 4: "已封禁"}[*row.Status]
			if status == "" {
				status = "状态未确认"
			}
		}
		result.Items = append(result.Items, ManagedShare{ID: handle, Name: h.Name, Status: status, Created: managedShareTime(row.Created), Expiry: gyManagedExpiry(row.Left), CanEdit: row.Status != nil && *row.Status == 1 && h.URL != "" && h.PublicID != "", CanRevoke: row.Status != nil && *row.Status == 1, SelectionKey: managedShareSelectionKey(h)})
	}
	return result, nil
}

func gyManagedShareLink(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (ManagedShareLink, error) {
	if h.Page < 1 || h.Page > 1000 {
		return ManagedShareLink{}, errors.New("光鸭原页码无效，请刷新列表")
	}
	rows, _, e := gyManagedRecords(ctx, client, a, h.Page)
	if e != nil {
		return ManagedShareLink{}, e
	}
	for _, row := range rows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		link, e := shareURL("guangya", row.URL)
		if e != nil || gyManagedName(row) != h.Name || h.URL != "" && link != h.URL {
			return ManagedShareLink{}, errors.New("光鸭原分享名称或链接已变化，请刷新重选")
		}
		password, accessErr := gyShareAccess(row.gyShareRecord)
		if accessErr != nil {
			return ManagedShareLink{}, errors.New("光鸭原分享访问设置未确认 [SHARE-GY/access] " + accessErr.Error())
		}
		if row.Status != nil && *row.Status >= 2 && *row.Status <= 4 {
			return ManagedShareLink{}, errors.New("光鸭原分享已过期、删除或受限")
		}
		if row.Status == nil || row.Left == nil {
			publicID := strings.TrimPrefix(link, "https://www.guangyapan.com/s/")
			var summary gyShareRecord
			code, e := (gyClient{client, a}).call(ctx, "/userres/v1/get_share_summary", map[string]string{"shareId": publicID}, &summary, 200, 201, 202)
			if e != nil || code == 200 || code == 201 || code == 202 || shareScalar(summary.ShareID) != publicID {
				return ManagedShareLink{}, errors.New("光鸭原分享摘要未能确认原标识或当前状态")
			}
			if summary.NeedCode != nil && *summary.NeedCode != (password != "") || row.Status != nil && summary.Status != nil && *row.Status != *summary.Status || row.Left != nil && summary.Left != nil && (*row.Left == -1) != (*summary.Left == -1) {
				return ManagedShareLink{}, errors.New("光鸭原分享列表和摘要状态不一致")
			}
			if row.Status == nil {
				row.Status = summary.Status
			}
			if row.Left == nil {
				row.Left = summary.Left
			}
		}
		if row.Status == nil || *row.Status != 1 || row.Left == nil || *row.Left != -1 && *row.Left <= 0 {
			return ManagedShareLink{}, errors.New("光鸭原分享当前状态未确认或已失效")
		}
		return ManagedShareLink{Name: h.Name, URL: link, Password: password, Expiry: gyManagedExpiry(row.Left), Message: "已读取同一原分享并核对当前状态，没有新建、修改或延长期限"}, nil
	}
	return ManagedShareLink{}, errors.New("光鸭原分享已不在原页，请刷新列表重选")
}

func gyObserveShare(ctx context.Context, client *http.Client, a Account, h managedShareHandle) (bool, error) {
	for page := 1; page <= 10; page++ {
		rows, more, e := gyManagedRecords(ctx, client, a, page)
		if e != nil {
			return false, e
		}
		for _, row := range rows {
			if shareScalar(row.ID) == h.ProviderID {
				if row.Status != nil && *row.Status == 3 {
					return false, nil
				} // Official UI explicitly labels this same-ID state deleted.
				link := ""
				if row.URL != "" {
					link, _ = shareURL("guangya", row.URL)
				}
				if gyManagedName(row) != h.Name || h.URL != "" && link != h.URL || row.Status == nil || *row.Status != 1 {
					return false, errors.New("光鸭原分享目标或可撤销状态已变化")
				}
				return true, nil
			}
		}
		if !more {
			return false, nil
		}
	}
	return false, errors.New("光鸭分享核对超过10页/500项，不能确认原结果")
}
