package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const quarkTrashPurpose = "quark-trash-v1"

func validQuarkTrashSource(s trashSource) bool {
	in, row := s.Input, s.Original
	return s.Scope != "" && in.AccountID != "" && validQuarkFolder(in.ParentID) &&
		validQuarkFolder(row.ID) && row.ID == in.Entry.ID && row.ID != "0" && row.ID != in.ParentID &&
		row.ParentID == in.ParentID && row.Name == in.Entry.Name && validQuarkTrashName(row.Name) &&
		(row.Kind == "folder" && in.Entry.Folder || row.Kind == "file" && !in.Entry.Folder &&
			row.Size != nil && in.Entry.Size != nil && *row.Size == *in.Entry.Size) &&
		(row.Size == nil || *row.Size >= 0) && row.FileID == "" && row.RestoreHandle == "" &&
		row.OriginalPath == trashDirectoryPath(row.ParentID, row.Name)
}

func validQuarkTrashName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 4096 && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func readQuarkTrashParent(ctx context.Context, client *http.Client, a Account, parent string) ([]RecycleEntry, error) {
	if !validQuarkFolder(parent) {
		return nil, errors.New("夸克原目录无效")
	}
	c := newQuarkClient(client, a)
	var rows []RecycleEntry
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 50; page++ {
		var data struct {
			List []struct {
				quarkEntry
				Dir *bool `json:"dir"`
			} `json:"list"`
		}
		query := url.Values{"pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {"100"}, "_fetch_total": {"1"}, "_fetch_sub_dirs": {"0"}, "_sort": {"file_name:asc"}}
		meta, err := c.request(ctx, http.MethodGet, "/file/sort", query, nil, &data)
		if err != nil {
			return nil, err
		}
		var metadata struct {
			Total *int `json:"_total"`
		}
		if json.Unmarshal(meta, &metadata) != nil || metadata.Total == nil || *metadata.Total < 0 ||
			data.List == nil || len(data.List) > 100 || total >= 0 && total != *metadata.Total {
			return nil, errors.New("夸克原目录列表或总数无法核对")
		}
		total = *metadata.Total
		for _, row := range data.List {
			kind := quarkRecycleKind(row.Dir, row.FileType)
			if !validQuarkFolder(row.FID) || row.FID == "0" || row.FID == parent || seen[row.FID] ||
				shareScalar(row.ParentID) != parent || kind == "unknown" || !validQuarkTrashName(row.Name) ||
				row.Size != nil && *row.Size < 0 {
				return nil, errors.New("夸克原目录条目来源、类型或分页无法核对")
			}
			seen[row.FID] = true
			rows = append(rows, RecycleEntry{ID: row.FID, Name: row.Name, Kind: kind, ParentID: parent, Size: row.Size, OriginalPath: trashDirectoryPath(parent, row.Name)})
		}
		if len(rows) == total {
			return rows, nil
		}
		if len(rows) > total || len(data.List) < 100 {
			return nil, errors.New("夸克原目录列表不完整")
		}
	}
	return nil, errors.New("夸克原目录超过5000项，无法完整核对")
}

func submitQuarkTrash(ctx context.Context, client *http.Client, a Account, fid string) bool {
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := newQuarkClient(&bounded, a)
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	// The web file list uses ASYNC (2) for ordinary deletion. Permanent
	// deletion uses the separate recycle endpoint and a recycle record ID.
	payload := map[string]any{"action_type": 2, "filelist": []string{fid}, "exclude_fids": []string{}}
	if _, err := c.request(ctx, http.MethodPost, "/file/delete", nil, payload, &accepted); err != nil || !validQuarkFolder(accepted.TaskID) || accepted.TaskID == "0" {
		return false
	}
	for attempt := 0; attempt < 3; attempt++ {
		var task struct {
			Status *int `json:"status"`
		}
		query := url.Values{"task_id": {accepted.TaskID}, "retry_index": {strconv.Itoa(attempt)}}
		if _, err := c.request(ctx, http.MethodGet, "/task", query, nil, &task); err != nil ||
			task.Status == nil || *task.Status < 0 || *task.Status >= 2 || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(400 * time.Millisecond):
		}
	}
	// The task ID proves acceptance. The caller still has to verify the move.
	return true
}

func checkQuarkTrash(ctx context.Context, client *http.Client, a Account, s trashSource) error {
	original := s.Original
	seen := map[string]bool{}
	var expectedTotal *int64
	for page := 1; page <= 50; page++ {
		p, err := listAccountRecycle(ctx, client, a, page)
		if err != nil {
			return err
		}
		if p.Incomplete || page > 1 && (p.Total == nil || expectedTotal == nil || *p.Total != *expectedTotal) {
			return errors.New("夸克回收站分页不完整或总数已变化")
		}
		expectedTotal = p.Total
		matched := false
		for _, row := range p.Items {
			if seen[row.ID] {
				return errors.New("夸克回收站分页重复")
			}
			seen[row.ID] = true
			if row.FileID != original.ID {
				continue
			}
			if matched || row.ParentID != original.ParentID || row.Name != html.UnescapeString(original.Name) ||
				row.Kind != original.Kind || original.Kind == "file" && (row.Size == nil || *row.Size != *original.Size) {
				return errors.New("夸克回收站原文件 ID 的名称、目录、类型或大小无法核对")
			}
			matched = true
		}
		if matched {
			return nil
		}
		if !p.HasMore {
			return errors.New("尚未在夸克回收站核对到原文件 ID")
		}
	}
	return errors.New("夸克回收站核对超过5000项，结果不完整")
}
