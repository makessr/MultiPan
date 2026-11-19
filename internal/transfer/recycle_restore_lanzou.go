package transfer

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const lanzouRestorePurpose = "lanzou-recycle-restore-v1"
const lanzouListedFileRestorePurpose = "lanzou-recycle-listed-file-restore-v1"

func isLanzouRestorePurpose(purpose string) bool {
	return purpose == lanzouRestorePurpose || purpose == lanzouListedFileRestorePurpose
}

func lanzouListedRestoreFile(row RecycleEntry) (RecycleEntry, bool) {
	return lanzouListedRecycleEntry(row, "file")
}

func lanzouListedRecycleEntry(row RecycleEntry, kind string) (RecycleEntry, bool) {
	id, matches := strings.CutPrefix(row.ID, kind+":")
	// Keep below historical display cutoffs and reject explicit truncation.
	// The filename is used to detect conflicts in the restore destination.
	if !matches || row.Kind != kind || !validLanzouRecycleID(id) ||
		!relayName(row.Name) ||
		len(row.Name) >= 34 || utf8.RuneCountInString(row.Name) >= 17 ||
		strings.Contains(row.Name, "…") || strings.Contains(row.Name, "...") ||
		row.ParentID != "" || row.OriginalPath != "" || row.Size != nil || row.RestoreHandle != "" {
		return row, false
	}
	row.FileID = id
	return row, true
}

// Caller holds Manager.mu.
func (m *Manager) lanzouCanRestoreLocked(a Account, source recycleRestoreSource) bool {
	if source.Purpose == lanzouRestorePurpose {
		row, ok := m.lanzouRestoreOriginLocked(a, source.Entry)
		return ok && sameRecycleRestoreEntry(row, source.Entry)
	}
	return !m.lanzouPendingMoveLocked(source.Scope, source.Entry)
}

// Caller holds Manager.mu.
func (m *Manager) lanzouPendingMoveLocked(scope string, entry RecycleEntry) bool {
	for _, op := range m.trashes {
		d := op.saved
		if d.Source.Scope == scope && d.Source.Original.Kind == entry.Kind &&
			d.Source.Original.ID == entry.FileID && (op.running || d.Receipt.State == "unknown") {
			return true
		}
	}
	return false
}

var errLanzouRestoreContentsPending = errors.New("文件夹已回到原位置，内容与用于核对的清单尚未一致")

type LanzouRecycleRestorePreview struct {
	Evidence string   `json:"evidence"`
	Files    []string `json:"files"`
}

