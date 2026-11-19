package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type pan123Result struct {
	FileInfo *pan123Entry `json:"file_info"`
	Duration *float64     `json:"duration"`
}

func validate123Result(info pan123Entry, folder string, f File, digest string) (json.Number, error) {
	if !valid123Folder(string(info.ID)) || info.ID == "0" || info.ParentID != json.Number(folder) || info.Type != 0 || info.Name != f.Name || info.Size != f.Size || !strings.EqualFold(strings.TrimSpace(info.MD5), digest) {
		return "", errors.New("123 返回的最终文件信息与上传目标不一致 [123-RESULT-MISMATCH/0.5.1]；未自动重传，请核对网盘")
	}
	return info.ID, nil
}

// Completion may be asynchronous and can map an allocation ID to a different
// final object ID. Submit completion once, then query its read-only result.
func (c pan123Client) complete123(ctx context.Context, pre pan123Pre, folder string, f File, digest string, multipart, submit bool) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	payload := map[string]any{"StorageNode": pre.StorageNode, "bucket": pre.Bucket, "fileId": pre.FileID, "fileSize": f.Size, "isMultipart": multipart, "key": pre.Key, "uploadId": pre.UploadID}
	var result pan123Result
	if submit {
		if e := c.request(ctx, "POST", "/file/upload_complete/v2", nil, payload, &result); e != nil {
			return e
		}
	}
	q := url.Values{"StorageNode": {pre.StorageNode}, "bucket": {pre.Bucket}, "fileId": {string(pre.FileID)}, "fileSize": {strconv.FormatInt(f.Size, 10)}, "isMultipart": {strconv.FormatBool(multipart)}, "key": {pre.Key}, "uploadId": {pre.UploadID}}
	for {
		if result.FileInfo != nil {
			_, e := validate123Result(*result.FileInfo, folder, f, digest)
			if e != nil {
				return e
			}
			// file_info is the authoritative terminal metadata. A stale directory
			// listing must not turn this validated completion into a failure.
			return nil
		}
		result = pan123Result{}
		if e := c.request(ctx, "GET", "/file/upload_complete/result", q, nil, &result); e != nil {
			return e
		}
		if result.FileInfo != nil {
			continue
		}
		delay := time.Second
		if result.Duration != nil {
			if *result.Duration < 0 || *result.Duration > 86400 {
				return errors.New("123 上传结果等待时间无效 [123-RESULT-DURATION/0.5.1]")
			}
			delay = time.Duration(min(max(*result.Duration, 0.2), 5) * float64(time.Second))
		}
		select {
		case <-ctx.Done():
			return errors.New("123 正在确认云端结果 [123-RESULT-PENDING/0.5.1]；文件可能已保存，未自动重传，请稍后核对")
		case <-time.After(delay):
		}
	}
}
