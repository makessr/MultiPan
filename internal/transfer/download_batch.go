package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type BatchDownloadItem struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Result  DownloadResult `json:"result"`
}
type BatchDownloadResult struct {
	RetryID   string `json:"retryId,omitempty"`
	retry     *directoryRetryState
	Items     []BatchDownloadItem `json:"items"`
	Cancelled bool                `json:"cancelled"`
	Directory string              `json:"directory"`
}
type BatchDownloadEvent struct {
	Index    int                `json:"index"`
	Count    int                `json:"count"`
	Name     string             `json:"name"`
	Progress DownloadProgress   `json:"progress"`
	Item     *BatchDownloadItem `json:"item,omitempty"`
}

func ValidateDownloadBatch(inputs []DownloadInput) error {
	if len(inputs) == 0 || len(inputs) > 200 {
		return errors.New("每批请选择1至200个普通文件")
	}
	seen := map[string]bool{}
	for _, in := range inputs {
		if in.AccountID == "" || in.EntryID == "" || in.Name == "" || in.AccountID != inputs[0].AccountID || in.ParentID != inputs[0].ParentID || seen[in.EntryID] {
			return errors.New("批量下载仅支持同一账号当前目录的不同普通文件")
		}
		seen[in.EntryID] = true
	}
	return nil
}
func RunDownloadBatch(ctx context.Context, inputs []DownloadInput, directory string, download func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error), update func(BatchDownloadEvent)) (BatchDownloadResult, error) {
	result := BatchDownloadResult{Items: []BatchDownloadItem{}, Directory: directory}
	if e := ValidateDownloadBatch(inputs); e != nil {
		return result, e
	}
	info, e := os.Stat(directory)
	if e != nil || !info.IsDir() || !filepath.IsAbs(directory) {
		return result, errors.New("下载目录不存在或无法访问")
	}
	names := map[string]bool{}
	for i, in := range inputs {
		item := BatchDownloadItem{Name: in.Name}
		name := SuggestedDownloadName(in.Name)
		key := strings.ToLower(name)
		if ctx.Err() != nil {
			result.Cancelled = true
			item.Status = "skipped"
			item.Message = "已取消，未开始下载"
		} else if names[key] {
			item.Status = "skipped"
			item.Message = "本批文件保存名称冲突，未覆盖"
		} else {
			names[key] = true
			if update != nil {
				update(BatchDownloadEvent{Index: i + 1, Count: len(inputs), Name: in.Name, Progress: DownloadProgress{Total: -1}})
			}
			saved, err := download(ctx, in, filepath.Join(directory, name), func(p DownloadProgress) {
				if update != nil {
					update(BatchDownloadEvent{Index: i + 1, Count: len(inputs), Name: in.Name, Progress: p})
				}
			})
			item.Result = saved
			if err == nil && saved.Path != "" {
				item.Status = "saved"
			} else if errors.Is(err, context.Canceled) || ctx.Err() != nil || saved.Cancelled {
				result.Cancelled = true
				item.Status = "cancelled"
				item.Message = "下载已取消，未保存正式文件"
			} else {
				item.Status = "failed"
				if err != nil {
					item.Message = err.Error()
				} else {
					item.Message = "下载未返回保存结果"
				}
			}
		}
		result.Items = append(result.Items, item)
		if update != nil {
			copy := item
			update(BatchDownloadEvent{Index: i + 1, Count: len(inputs), Name: in.Name, Item: &copy})
		}
	}
	return result, nil
}
