package transfer

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

const guangyaCopyFolderPrefix = "MultiPan-GY-copy-"
const guangyaCopyMaxBytes = 2 << 30

type guangyaCopyProof struct {
	SHA256   string `json:"sha256"`
	FolderID string `json:"folderId,omitempty"`
	TaskID   string `json:"taskId,omitempty"`
}

func validGuangyaCopyInput(row MoveBatchEntry) bool {
	in := row.Input
	return in.AccountID != "" && relayName(in.Name) &&
		validQuarkFolder(in.ParentID) && in.ParentID != "0" &&
		validQuarkFolder(in.DestinationID) && in.DestinationID != "0" &&
		validGuangyaCopyFileID(in.EntryID) &&
		in.EntryID != in.ParentID && in.EntryID != in.DestinationID && in.ParentID != in.DestinationID
}

func validGuangyaCopyFileID(id string) bool {
	return validQuarkFolder(id) && id != "0" && id != guangyaRoot
}

func validSavedGuangyaCopy(d diskCopy) bool {
	if !validGuangyaCopyInput(d.Input) || d.Pan123 != nil ||
		d.Direct123 ||
		d.DirectGuangya && (!d.Input.Folder || d.Receipt.OutputFolder != d.Input.Input.Name) ||
		!d.DirectGuangya && d.Receipt.OutputFolder != guangyaCopyFolderPrefix+d.Receipt.ID ||
		validateCloudFolderName(d.Receipt.OutputFolder) != nil {
		return false
	}
	p := d.Guangya
	if !d.Submitted {
		return p == nil
	}
	if p == nil || !validRelayGuangyaVersion("sha256:"+p.SHA256) {
		return false
	}
	if d.Input.Folder {
		if !validGuangyaCopyTree(d.Manifest, d.Input.Input.EntryID) {
			return false
		}
	} else if d.Source.Size == nil || *d.Source.Size < 0 || *d.Source.Size > guangyaCopyMaxBytes {
		return false
	}
	in := d.Input.Input
	if p.FolderID != "" && (!validGuangyaCopyFileID(p.FolderID) ||
		p.FolderID == in.EntryID || p.FolderID == in.ParentID || p.FolderID == in.DestinationID) {
		return false
	}
	if p.TaskID != "" && (!validGuangyaRestoreTaskID(p.TaskID) || p.FolderID == "") {
		return false
	}
	return d.Receipt.State != "confirmed" || p.TaskID != "" || guangyaEmptyDirectCopy(d)
}

func guangyaEmptyDirectCopy(d diskCopy) bool {
	return d.DirectGuangya && len(d.Manifest) == 1 && d.Guangya.FolderID != "" && d.Guangya.TaskID == ""
}

func readGuangyaCopySource(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	in := d.Input.Input
	page, err := relayGuangyaPage(ctx, client, a, in.ParentID)
	if err != nil {
		return CloudEntry{}, err
	}
	return selectGuangyaCopyEntry(page, d.Input, d.Source.Size)
}

func selectGuangyaCopyEntry(page FilePage, row MoveBatchEntry, size *int64) (CloudEntry, error) {
	in := row.Input
	if row.Folder {
		for _, entry := range page.Entries {
			if entry.ID == in.EntryID && entry.Folder && entry.Name == in.Name {
				return entry, nil
			}
		}
		return CloudEntry{}, errors.New("光鸭来源文件夹已变化")
	}
	return selectGuangyaCopySource(page, in, size)
}

func selectGuangyaCopySource(page FilePage, in MoveFileInput, size *int64) (CloudEntry, error) {
	for _, entry := range page.Entries {
		if entry.ID == in.EntryID && !entry.Folder && entry.Name == in.Name && entry.Size != nil &&
			*entry.Size >= 0 && *entry.Size <= guangyaCopyMaxBytes &&
			(size == nil || *entry.Size == *size) {
			return entry, nil
		}
	}
	return CloudEntry{}, errors.New("光鸭原文件已变化、大小未知或超过 2 GiB")
}

func (m *Manager) ValidateGuangyaCopyBatch(ctx context.Context, rows []MoveBatchEntry) error {
	return m.validateNativeCopyBatch(ctx, NewHTTPUploader().Client, rows, "guangya", false)
}

func (m *Manager) ValidateGuangyaDirectCopyBatch(ctx context.Context, rows []MoveBatchEntry) error {
	return m.validateNativeCopyBatch(ctx, NewHTTPUploader().Client, rows, "guangya", true)
}

