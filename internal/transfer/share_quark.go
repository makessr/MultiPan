package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type quarkShareCreationSnapshot struct {
	Success *int `json:"success_count"`
	Failed  *int `json:"failed_count"`
}

func (s quarkShareCreationSnapshot) conflicts(count int) bool {
	return s.Failed != nil && *s.Failed != 0 || s.Success != nil && *s.Success != count
}

func shareQuark(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	c := newQuarkClient(client, a)
	unknown := func(message string) ShareResult { return ShareResult{State: "unknown", Message: message} }
	gap := 800 * time.Millisecond
	targets := shareTargets(op.in)
	grouped := len(targets) > 1
	wantExpiry := map[int]int{0: 1, 1: 2, 7: 3, 30: 4}[op.in.Days]
	if !checkOnly {
		ids := make([]string, 0, len(targets))
		for _, target := range targets {
			ids = append(ids, target.ID)
		}
		mode := 1
		if op.in.Password != "" {
			mode = 2
		}
		var response struct {
			Task string `json:"task_id"`
		}
		metadata, e := c.request(ctx, "POST", "/share", nil, map[string]any{"fid_list": ids, "title": op.in.Name, "url_type": mode, "passcode": op.in.Password, "expired_type": wantExpiry, "support_error_code": []string{"41060"}}, &response)
		if e != nil || !shareToken.MatchString(response.Task) {
			return unknown("夸克创建分享结果未确认，可能已创建；请到官网分享管理核对，不要重复创建 [SHARE-QK/create]")
		}
		op.task = response.Task
		if op.saveCheckpoint() != nil {
			return shareStorageUnknown()
		}
		var hints struct {
			Gap int `json:"tq_gap"`
		}
		if json.Unmarshal(metadata, &hints) == nil && hints.Gap > 0 {
			gap = time.Duration(hints.Gap) * time.Millisecond
			if gap < 50*time.Millisecond {
				gap = 50 * time.Millisecond
			}
			if gap > 3*time.Second {
				gap = 3 * time.Second
			}
		}
	}
	scopeVerified := op.quarkGroupVerified
	if op.share == "" || grouped && !scopeVerified {
		if op.task == "" {
			return unknown("没有可用于检查的原任务标识，请到官网分享管理核对，不重新创建")
		}
		taskFinished := false
		for n := 0; n < 10; n++ {
			var task struct {
				Status   int                        `json:"status"`
				Share    json.RawMessage            `json:"share_id"`
				Snapshot quarkShareCreationSnapshot `json:"creation_snapshot"`
			}
			if _, e := c.request(ctx, "GET", "/task", url.Values{"task_id": {op.task}, "retry_index": {strconv.Itoa(n)}}, nil, &task); e != nil {
				return unknown("无法确认原分享任务，请稍后检查结果，不要重复创建 [SHARE-QK/task]")
			}
			if task.Status == 2 {
				id := shareScalar(task.Share)
				if id == "" || op.share != "" && op.share != id {
					return unknown("原任务未返回一致的分享标识，请到官网核对")
				}
				op.share = id
				if op.saveCheckpoint() != nil {
					return shareStorageUnknown()
				}
				if grouped && task.Snapshot.conflicts(len(targets)) {
					return unknown("夸克返回的合并范围不完整，未展示链接，请到官网核对")
				}
				scopeVerified = task.Snapshot.Success != nil
				taskFinished = true
				break
			}
			if task.Status == 3 {
				if grouped && (task.Snapshot.Success == nil || *task.Snapshot.Success > 0) {
					return unknown("夸克未确认合并分享完整创建，请到官网核对原任务")
				}
				return ShareResult{State: "failed", Message: "夸克报告分享任务失败，请到官网核对文件限制或审核状态；没有自动重试"}
			}
			if n < 9 {
				timer := time.NewTimer(gap)
				select {
				case <-ctx.Done():
					timer.Stop()
					return unknown("原分享任务仍未确认，请稍后检查结果，不要重复创建")
				case <-timer.C:
				}
			}
		}
		if !taskFinished {
			return unknown("原分享任务尚未返回可用链接，请稍后检查结果，不要重复创建 [SHARE-QK/pending]")
		}
	}
	var link struct {
		URL        string                     `json:"share_url"`
		Key        json.RawMessage            `json:"pwd_id"`
		Password   *string                    `json:"passcode"`
		URLType    json.RawMessage            `json:"url_type"`
		Status     int                        `json:"status"`
		Audit      int                        `json:"audit_status"`
		ExpiryType int                        `json:"expired_type"`
		ExpiresAt  int64                      `json:"expired_at"`
		FileCount  *int                       `json:"all_file_num"`
		Restricted *int                       `json:"violation_cnt"`
		Snapshot   quarkShareCreationSnapshot `json:"creation_snapshot"`
	}
	if _, e := c.request(ctx, "POST", "/share/password", nil, map[string]string{"share_id": op.share}, &link); e != nil {
		return unknown("原分享已返回标识，但链接未读取完成；请检查原操作结果 [SHARE-QK/link]")
	}
	if link.Status == 3 || link.Audit == 2 || link.Audit == 3 {
		return ShareResult{State: "failed", Message: "夸克分享已失效或审核未通过，请到官网核对；没有重新创建"}
	}
	detailHasExactCount := link.FileCount != nil && *link.FileCount == len(targets) && link.Restricted != nil && *link.Restricted == 0
	if grouped && (link.Snapshot.conflicts(len(targets)) || link.Restricted != nil && *link.Restricted != 0 ||
		!scopeVerified && link.Snapshot.Success == nil && !detailHasExactCount) {
		return unknown("原分享的项目数无法核对，未展示合并链接，请到官网核对")
	}
	if link.ExpiresAt > 0 && link.ExpiryType != 1 {
		deadline := time.Unix(link.ExpiresAt, 0)
		if link.ExpiresAt >= 1000000000000 {
			deadline = time.UnixMilli(link.ExpiresAt)
		}
		if !deadline.After(time.Now()) {
			return ShareResult{State: "failed", Message: "夸克返回的原分享已经到期，未展示过期链接；请到官网核对"}
		}
	}
	if grouped && link.ExpiryType != 0 && link.ExpiryType != wantExpiry {
		return unknown("原分享有效期与申请不一致，未展示合并链接")
	}
	// Public shares may omit passcode or return null. This is not a missing
	// verification value when the successful original request used url_type=1.
	// Protected requests still require the returned code, and explicit mode
	// information must never contradict the requested protection.
	password := ""
	if link.Password != nil {
		password = *link.Password
	}
	wantedMode := "1"
	if op.in.Password != "" {
		wantedMode = "2"
	}
	if mode := strings.Trim(string(link.URLType), "\""); mode != "" && mode != "null" && mode != wantedMode {
		return unknown("分享访问模式与设定不一致，未展示链接；请检查原操作结果或到官网核对 [SHARE-QK/mode]")
	}
	if password != op.in.Password || op.in.Password != "" && link.Password == nil {
		return unknown("无法确认分享提取码与设定一致，未展示链接；请在官网核对，不重新创建 [SHARE-QK/password]")
	}
	raw := link.URL
	if raw == "" {
		if key := shareScalar(link.Key); key != "" {
			raw = "https://pan.quark.cn/s/" + key
		}
	}
	validated, e := shareURL("quark", raw)
	if e != nil {
		return unknown("原分享链接格式无法确认，请检查原操作结果或到官网核对 [SHARE-QK/url]")
	}
	if grouped {
		op.quarkGroupVerified = true
	}
	expiry := ""
	if link.ExpiryType == 1 && link.ExpiresAt <= 0 {
		expiry = "永久"
	} else if link.ExpiresAt > 0 {
		expiry = "到期：" + managedShareTime(link.ExpiresAt)
	}
	return ShareResult{State: "ready", URL: validated, Password: password, Expiry: expiry, Message: "已读取原分享链接"}
}
