package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type share123Record struct {
	ID         json.RawMessage   `json:"ShareId"`
	Key        json.RawMessage   `json:"ShareKey"`
	Password   *string           `json:"SharePwd"`
	Expiration string            `json:"Expiration"`
	Violations []json.RawMessage `json:"ViolateFileIdList"`
	Links      struct {
		List []string `json:"list"`
	} `json:"shareLinkList"`
}

func share123Expiry(days int, now time.Time) time.Time {
	zone := time.FixedZone("CST", 8*3600)
	if days == 0 {
		return time.Date(2099, 12, 12, 8, 0, 0, 0, zone)
	}
	return now.In(zone).AddDate(0, 0, days)
}

func share123Link(record share123Record) string {
	for _, raw := range record.Links.List {
		if validated, e := shareURL("pan123", raw); e == nil {
			return validated
		}
	}
	if key := shareScalar(record.Key); key != "" {
		validated, _ := shareURL("pan123", "https://www.123pan.com/s/"+key)
		return validated
	}
	return ""
}

func share123(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	c := pan123Client{client, a}
	unknown := func(message string) ShareResult { return ShareResult{State: "unknown", Message: message} }
	ready := func(record share123Record, checked bool) ShareResult {
		if record.Password != nil && *record.Password != op.in.Password {
			return unknown("原分享提取码与申请不一致，未展示链接；请到官网核对 [SHARE-123/password]")
		}
		if checked && record.Password == nil {
			return unknown("原分享记录未返回访问设置，未展示链接；请到官网核对 [SHARE-123/settings]")
		}
		if len(record.Violations) > 0 {
			return ShareResult{State: "failed", Message: "123 报告分享项目受限，请到官网核对；未重新创建"}
		}
		if checked {
			deadline, e := time.Parse(time.RFC3339, record.Expiration)
			if e != nil {
				deadline, e = time.ParseInLocation("2006-01-02 15:04:05", record.Expiration, time.FixedZone("CST", 8*3600))
			}
			if e != nil {
				return unknown("原分享有效期无法确认，请到官网核对 [SHARE-123/expiry]")
			}
			if !deadline.After(time.Now()) {
				return ShareResult{State: "failed", Message: "123 原分享已过期，未展示链接；没有重新创建"}
			}
		}
		link := share123Link(record)
		if link == "" {
			return unknown("原分享链接未读取完成，请检查原操作结果，不要重复创建 [SHARE-123/link]")
		}
		message := "分享链接已取得；提取码及有效期按本次申请提交，最终以网盘显示为准。永久按123网页规则设至2099年。"
		if checked {
			message = "已读取原分享记录并核对提取码及未过期状态；显示的申请有效期不代表重新延长，实际到期时间以网盘为准。"
		}
		return ShareResult{State: "ready", URL: link, Password: op.in.Password, Message: message}
	}
	if !checkOnly {
		var record share123Record
		ids := []string{}
		for _, target := range shareTargets(op.in) {
			ids = append(ids, target.ID)
		}
		mode := map[int]int{1: 1, 7: 2, 30: 3, 0: 4}[op.in.Days]
		err := c.request(ctx, "POST", "/share/create", nil, map[string]any{
			"driveId": 0, "expiration": share123Expiry(op.in.Days, time.Now()).Format(time.RFC3339),
			"fileIdList": strings.Join(ids, ","), "shareName": op.in.Name, "sharePwd": op.in.Password,
			"event": "shareCreate", "fileNum": len(ids), "renameVisible": false, "shareModality": mode, "operatePlace": 1, "fillPwdSwitch": 0,
		}, &record)
		if err != nil {
			return unknown("123 创建结果未确认，可能已创建；请到官网分享管理核对，不要重复创建 [SHARE-123/create]")
		}
		op.share, op.key123 = shareScalar(record.ID), shareScalar(record.Key)
		if op.saveCheckpoint() != nil {
			return shareStorageUnknown()
		}
		result := ready(record, false)
		if result.State != "unknown" || op.share == "" && op.key123 == "" {
			return result
		}
	}
	if op.share == "" && op.key123 == "" {
		return unknown("缺少原分享标识，请到官网核对，不重新创建 [SHARE-123/identity]")
	}
	next := "0"
	seen := map[string]bool{}
	for page := 0; page < 5; page++ {
		var list struct {
			Entries *[]share123Record `json:"InfoList"`
			Next    json.RawMessage   `json:"Next"`
		}
		if e := c.request(ctx, "GET", "/share/list", url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {next}, "orderBy": {"share_id"}, "orderDirection": {"desc"}, "SearchData": {""}, "event": {"shareListFile"}}, nil, &list); e != nil || list.Entries == nil {
			return unknown("原分享记录未读取完成，请稍后检查原操作结果；不重新创建 [SHARE-123/list]")
		}
		var match *share123Record
		for i := range *list.Entries {
			r := &(*list.Entries)[i]
			matches := op.share != "" && shareScalar(r.ID) == op.share || op.share == "" && op.key123 != "" && shareScalar(r.Key) == op.key123
			if matches {
				if match != nil {
					return unknown("原分享记录出现重复标识，请到官网核对 [SHARE-123/duplicate]")
				}
				match = r
			}
		}
		if match != nil {
			return ready(*match, true)
		}
		cursor := shareScalar(list.Next)
		if cursor == "" || cursor == "0" || cursor == next || seen[cursor] || len(*list.Entries) == 0 {
			break
		}
		seen[cursor] = true
		next = cursor
	}
	return unknown("未在有限范围内找到原分享标识；可能未同步、已取消或超出检查范围，请到官网核对，不重新创建 [SHARE-123/not-found]")
}
