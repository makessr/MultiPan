package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

func readRelayNativeTargetIDs(ctx context.Context, client *http.Client, a Account, parent, name string) (map[string]bool, error) {
	page, err := relayDirectoryPage(ctx, client, a, parent)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(page.Entries))
	for _, item := range page.Entries {
		if strings.EqualFold(item.Name, name) {
			return nil, errors.New("目标目录已有同名项目")
		}
		ids[item.ID] = true
	}
	return ids, nil
}

func (m *Manager) relayNativeTargetUpload(ctx context.Context, client *http.Client, a Account, d *diskRelay, spool *os.File, progress func(int64)) error {
	parent := relayTargetParent(*d)
	if !validRelayTarget(a, parent) || d.ParentJobID != "" && !validRelayTargetFolderID(a.Kind, parent) {
		return errors.New("中转文件缺少已确认的目标父目录")
	}
	info, err := spool.Stat()
	if err != nil {
		return err
	}
	file := File{Path: spool.Name(), Name: d.Input.Name, Size: d.Input.Size, Modified: info.ModTime().UnixMilli()}
	recordID := func(id string) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		op := m.relayByIDLocked(d.Job.ID)
		if op == nil || !op.Submitted || op.TargetFileID != "" || !validRelayTargetFolderID(a.Kind, id) ||
			id == parent || d.Input.SourceAccountID == d.Input.TargetAccountID && id == d.Input.EntryID ||
			downloadScope(m.downloadAccountLocked(a.ID)) != d.TargetScope {
			return errors.New("上传返回的文件 ID 无法保存，保持待核对")
		}
		op.TargetFileID = id
		if err := m.saveLocked(); err != nil {
			op.TargetFileID = ""
			return errors.New("上传结果保存失败，保持待核对")
		}
		d.TargetFileID = id
		return nil
	}
	uploader := &HTTPUploader{Client: client}
	if a.Kind == "quark" {
		return uploader.quarkUpload(ctx, a, file, parent, spool, progress, recordID)
	}
	return uploader.guangyaUpload(ctx, a, file, parent, spool, progress, recordID)
}

func relayNativeTargetReadback(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	parent := relayTargetParent(d)
	if !validRelayTargetFolderID(a.Kind, d.TargetFileID) || !validRelayTarget(a, parent) ||
		d.ParentJobID != "" && !validRelayTargetFolderID(a.Kind, parent) || d.TargetFileID == parent {
		return errors.New("缺少已保存的上传文件 ID，请到目标盘核对")
	}
	page, err := relayDirectoryPage(ctx, client, a, parent)
	if err != nil {
		return err
	}
	var found CloudEntry
	for _, entry := range page.Entries {
		if !strings.EqualFold(entry.Name, d.Input.Name) {
			continue
		}
		if found.ID != "" || entry.ID != d.TargetFileID || entry.Name != d.Input.Name ||
			entry.Folder || entry.Size == nil || *entry.Size != d.Input.Size {
			return errors.New("目标文件 ID、名称或大小无法核对")
		}
		found = entry
	}
	if found.ID == "" {
		return errors.New("目标未列出已受理的文件")
	}
	if a.Kind == "quark" {
		d.Input.ParentID, d.Input.EntryID = parent, found.ID
		d.ETag = found.revision + ":" + d.Job.SHA256
		if !validRelayQuarkVersion(d.ETag) {
			return errors.New("夸克目标缺少可靠文件版本")
		}
		if _, err := relayQuarkContent(ctx, client, a, d, io.Discard, nil); err != nil {
			return err
		}
		return relaySource(ctx, client, a, d)
	}
	digest, err := readGuangyaFileHash(ctx, client, a, parent, found, nil)
	if err != nil {
		return err
	}
	if digest != d.Job.SHA256 {
		return errors.New("光鸭目标 SHA256 与中转副本不一致")
	}
	return nil
}
