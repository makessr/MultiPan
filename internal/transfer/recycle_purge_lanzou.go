package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	lanzouPurgePurpose                  = "lanzou-recycle-file-purge-v1"
	lanzouFolderPurgePurpose            = "lanzou-recycle-folder-purge-v1"
	lanzouListedFilePurgePurpose        = "lanzou-recycle-listed-file-purge-v1"
	lanzouListedEmptyFolderPurgePurpose = "lanzou-recycle-listed-empty-folder-purge-v1"
	lanzouListedFolderPurgePurpose      = "lanzou-recycle-listed-folder-purge-v1"
)

func lanzouRecyclePurgeAccount(a Account) bool {
	return a.Kind == "lanzou" && a.Endpoint == lanzouRecycleOrigin && a.Secret != ""
}

func validLanzouPurgeFileIDs(s recyclePurgeSource) bool {
	if len(s.FolderFileIDs) == 0 {
		return s.FolderInventoryHash == ""
	}
	hash, err := hex.DecodeString(s.FolderInventoryHash)
	if err != nil || len(hash) != sha256.Size || s.Entry.Kind != "folder" || len(s.FolderFileIDs) > 200 || !slices.IsSorted(s.FolderFileIDs) {
		return false
	}
	for i, id := range s.FolderFileIDs {
		if !valid123Folder(id) || id == "0" || i > 0 && s.FolderFileIDs[i-1] == id {
			return false
		}
	}
	return true
}