func guangyaDirectFolderChildren(d diskCopy) ([]string, error) {
	if !validCopyManifest(d) || !validGuangyaCopyTree(d.Manifest, d.Source.ID) {
		return nil, errors.New("光鸭来源目录清单不完整，未复制")
	}
	var ids []string
	for _, item := range d.Manifest[1:] {
		if !strings.Contains(item.Path, "/") {
			ids = append(ids, item.NativeID)
		}
	}
	if len(ids) > 50 {
		return nil, errors.New("光鸭文件夹直接复制最多支持 50 个直接子项目")
	}
	return ids, nil
}

func startGuangyaCopy(ctx context.Context, client *http.Client, a Account, d diskCopy, persist func(diskCopy) error) (CloudEntry, error) {
	if a.Endpoint != GuangyaAPI || !validGuangyaCopyInput(d.Input) {
		return CloudEntry{}, errors.New("光鸭盘内复制来源或目标无效")
	}
	in := d.Input.Input
	if _, _, _, err := verifiedMoveTrails(ctx, client, a, in); err != nil {
		return CloudEntry{}, errors.New("光鸭复制目录层级无法确认，请重新从根目录浏览")
	}
	source, err := readGuangyaCopySource(ctx, client, a, d)
	if err != nil {
		return CloudEntry{}, err
	}
	var sha string
	if d.Input.Folder {
		d.Manifest, sha, err = readGuangyaCopyTree(ctx, client, a, source.ID, in.DestinationID, true)
	} else {
		d.Manifest = []CopyManifestItem{{Size: *source.Size}}
		sha, err = readGuangyaFileHash(ctx, client, a, in.ParentID, source, nil)
	}
	if err != nil {
		return CloudEntry{}, err
	}
	before, err := relayGuangyaPage(ctx, client, a, in.DestinationID)
	if err != nil {
		return CloudEntry{}, err
	}
	for _, entry := range before.Entries {
		if strings.EqualFold(entry.Name, d.Receipt.OutputFolder) {
			return CloudEntry{}, errors.New("光鸭目标已有同名复制子目录")
		}
	}
	proof := guangyaCopyProof{SHA256: sha}
	d.Source, d.Guangya = source, &proof
	d.Submitted = true
	ids := []string{in.EntryID}
	if d.DirectGuangya {
		ids, err = guangyaDirectFolderChildren(d)
		if err != nil {
			return CloudEntry{}, err
		}
	}
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	api := gyClient{client, a}
	parent := in.DestinationID
	if parent == guangyaRoot {
		parent = ""
	}
	var directory struct {
		ID string `json:"fileId"`
	}
	_, err = api.call(ctx, "/userres/v1/file/create_dir", map[string]any{
		"parentId": parent, "dirName": d.Receipt.OutputFolder, "failIfNameExist": true,
	}, &directory)
	if err != nil || !validGuangyaCopyFileID(directory.ID) ||
		directory.ID == in.EntryID || directory.ID == in.ParentID || directory.ID == in.DestinationID ||
		slices.ContainsFunc(d.Manifest, func(item CopyManifestItem) bool { return item.NativeID == directory.ID }) {
		return CloudEntry{}, errors.New("光鸭新子目录受理结果无法确认")
	}
	for _, entry := range before.Entries {
		if entry.ID == directory.ID {
			return CloudEntry{}, errors.New("光鸭返回原有项目 ID，未提交复制")
		}
	}
	proof.FolderID = directory.ID
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	page, err := readGuangyaCopyFolder(ctx, client, a, d)
	if err != nil || len(page.Entries) != 0 {
		return CloudEntry{}, errors.New("光鸭新子目录未能确认为空，未提交复制")
	}
	if _, err := readGuangyaCopySource(ctx, client, a, d); err != nil {
		return CloudEntry{}, err
	}
	if d.Input.Folder {
		tree, _, err := readGuangyaCopyTree(ctx, client, a, source.ID, in.DestinationID, false)
		if err != nil || !slices.Equal(tree, d.Manifest) {
			return CloudEntry{}, errors.New("光鸭来源目录清单已变化，未提交复制")
		}
	}
	if len(ids) == 0 {
		return checkGuangyaCopy(ctx, client, a, d)
	}
	var ack struct {
		TaskID string `json:"taskId"`
	}
	_, err = api.call(ctx, "/userres/v1/file/copy_file", map[string]any{
		"fileIds": ids, "parentId": proof.FolderID,
	}, &ack)
	if err != nil || !validGuangyaRestoreTaskID(ack.TaskID) {
		return CloudEntry{}, errors.New("光鸭复制未获得明确任务凭证")
	}
	proof.TaskID = ack.TaskID
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	return checkGuangyaCopy(ctx, client, a, d)
}

