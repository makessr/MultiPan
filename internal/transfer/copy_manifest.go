package transfer

import (
	"context"
	"errors"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strings"
)

type CopyManifestItem struct {
	Path     string `json:"path"`
	Folder   bool   `json:"folder"`
	Size     int64  `json:"size"`
	NativeID string `json:"nativeId,omitempty"`
	MD5      string `json:"md5,omitempty"`
}

// No file content is downloaded. Record empty directories and exact file sizes.
func copyManifest(ctx context.Context, client *http.Client, a Account, root CloudEntry) ([]CopyManifestItem, error) {
	result := []CopyManifestItem{{Path: "", Folder: root.Folder, Size: 0}}
	if !root.Folder {
		if root.Size == nil || *root.Size < 0 {
			return nil, errors.New("文件大小无法确认")
		}
		result[0].Size = *root.Size
		return result, nil
	}
	var walk func(string, int) error
	walk = func(id string, depth int) error {
		if depth > 20 {
			return errors.New("复制目录超过20层，请拆分操作")
		}
		page, e := ListAccountFiles(ctx, client, a, id)
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, r := range page.Entries {
			if e := ctx.Err(); e != nil {
				return e
			}
			if !validBaiduPath(r.ID) || path.Dir(r.ID) != id || r.Name != path.Base(r.ID) || r.ID == id || seen[r.Name] || !strings.HasPrefix(r.ID, root.ID+"/") {
				return errors.New("目录清单的来源、名称或层级无法确认")
			}
			seen[r.Name] = true
			item := CopyManifestItem{Path: strings.TrimPrefix(r.ID, root.ID+"/"), Folder: r.Folder}
			if !r.Folder {
				if r.Size == nil || *r.Size < 0 {
					return errors.New("子文件大小无法确认，未提交或结果待核对")
				}
				item.Size = *r.Size
			}
			result = append(result, item)
			if len(result) > 501 {
				return errors.New("复制目录超过500个子项目，请拆分操作")
			}
			if r.Folder {
				if e := walk(r.ID, depth+1); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(root.ID, 1); e != nil {
		return nil, e
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func checkCopySnapshot(ctx context.Context, client *http.Client, a Account, row MoveBatchEntry, source CloudEntry, manifest []CopyManifestItem) (CloudEntry, error) {
	uncertain := errors.New("复制结果待核对：源或目标目录清单不匹配，仅读检查，不会重发复制请求")
	before, e := ListAccountFiles(ctx, client, a, row.Input.ParentID)
	if e != nil {
		return CloudEntry{}, e
	}
	after, e := ListAccountFiles(ctx, client, a, row.Input.DestinationID)
	if e != nil {
		return CloudEntry{}, e
	}
	var original, target CloudEntry
	countSource, countTarget := 0, 0
	for _, r := range before.Entries {
		if r.ID == source.ID && r.Name == source.Name && r.Folder == source.Folder {
			original = r
			countSource++
		}
	}
	for _, r := range after.Entries {
		if r.ID == path.Join(row.Input.DestinationID, source.Name) && r.Name == source.Name && r.Folder == source.Folder {
			target = r
			countTarget++
		}
	}
	if countSource != 1 || countTarget != 1 {
		return CloudEntry{}, uncertain
	}
	old, e := copyManifest(ctx, client, a, original)
	if e != nil {
		return CloudEntry{}, e
	}
	next, e := copyManifest(ctx, client, a, target)
	if e != nil {
		return CloudEntry{}, e
	}
	if !reflect.DeepEqual(old, manifest) || !reflect.DeepEqual(next, manifest) {
		return CloudEntry{}, uncertain
	}
	return target, nil
}
