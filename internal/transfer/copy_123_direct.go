package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (m *Manager) ValidatePan123DirectCopyBatch(ctx context.Context, rows []MoveBatchEntry) error {
	return m.validateNativeCopyBatch(ctx, NewHTTPUploader().Client, rows, "pan123", true)
}

func checkDirectCopyName(page FilePage, name string) error {
	for _, row := range page.Entries {
		if strings.EqualFold(row.Name, name) {
			return fmt.Errorf("目标已有同名项目「%s」，未提交", name)
		}
	}
	return nil
}

func validSavedPan123DirectCopy(d diskCopy) bool {
	if !d.Direct123 || d.Input.Folder || d.Guangya != nil || d.Receipt.OutputFolder != "" || !validPan123CopyInput(d.Input) {
		return false
	}
	p := d.Pan123
	if !d.Submitted {
		return p == nil
	}
	if p == nil || p.FolderID != "" || p.TaskID != "" || p.Immediate || !validRelayVersion("pan123", p.MD5) {
		return false
	}
	if p.FileID == "" {
		return d.Receipt.State != "confirmed"
	}
	in := d.Input.Input
	return valid123Folder(p.FileID) && p.FileID != "0" && p.FileID != in.EntryID &&
		p.FileID != in.ParentID && p.FileID != in.DestinationID
}

func startPan123DirectCopy(ctx context.Context, client *http.Client, a Account, d diskCopy, persist func(diskCopy) error) (CloudEntry, error) {
	in := d.Input.Input
	if a.Kind != "pan123" || a.Endpoint != Pan123API || !validSavedPan123DirectCopy(d) || d.Submitted {
		return CloudEntry{}, errors.New("123 直接复制来源或目标无效")
	}
	if _, _, _, err := verifiedMoveTrails(ctx, client, a, in); err != nil {
		return CloudEntry{}, errors.New("123 复制目录层级无法确认，请重新浏览")
	}
	source, err := readPan123CopySource(ctx, client, a, d.Input)
	if err != nil {
		return CloudEntry{}, err
	}
	before, err := relayDirectoryPage(ctx, client, a, in.DestinationID)
	if err != nil {
		return CloudEntry{}, err
	}
	if err := checkDirectCopyName(before, source.Name); err != nil {
		return CloudEntry{}, err
	}
	d.Source = source
	d.Pan123 = &pan123CopyProof{MD5: source.revision}
	d.Manifest = []CopyManifestItem{{Size: *source.Size}}
	d.Submitted = true
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	var pre pan123Pre
	err = (pan123Client{client, a}).request(ctx, http.MethodPost, "/file/upload_request", nil, map[string]any{
		"driveId": 0, "duplicate": 1, "RequestSource": nil, "etag": source.revision,
		"fileName": source.Name, "parentFileId": json.Number(in.DestinationID), "size": *source.Size, "type": 0,
	}, &pre)
	if err != nil || !pre.Reuse || pre.Info == nil {
		return CloudEntry{}, errors.New("123 未返回可核对的秒传副本，未发送文件内容，请检查目标目录")
	}
	id, err := validate123Result(*pre.Info, in.DestinationID, File{Name: source.Name, Size: *source.Size}, source.revision)
	if err != nil {
		return CloudEntry{}, err
	}
	for _, row := range before.Entries {
		if row.ID == string(id) {
			return CloudEntry{}, errors.New("123 返回的是目标中已有的项目 ID")
		}
	}
	d.Pan123.FileID = string(id)
	if !validSavedPan123DirectCopy(d) {
		return CloudEntry{}, errors.New("123 未返回独立的副本 ID")
	}
	if err := persist(d); err != nil {
		return CloudEntry{}, err
	}
	return checkPan123DirectCopy(ctx, client, a, d)
}

func checkPan123DirectCopy(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	if a.Kind != "pan123" || a.Endpoint != Pan123API || !d.Submitted || !validSavedPan123DirectCopy(d) ||
		!validCopyManifest(d) || d.Pan123.FileID == "" {
		return CloudEntry{}, errors.New("123 原秒传副本缺少明确 ID，请到目标目录核对")
	}
	in := d.Input.Input
	if _, _, _, err := verifiedMoveTrails(ctx, client, a, in); err != nil {
		return CloudEntry{}, err
	}
	source, err := readPan123CopySource(ctx, client, a, d.Input)
	if err != nil || *source.Size != *d.Source.Size || source.revision != d.Pan123.MD5 {
		return CloudEntry{}, errors.New("123 源文件大小或 MD5 已变化")
	}
	page, err := relayDirectoryPage(ctx, client, a, in.DestinationID)
	if err != nil {
		return CloudEntry{}, err
	}
	for _, row := range page.Entries {
		if row.ID == d.Pan123.FileID && !row.Folder && row.Name == source.Name &&
			row.Size != nil && *row.Size == *source.Size && row.revision == d.Pan123.MD5 {
			return row, nil
		}
	}
	return CloudEntry{}, errors.New("123 目标副本的名称、大小或 MD5 无法确认")
}