func locateGuangyaCopyFolder(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	parent, err := relayGuangyaPage(ctx, client, a, d.Input.Input.DestinationID)
	if err != nil {
		return CloudEntry{}, err
	}
	var match CloudEntry
	for _, entry := range parent.Entries {
		if d.DirectGuangya && strings.EqualFold(entry.Name, d.Receipt.OutputFolder) &&
			(entry.ID != d.Guangya.FolderID || !entry.Folder || entry.Name != d.Receipt.OutputFolder) {
			return CloudEntry{}, errors.New("光鸭目标出现另一个同名项目，无法核对副本")
		}
		if entry.ID == d.Guangya.FolderID && entry.Folder && entry.Name == d.Receipt.OutputFolder {
			match = entry
		}
	}
	if match.ID == "" {
		return CloudEntry{}, errors.New("光鸭复制子目录名称或位置已变化")
	}
	return match, nil
}

func readGuangyaCopyFolder(ctx context.Context, client *http.Client, a Account, d diskCopy) (FilePage, error) {
	folder, err := locateGuangyaCopyFolder(ctx, client, a, d)
	if err != nil {
		return FilePage{}, err
	}
	return relayGuangyaPage(ctx, client, a, folder.ID)
}

func checkGuangyaCopy(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	if a.Kind != "guangya" || a.Endpoint != GuangyaAPI || !validSavedGuangyaCopy(d) || d.Guangya == nil {
		return CloudEntry{}, errors.New("光鸭原复制任务凭证缺失，请核对原目录和新子目录")
	}
	if d.Guangya.TaskID == "" && !guangyaEmptyDirectCopy(d) {
		return CloudEntry{}, errors.New("光鸭原复制任务凭证缺失，请核对原目录和新子目录")
	}
	for attempt := 0; d.Guangya.TaskID != ""; attempt++ {
		done, err := guangyaFileTaskDone(ctx, client, a, d.Guangya.TaskID)
		if err != nil {
			return CloudEntry{}, err
		}
		if done {
			break
		}
		if attempt >= 2 {
			return CloudEntry{}, errors.New("光鸭复制任务仍在运行，请稍后核对")
		}
		select {
		case <-ctx.Done():
			return CloudEntry{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if d.DirectGuangya {
		target, err := locateGuangyaCopyFolder(ctx, client, a, d)
		if err != nil {
			return CloudEntry{}, err
		}
		return checkGuangyaCopiedTree(ctx, client, a, d, target)
	}
	page, err := readGuangyaCopyFolder(ctx, client, a, d)
	if err != nil || len(page.Entries) != 1 {
		return CloudEntry{}, errors.New("光鸭复制子目录的内容无法确认")
	}
	target := page.Entries[0]
	if d.Input.Folder {
		return checkGuangyaCopiedTree(ctx, client, a, d, target)
	}
	if target.Folder || target.ID == d.Source.ID || target.Name != d.Source.Name || target.Size == nil || *target.Size != *d.Source.Size {
		return CloudEntry{}, errors.New("光鸭副本的标识、名称或大小无法确认")
	}
	source, err := readGuangyaCopySource(ctx, client, a, d)
	if err != nil {
		return CloudEntry{}, err
	}
	for _, item := range []struct {
		parent string
		entry  CloudEntry
	}{{d.Input.Input.ParentID, source}, {d.Guangya.FolderID, target}} {
		sha, err := readGuangyaFileHash(ctx, client, a, item.parent, item.entry, nil)
		if err != nil || sha != d.Guangya.SHA256 {
			return CloudEntry{}, errors.New("光鸭源文件或副本的完整内容无法核对")
		}
	}
	return target, nil
}

func checkGuangyaCopiedTree(ctx context.Context, client *http.Client, a Account, d diskCopy, target CloudEntry) (CloudEntry, error) {
	if !target.Folder || target.ID == d.Source.ID || target.Name != d.Source.Name {
		return CloudEntry{}, errors.New("光鸭副本文件夹身份无法确认")
	}
	source, err := readGuangyaCopySource(ctx, client, a, d)
	if err != nil {
		return CloudEntry{}, err
	}
	original, sha, err := readGuangyaCopyTree(ctx, client, a, source.ID, d.Input.Input.DestinationID, true)
	if err != nil || sha != d.Guangya.SHA256 || !slices.Equal(original, d.Manifest) {
		return CloudEntry{}, errors.New("光鸭来源目录或文件内容已变化")
	}
	copied, sha, err := readGuangyaCopyTree(ctx, client, a, target.ID, "", true)
	if err != nil || sha != d.Guangya.SHA256 || !matchingNativeCopyTrees(d.Manifest, copied) {
		return CloudEntry{}, errors.New("光鸭副本的目录结构或完整内容无法核对")
	}
	return target, nil
}
