package transfer

import (
	"context"
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

type CopyReceipt struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"accountId"`
	Name          string    `json:"name"`
	Destination   string    `json:"destination"`
	OutputFolder  string    `json:"outputFolder,omitempty"`
	State         string    `json:"state"`
	Message       string    `json:"message"`
	Created       time.Time `json:"created"`
	CanCheck      bool      `json:"canCheck"`
	CanOpenResult bool      `json:"canOpenResult"`
}
type diskCopy struct {
	Scope         string             `json:"scope"`
	Direct123     bool               `json:"direct123,omitempty"`
	DirectGuangya bool               `json:"directGuangya,omitempty"`
	Input         MoveBatchEntry     `json:"input"`
	Source        CloudEntry         `json:"source"`
	Manifest      []CopyManifestItem `json:"manifest"`
	Submitted     bool               `json:"submitted"`
	Receipt       CopyReceipt        `json:"receipt"`
	Pan123        *pan123CopyProof   `json:"pan123,omitempty"`
	Guangya       *guangyaCopyProof  `json:"guangya,omitempty"`
	Lanzou        *lanzouCopyProof   `json:"lanzou,omitempty"`
}
type copyOperation struct {
	saved   diskCopy
	running bool
}

func copyKey(d diskCopy) string {
	return d.Scope + "\x00" + d.Input.Input.EntryID + "\x00" + d.Input.Input.DestinationID
}
func validCopyInput(in MoveBatchEntry) bool {
	r := in.Input
	return r.AccountID != "" && validBaiduPath(r.EntryID) && r.EntryID != "/" && validBaiduPath(r.ParentID) && validBaiduPath(r.DestinationID) && path.Dir(r.EntryID) == r.ParentID && path.Base(r.EntryID) == r.Name && r.ParentID != r.DestinationID
}

func validSavedCopy(d diskCopy) bool {
	if d.Receipt.OutputFolder == lanzouCopyFolderPrefix+d.Receipt.ID {
		return validSavedLanzouCopy(d)
	}
	if d.Lanzou != nil {
		return false
	}
	if d.DirectGuangya {
		return validSavedGuangyaCopy(d)
	}
	if d.Direct123 && !d.Input.Folder {
		return validSavedPan123DirectCopy(d)
	}
	if d.Direct123 && (d.Receipt.OutputFolder != d.Input.Input.Name || d.Receipt.OutputFolder == "") {
		return false
	}
	if d.Receipt.OutputFolder == "" {
		return d.Pan123 == nil && d.Guangya == nil && validCopyInput(d.Input)
	}
	if !d.Direct123 && d.Receipt.OutputFolder == guangyaCopyFolderPrefix+d.Receipt.ID {
		return validSavedGuangyaCopy(d)
	}
	if d.Guangya != nil || !validPan123CopyInput(d.Input) || !d.Direct123 && d.Receipt.OutputFolder != pan123CopyFolderPrefix+d.Receipt.ID ||
		validateCloudFolderName(d.Receipt.OutputFolder) != nil {
		return false
	}
	p := d.Pan123
	if !d.Submitted {
		return p == nil
	}
	if p == nil || p.FileID != "" || (!d.Input.Folder && !validRelayVersion("pan123", p.MD5)) || (d.Input.Folder && p.MD5 != "") ||
		p.FolderID != "" && (!valid123Folder(p.FolderID) || p.FolderID == "0" ||
			p.FolderID == d.Input.Input.EntryID || p.FolderID == d.Input.Input.ParentID || p.FolderID == d.Input.Input.DestinationID) ||
		p.TaskID != "" && (!valid123Folder(p.TaskID) || p.TaskID == "0" || p.Immediate) {
		return false
	}
	if p.FolderID == "" && (p.Immediate || p.TaskID != "") {
		return false
	}
	return d.Receipt.State != "confirmed" || p.Immediate || p.TaskID != ""
}
func validCopyManifest(d diskCopy) bool {
	if len(d.Manifest) < 1 || len(d.Manifest) > 501 || d.Source.ID != d.Input.Input.EntryID || d.Source.Name != d.Input.Input.Name || d.Source.Folder != d.Input.Folder {
		return false
	}
	if d.Manifest[0].Path != "" || d.Manifest[0].Folder != d.Input.Folder {
		return false
	}
	if !d.Input.Folder {
		return len(d.Manifest) == 1 && d.Source.Size != nil && *d.Source.Size >= 0 && d.Manifest[0].Size == *d.Source.Size
	}
	if (d.Pan123 != nil || d.Direct123) && !validPan123CopyTree(d.Manifest, d.Source.ID) {
		return false
	}
	dirs := map[string]bool{"": true}
	seen := map[string]bool{"": true}
	for _, r := range d.Manifest[1:] {
		parent := path.Dir(r.Path)
		if parent == "." {
			parent = ""
		}
		if r.Path == "" || seen[r.Path] || path.Clean("/"+r.Path) != "/"+r.Path || !dirs[parent] || r.Size < 0 {
			return false
		}
		seen[r.Path] = true
		if r.Folder {
			dirs[r.Path] = true
		}
	}
	return true
}
func (m *Manager) restoreCopies(rows []diskCopy) error {
	if len(rows) > 1000 {
		return errors.New("复制记录数量异常")
	}
	m.copies = map[string]*copyOperation{}
	seen := map[string]bool{}
	for _, d := range rows {
		r := d.Receipt
		if !validSavedCopy(d) || d.Scope == "" || r.ID == "" || r.Created.IsZero() || r.AccountID != d.Input.Input.AccountID || r.Name != d.Input.Input.Name || r.Destination != d.Input.Input.DestinationID || m.copies[r.ID] != nil || seen[copyKey(d)] || d.Submitted && !validCopyManifest(d) {
			return errors.New("复制记录损坏或重复，未覆盖原状态")
		}
		seen[copyKey(d)] = true
		switch r.State {
		case "working", "unknown":
			if d.Submitted {
				r.State = "unknown"
				r.Message = "上次复制结果待核对，只检查原结果，不重新发送"
			} else {
				r.State = "not_sent"
				r.Message = "上次尚未提交复制"
			}
		case "confirmed":
			if !d.Submitted {
				return errors.New("复制记录状态矛盾")
			}
		case "not_sent":
			if d.Submitted {
				return errors.New("复制记录状态矛盾")
			}
		default:
			return errors.New("复制记录状态无效")
		}
		r.CanCheck = false
		d.Receipt = r
		m.copies[r.ID] = &copyOperation{saved: d}
	}
	return nil
}
func copyOperationTimeout(a Account, folder bool) time.Duration {
	if a.Kind == "guangya" && a.Endpoint == GuangyaAPI {
		// A 2 GiB copy can require three full reads, including preflight.
		return 4 * time.Hour
	}
	if a.Kind == "lanzou" {
		if folder {
			return 2 * time.Hour
		}
		return 30 * time.Minute
	}
	return time.Minute
}

func (m *Manager) CopyEntry(ctx context.Context, row MoveBatchEntry) (CloudEntry, error) {
	return m.copyEntry(ctx, row, "")
}

func (m *Manager) CancelCopy(accountID, entryID, destinationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeCopy == nil || m.copyCancel == nil {
		return false
	}
	in := m.activeCopy.saved.Input.Input
	if in.AccountID != accountID || in.EntryID != entryID || in.DestinationID != destinationID {
		return false
	}
	m.copyCancel()
	return true
}

func (m *Manager) CopyPan123FileToDirectory(ctx context.Context, row MoveBatchEntry) (CloudEntry, error) {
	if row.Folder {
		return CloudEntry{}, errors.New("此入口仅支持 123 文件秒传复制")
	}
	return m.copyEntry(ctx, row, "pan123")
}

func (m *Manager) CopyPan123FolderToDirectory(ctx context.Context, row MoveBatchEntry) (CloudEntry, error) {
	if !row.Folder || validateCloudFolderName(row.Input.Name) != nil {
		return CloudEntry{}, errors.New("123 直接复制需要一个有效名称的文件夹")
	}
	return m.copyEntry(ctx, row, "pan123")
}

func (m *Manager) CopyGuangyaFolderToDirectory(ctx context.Context, row MoveBatchEntry) (CloudEntry, error) {
	if !row.Folder || validateCloudFolderName(row.Input.Name) != nil {
		return CloudEntry{}, errors.New("光鸭直接复制需要一个有效名称的文件夹")
	}
	return m.copyEntry(ctx, row, "guangya")
}

func (m *Manager) copyEntry(ctx context.Context, row MoveBatchEntry, directKind string) (CloudEntry, error) {
	if !validCopyInput(row) && !validPan123CopyInput(row) && !validGuangyaCopyInput(row) && !validLanzouCopyInput(row) {
		return CloudEntry{}, errors.New("复制项目无效，请刷新")
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(row.Input.AccountID)
	if m.closing || a.ID == "" {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("账号不可用")
	}
	native123 := a.Kind == "pan123" && a.Endpoint == Pan123API
	nativeGuangya := a.Kind == "guangya" && a.Endpoint == GuangyaAPI
	nativeLanzou := a.Kind == "lanzou" && a.Endpoint == "https://up.woozooo.com"
	direct123 := directKind == "pan123"
	directGuangya := directKind == "guangya"
	if direct123 && (!native123 || !validPan123CopyInput(row)) {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("直接复制仅支持同一 123 账号的项目")
	}
	if directGuangya && (!nativeGuangya || !validGuangyaCopyInput(row)) {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("直接复制仅支持同一光鸭账号的文件夹")
	}
	if !(native123 && validPan123CopyInput(row) || nativeGuangya && validGuangyaCopyInput(row) || nativeLanzou && validLanzouCopyInput(row) ||
		(a.Kind == "baidu" || a.Kind == "ctdav" || a.Kind == "webdav") && validCopyInput(row)) {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("此网盘不支持复制")
	}
	d := diskCopy{Scope: downloadScope(a), Direct123: direct123, Input: row, Receipt: CopyReceipt{ID: newID(), AccountID: a.ID, Name: row.Input.Name, Destination: row.Input.DestinationID, State: "working", Message: "正在预检查，尚未提交", Created: time.Now()}}
	d.DirectGuangya = directGuangya
	if (direct123 || directGuangya) && row.Folder {
		d.Receipt.OutputFolder = row.Input.Name
	} else if native123 && !direct123 {
		d.Receipt.OutputFolder = pan123CopyFolderPrefix + d.Receipt.ID
	} else if nativeGuangya {
		d.Receipt.OutputFolder = guangyaCopyFolderPrefix + d.Receipt.ID
	} else if nativeLanzou {
		d.Receipt.OutputFolder = lanzouCopyFolderPrefix + d.Receipt.ID
	}
	for _, op := range m.copies {
		if copyKey(op.saved) == copyKey(d) {
			m.mu.Unlock()
			return CloudEntry{}, errors.New("已有同源同目标复制记录，请先检查原结果；已确认或未提交记录可清理后再操作")
		}
	}
	if len(m.copies) >= 1000 {
		m.mu.Unlock()
		return CloudEntry{}, errors.New("复制记录已达1000条，请清理已完成记录")
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return CloudEntry{}, e
	}
	op := &copyOperation{saved: d, running: true}
	if m.copies == nil {
		m.copies = map[string]*copyOperation{}
	}
	m.copies[d.Receipt.ID] = op
	if e := m.saveLocked(); e != nil {
		delete(m.copies, d.Receipt.ID)
		delete(m.renaming, a.ID)
		m.mu.Unlock()
		return CloudEntry{}, errors.New("复制记录保存失败，未提交")
	}
	ctx, cancel := context.WithTimeout(ctx, copyOperationTimeout(a, row.Folder))
	m.copyCancel = cancel
	m.activeCopy = op
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	checkpoint := func(next diskCopy) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		previous := op.saved
		if !previous.Submitted && ctx.Err() != nil {
			return ctx.Err()
		}
		next.Submitted = true
		next.Receipt.State = "unknown"
		next.Receipt.Message = "提交检查点已保存，结果待核对"
		if next.Pan123 != nil {
			proof := *next.Pan123
			next.Pan123 = &proof
		}
		if next.Guangya != nil {
			proof := *next.Guangya
			next.Guangya = &proof
		}
		if next.Lanzou != nil {
			proof := *next.Lanzou
			next.Lanzou = &proof
		}
		op.saved = next
		if e := m.saveLocked(); e != nil {
			op.saved = previous
			return errors.New("复制检查点保存失败，已停止后续请求")
		}
		return nil
	}
	client := NewHTTPUploader().Client
	var result CloudEntry
	var err error
	if direct123 && !row.Folder {
		result, err = startPan123DirectCopy(ctx, client, a, d, checkpoint)
	} else if native123 {
		result, err = startPan123Copy(ctx, client, a, d, checkpoint)
	} else if nativeGuangya {
		result, err = startGuangyaCopy(ctx, client, a, d, checkpoint)
	} else if nativeLanzou {
		result, err = startLanzouCopy(ctx, client, a, d, checkpoint)
	} else {
		result, err = copyAccountEntry(ctx, client, a, row, func(source CloudEntry, manifest []CopyManifestItem) error {
			d.Source, d.Manifest = source, manifest
			return checkpoint(d)
		})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cancelled := err != nil && errors.Is(ctx.Err(), context.Canceled)
	if cancelled {
		err = errors.New("已停止本机复制处理")
	}
	cancel()
	m.copyCancel = nil
	m.activeCopy = nil
	delete(m.renaming, a.ID)
	op.running = false
	if err == nil {
		op.saved.Receipt.State = "confirmed"
		op.saved.Receipt.Message = "源和目标清单一致：位置、类型、文件大小及空目录已核对（不含内容哈希）"
		if native123 {
			op.saved.Receipt.Message = "源和副本的目录、项目类型、文件大小及网盘 MD5 已核对"
		} else if nativeGuangya || nativeLanzou {
			op.saved.Receipt.Message = "源文件和副本的目录、名称、大小及完整内容 SHA256 已核对"
		}
	} else if op.saved.Submitted {
		op.saved.Receipt.State = "unknown"
		op.saved.Receipt.Message = "结果待核对，只检查原结果，不重复复制：" + err.Error()
	} else {
		op.saved.Receipt.State = "not_sent"
		op.saved.Receipt.Message = "未提交复制：" + err.Error()
	}
	if m.saveLocked() != nil {
		if op.saved.Submitted {
			op.saved.Receipt.State = "unknown"
			op.saved.Receipt.Message = "最终记录保存失败，原复制结果待核对，不要重复提交"
		}
		return CloudEntry{}, errors.New("复制最终记录保存失败，请勿重复提交，刷新记录后检查原结果")
	}
	if cancelled && op.saved.Submitted {
		return result, errors.New(op.saved.Receipt.Message)
	}
	return result, err
}

func (m *Manager) CopyHistory(accountID string) []CopyReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []CopyReceipt{}
	a := m.downloadAccountLocked(accountID)
	for _, op := range m.copies {
		if op.saved.Receipt.AccountID != accountID {
			continue
		}
		r := op.saved.Receipt
		r.CanCheck = !op.running && op.saved.Submitted && r.State == "unknown" && downloadScope(a) == op.saved.Scope
		r.CanOpenResult = !op.running && r.State == "confirmed" && downloadScope(a) == op.saved.Scope && copyResultFolderID(op.saved) != ""
		if downloadScope(a) != op.saved.Scope {
			r.State = "unknown"
			r.Message = "账号来源已改变，请在原账号官网核对；此记录不允许复用"
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
	return rows
}

type CopyDestinationLocation struct {
	FolderID    string        `json:"folderId"`
	Breadcrumbs []CloudFolder `json:"breadcrumbs,omitempty"`
}

func (m *Manager) LocateCopyDestination(ctx context.Context, accountID, receiptID string) (CopyDestinationLocation, error) {
	return m.locateCopyDestination(ctx, NewHTTPUploader().Client, accountID, receiptID, false)
}

func (m *Manager) LocateCopyResult(ctx context.Context, accountID, receiptID string) (CopyDestinationLocation, error) {
	return m.locateCopyDestination(ctx, NewHTTPUploader().Client, accountID, receiptID, true)
}

func copyResultFolderID(d diskCopy) string {
	switch {
	case d.Lanzou != nil:
		return d.Lanzou.FolderID
	case d.Guangya != nil:
		return d.Guangya.FolderID
	case d.Pan123 != nil:
		return d.Pan123.FolderID
	default:
		return ""
	}
}

func (m *Manager) locateCopyDestination(ctx context.Context, client *http.Client, accountID, receiptID string, openResult bool) (CopyDestinationLocation, error) {
	m.mu.Lock()
	op := m.copies[receiptID]
	a := m.downloadAccountLocked(accountID)
	if m.closing || a.ID == "" || op == nil || op.running || op.saved.Receipt.AccountID != accountID ||
		op.saved.Receipt.State != "confirmed" || downloadScope(a) != op.saved.Scope {
		m.mu.Unlock()
		return CopyDestinationLocation{}, errors.New("当前账号没有可打开目标目录的已核对复制记录")
	}
	d := op.saved
	m.mu.Unlock()

	in := d.Input.Input
	location := CopyDestinationLocation{FolderID: in.DestinationID}
	if a.Kind == "lanzou" {
		page, err := listLanzouFolders(ctx, client, a, in.DestinationID, true)
		if err != nil {
			return CopyDestinationLocation{}, errors.New("蓝奏复制目标目录层级已变化或无法读取")
		}
		location.Breadcrumbs = page.Breadcrumbs
	}
	if a.Kind == "pan123" || a.Kind == "guangya" {
		root := cloudRoot(a.Kind)
		list, _, _, err := verifiedMoveTrails(ctx, client, a, MoveFileInput{
			ParentID: root, SourceTrail: []CloudFolder{{ID: root}}, DestinationID: in.DestinationID,
			DestinationTrail: in.DestinationTrail,
		})
		if err != nil {
			return CopyDestinationLocation{}, errors.New("复制目标目录层级已变化或无法读取")
		}
		location.Breadcrumbs = []CloudFolder{{ID: root, Name: "根目录"}}
		for i, folder := range in.DestinationTrail[1:] {
			page, err := list(in.DestinationTrail[i].ID)
			if err != nil {
				return CopyDestinationLocation{}, err
			}
			for _, item := range page.Entries {
				if item.ID == folder.ID && item.Folder {
					location.Breadcrumbs = append(location.Breadcrumbs, CloudFolder{ID: item.ID, Name: item.Name})
					break
				}
			}
		}
	}
	if openResult {
		result, err := verifyCopyResultFolder(ctx, client, a, d)
		if err != nil {
			return CopyDestinationLocation{}, err
		}
		location.FolderID = result.ID
		location.Breadcrumbs = append(location.Breadcrumbs, result)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.copies[receiptID] != op || downloadScope(m.downloadAccountLocked(accountID)) != d.Scope {
		return CopyDestinationLocation{}, errors.New("当前账号或复制记录已变化")
	}
	return location, nil
}

func verifyCopyResultFolder(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudFolder, error) {
	id, name := copyResultFolderID(d), d.Receipt.OutputFolder
	if id == "" || name == "" {
		return CloudFolder{}, errors.New("复制记录没有可核对的副本目录")
	}
	expected := CloudFolder{ID: id, Name: name}
	if a.Kind == "lanzou" && d.Lanzou != nil {
		page, err := listLanzouFolders(ctx, client, a, d.Input.Input.DestinationID, true)
		if err != nil {
			return CloudFolder{}, err
		}
		var matches int
		for _, entry := range page.Folders {
			if strings.EqualFold(entry.Name, name) {
				if entry != expected {
					return CloudFolder{}, errors.New("蓝奏副本目录的 ID 或名称已变化")
				}
				matches++
			}
		}
		if matches == 1 {
			return expected, nil
		}
	} else if a.Kind == "pan123" && d.Pan123 != nil || a.Kind == "guangya" && d.Guangya != nil {
		job := diskRelay{
			Input: RelayInput{DestinationID: d.Input.Input.DestinationID, Name: name}, TargetKind: a.Kind,
			TargetFolderID: id, CreatedAck: true,
		}
		if err := relayDirectoryTarget(ctx, client, a, job); err != nil {
			return CloudFolder{}, err
		}
		return expected, nil
	}
	return CloudFolder{}, errors.New("副本目录已移动、改名或无法读取")
}
func (m *Manager) CheckCopy(ctx context.Context, id string) (CopyReceipt, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	op := m.copies[id]
	if m.closing || op == nil {
		m.mu.Unlock()
		return CopyReceipt{}, errors.New("原复制记录不存在")
	}
	a := m.downloadAccountLocked(op.saved.Receipt.AccountID)
	if downloadScope(a) != op.saved.Scope {
		m.mu.Unlock()
		return CopyReceipt{}, errors.New("账号来源已改变，不能检查旧复制")
	}
	if !op.saved.Submitted || op.saved.Receipt.State != "unknown" {
		r := op.saved.Receipt
		m.mu.Unlock()
		return r, nil
	}
	if e := m.reserveShareLocked(a.ID); e != nil {
		m.mu.Unlock()
		return CopyReceipt{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, copyOperationTimeout(a, op.saved.Input.Folder))
	m.copyCancel = cancel
	op.running = true
	m.workers.Add(1)
	d := op.saved
	m.mu.Unlock()
	defer m.workers.Done()
	client := NewHTTPUploader().Client
	var e error
	if d.Direct123 && !d.Input.Folder {
		_, e = checkPan123DirectCopy(ctx, client, a, d)
	} else if d.Pan123 != nil {
		_, e = checkPan123Copy(ctx, client, a, d)
	} else if d.Guangya != nil {
		_, e = checkGuangyaCopy(ctx, client, a, d)
	} else if d.Lanzou != nil {
		_, e = checkLanzouCopy(ctx, client, a, d)
	} else {
		_, e = checkCopySnapshot(ctx, client, a, d.Input, d.Source, d.Manifest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel()
	m.copyCancel = nil
	op.running = false
	delete(m.renaming, a.ID)
	if e == nil {
		op.saved.Receipt.State = "confirmed"
		op.saved.Receipt.Message = "原复制结果已核对，源和目标完整元数据清单匹配；没有发送新复制请求"
		if d.Pan123 != nil {
			op.saved.Receipt.Message = "原复制结果已核对，源和副本的目录、文件大小及网盘 MD5 一致"
		} else if d.Guangya != nil || d.Lanzou != nil {
			op.saved.Receipt.Message = "原复制结果已核对，源和副本的完整内容 SHA256 一致"
		}
	} else {
		op.saved.Receipt.Message = "原结果仍未确认：" + e.Error()
	}
	if m.saveLocked() != nil {
		op.saved.Receipt.State = "unknown"
		return op.saved.Receipt, errors.New("核对记录保存失败，请勿重新提交复制")
	}
	return op.saved.Receipt, nil
}
func (m *Manager) ClearCopyHistory(accountID string) (int, error) {
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, errors.New("程序正在退出")
	}
	a := m.downloadAccountLocked(accountID)
	removed := map[string]*copyOperation{}
	for id, op := range m.copies {
		if op.saved.Receipt.AccountID == accountID && downloadScope(a) == op.saved.Scope && !op.running && (op.saved.Receipt.State == "confirmed" || op.saved.Receipt.State == "not_sent") {
			removed[id] = op
			delete(m.copies, id)
		}
	}
	if len(removed) > 0 && m.saveLocked() != nil {
		for id, op := range removed {
			m.copies[id] = op
		}
		return 0, errors.New("清理保存失败，已保留原记录")
	}
	return len(removed), nil
}
