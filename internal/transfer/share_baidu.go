package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

func shareBaidu(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	c := newBaiduClient(client, a)
	unknown := func(message string) ShareResult { return ShareResult{State: "unknown", Message: message} }
	if e := c.session(ctx); e != nil {
		if checkOnly {
			return unknown("无法读取百度登录状态，请到官网核对原分享；不重新创建 [SHARE-BD/session]")
		}
		return ShareResult{State: "failed", Message: "无法读取百度登录状态，请检查登录及网络；未提交分享 [SHARE-BD/session]"}
	}
	entries, e := c.list(ctx, op.in.ParentID)
	if e != nil {
		return unknown("无法核对百度分享源，请刷新后到官网核对；未提交新的分享 [SHARE-BD/source]")
	}
	sources := []string{}
	pathsList := []string{}
	for _, target := range shareTargets(op.in) {
		source := ""
		count := 0
		for _, entry := range entries {
			if entry.Path == target.ID && entry.Name == target.Name && entry.Dir != nil && (*entry.Dir == 1) == target.Folder {
				source = string(entry.ID)
				count++
			}
		}
		if count != 1 || !valid123Folder(source) || source == "0" {
			return unknown("百度源项目已变化或无法确认，未提交新的分享 [SHARE-BD/source-id]")
		}
		sources = append(sources, source)
		pathsList = append(pathsList, target.ID)
	}
	source := baiduShareSources(sources)
	if source == "" || checkOnly && source != op.baiduSource {
		return unknown("百度原分享项目集合已变化，未提交新的分享 [SHARE-BD/source-id]")
	}
	if !checkOnly {
		op.baiduSource = source
		if op.saveCheckpoint() != nil {
			return shareStorageUnknown()
		}
		paths, _ := json.Marshal(pathsList)
		var result struct {
			ID       json.RawMessage `json:"shareid"`
			Link     string          `json:"link"`
			Password *string         `json:"pwd"`
		}
		e = c.shareAPI(ctx, "POST", "pset", nil, url.Values{"path_list": {string(paths)}, "schannel": {"4"}, "channel_list": {"[]"}, "period": {strconv.Itoa(op.in.Days)}, "pwd": {op.in.Password}, "share_type": {"9"}}, &result)
		if e != nil {
			return unknown("百度创建分享结果未确认，可能已创建；请到官网我的分享核对，不要重复创建 [SHARE-BD/create]")
		}
		op.share = shareScalar(result.ID)
		if !valid123Folder(op.share) || op.share == "0" {
			op.share = ""
		}
		if op.share != "" {
			op.baiduURL, _ = resolveBaiduShareURL(result.Link)
		}
		if op.saveCheckpoint() != nil {
			return shareStorageUnknown()
		}
		// A repeated share may return its existing access code. Resolve the exact
		// original record before using that code; never rewrite or recreate it.
		if link, err := resolveBaiduShareURL(result.Link); len(sources) == 1 && err == nil && result.Password != nil && *result.Password == op.in.Password {
			return ShareResult{State: "ready", URL: link, Password: op.in.Password, Message: "分享链接已取得；提取码及有效期按本次申请提交，实际访问及到期时间请以百度显示为准。"}
		}
	}
	if op.share == "" {
		return unknown("没有可检查的百度原分享标识，请到官网核对，不重新创建 [SHARE-BD/identity]")
	}
	for page := 1; page <= 5; page++ {
		records, err := baiduManagedRecords(ctx, c, page)
		if err != nil {
			return unknown("百度原分享列表未读取完成，请稍后检查或到官网核对 [SHARE-BD/record]")
		}
		matched := -1
		for i, r := range records {
			if shareScalar(r.ID) == op.share {
				matched = i
				break
			}
		}
		if matched >= 0 {
			r := records[matched]
			recordSources := []string{}
			for _, raw := range r.Sources {
				recordSources = append(recordSources, shareScalar(raw))
			}
			if baiduShareSources(recordSources) != op.baiduSource {
				return unknown("百度原分享包含的项目与申请不一致，未展示链接 [SHARE-BD/scope]")
			}
			if r.Expiry == nil || r.Remaining == nil {
				return unknown("百度原分享状态未返回完整，请到官网核对 [SHARE-BD/settings]")
			}
			if *r.Expiry == -1 || *r.Remaining < 0 ||
				*r.Expiry == 0 && r.ExpiresAt != nil && *r.ExpiresAt > 0 && *r.ExpiresAt <= time.Now().Unix() {
				return ShareResult{State: "failed", Message: "百度原分享已失效或过期；未重新创建"}
			}
			if r.Status != nil && *r.Status == baiduShareFrozen {
				return unknown("百度原分享已被冻结，请在云端分享中核对激活条件")
			}
			if r.Public != nil && *r.Public != 0 || r.Channel != nil && *r.Channel != 4 {
				return unknown("百度原分享访问模式与提取码分享不一致，未展示链接 [SHARE-BD/mode]")
			}
			var detail struct {
				Password *string `json:"pwd"`
				URL      string  `json:"shorturl"`
			}
			// This digest is a provider request parameter, not a file integrity check.
			sum := md5.Sum([]byte(op.share + "_sharesurlinfo!@#"))
			if e := c.shareAPI(ctx, "GET", "surlinfoinrecord", url.Values{"shareid": {op.share}, "sign": {hex.EncodeToString(sum[:])}}, nil, &detail); e != nil {
				return unknown("百度原分享详情未读取完成，请稍后检查；不重新创建 [SHARE-BD/detail]")
			}
			if detail.Password == nil {
				return unknown("百度原分享未返回提取码，请稍后检查原结果；未重新创建 [SHARE-BD/password-missing]")
			}
			if len(*detail.Password) != 4 || !shareCode.MatchString(*detail.Password) {
				return unknown("百度原分享提取码格式无法确认，未展示链接 [SHARE-BD/password-format]")
			}
			changedCode := *detail.Password != op.in.Password
			privateMode := r.Public != nil && *r.Public == 0 || r.Channel != nil && *r.Channel == 4
			if changedCode && !privateMode {
				return unknown("百度原分享访问模式无法确认，未展示链接 [SHARE-BD/password-mode]")
			}
			link, err := resolveBaiduShareURL(detail.URL, r.Link, op.baiduURL)
			if err != nil {
				return unknown("百度原分享链接格式未确认；未重新创建 [SHARE-BD/url] " + err.Error())
			}
			message := "已核对原分享项目、提取码和有效期。"
			if changedCode {
				message = "已核对原分享和全部所选项目，显示网盘当前的提取码。"
			}
			return ShareResult{State: "ready", URL: link, Password: *detail.Password, Expiry: managedBaiduExpiry(r), Message: message}
		}
		if len(records) == 0 {
			break
		}
	}
	return unknown("未在有限范围内找到百度原分享记录，可能未同步或已取消；请到官网核对，不重新创建 [SHARE-BD/not-found]")
}

// Preserve the legacy single-ID representation and compare multi-item sets exactly.
func baiduShareSources(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	for i, id := range ids {
		if !valid123Folder(id) || id == "0" || i > 0 && id == ids[i-1] {
			return ""
		}
	}
	if len(ids) == 1 {
		return ids[0]
	}
	raw, _ := json.Marshal(ids)
	return string(raw)
}