func lanzouPurgeInventoryHash(inventory *lanzouFolderContents) string {
	raw, _ := json.Marshal(inventory)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

var errLanzouPurgeFolderLimit = errors.New("蓝奏回收站文件夹超过 10 个，无法核对全部可见文件夹")
var errLanzouRecycleFolderMarkers = errors.New("蓝奏回收站文件夹含子目录或其他条目标记")

type lanzouPurgeFolderScan struct {
	files  map[string][]lanzouFolderFile
	errors map[string]error
}

// The root webpage also lists files in recycled folders. Only a checked
// inventory of every visible folder can distinguish those files by ID.
func readLanzouPurgeFolderMembers(ctx context.Context, client *http.Client, a Account, rows []RecycleEntry, scan *lanzouPurgeFolderScan) (map[string]bool, error) {
	const maxFolders = 10
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	members := map[string]bool{}
	var incomplete error
	n := 0
	for _, row := range rows {
		if row.Kind != "folder" {
			continue
		}
		n++
		if n > maxFolders {
			return nil, errLanzouPurgeFolderLimit
		}
		id := strings.TrimPrefix(row.ID, "folder:")
		names, err := readLanzouPurgeFolderNames(ctx, client, a, id)
		var files []lanzouFolderFile
		if err == nil {
			files, err = readLanzouFolderFilesWithIDs(ctx, client, a, id, names)
		}
		if err == nil {
			for _, file := range files {
				if members[file.ID] || len(members) >= 1000 {
					err = errors.New("蓝奏回收站文件夹内的文件标识重复或超出核对范围")
					break
				}
				members[file.ID] = true
			}
		}
		if err != nil {
			if errors.Is(err, errLanzouRecycleLogin) {
				return nil, err
			}
			if scan != nil && scan.errors != nil {
				scan.errors[row.ID] = err
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if scan == nil {
				return nil, err
			}
			// An independently verified empty folder has no member ID to
			// reconcile. Keep gathering those within the existing bounds.
			if incomplete == nil {
				incomplete = err
			}
			continue
		}
		if scan != nil {
			scan.files[row.ID] = files
		}
	}
	if incomplete != nil {
		return nil, incomplete
	}
	return members, nil
}

func readLanzouPurgeFolderNames(ctx context.Context, client *http.Client, a Account, id string) ([]string, error) {
	body, err := readLanzouRecyclePage(ctx, client, a, url.Values{"item": {"recycle"}, "action": {"show_files"}, "folder_id": {id}})
	if err != nil {
		return nil, err
	}
	names, err := parseLanzouRecycleFolder(body)
	if lanzouRecycleFolderHasChildren(body, id) {
		return nil, errLanzouRecycleFolderMarkers
	}
	return names, err
}

func findLanzouPurgeEntry(ctx context.Context, client *http.Client, a Account, entry RecycleEntry, childIDs []string, purpose string) (bool, error) {
	rows, err := readLanzouRecycleRows(ctx, client, a)
	if err != nil {
		return false, err
	}
	if purpose == lanzouListedFilePurgePurpose || purpose == lanzouListedFolderPurgePurpose && len(childIDs) != 0 {
		members, err := readLanzouPurgeFolderMembers(ctx, client, a, rows, nil)
		if err != nil {
			return false, err
		}
		if purpose == lanzouListedFilePurgePurpose && members[entry.FileID] {
			return true, errors.New("蓝奏原文件属于回收站文件夹，请到官网处理")
		}
		for _, id := range childIDs {
			if members[id] {
				return true, nil
			}
		}
	}
	for _, row := range rows {
		if row.Kind == "file" && slices.Contains(childIDs, strings.TrimPrefix(row.ID, "file:")) {
			return true, nil
		}
		if row.ID == entry.ID {
			if !sameRecycleRestoreRow(row, entry) {
				return true, errors.New("蓝奏回收站同 ID 项目名称已变化")
			}
			return true, nil
		}
	}
	return false, nil
}

type lanzouPurgeContents struct {
	ids   []string
	names []string
	hash  string
}

func lanzouPurgeRootNames(rows []RecycleEntry) map[string]string {
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names
}

func validLanzouListedPurgeFiles(rootNames map[string]string, files []lanzouFolderFile) bool {
	if len(files) > 200 {
		return false
	}
	for _, file := range files {
		row := RecycleEntry{ID: "file:" + file.ID, Kind: "file", Name: file.Name}
		if _, ok := lanzouListedRestoreFile(row); !ok || rootNames[row.ID] != file.Name {
			return false
		}
	}
	return true
}

func readLanzouListedFolderPurgeContents(ctx context.Context, client *http.Client, a Account, entry RecycleEntry) (lanzouPurgeContents, error) {
	rows, err := readLanzouRecycleRows(ctx, client, a)
	if err != nil {
		return lanzouPurgeContents{}, err
	}
	rootNames := lanzouPurgeRootNames(rows)
	if rootNames[entry.ID] != entry.Name {
		return lanzouPurgeContents{}, errors.New("蓝奏文件夹已不在回收站或名称已变化")
	}
	folders := make(map[string][]lanzouFolderFile)
	_, err = readLanzouPurgeFolderMembers(ctx, client, a, rows, &lanzouPurgeFolderScan{files: folders})
	if errors.Is(err, errLanzouRecycleLogin) {
		return lanzouPurgeContents{}, err
	}
	if ctx.Err() != nil {
		return lanzouPurgeContents{}, ctx.Err()
	}
	files, found := folders[entry.ID]
	if found && len(files) == 0 {
		return lanzouPurgeContents{}, nil
	}
	if err != nil {
		return lanzouPurgeContents{}, err
	}
	if !found || !validLanzouListedPurgeFiles(rootNames, files) {
		return lanzouPurgeContents{}, errors.New("蓝奏网页无法完整核对文件夹内的项目")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	contents := lanzouPurgeContents{hash: lanzouPurgeInventoryHash(&lanzouFolderContents{Files: files})}
	for _, file := range files {
		contents.ids = append(contents.ids, file.ID)
		contents.names = append(contents.names, file.Name)
	}
	return contents, nil
}

func readLanzouPurgeForm(ctx context.Context, client *http.Client, a Account, source recyclePurgeSource, inventory *lanzouFolderContents, preparing bool) (url.Values, lanzouPurgeContents, error) {
	entry := source.Entry
	var contents lanzouPurgeContents
	if source.Purpose == lanzouListedEmptyFolderPurgePurpose || source.Purpose == lanzouListedFolderPurgePurpose {
		if inventory != nil && len(inventory.Files) != 0 {
			return nil, contents, errors.New("蓝奏文件夹存在本机移入清单，请刷新后重新预览")
		}
		checked, err := readLanzouListedFolderPurgeContents(ctx, client, a, entry)
		if err != nil {
			return nil, contents, err
		}
		if !preparing && (checked.hash != source.FolderInventoryHash || !slices.Equal(checked.ids, source.FolderFileIDs)) {
			return nil, contents, errors.New("蓝奏文件夹内容已变化，请重新预览")
		}
		form, err := readLanzouRecycleConfirmation(ctx, client, a, entry.ID, lanzouPurge)
		return form, checked, err
	}
	if entry.Kind == "folder" {
		files, err := readLanzouPurgeFolderNames(ctx, client, a, entry.FileID)
		if err != nil {
			return nil, contents, err
		}
		if len(files) > 0 {
			if inventory == nil || len(files) != len(inventory.Files) {
				return nil, contents, errors.New("蓝奏文件夹缺少一致的移入前清单，未提交永久删除")
			}
			checked := compareLanzouFolderInventory(ctx, client, a, entry.FileID, files, inventory, true)
			if checked == nil || checked.Matching != len(files) || checked.OriginalOnly != 0 || checked.CurrentOnly != 0 {
				return nil, contents, errors.New("蓝奏网页文件 ID 与移入前清单无法逐项核对，未提交永久删除")
			}
			rows, err := readLanzouRecycleRows(ctx, client, a)
			if err != nil {
				return nil, contents, err
			}
			observed := lanzouPurgeRootNames(rows)
			if observed[entry.ID] != entry.Name {
				return nil, contents, errors.New("蓝奏文件夹已不在回收站或名称已变化")
			}
			for _, file := range inventory.Files {
				if !matchingLanzouPurgeName(observed["file:"+file.ID], file.Name) {
					return nil, contents, errors.New("蓝奏回收站根列表缺少可核对的子文件，未提交永久删除")
				}
				contents.ids = append(contents.ids, file.ID)
				contents.names = append(contents.names, file.Name)
			}
			slices.Sort(contents.ids)
			contents.hash = lanzouPurgeInventoryHash(inventory)
		} else if inventory != nil && len(inventory.Files) > 0 {
			return nil, contents, errors.New("蓝奏网页为空，但移入前清单有文件，请到官网核对")
		}
	}
	form, err := readLanzouRecycleConfirmation(ctx, client, a, entry.ID, lanzouPurge)
	return form, contents, err
}

func matchingLanzouPurgeName(display, original string) bool {
	if original == "" {
		return false
	}
	if display == original {
		return true
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(display, "…"), "...")
	return prefix != "" && prefix != display && len(prefix) < len(original) && strings.HasPrefix(original, prefix)
}

func lanzouRecycleFolderHasChildren(body, folderID string) bool {
	body = lanzouRecycleHidden.ReplaceAllString(body, "")
	for _, input := range lanzouRecycleInput.FindAllString(body, -1) {
		switch lanzouRecycleAttributes(input)["name"] {
		case "fd_sel_ids", "fd_sel_ids[]", "fl_sel_ids", "fl_sel_ids[]":
			return true
		}
	}
	for _, link := range lanzouRecycleLink.FindAllStringSubmatch(body, -1) {
		target, err := url.Parse(lanzouRecycleAttributes(link[1])["href"])
		if err == nil {
			for _, id := range target.Query()["folder_id"] {
				if id != "" && id != folderID {
					return true
				}
			}
		}
	}
	return false
}

func submitLanzouPurge(ctx context.Context, client *http.Client, a Account, form url.Values) error {
	body, err := submitLanzouRecycleForm(ctx, client, a, form)
	if err != nil {
		return err
	}
	// A success message embedded in markup, script, or a longer instruction
	// is not an acknowledgement.
	for _, field := range lanzouRecycleTag.Split(body, -1) {
		message := strings.TrimSuffix(strings.TrimSpace(html.UnescapeString(field)), "。")
		if message == "删除成功" {
			return nil
		}
	}
	return errors.New("蓝奏未返回明确的永久删除回执")
}

func checkLanzouPurge(ctx context.Context, client *http.Client, a Account, d diskRecyclePurge) (string, string) {
	if !d.Acknowledged {
		return "unknown", "蓝奏永久删除回执未确认，请到官网核对原项目"
	}
	for scan := range 2 {
		present, err := findLanzouPurgeEntry(ctx, client, a, d.Source.Entry, d.Source.FolderFileIDs, d.Source.Purpose)
		if err != nil {
			return "unknown", "蓝奏回收站未返回可核对的列表，请到官网或稍后核对原项目"
		}
		if present && scan == 0 {
			// The provider may return the old list just after the write.
			timer := time.NewTimer(1500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "unknown", "蓝奏永久删除核对已停止，请稍后检查原结果"
			case <-timer.C:
			}
			present, err = findLanzouPurgeEntry(ctx, client, a, d.Source.Entry, d.Source.FolderFileIDs, d.Source.Purpose)
			if err != nil {
				return "unknown", "蓝奏回收站未返回可核对的列表，请到官网或稍后核对原项目"
			}
		}
		if present {
			return "unknown", "蓝奏回收站仍显示原项目，请稍后检查"
		}
	}
	return "confirmed", "已核对蓝奏永久删除回执，回收站列表已移除原项目"
}
