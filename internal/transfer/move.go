package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type MoveFileInput struct {
	AccountID        string        `json:"accountId"`
	ParentID         string        `json:"parentId"`
	DestinationID    string        `json:"destinationId"`
	EntryID          string        `json:"entryId"`
	Name             string        `json:"name"`
	ExpectedSize     *int64        `json:"expectedSize,omitempty"`
	SourceTrail      []CloudFolder `json:"sourceTrail,omitempty"`
	DestinationTrail []CloudFolder `json:"destinationTrail,omitempty"`
	restoredSource   *recycleRestoreSource
	restoreReceiptID string
}

func (m *Manager) MoveFile(ctx context.Context, in MoveFileInput) (CloudEntry, error) {
	return m.moveEntry(ctx, in, false, "")
}

func (m *Manager) MoveFolder(ctx context.Context, in MoveFileInput) (CloudEntry, error) {
	return m.moveEntry(ctx, in, true, "")
}

func (m *Manager) moveEntry(ctx context.Context, in MoveFileInput, folder bool, scope string) (CloudEntry, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	if m.closing || m.renaming[in.AccountID] {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("此账号正在操作或程序正在退出，请稍后再移动")
	}
	var account Account
	for _, a := range m.accounts {
		if a.ID == in.AccountID {
			account = a
			break
		}
	}
	if account.ID == "" || scope != "" && scope != downloadScope(account) {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("账号不存在或来源已变化")
	}
	if in.restoreReceiptID != "" {
		op := m.recycleRestores[in.restoreReceiptID]
		if in.restoredSource == nil || op == nil || op.running || op.saved.Result.State != "confirmed" ||
			!recycleRestoreAccount(account, op.saved.Source) || !sameRecycleRestoreEntry(op.saved.Source.Entry, in.restoredSource.Entry) {
			m.mu.Unlock()
			return CloudEntry{}, errors.New("已确认的文件夹还原记录已变化，请刷新后重试")
		}
	}
	for _, t := range m.tasks {
		if t.AccountID == account.ID && (t.Status == "running" || t.Status == "queued" || m.active[t.ID] != nil) {
			m.mu.Unlock()
			return CloudEntry{}, errors.New("请先完成或停止此网盘的上传任务，再移动网盘项目")
		}
	}
	if m.renaming == nil {
		m.renaming = map[string]bool{}
	}
	m.renaming[account.ID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.renaming, account.ID); m.mu.Unlock() }()
	var labels folderMoveLabels
	entry, err := moveAccountEntry(ctx, NewHTTPUploader().Client, account, in, folder, &labels)
	if err != nil || !folder {
		return entry, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// WebDAV IDs are paths. Update defaults and retryable upload destinations,
	// including descendants; completed history retains its original destination.
	changed := false
	if account.Kind == "quark" || account.Kind == "pan123" || account.Kind == "guangya" {
		changed = m.updateMovedIDFolderLocked(in, labels)
	} else {
		changed = m.updateRenamedFolderLocked(RenameInput{AccountID: in.AccountID, EntryID: in.EntryID}, account.Kind, entry.ID)
	}
	if changed {
		if err = m.saveLocked(); err != nil {
			return entry, errors.New("云端文件夹已移动，但本机目录配置保存失败，请勿重复移动；请检查数据目录并重新选择上传位置")
		}
	}
	return entry, nil
}

func MoveAccountFile(ctx context.Context, client *http.Client, a Account, in MoveFileInput) (CloudEntry, error) {
	return moveAccountEntry(ctx, client, a, in, false, nil)
}

func MoveAccountFolder(ctx context.Context, client *http.Client, a Account, in MoveFileInput) (CloudEntry, error) {
	return moveAccountEntry(ctx, client, a, in, true, nil)
}

