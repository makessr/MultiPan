package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
)

const (
	lanzouCopyTreeDepth   = 8
	lanzouCopyTreeFolders = 20
	lanzouCopyTreeFiles   = 20
	lanzouCopyTreeBytes   = 400_000_000
)

func validLanzouFolderManifest(rows []CopyManifestItem, root string) bool {
	if len(rows) == 0 || rows[0].NativeID != root || len(rows) > lanzouCopyTreeFolders+lanzouCopyTreeFiles {
		return false
	}
	ids := map[string]bool{}
	var folders, files int
	var total int64
	for _, r := range rows {
		if !valid123Folder(r.NativeID) || r.NativeID == "0" || ids[r.NativeID] || r.MD5 != "" {
			return false
		}
		ids[r.NativeID] = true
		if r.Folder {
			folders++
			if r.Size != 0 || r.Path != "" && strings.Count(r.Path, "/")+2 > lanzouCopyTreeDepth {
				return false
			}
		} else {
			files++
			if r.Size <= 0 || r.Size > lanzouCopyMaxBytes || r.Size > lanzouCopyTreeBytes-total {
				return false
			}
			total += r.Size
		}
	}
	return folders <= lanzouCopyTreeFolders && files <= lanzouCopyTreeFiles
}

func planLanzouFolderCopy(ctx context.Context, client *http.Client, a Account, row MoveBatchEntry) (CloudEntry, []CopyManifestItem, error) {
	in := row.Input
	parent, err := listLanzouFolders(ctx, client, a, in.ParentID, true)
	if err != nil {
		return CloudEntry{}, nil, err
	}
	for _, folder := range parent.Folders {
		if folder.ID == in.EntryID && folder.Name == in.Name {
			tree, err := readLanzouCopyTree(ctx, client, a, folder.ID, in.DestinationID)
			return CloudEntry{ID: folder.ID, Name: folder.Name, Folder: true}, tree, err
		}
	}
	return CloudEntry{}, nil, errors.New("蓝奏来源文件夹身份已变化")
}

// The tree is collected before any cloud writes. File size comes from the
// download response because the web file list reports rounded display sizes.
func readLanzouCopyTree(ctx context.Context, client *http.Client, a Account, root, excluded string) ([]CopyManifestItem, error) {
	rows := []CopyManifestItem{{Folder: true, NativeID: root}}
	seen := map[string]bool{root: true}
	folders, files := 1, 0
	var total int64
	var visit func(string, string, int) error
	visit = func(id, parentPath string, depth int) error {
		dirs, err := listLanzouFolders(ctx, client, a, id, true)
		if err != nil {
			return err
		}
		if id == excluded {
			return errors.New("目标位于蓝奏来源目录树内")
		}
		list, err := readLanzouFiles(ctx, client, a, id, true)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, folder := range dirs.Folders {
			key := strings.ToLower(folder.Name)
			if folder.ID == excluded || seen[folder.ID] || names[key] || validateLanzouFolderName(folder.Name) != nil {
				return errors.New("蓝奏子目录与目标重叠、重复或名称不支持复制")
			}
			folders++
			if depth >= lanzouCopyTreeDepth || folders > lanzouCopyTreeFolders {
				return errors.New("蓝奏复制目录最多 8 层、20 个文件夹")
			}
			seen[folder.ID], names[key] = true, true
			name := path.Join(parentPath, folder.Name)
			rows = append(rows, CopyManifestItem{Path: name, Folder: true, NativeID: folder.ID})
			if err := visit(folder.ID, name, depth+1); err != nil {
				return err
			}
		}
		for _, file := range list {
			key := strings.ToLower(file.Name)
			if seen[file.ID] || names[key] {
				return errors.New("蓝奏复制目录存在重复名称或标识")
			}
			files++
			if files > lanzouCopyTreeFiles {
				return errors.New("蓝奏复制目录最多 20 个文件")
			}
			entry, resp, err := lanzouCopyDownload(ctx, client, a, id, file.ID, file.Name)
			if err != nil {
				return err
			}
			resp.Body.Close()
			total += *entry.Size
			if total > lanzouCopyTreeBytes {
				return errors.New("蓝奏复制目录内文件合计超过 400 MB")
			}
			seen[file.ID], names[key] = true, true
			rows = append(rows, CopyManifestItem{Path: path.Join(parentPath, file.Name), Size: *entry.Size, NativeID: file.ID})
		}
		return nil
	}
	if err := visit(root, "", 1); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b CopyManifestItem) int { return strings.Compare(a.Path, b.Path) })
	return rows, nil
}

func uploadLanzouFolderCopyFile(ctx context.Context, client *http.Client, a Account, item CopyManifestItem, source, target, expected string) error {
	row, file, digest, err := cacheLanzouCopyFile(ctx, client, a, source, item.NativeID, path.Base(item.Path))
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if *row.Size != item.Size || digest != expected {
		return errors.New("蓝奏目录内源文件已变化，停止复制")
	}
	return uploadCachedLanzouFile(ctx, client, a, row, target, file)
}

func lanzouTreeParent(name string, parents map[string]string) string {
	dir := path.Dir(name)
	if dir == "." {
		dir = ""
	}
	return parents[dir]
}