func (m *Manager) PreviewLanzouRecycleRestore(ctx context.Context, token string) (LanzouRecycleRestorePreview, error) {
	h, err := decodeRecycleRestore(token)
	if err != nil {
		return LanzouRecycleRestorePreview{}, err
	}
	if !isLanzouRestorePurpose(h.Purpose) {
		return LanzouRecycleRestorePreview{}, errors.New("只能预检蓝奏回收站的可还原项目")
	}
	m.mu.Lock()
	a := m.downloadAccountLocked(h.AccountID)
	available := !m.closing && recycleRestoreAccount(a, h) && m.lanzouCanRestoreLocked(a, h)
	conflict := m.pendingPurgeLocked(h.Scope, h.Entry.ID) || m.pendingRestoreLocked(h.Scope, h.Entry.ID)
	folder := h.Entry.Kind == "folder"
	var local *lanzouFolderContents
	if available && folder {
		local = m.lanzouFolderInventoryLocked(a, h.Entry.ID, h.Entry.Name).LanzouContents
	}
	m.mu.Unlock()
	if !available {
		return LanzouRecycleRestorePreview{}, errors.New("蓝奏账号或原位置记录已变化，请刷新回收站")
	}
	if conflict {
		return LanzouRecycleRestorePreview{}, errors.New("此项目已有待核对的还原或永久删除，请先检查原结果")
	}
	client := NewHTTPUploader().Client
	page, err := listAccountRecycle(ctx, client, a, h.Page)
	if err != nil {
		return LanzouRecycleRestorePreview{}, err
	}
	match := false
	for _, row := range page.Items {
		if sameRecycleRestoreRow(row, h.Entry) {
			match = true
			break
		}
	}
	if !match || page.Incomplete {
		return LanzouRecycleRestorePreview{}, errors.New("蓝奏回收站原记录已变化或列表不完整，请刷新")
	}
	_, occupied, err := lanzouRestoreLocation(ctx, client, a, h)
	if err != nil {
		return LanzouRecycleRestorePreview{}, err
	}
	if occupied {
		if !folder {
			return LanzouRecycleRestorePreview{}, errors.New("根目录已有同名或同标识项目")
		}
		return LanzouRecycleRestorePreview{}, errors.New("原目录已有同名或同标识项目")
	}
	if err = checkLanzouRecycleConfirmation(ctx, client, a, h.Entry.ID, lanzouRestore); err != nil {
		return LanzouRecycleRestorePreview{}, err
	}
	result := LanzouRecycleRestorePreview{Evidence: "position", Files: []string{}}
	inventory := local
	if !folder {
		result.Evidence = "root"
	} else if local != nil {
		result.Evidence = "local"
	} else if inventory = captureLanzouRecycleRestoreContents(ctx, client, a, h.Entry); inventory != nil {
		result.Evidence = "recycle"
	}
	if err := ctx.Err(); err != nil {
		return LanzouRecycleRestorePreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || !recycleRestoreAccount(m.downloadAccountLocked(a.ID), h) || !m.lanzouCanRestoreLocked(a, h) ||
		m.pendingPurgeLocked(h.Scope, h.Entry.ID) || m.pendingRestoreLocked(h.Scope, h.Entry.ID) ||
		folder && m.lanzouFolderInventoryLocked(a, h.Entry.ID, h.Entry.Name).LanzouContents != local {
		return LanzouRecycleRestorePreview{}, errors.New("核对期间蓝奏账号或原记录已变化，请刷新回收站")
	}
	if inventory != nil {
		for _, file := range inventory.Files {
			result.Files = append(result.Files, file.Name)
		}
	}
	return result, nil
}

func captureLanzouRecycleRestoreContents(ctx context.Context, client *http.Client, a Account, entry RecycleEntry) *lanzouFolderContents {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	rows, err := readLanzouRecycleRows(ctx, client, a)
	if err != nil {
		return nil
	}
	rootNames := lanzouPurgeRootNames(rows)
	if rootNames[entry.ID] != entry.Name {
		return nil
	}
	names, err := readLanzouPurgeFolderNames(ctx, client, a, entry.FileID)
	if err != nil || len(names) > 200 {
		return nil
	}
	files, err := readLanzouFolderFilesWithIDs(ctx, client, a, entry.FileID, names)
	if err != nil || !validLanzouListedPurgeFiles(rootNames, files) {
		return nil
	}
	contents := &lanzouFolderContents{Files: files}
	if !validLanzouFolderContents(contents) {
		return nil
	}
	return contents
}

func checkLanzouRestoredContents(ctx context.Context, client *http.Client, a Account, entry RecycleEntry, inventory *lanzouFolderContents) error {
	page, err := listLanzouFolders(ctx, client, a, entry.FileID, true)
	if err != nil {
		return err
	}
	n := len(page.Breadcrumbs)
	if n < 2 || page.Breadcrumbs[n-2].ID != entry.ParentID ||
		page.Breadcrumbs[n-1].ID != entry.FileID || page.Breadcrumbs[n-1].Name != entry.Name {
		return errors.New("还原后的蓝奏文件夹位置或身份已变化")
	}
	if len(page.Folders) != 0 {
		return errLanzouRestoreContentsPending
	}
	files, err := readLanzouFiles(ctx, client, a, entry.FileID, true)
	if err != nil {
		return err
	}
	if len(files) != len(inventory.Files) {
		return errLanzouRestoreContentsPending
	}
	expected := make(map[string]string, len(inventory.Files))
	for _, file := range inventory.Files {
		expected[file.ID] = file.Name
	}
	for _, file := range files {
		if expected[file.ID] != file.Name {
			return errLanzouRestoreContentsPending
		}
	}
	return nil
}

func validLanzouRestoreEntry(entry RecycleEntry) bool {
	kind, id, ok := strings.Cut(entry.ID, ":")
	return ok && (kind == "file" || kind == "folder") && kind == entry.Kind &&
		validLanzouRecycleID(id) && valid123Folder(id) && entry.FileID == id &&
		validLanzouTrashParent(entry.ParentID) && (kind != "folder" || id != entry.ParentID) &&
		relayName(entry.Name) &&
		entry.Size == nil && entry.RestoreHandle == "" &&
		entry.OriginalPath == lanzouTrashEntry(entry.ParentID, CloudEntry{Name: entry.Name}).OriginalPath
}

// Caller holds Manager.mu. Only a verified local move can supply a directory.
func (m *Manager) lanzouRestoreOriginLocked(a Account, row RecycleEntry) (RecycleEntry, bool) {
	scope := downloadScope(a)
	for _, op := range m.trashes {
		d := op.saved
		original := d.Source.Original
		if !d.Submitted || d.Receipt.State != "confirmed" || !trashAccount(a, d.Source) ||
			d.Source.Scope != scope || row.ID != original.Kind+":"+original.ID || row.Kind != original.Kind || row.Name != original.Name {
			continue
		}
		row.FileID, row.ParentID, row.OriginalPath = original.ID, original.ParentID, original.OriginalPath
		return row, validLanzouRestoreEntry(row)
	}
	return row, false
}

func sameRecycleRestoreRow(row, source RecycleEntry) bool {
	if strings.HasPrefix(source.ID, "file:") || strings.HasPrefix(source.ID, "folder:") {
		return row.ID == source.ID && row.Kind == source.Kind && row.Name == source.Name
	}
	return sameRecycleRestoreEntry(row, source)
}

func lanzouRestoreParent(entry RecycleEntry) string {
	// file_restore has no destination field. Check the expected drive root
	// before submitting and require the restored file to appear there.
	if entry.Kind == "file" {
		return "-1"
	}
	return entry.ParentID
}

func lanzouRestoreLocation(ctx context.Context, client *http.Client, a Account, source recycleRestoreSource) (bool, bool, error) {
	entry := source.Entry
	parent := lanzouRestoreParent(entry)
	folders, err := lanzouTrashFolders(ctx, client, a, parent)
	if err != nil {
		return false, false, err
	}
	files, err := trashLive(ctx, client, a, parent)
	if err != nil {
		return false, false, err
	}
	same, occupied := false, false
	for _, row := range append(folders, files...) {
		identity := row.ID == entry.FileID && row.Kind == entry.Kind
		if !strings.EqualFold(row.Name, entry.Name) && !identity {
			continue
		}
		if occupied || !identity || row.Name != entry.Name {
			return false, true, nil
		}
		same, occupied = true, true
	}
	return same, occupied, nil
}

func submitLanzouRestore(ctx context.Context, client *http.Client, a Account, form url.Values) error {
	body, err := submitLanzouRecycleForm(ctx, client, a, form)
	if err != nil {
		return err
	}
	for _, field := range lanzouRecycleTag.Split(body, -1) {
		message := strings.TrimSuffix(strings.TrimSpace(html.UnescapeString(field)), "。")
		if message == "恢复成功" || message == "还原成功" {
			return nil
		}
	}
	return errors.New("蓝奏未返回明确的还原回执")
}

func submitLanzouRecycleForm(ctx context.Context, client *http.Client, a Account, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lanzouRecycleOrigin+"/mydisk.php?item=recycle", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", userAgent(a))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", lanzouRecycleOrigin+"/mydisk.php?item=recycle")
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return "", errors.New("蓝奏回收站操作响应未确认")
	}
	defer resp.Body.Close()
	const maxBody = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(raw) > maxBody {
		return "", errors.New("蓝奏回收站操作响应未确认")
	}
	return lanzouRecycleHidden.ReplaceAllString(string(raw), ""), nil
}
