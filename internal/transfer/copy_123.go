package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const pan123CopyFolderPrefix = "MultiPan-复制-"

type pan123CopyProof struct {
	MD5       string `json:"md5"`
	FolderID  string `json:"folderId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	Immediate bool   `json:"immediate,omitempty"`
	FileID    string `json:"fileId,omitempty"`
}

func validPan123CopyInput(row MoveBatchEntry) bool {
	in := row.Input
	return in.AccountID != "" && relayName(in.Name) &&
		valid123Folder(in.ParentID) && valid123Folder(in.DestinationID) &&
		valid123Folder(in.EntryID) && in.EntryID != "0" && in.EntryID != in.ParentID &&
		in.EntryID != in.DestinationID && in.ParentID != in.DestinationID
}

func readPan123CopySource(ctx context.Context, client *http.Client, a Account, row MoveBatchEntry) (CloudEntry, error) {
	in := row.Input
	page, err := relayDirectoryPage(ctx, client, a, in.ParentID)
	if err != nil {
		return CloudEntry{}, err
	}
	return selectPan123CopySource(page, row)
}

func selectPan123CopySource(page FilePage, input MoveBatchEntry) (CloudEntry, error) {
	in := input.Input
	for _, row := range page.Entries {
		if row.ID == in.EntryID && row.Name == in.Name && row.Folder == input.Folder &&
			(row.Folder || row.Size != nil && *row.Size >= 0 && validRelayVersion("pan123", row.revision)) {
			return row, nil
		}
	}
	return CloudEntry{}, errors.New("123 原项目身份、大小或 MD5 无法确认")
}

// ValidatePan123CopyBatch reads the original selection and destination only.
// The individual copy flow rechecks each source before it creates a folder.
func (m *Manager) ValidatePan123CopyBatch(ctx context.Context, rows []MoveBatchEntry) error {
	return m.validateNativeCopyBatch(ctx, NewHTTPUploader().Client, rows, "pan123", false)
}

func validatePan123CopyBatchSource(ctx context.Context, client *http.Client, a Account, page FilePage, row MoveBatchEntry, direct bool) error {
	source, err := selectPan123CopySource(page, row)
	if err != nil || !row.Folder {
		return err
	}
	manifest, _, err := readPan123CopyTreeForTarget(ctx, client, a, source.ID, row.Input.DestinationID)
	if err != nil || !direct {
		return err
	}
	_, err = pan123DirectFolderChildren(diskCopy{Direct123: true, Input: row, Source: source, Manifest: manifest})
	return err
}

func readPan123CopyTreeForTarget(ctx context.Context, client *http.Client, a Account, sourceID, destinationID string) ([]CopyManifestItem, int64, error) {
	tree, size, err := readPan123CopyTree(ctx, client, a, sourceID)
	if err != nil {
		return nil, 0, err
	}
	for _, item := range tree {
		if item.NativeID == destinationID {
			return nil, 0, errors.New("不能把文件夹复制到其子目录")
		}
	}
	return tree, size, nil
}

func startPan123Copy(ctx context.Context, client *http.Client, a Account, d diskCopy, persist func(diskCopy) error) (CloudEntry, error) {
	in := d.Input.Input
	if a.Endpoint != Pan123API || !validPan123CopyInput(d.Input) {
		return CloudEntry{}, errors.New("123 盘内复制来源或目标无效")
	}
	if _, _, _, err := verifiedMoveTrails(ctx, client, a, in); err != nil {
		return CloudEntry{}, errors.New("123 复制目录层级无法确认，请重新浏览目录")
	}
	source, err := readPan123CopySource(ctx, client, a, d.Input)
	if err != nil {
		return CloudEntry{}, err
	}
	before, err := relayDirectoryPage(ctx, client, a, in.DestinationID)
	if err != nil {
		return CloudEntry{}, err
	}
	for _, child := range before.Entries {
		if strings.EqualFold(child.Name, d.Receipt.OutputFolder) {
			return CloudEntry{}, errors.New("123 目标已有同名复制子目录")
		}
	}
	d.Source = source
	proof := pan123CopyProof{}
	copyType := 0
	var copySize int64
	if d.Input.Folder {
		copyType = 1
		d.Manifest, copySize, err = readPan123CopyTreeForTarget(ctx, client, a, source.ID, in.DestinationID)
		if err != nil {
			return CloudEntry{}, err
		}
	} else {
		copySize = *source.Size
		d.Manifest = []CopyManifestItem{{Size: copySize}}
		proof.MD5 = source.revision
	}
	d.Pan123 = &proof
	entries := []map[string]any{pan123CopyRequestEntry(in.EntryID, in.ParentID, source.Name, proof.MD5, copyType, copySize)}
	if d.Direct123 {
		entries, err = pan123DirectFolderChildren(d)
		if err != nil {
			return CloudEntry{}, err
		}
	}
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	job := diskRelay{Input: RelayInput{DestinationID: in.DestinationID, Name: d.Receipt.OutputFolder}}
	create, err := prepareRelayDirectoryCreate(ctx, client, a, job)
	if err != nil {
		return CloudEntry{}, err
	}
	proof.FolderID, err = create()
	if err != nil {
		return CloudEntry{}, err
	}
	for _, child := range before.Entries {
		if child.ID == proof.FolderID {
			return CloudEntry{}, errors.New("123 返回了原有目录 ID，未复制文件")
		}
	}
	if proof.FolderID == in.EntryID || proof.FolderID == in.ParentID ||
		slices.ContainsFunc(d.Manifest, func(item CopyManifestItem) bool { return item.NativeID == proof.FolderID }) {
		return CloudEntry{}, errors.New("123 返回了来源项目 ID，未复制文件")
	}
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	if err = checkPan123CopyFolder(ctx, client, a, d); err != nil {
		return CloudEntry{}, err
	}
	folder, err := relayDirectoryPage(ctx, client, a, proof.FolderID)
	if err != nil || len(folder.Entries) != 0 {
		return CloudEntry{}, errors.New("123 新复制目录未能确认为空，未复制文件")
	}
	if err = checkPan123Original(ctx, client, a, d); err != nil {
		return CloudEntry{}, err
	}
	if len(entries) == 0 {
		proof.Immediate = true
		if err = persist(d); err != nil {
			return CloudEntry{}, err
		}
		return checkPan123Copy(ctx, client, a, d)
	}
	var ack struct {
		Mode *int        `json:"mode"`
		Task json.Number `json:"taskId"`
	}
	body := map[string]any{
		"fileList":     entries,
		"targetFileId": json.Number(proof.FolderID),
	}
	pan := pan123Client{client, a}
	err = pan.request(ctx, http.MethodPost, "/restful/goapi/v1/file/copy/async", nil, body, &ack)
	if err != nil || ack.Mode == nil {
		return CloudEntry{}, errors.New("123 复制未获得明确受理，请核对新子目录")
	}
	switch {
	case *ack.Mode == 2:
		proof.Immediate = true
	case *ack.Mode == 1 && valid123Folder(string(ack.Task)) && ack.Task != "0":
		proof.TaskID = string(ack.Task)
	default:
		return CloudEntry{}, errors.New("123 复制受理信息缺少可核对的任务")
	}
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	return checkPan123Copy(ctx, client, a, d)
}

func pan123CopyRequestEntry(id, parent, name, md5 string, kind int, size int64) map[string]any {
	return map[string]any{
		"fileId": json.Number(id), "parentFileId": json.Number(parent), "fileName": name,
		"type": kind, "size": size, "etag": md5, "driveId": 0,
	}
}

func pan123DirectFolderChildren(d diskCopy) ([]map[string]any, error) {
	if !validCopyManifest(d) {
		return nil, errors.New("123 来源目录清单不完整，未复制")
	}
	manifest := d.Manifest[1:]
	sizes := make(map[string]int64)
	paths := make(map[string]bool)
	for _, item := range manifest {
		key := strings.ToLower(item.Path)
		if paths[key] {
			return nil, errors.New("123 来源目录包含同名项目，未复制")
		}
		paths[key] = true
		if !item.Folder {
			name, _, _ := strings.Cut(item.Path, "/")
			sizes[name] += item.Size
		}
	}
	var entries []map[string]any
	for _, item := range manifest {
		if strings.Contains(item.Path, "/") {
			continue
		}
		kind := 0
		if item.Folder {
			kind = 1
		}
		entries = append(entries, pan123CopyRequestEntry(item.NativeID, d.Source.ID, item.Path, item.MD5, kind, sizes[item.Path]))
	}
	if len(entries) > 50 {
		return nil, errors.New("123 文件夹直接复制最多支持 50 个直接子项目")
	}
	return entries, nil
}

func checkPan123CopyFolder(ctx context.Context, client *http.Client, a Account, d diskCopy) error {
	j := diskRelay{
		Input:      RelayInput{DestinationID: d.Input.Input.DestinationID, Name: d.Receipt.OutputFolder},
		TargetKind: "pan123", TargetFolderID: d.Pan123.FolderID, CreatedAck: true,
	}
	return relayDirectoryTarget(ctx, client, a, j)
}

func checkPan123Original(ctx context.Context, client *http.Client, a Account, d diskCopy) error {
	row, err := readPan123CopySource(ctx, client, a, d.Input)
	if err != nil {
		return err
	}
	if d.Input.Folder {
		tree, _, err := readPan123CopyTree(ctx, client, a, row.ID)
		if err != nil {
			return err
		}
		if !slices.Equal(tree, d.Manifest) {
			return errors.New("123 原文件夹清单已变化")
		}
	} else if *row.Size != *d.Source.Size || row.revision != d.Pan123.MD5 {
		return errors.New("123 原文件已变化或无法核对")
	}
	return nil
}

func checkPan123Copy(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	proof := d.Pan123
	if a.Endpoint != Pan123API || proof == nil || proof.FolderID == "" || !proof.Immediate && proof.TaskID == "" {
		return CloudEntry{}, errors.New("缺少 123 原复制受理凭据，请按记录中的新子目录核对")
	}
	if !proof.Immediate {
		pan := pan123Client{client, a}
		for i := 0; ; i++ {
			var task struct {
				ID        json.Number `json:"taskId"`
				Status    *int        `json:"status"`
				ErrorCode *int        `json:"errorCode"`
			}
			err := pan.request(ctx, http.MethodGet, "/restful/goapi/v1/file/copy/task", url.Values{"taskId": {proof.TaskID}}, nil, &task)
			if err != nil || task.ID != json.Number(proof.TaskID) || task.Status == nil || task.ErrorCode != nil && *task.ErrorCode != 0 {
				return CloudEntry{}, errors.New("123 原复制任务状态无法核对")
			}
			if *task.Status == 2 {
				break
			}
			if i == 4 || *task.Status != 0 && *task.Status != 1 {
				return CloudEntry{}, errors.New("123 原复制任务尚未确认成功")
			}
			select {
			case <-ctx.Done():
				return CloudEntry{}, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	if err := checkPan123CopyFolder(ctx, client, a, d); err != nil {
		return CloudEntry{}, err
	}
	target := CloudEntry{ID: proof.FolderID, Name: d.Receipt.OutputFolder, Folder: true}
	if !d.Direct123 {
		folder, err := relayDirectoryPage(ctx, client, a, proof.FolderID)
		if err != nil || len(folder.Entries) != 1 {
			return CloudEntry{}, errors.New("123 新目录中的复制结果无法核对")
		}
		target = folder.Entries[0]
	}
	if d.Input.Folder {
		if !target.Folder || target.Name != d.Source.Name {
			return CloudEntry{}, errors.New("123 目标文件夹类型或名称不符")
		}
		tree, _, err := readPan123CopyTree(ctx, client, a, target.ID)
		if err != nil {
			return CloudEntry{}, err
		}
		if !matchingNativeCopyTrees(d.Manifest, tree) {
			return CloudEntry{}, errors.New("123 副本目录结构、文件大小、MD5 或标识不符")
		}
		if err := checkPan123Original(ctx, client, a, d); err != nil {
			return CloudEntry{}, err
		}
		return target, nil
	}
	if target.ID == d.Source.ID || target.Folder || target.Name != d.Source.Name || target.Size == nil ||
		*target.Size != *d.Source.Size || target.revision != proof.MD5 {
		return CloudEntry{}, errors.New("123 副本的标识、名称、大小或 MD5 不符")
	}
	if err := checkPan123Original(ctx, client, a, d); err != nil {
		return CloudEntry{}, err
	}
	return target, nil
}