func hashLanzouCopyTree(ctx context.Context, client *http.Client, a Account, rows []CopyManifestItem) (map[string]string, error) {
	parents := map[string]string{}
	hashes := map[string]string{}
	for _, row := range rows {
		if row.Folder {
			parents[row.Path] = row.NativeID
			continue
		}
		entry, resp, err := lanzouCopyDownload(ctx, client, a, lanzouTreeParent(row.Path, parents), row.NativeID, path.Base(row.Path))
		if err != nil {
			return nil, err
		}
		digest, err := relayHash(ctx, resp.Body, row.Size, nil)
		resp.Body.Close()
		if err != nil || *entry.Size != row.Size {
			return nil, errors.New("蓝奏目录内文件读取不完整或大小已变化")
		}
		hashes[row.Path] = digest
	}
	return hashes, nil
}

func lanzouTreeDigest(hashes map[string]string) string {
	raw, _ := json.Marshal(hashes)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func startLanzouFolderCopy(ctx context.Context, client *http.Client, a Account, d diskCopy, persist func(diskCopy) error) (CloudEntry, error) {
	source, manifest, err := planLanzouFolderCopy(ctx, client, a, d.Input)
	if err != nil {
		return CloudEntry{}, err
	}
	hashes, err := hashLanzouCopyTree(ctx, client, a, manifest)
	if err != nil {
		return CloudEntry{}, err
	}
	d.Source, d.Manifest, d.Submitted = source, manifest, true
	d.Lanzou = &lanzouCopyProof{SHA256: lanzouTreeDigest(hashes)}
	if !validSavedLanzouCopy(d) {
		return CloudEntry{}, errors.New("蓝奏目录清单无法安全保存")
	}
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	root, err := createLanzouFolder(ctx, client, a, d.Input.Input.DestinationID, d.Receipt.OutputFolder, false)
	if err != nil {
		return CloudEntry{}, err
	}
	if slices.ContainsFunc(manifest, func(item CopyManifestItem) bool { return item.NativeID == root.ID }) {
		return CloudEntry{}, errors.New("蓝奏返回了来源目录 ID，未复制内容")
	}
	d.Lanzou.FolderID = root.ID
	if !validSavedLanzouCopy(d) {
		return CloudEntry{}, errors.New("蓝奏副本根目录 ID 不符，已停止复制")
	}
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	// Recheck the fresh root before writing to it.
	entries, err := lanzouCopyTarget(ctx, client, a, d)
	if err != nil || len(entries) != 0 {
		return CloudEntry{}, errors.New("蓝奏新建复制目录未确认为空")
	}
	origin := map[string]string{"": source.ID}
	target := map[string]string{"": root.ID}
	targetIDs := map[string]bool{root.ID: true}
	for _, item := range manifest[1:] {
		parent := lanzouTreeParent(item.Path, target)
		if item.Folder {
			folder, err := createLanzouFolder(ctx, client, a, parent, path.Base(item.Path), false)
			if err != nil {
				return CloudEntry{}, err
			}
			if targetIDs[folder.ID] || slices.ContainsFunc(manifest, func(row CopyManifestItem) bool { return row.NativeID == folder.ID }) {
				return CloudEntry{}, errors.New("蓝奏返回了来源项目 ID，已停止复制")
			}
			page, err := listLanzouFolders(ctx, client, a, folder.ID, true)
			if err != nil || len(page.Breadcrumbs) < 2 || len(page.Folders) != 0 ||
				page.Breadcrumbs[len(page.Breadcrumbs)-2].ID != parent || page.Breadcrumbs[len(page.Breadcrumbs)-1] != folder {
				return CloudEntry{}, errors.New("蓝奏新建子目录位置无法确认，已停止复制")
			}
			files, err := readLanzouFiles(ctx, client, a, folder.ID, true)
			if err != nil || len(files) != 0 {
				return CloudEntry{}, errors.New("蓝奏新建子目录未确认为空，已停止复制")
			}
			targetIDs[folder.ID] = true
			origin[item.Path], target[item.Path] = item.NativeID, folder.ID
			continue
		}
		if err := uploadLanzouFolderCopyFile(ctx, client, a, item, lanzouTreeParent(item.Path, origin), parent, hashes[item.Path]); err != nil {
			return CloudEntry{}, err
		}
	}
	d.Lanzou.Acknowledged = true
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	return checkLanzouCopy(ctx, client, a, d)
}

func checkLanzouFolderCopy(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	_, original, err := planLanzouFolderCopy(ctx, client, a, d.Input)
	if err != nil || !slices.Equal(original, d.Manifest) {
		return CloudEntry{}, errors.New("蓝奏来源目录清单已变化")
	}
	parent, err := listLanzouFolders(ctx, client, a, d.Input.Input.DestinationID, true)
	if err != nil || !slices.Contains(parent.Folders, CloudFolder{ID: d.Lanzou.FolderID, Name: d.Receipt.OutputFolder}) {
		return CloudEntry{}, errors.New("蓝奏副本目录位置已变化")
	}
	target, err := readLanzouCopyTree(ctx, client, a, d.Lanzou.FolderID, "")
	if err != nil || !matchingNativeCopyTrees(original, target) {
		return CloudEntry{}, errors.New("蓝奏副本目录结构或文件大小不符")
	}
	for _, rows := range [][]CopyManifestItem{original, target} {
		hashes, err := hashLanzouCopyTree(ctx, client, a, rows)
		if err != nil || lanzouTreeDigest(hashes) != d.Lanzou.SHA256 {
			return CloudEntry{}, errors.New("蓝奏源目录或副本的完整内容无法核对")
		}
	}
	return CloudEntry{ID: d.Lanzou.FolderID, Name: d.Receipt.OutputFolder, Folder: true}, nil
}
