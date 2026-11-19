package transfer

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func submitGuangyaEmpty(ctx context.Context, client *http.Client, a Account) (string, error) {
	var result struct {
		TaskID string `json:"taskId"`
	}
	_, err := (gyClient{client, a}).call(ctx, "/userres/v1/file/clear_recycle_bin", nil, &result)
	if err != nil {
		return "", err
	}
	if !validGuangyaRestoreTaskID(result.TaskID) {
		return "", errors.New("光鸭未返回可核对的清空任务凭证")
	}
	return result.TaskID, nil
}

func checkGuangyaEmpty(ctx context.Context, client *http.Client, a Account, d diskRecyclePurge) (string, string) {
	if !d.Acknowledged {
		return "unknown", "光鸭清空任务凭证缺失，请到官网核对原结果"
	}
	for attempt := 0; attempt < 3; attempt++ {
		done, err := guangyaFileTaskDone(ctx, client, a, d.TaskID)
		if err != nil {
			return "unknown", "光鸭清空结果待核对：" + err.Error()
		}
		if done {
			page, err := listAccountRecycle(ctx, client, a, 1)
			if err != nil {
				return "unknown", "光鸭清空任务已完成，但无法读取回收站确认是否为空"
			}
			if page.Incomplete || page.HasMore || page.Total == nil || *page.Total != 0 || len(page.Items) != 0 {
				return "unknown", "光鸭清空任务已完成，但目前无法确认回收站为空"
			}
			return "confirmed", "已核对光鸭清空任务完成，回收站在此次读取时为空"
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return "unknown", "光鸭清空任务仍待核对"
			case <-time.After(time.Second):
			}
		}
	}
	return "unknown", "光鸭清空任务仍待核对"
}
