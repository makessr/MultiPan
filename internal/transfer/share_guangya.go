package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type gyShareRecord struct {
	ID       json.RawMessage `json:"id"`
	ShareID  json.RawMessage `json:"shareId"`
	URL      string          `json:"shareUrl"`
	Code     *string         `json:"code"`
	Type     *int            `json:"shareType"`
	Duration *int64          `json:"validateDuration"`
	Status   *int            `json:"shareStatus"`
	Left     *int64          `json:"leftTime"`
	NeedCode *bool           `json:"needCode"`
}

func shareGuangya(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	c := gyClient{client, a}
	unknown := func(message string) ShareResult { return ShareResult{State: "unknown", Message: message} }
	ready := func(record gyShareRecord, checked bool) ShareResult {
		if record.Type != nil && *record.Type == 1 {
			if _, e := gyShareAccess(record); e != nil {
				return unknown("光鸭随机提取码设置未能核实 [SHARE-GY/access] " + e.Error())
			}
		}
		mode := 0
		if op.in.Password != "" {
			mode = 2
		}
		if record.Type != nil && *record.Type != mode && !(mode == 2 && *record.Type == 1) {
			return unknown("光鸭分享访问模式与申请不一致，未展示链接 [SHARE-GY/mode]")
		}
		code := ""
		if record.Code != nil {
			code = *record.Code
		}
		if code != op.in.Password && (checked || record.Code != nil) || checked && op.in.Password != "" && record.Code == nil {
			return unknown("光鸭原分享提取码未确认或与申请不同，请到官网核对 [SHARE-GY/password]")
		}
		if checked {
			// validateDuration describes the original request and may be omitted,
			// including for valid permanent shares. It is not required to establish
			// current availability: the official share page uses status/leftTime.
			if record.Status != nil && *record.Status >= 2 && *record.Status <= 4 {
				return ShareResult{State: "failed", Message: "光鸭原分享已过期、删除或受限，未重新创建"}
			}
			if record.Status == nil || record.Left == nil {
				var summary gyShareRecord
				// The public page takes its identifier from /s/:shareId. The
				// authenticated record's id can instead be an internal numeric ID.
				summaryID := op.share
				if link, err := shareURL("guangya", record.URL); err == nil {
					summaryID = strings.TrimPrefix(link, "https://www.guangyapan.com/s/")
				}
				code, err := c.call(ctx, "/userres/v1/get_share_summary", map[string]string{"shareId": summaryID}, &summary, 200, 201, 202)
				if err != nil {
					return unknown("光鸭原分享摘要未读取完成，请稍后检查原结果，不重新创建 [SHARE-GY/summary]")
				}
				if code == 200 || code == 201 || code == 202 {
					return ShareResult{State: "failed", Message: "光鸭报告原分享已失效或过期，未重新创建"}
				}
				if shareScalar(summary.ShareID) != summaryID {
					return unknown("光鸭原分享摘要标识无法确认，请到官网核对 [SHARE-GY/summary-id]")
				}
				if summary.NeedCode != nil && *summary.NeedCode != (op.in.Password != "") {
					return unknown("光鸭原分享摘要访问模式与申请不一致，未展示链接 [SHARE-GY/summary-mode]")
				}
				if summary.Status != nil && record.Status != nil && *summary.Status != *record.Status || summary.Left != nil && record.Left != nil && (*summary.Left == -1) != (*record.Left == -1) {
					return unknown("光鸭原分享列表与摘要状态不一致，请稍后检查原结果 [SHARE-GY/summary-state]")
				}
				if record.Status == nil {
					record.Status = summary.Status
				}
				if record.Left == nil {
					record.Left = summary.Left
				}
			}
			if record.Status == nil || record.Left == nil {
				return unknown("光鸭原分享状态仍无法确认，请到官网核对 [SHARE-GY/settings:status,leftTime]")
			}
			if *record.Status >= 2 && *record.Status <= 4 || *record.Left == 0 || *record.Left < -1 {
				return ShareResult{State: "failed", Message: "光鸭原分享已过期、删除或受限，未重新创建"}
			}
			if *record.Status != 1 {
				return unknown("光鸭原分享尚未处于正常状态，请到官网核对 [SHARE-GY/status]")
			}
			if record.Duration != nil && *record.Duration != int64(op.in.Days)*86400 || (*record.Left == -1) != (op.in.Days == 0) {
				return unknown("光鸭原分享有效期与申请不一致，请到官网核对；不重新创建 [SHARE-GY/expiry]")
			}
		}
		link, e := shareURL("guangya", record.URL)
		if e != nil {
			return unknown("光鸭原分享链接格式未确认，请检查原操作结果或到官网核对 [SHARE-GY/url]")
		}
		message := "分享链接已取得；提取码及有效期按本次申请提交，实际生效状态以光鸭显示为准。"
		if checked {
			message = "已读取原分享并核对提取码及未过期状态；显示的有效期为本次申请，实际到期时间以光鸭为准，不会重新创建或延长原分享。"
		}
		return ShareResult{State: "ready", URL: link, Password: op.in.Password, Message: message}
	}
	if !checkOnly {
		mode := 0
		if op.in.Password != "" {
			mode = 2
		}
		var record gyShareRecord
		ids := []string{}
		for _, target := range shareTargets(op.in) {
			ids = append(ids, target.ID)
		}
		_, e := c.call(ctx, "/userres/v1/share_file", map[string]any{"fileIds": ids, "title": op.in.Name, "validateDuration": int64(op.in.Days) * 86400, "shareType": mode, "code": op.in.Password, "autoFillCode": false, "downloadType": 1, "maxRestoreCount": 0, "trafficLimit": "0"}, &record)
		if e != nil {
			return unknown("光鸭创建结果未确认，可能已创建；请到官网我的分享核对，不要重复创建 [SHARE-GY/create]")
		}
		op.share = shareScalar(record.ShareID)
		if op.share == "" {
			op.share = shareScalar(record.ID)
		}
		if link, e := shareURL("guangya", record.URL); e == nil && op.share == "" {
			op.share = strings.TrimPrefix(strings.TrimPrefix(link, "https://www.guangyapan.com"), "/s/")
		}
		result := ready(record, false)
		if op.saveCheckpoint() != nil {
			return shareStorageUnknown()
		}
		if result.State == "ready" || op.share == "" {
			return result
		}
	}
	if op.share == "" {
		return unknown("缺少光鸭原分享标识，请到官网核对，不重新创建 [SHARE-GY/identity]")
	}
	for page := 0; page < 5; page++ {
		var response struct {
			List  *[]gyShareRecord `json:"list"`
			Total *int             `json:"total"`
		}
		if _, e := c.call(ctx, "/userres/v1/get_share_list", map[string]any{"page": page, "pageSize": 100, "orderType": 1, "sortType": 1}, &response); e != nil || response.List == nil {
			return unknown("光鸭原分享列表未读取完成，请稍后检查或到官网核对 [SHARE-GY/list]")
		}
		var match *gyShareRecord
		for i := range *response.List {
			r := &(*response.List)[i]
			id := shareScalar(r.ID)
			if id == "" {
				id = shareScalar(r.ShareID)
			}
			if id == op.share || shareScalar(r.ShareID) == op.share {
				if match != nil {
					return unknown("光鸭原分享标识重复，请到官网核对 [SHARE-GY/duplicate]")
				}
				match = r
			}
		}
		if match != nil {
			return ready(*match, true)
		}
		if len(*response.List) < 100 || response.Total != nil && (page+1)*100 >= *response.Total {
			break
		}
	}
	return unknown("未在有限范围内找到光鸭原分享，可能未同步或已取消；请到官网核对，不重新创建 [SHARE-GY/not-found]")
}
