package transfer

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const guangyaPurgePurpose = "guangya-recycle-purge-v1"
const guangyaFileDeletedCode = 149

func submitGuangyaPurge(ctx context.Context, client *http.Client, a Account, id string) (string, error) {
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var result struct {
		TaskID string `json:"taskId"`
	}
	// The web app uses this endpoint in the recycle list as well as for live
	// files. The caller must first recheck the exact recycle record.
	_, err := (gyClient{&bounded, a}).call(ctx, "/userres/v1/file/delete_file", map[string][]string{"fileIds": {id}}, &result)
	if err != nil {
		return "", err
	}
	if !validGuangyaRestoreTaskID(result.TaskID) {
		return "", errors.New("光鸭未返回可核对的删除任务凭证")
	}
	return result.TaskID, nil
}

func checkGuangyaPurge(ctx context.Context, client *http.Client, a Account, d diskRecyclePurge) (string, string) {
	if !d.Acknowledged {
		return "unknown", "光鸭删除任务凭证缺失，请到官网核对原结果"
	}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "unknown", "光鸭删除任务仍待核对"
			case <-time.After(time.Second):
			}
		}
		done, err := guangyaFileTaskDone(ctx, client, a, d.TaskID)
		if err != nil {
			return "unknown", "光鸭删除任务待核对：" + err.Error()
		}
		if !done {
			continue
		}
		present, err := findRecyclePurge(ctx, client, a, d.Source.Entry)
		if err != nil {
			return "unknown", "光鸭删除后回收站核对不完整：" + err.Error()
		}
		if present {
			return "unknown", "原项目仍在光鸭回收站，请核对是否有其他客户端还原或移动该项目"
		}
		code, _ := (gyClient{client, a}).call(ctx, "/userres/v1/file/get_file_detail", map[string]string{"fileId": d.Source.Entry.ID}, nil)
		if code != guangyaFileDeletedCode {
			return "unknown", "回收站已无原项目，但光鸭未明确确认原文件 ID 已删除，请到官网核对"
		}
		present, err = findRecyclePurge(ctx, client, a, d.Source.Entry)
		if err != nil || present {
			return "unknown", "核对原文件 ID 后未能再次确认回收站记录已移除，请到官网核对"
		}
		return "confirmed", "已核对光鸭删除任务完成、原记录移出回收站，原文件 ID 返回已删除"
	}
	return "unknown", "光鸭删除任务仍待核对"
}
