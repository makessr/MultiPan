package transfer

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const pan123PaidShareCreate = "/restful/goapi/v1/share/content/payment/create"

func share123Paid(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	in := op.in
	if !checkOnly {
		var response struct {
			share123Record
			Data share123Record `json:"data"`
		}
		body := map[string]any{
			"driveId": 0, "expiration": share123Expiry(0, op.created).Format(time.RFC3339),
			"fileIdList": in.EntryID, "fileNum": 1, "shareName": in.Name, "sharePwd": "",
			"event": "shareCreate", "renameVisible": false, "shareModality": 4, "operatePlace": 1,
			"trafficSwitch": 1, "trafficLimitSwitch": 1, "trafficLimit": 0,
			"isPayShare": true, "payAmount": in.PaidPriceFen123, "resourceDesc": in.PaidDescription123,
		}
		if err := (pan123Client{client, a}).request(ctx, http.MethodPost, pan123PaidShareCreate, nil, body, &response); err != nil {
			var business *pan123BusinessError
			if errors.As(err, &business) {
				switch business.code {
				case 7013, 11076:
					return ShareResult{State: "failed", Message: "123 要求先在官网完成付费分享认证"}
				case 11000, 5002:
					return ShareResult{State: "failed", Message: "123 要求在官网完成本次验证后提交"}
				}
			}
			return ShareResult{State: "unknown", Message: "付费分享提交结果待确认，请先到官网的「付费分享」核对"}
		}
		if len(response.ID) == 0 && len(response.Key) == 0 {
			response.share123Record = response.Data
		}
		op.share, op.key123 = shareScalar(response.ID), shareScalar(response.Key)
		if len(response.Violations) > 0 || len(response.Data.Violations) > 0 {
			return ShareResult{State: "failed", Message: "123 报告分享内容受限，请到官网核对"}
		}
		if err := op.saveCheckpoint(); err != nil {
			return shareStorageUnknown()
		}
		if op.share == "" && op.key123 == "" {
			return ShareResult{State: "unknown", Message: "已提交审核，网盘未返回可核对的分享标识，请到官网的「付费分享」查看审核进度"}
		}
		return ShareResult{State: "unknown", Message: "已提交审核，可检查原结果查看发布状态"}
	}
	if op.share == "" && op.key123 == "" {
		return ShareResult{State: "unknown", Message: "缺少原付费分享标识，请到官网核对原申请"}
	}
	cursor := "0"
	seen := map[string]bool{cursor: true}
	for page := 0; page < 10; page++ {
		rows, next, err := pan123ManagedRecordsByType(ctx, client, a, cursor, true)
		if err != nil {
			break
		}
		for _, row := range rows {
			id, key := shareScalar(row.ID), shareScalar(row.Key)
			if op.share != "" && id != op.share || op.share == "" && key != op.key123 {
				continue
			}
			if !valid123Folder(id) || id == "0" {
				break
			}
			result := paid123PublishedResult(in, op.key123, row)
			if result.State == "ready" {
				op.share = id
			}
			return result
		}
		if next == "" || seen[next] {
			break
		}
		seen[next] = true
		cursor = next
	}
	return ShareResult{State: "unknown", Message: "尚未核对到原付费分享，请在「付费分享」查看审核进度"}
}

func paid123PublishedResult(in ShareInput, originalKey string, row managed123Record) ShareResult {
	price := managed123Amount(row.PriceFen)
	if row.Paid == nil || *row.Paid != 1 || managed123Name(row) != in.Name ||
		originalKey != "" && shareScalar(row.Key) != originalKey || price == nil || *price != int64(in.PaidPriceFen123) {
		return ShareResult{State: "unknown", Message: "原付费分享身份、主题或价格无法核对"}
	}
	if row.PaidAudit == nil || *row.PaidAudit != 1 {
		return ShareResult{State: "unknown", Message: "审核尚未显示通过，请到官网查看进度"}
	}
	link := managed123ListedLink(row)
	expiry, err := managed123Deadline(row.Expiration)
	expired, stateErr := managed123Expired(row.Expired)
	if link == "" || err != nil || stateErr != nil || expired || !expiry.After(time.Now()) ||
		len(row.Violations) != 0 || row.Password != nil && *row.Password != "" {
		return ShareResult{State: "unknown", Message: "原付费分享的链接或访问状态无法核对"}
	}
	return ShareResult{State: "ready", URL: link, Expiry: expiry.Format(time.RFC3339Nano), Message: "原付费分享已通过审核，链接已核对"}
}
