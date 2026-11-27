package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type ShareRecreation struct {
	ReceiptID       string `json:"receiptId"`
	AttemptID       string `json:"attemptId"`
	Protected       bool   `json:"protected"`
	Days            int    `json:"days"`
	PaidPriceFen123 int    `json:"paidPriceFen123,omitempty"`
}

func baiduReceiptSourceIDs(op *shareOperation) []string {
	if op.baiduSource == "" {
		return nil
	}
	ids := []string{op.baiduSource}
	if strings.HasPrefix(op.baiduSource, "[") {
		if json.Unmarshal([]byte(op.baiduSource), &ids) != nil {
			return nil
		}
	}
	if len(ids) != len(shareTargets(op.in)) || baiduShareSources(ids) != op.baiduSource {
		return nil
	}
	return ids
}

func (m *Manager) RecreateShare(ctx context.Context, receiptID, attemptID string, in ShareInput) (ShareResult, error) {
	if in.Action != "create" || !shareToken.MatchString(receiptID) || !shareToken.MatchString(attemptID) {
		return ShareResult{}, errors.New("重新分享请求无效，请从本机回执再次打开")
	}
	in.recreateReceiptID, in.recreateAttemptID = receiptID, attemptID
	return m.shareEntry(ctx, in, true)
}

func (m *Manager) bindShareRecreationLocked(a Account, in *ShareInput) error {
	original := m.shares[in.recreateReceiptID]
	if !canLocateCreatedShare(a, original) || original.in.ParentID != in.ParentID {
		return errors.New("原分享回执、账号或目录已变化，请重新打开回执")
	}
	if result, overlaid := m.shareRevocationOverlayLocked(original); overlaid && result.State == "unknown" {
		return errors.New("原分享结果待核对，请先处理原回执")
	}
	expected, current := shareTargets(original.in), shareTargets(*in)
	if len(expected) != len(current) {
		return errors.New("原分享项目范围已变化，未提交创建请求")
	}
	byID := make(map[string]ShareTarget, len(expected))
	for _, target := range expected {
		byID[target.ID] = target
	}
	for _, target := range current {
		if byID[target.ID] != target {
			return errors.New("原分享项目范围已变化，未提交创建请求")
		}
		delete(byID, target.ID)
	}
	if a.Kind == "baidu" {
		if len(baiduReceiptSourceIDs(original)) == 0 {
			return errors.New("百度原分享回执缺少文件身份，请从文件管理重新选择")
		}
		in.expectedBaiduSources = original.baiduSource
	}
	in.previousShareURL = original.result.URL
	return nil
}