func moveAccountEntry(ctx context.Context, client *http.Client, a Account, in MoveFileInput, folder bool, labels *folderMoveLabels) (CloudEntry, error) {
	idFolder := folder && (a.Kind == "quark" || a.Kind == "pan123" || a.Kind == "guangya")
	if folder {
		if a.Kind != "ctdav" && a.Kind != "webdav" && a.Kind != "baidu" && !idFolder {
			return CloudEntry{}, errors.New("此网盘暂未接入文件夹整体移动")
		}
		if !idFolder && (!validBaiduPath(in.ParentID) || !validBaiduPath(in.DestinationID) || !validBaiduPath(in.EntryID) || in.EntryID == "/" || path.Dir(in.EntryID) != in.ParentID || path.Base(in.EntryID) != in.Name) {
			return CloudEntry{}, errors.New("文件夹路径无效，请刷新列表")
		}
		if idFolder && (!validIDFolder(a.Kind, in.ParentID) || !validIDFolder(a.Kind, in.DestinationID) || !validIDFolder(a.Kind, in.EntryID) || in.EntryID == cloudRoot(a.Kind)) {
			return CloudEntry{}, errors.New("文件夹ID无效，请刷新列表")
		}
		if in.DestinationID == in.EntryID || (!idFolder && strings.HasPrefix(in.DestinationID, in.EntryID+"/")) {
			return CloudEntry{}, errors.New("不能将文件夹移入自身或其子目录")
		}
	}
	switch a.Kind {
	case "lanzou", "quark", "pan123", "baidu", "guangya", "ctdav", "webdav":
	default:
		return CloudEntry{}, errors.New("此网盘暂不支持移动文件")
	}
	if in.ParentID == "" || in.DestinationID == "" || in.EntryID == "" || in.Name == "" {
		return CloudEntry{}, errors.New("移动目标不完整，请刷新列表")
	}
	if in.ParentID == in.DestinationID {
		return CloudEntry{}, errors.New("请选择其他目标目录")
	}
	list := func(id string) (FilePage, error) { return ListAccountFiles(ctx, client, a, id) }
	var oldLabel, newLabel string
	if idFolder {
		var err error
		list, oldLabel, newLabel, err = verifiedMoveTrails(ctx, client, a, in)
		if err != nil {
			return CloudEntry{}, err
		}
	}
	before, e := list(in.ParentID)
	if e != nil {
		return CloudEntry{}, e
	}
	var source CloudEntry
	count := 0
	for _, f := range before.Entries {
		if f.ID == in.EntryID && f.Folder == folder {
			source = f
			count++
		}
	}
	if count != 1 || source.Name != in.Name {
		return CloudEntry{}, errors.New("目标已变化、不在当前目录或类型不符，请刷新后重试")
	}
	if in.ExpectedSize != nil && (folder || a.Kind != "pan123" || *in.ExpectedSize < 0 ||
		source.Size == nil || *source.Size != *in.ExpectedSize) {
		return CloudEntry{}, errors.New("还原文件大小已变化或无法核对，未提交移动")
	}
	if in.restoredSource != nil {
		receipt := *in.restoredSource
		if folder {
			if restoredFolderID(receipt) != source.ID || a.Kind == "baidu" && source.ObjectID != receipt.Entry.ID {
				return CloudEntry{}, errors.New("已还原文件夹身份已变化，未提交移动")
			}
		} else {
			restoredFile := restoredFileForMove(receipt)
			if restoredFile == nil || source.Size == nil || *source.Size != restoredFile.Size ||
				restoredFile.ObjectID != "" && source.ObjectID != restoredFile.ObjectID {
				return CloudEntry{}, errors.New("还原文件大小或身份已变化，未提交移动")
			}
		}
	}
	if source.Name == "." || source.Name == ".." || strings.ContainsAny(source.Name, "/\\\x00\r\n") {
		return CloudEntry{}, errors.New("网盘文件名格式无效，未发送移动请求")
	}
	if idFolder && labels != nil {
		*labels = folderMoveLabels{Old: oldLabel + " / " + source.Name, New: newLabel + " / " + source.Name}
	}
	destination, e := list(in.DestinationID)
	if e != nil {
		return CloudEntry{}, e
	}
	for _, f := range destination.Entries {
		if f.Name == source.Name || (in.ExpectedSize != nil || in.restoredSource != nil) && strings.EqualFold(f.Name, source.Name) {
			return CloudEntry{}, errors.New("目标目录已有同名文件或文件夹，未移动；请选择其他目录")
		}
	}
	if in.restoredSource != nil {
		match, _, err := recycleRestoreOriginal(ctx, client, a, *in.restoredSource)
		if err != nil || !match {
			return CloudEntry{}, errors.New("无法在原目录重新核对还原项目，未提交移动")
		}
	} else if in.ExpectedSize != nil {
		match, _, err := pan123RestoreOriginal(ctx, client, a, recycleRestoreSource{Entry: RecycleEntry{
			ID: in.EntryID, Name: in.Name, Kind: "file", ParentID: in.ParentID, Size: in.ExpectedSize,
		}})
		if err != nil || !match {
			return CloudEntry{}, errors.New("无法在原目录重新核对还原文件，未提交移动")
		}
	}
	uncertain := errors.New("移动结果未确认，请先刷新原目录和目标目录核对，再决定是否重试 [MOVE/0.13.0]")
	if folder {
		uncertain = errors.New("文件夹移动结果未确认，请先核对原目录和目标目录及内容，再决定是否重试 [MOVE-FOLDER/0.17.0]")
	}
	targetID := source.ID
	switch a.Kind {
	case "lanzou":
		e = lanzouEditRequest(ctx, client, a, url.Values{"task": {"20"}, "file_id": {source.ID}, "folder_id": {in.DestinationID}}, nil)
	case "quark":
		_, e = newQuarkClient(client, a).request(ctx, "POST", "/file/move", nil, map[string]any{"action_type": 1, "exclude_fids": []string{}, "filelist": []string{source.ID}, "to_pdir_fid": in.DestinationID}, nil)
	case "pan123":
		e = (pan123Client{client, a}).request(ctx, "POST", "/file/mod_pid", nil, map[string]any{"fileIdList": []map[string]string{{"FileId": source.ID}}, "parentFileId": in.DestinationID}, nil)
	case "guangya":
		pid := in.DestinationID
		if pid == guangyaRoot {
			pid = ""
		}
		_, e = (gyClient{client, a}).call(ctx, "/userres/v1/file/move_file", map[string]any{"fileIds": []string{source.ID}, "parentId": pid}, nil)
	case "baidu":
		c := newBaiduClient(client, a)
		if e = c.session(ctx); e != nil {
			return CloudEntry{}, e
		}
		items, _ := json.Marshal([]map[string]string{{"path": source.ID, "dest": in.DestinationID, "newname": source.Name, "ondup": "fail"}})
		e = c.api(ctx, "POST", "filemanager", url.Values{"opera": {"move"}}, url.Values{"async": {"0"}, "ondup": {"fail"}, "filelist": {string(items)}}, nil)
		targetID = path.Join(in.DestinationID, source.Name)
	case "ctdav", "webdav":
		base, _ := url.Parse(a.Endpoint)
		prefix := strings.TrimRight(base.Path, "/")
		src := *base
		src.Path = prefix + source.ID
		src.RawPath = ""
		dst := *base
		targetID = path.Join(in.DestinationID, source.Name)
		dst.Path = prefix + targetID
		dst.RawPath = ""
		if folder {
			src.Path += "/"
			dst.Path += "/"
		}
		req, err := http.NewRequestWithContext(ctx, "MOVE", src.String(), nil)
		if err != nil {
			return CloudEntry{}, errors.New("WebDAV移动地址无效")
		}
		req.SetBasicAuth(a.Username, a.Secret)
		req.Header.Set("Destination", dst.String())
		req.Header.Set("Overwrite", "F")
		if folder {
			req.Header.Set("Depth", "infinity")
		}
		resp, err := client.Do(req)
		if err != nil {
			e = errors.New("WebDAV移动连接中断")
		} else {
			resp.Body.Close()
			if folder && resp.StatusCode == http.StatusMultiStatus {
				return CloudEntry{}, errors.New("文件夹移动返回部分结果，请核对原目录及目标目录的内容；程序未自动重试 [MOVE-FOLDER/0.17.0]")
			}
			if resp.StatusCode != 201 && resp.StatusCode != 204 {
				e = fmt.Errorf("WebDAV移动接口 HTTP=%d", resp.StatusCode)
			}
		}
	}
	operationErr := e
	attempts := 20
	if operationErr != nil {
		attempts = 1
	}
	// An acknowledgement or a task ID is not completion. Check both sides.
	// Even a lost response gets one read-only reconciliation, never a replay.
	for attempt := 0; attempt < attempts; attempt++ {
		after, e := ListAccountFiles(ctx, client, a, in.ParentID)
		if e != nil {
			return CloudEntry{}, fmt.Errorf("%w；读取原目录：%v", uncertain, e)
		}
		next, e := ListAccountFiles(ctx, client, a, in.DestinationID)
		if e != nil {
			return CloudEntry{}, fmt.Errorf("%w；读取目标目录：%v", uncertain, e)
		}
		oldRemains := false
		var matches []CloudEntry
		for _, f := range after.Entries {
			if f.Folder == folder && f.ID == source.ID ||
				folder && in.restoredSource != nil && source.ObjectID != "" && f.ObjectID == source.ObjectID {
				oldRemains = true
			}
		}
		for _, f := range next.Entries {
			if f.Folder == folder && f.ID == targetID && f.Name == source.Name && (folder || source.Size == nil || f.Size == nil || *source.Size == *f.Size) {
				matches = append(matches, f)
			}
		}
		if !oldRemains && len(matches) == 1 {
			if in.restoredSource == nil {
				return matches[0], nil
			}
			targetRestore := restoredMoveTarget(*in.restoredSource, in.DestinationID)
			match, _, err := recycleRestoreOriginal(ctx, client, a, targetRestore)
			if err != nil {
				return CloudEntry{}, fmt.Errorf("%w；无法复查目标项目身份", uncertain)
			}
			if match {
				return matches[0], nil
			}
		}
		if len(matches) > 1 {
			break
		}
		if attempt+1 < attempts {
			select {
			case <-ctx.Done():
				return CloudEntry{}, uncertain
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	if operationErr != nil {
		return CloudEntry{}, fmt.Errorf("%w；%v", uncertain, operationErr)
	}
	return CloudEntry{}, uncertain
}
