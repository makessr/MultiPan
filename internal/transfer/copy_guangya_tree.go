package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
)

const (
	guangyaCopyTreeItems   = 151
	guangyaCopyTreeFolders = 20
	guangyaCopyTreeDepth   = 8
)

func readGuangyaCopyTree(ctx context.Context, client *http.Client, a Account, rootID, excludedID string, hashFiles bool) ([]CopyManifestItem, string, error) {
	items := []CopyManifestItem{{NativeID: rootID, Folder: true}}
	seen := map[string]bool{rootID: true}
	hashes := map[string]string{}
	folders := 1
	var size int64
	var walk func(string, string, int) error
	walk = func(id, prefix string, depth int) error {
		if depth > guangyaCopyTreeDepth {
			return errors.New("光鸭复制目录超过 8 层")
		}
		page, err := readGuangyaRelayPage(ctx, client, a, id, true)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, entry := range page.Entries {
			if entry.ID == excludedID {
				return errors.New("不能把文件夹复制到其子目录")
			}
			name := strings.ToLower(entry.Name)
			if seen[entry.ID] || names[name] || len(items) >= guangyaCopyTreeItems {
				return errors.New("光鸭目录有重复项目，或超过 150 个子项目")
			}
			seen[entry.ID], names[name] = true, true
			item := CopyManifestItem{Path: path.Join(prefix, entry.Name), Folder: entry.Folder, NativeID: entry.ID}
			if entry.Folder {
				folders++
				if folders > guangyaCopyTreeFolders {
					return errors.New("光鸭复制目录最多包含 20 个文件夹")
				}
			} else {
				if entry.Size == nil || *entry.Size < 0 || *entry.Size > guangyaCopyMaxBytes-size {
					return errors.New("光鸭目录大小不明或子文件合计超过 2 GiB")
				}
				item.Size = *entry.Size
				size += item.Size
				if hashFiles {
					digest, err := readGuangyaFileHash(ctx, client, a, id, entry, nil)
					if err != nil {
						return err
					}
					hashes[item.Path] = digest
				}
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
		return nil, "", err
	}
	slices.SortFunc(items, func(a, b CopyManifestItem) int { return strings.Compare(a.Path, b.Path) })
	if !hashFiles {
		return items, "", nil
	}
	// Map keys are encoded in order, so the digest covers each file path and content.
	data, err := json.Marshal(hashes)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return items, hex.EncodeToString(digest[:]), nil
}

func validGuangyaCopyTree(items []CopyManifestItem, rootID string) bool {
	if len(items) == 0 || len(items) > guangyaCopyTreeItems || items[0].NativeID != rootID {
		return false
	}
	seen := map[string]bool{}
	folders := 0
	var size int64
	for i, item := range items {
		if !validGuangyaCopyFileID(item.NativeID) || seen[item.NativeID] || item.MD5 != "" ||
			i > 0 && item.Path <= items[i-1].Path {
			return false
		}
		seen[item.NativeID] = true
		if item.Folder {
			folders++
			if folders > guangyaCopyTreeFolders || item.Size != 0 || strings.Count(item.Path, "/")+2 > guangyaCopyTreeDepth && item.Path != "" {
				return false
			}
		} else {
			if item.Size < 0 || item.Size > guangyaCopyMaxBytes-size {
				return false
			}
			size += item.Size
		}
	}
	return true
}
