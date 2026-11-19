package transfer

import (
	"context"
	"errors"
	"math"
	"net/http"
	"path"
	"sort"
)

const (
	pan123CopyMaxTreeItems   = 151
	pan123CopyMaxTreeFolders = 20
	pan123CopyMaxTreeDepth   = 8
)

func readPan123CopyTree(ctx context.Context, client *http.Client, a Account, rootID string) ([]CopyManifestItem, int64, error) {
	items := []CopyManifestItem{{NativeID: rootID, Folder: true}}
	seen := map[string]bool{rootID: true}
	directories := 1
	var total int64
	var walk func(string, string, int) error
	walk = func(id, prefix string, depth int) error {
		if depth > pan123CopyMaxTreeDepth {
			return errors.New("123 复制目录超过 8 层")
		}
		page, err := relayDirectoryPage(ctx, client, a, id)
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if seen[entry.ID] || len(items) >= pan123CopyMaxTreeItems {
				return errors.New("123 复制目录包含重复 ID，或超过 150 个子项目")
			}
			seen[entry.ID] = true
			item := CopyManifestItem{Path: path.Join(prefix, entry.Name), Folder: entry.Folder, NativeID: entry.ID}
			if entry.Folder {
				directories++
				if directories > pan123CopyMaxTreeFolders {
					return errors.New("123 复制目录最多包含 20 个文件夹")
				}
			} else {
				if entry.Size == nil || *entry.Size < 0 || *entry.Size > math.MaxInt64-total || !validRelayVersion("pan123", entry.revision) {
					return errors.New("123 子文件缺少可核对的大小或网盘 MD5")
				}
				item.Size, item.MD5 = *entry.Size, entry.revision
				total += item.Size
			}
			items = append(items, item)
			if entry.Folder {
				if err := walk(entry.ID, item.Path, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(rootID, "", 1); err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, total, nil
}

func validPan123CopyTree(items []CopyManifestItem, rootID string) bool {
	if len(items) == 0 || len(items) > pan123CopyMaxTreeItems || items[0].NativeID != rootID {
		return false
	}
	seen := map[string]bool{}
	folders := 0
	for _, item := range items {
		if !valid123Folder(item.NativeID) || item.NativeID == "0" || seen[item.NativeID] {
			return false
		}
		seen[item.NativeID] = true
		if item.Folder {
			folders++
			if folders > pan123CopyMaxTreeFolders || item.MD5 != "" || item.Size != 0 {
				return false
			}
		} else if !validRelayVersion("pan123", item.MD5) {
			return false
		}
	}
	return true
}

func matchingNativeCopyTrees(source, target []CopyManifestItem) bool {
	if len(source) != len(target) {
		return false
	}
	originalIDs := make(map[string]bool, len(source))
	for _, item := range source {
		originalIDs[item.NativeID] = true
	}
	for i, item := range target {
		if originalIDs[item.NativeID] {
			return false
		}
		item.NativeID = source[i].NativeID
		if item != source[i] {
			return false
		}
	}
	return true
}
