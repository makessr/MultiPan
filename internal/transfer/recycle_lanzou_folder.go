package transfer

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	lanzouRecycleImages = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	lanzouRecycleCells  = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td\s*>`)
)

type LanzouRecycleFolderPreview struct {
	RemoteFiles          []string                       `json:"remoteFiles"`
	RemoteFolders        []string                       `json:"remoteFolders"`
	RemoteFolderIDs      []string                       `json:"remoteFolderIds"`
	HasAdditionalEntries bool                           `json:"hasAdditionalEntries"`
	OriginalFiles        []string                       `json:"originalFiles"`
	HasOriginal          bool                           `json:"hasOriginal"`
	Comparison           *LanzouRecycleFolderComparison `json:"comparison,omitempty"`
}

type LanzouRecycleFolderComparison struct {
	Matching     int `json:"matching"`
	OriginalOnly int `json:"originalOnly"`
	CurrentOnly  int `json:"currentOnly"`
}

func (m *Manager) ListLanzouRecycleFolder(ctx context.Context, accountID, displayID, name string) (LanzouRecycleFolderPreview, error) {
	a, err := m.recycleListingAccount(accountID)
	if err != nil || a.Kind != "lanzou" || a.Endpoint != lanzouRecycleOrigin {
		return LanzouRecycleFolderPreview{}, errors.New("只能读取当前蓝奏云账号的回收站文件夹")
	}
	client := NewHTTPUploader().Client
	preview, err := readLanzouRecycleFolder(ctx, client, a, displayID, name)
	if err != nil {
		return LanzouRecycleFolderPreview{}, err
	}
	m.mu.Lock()
	if m.closing || downloadScope(m.downloadAccountLocked(accountID)) != downloadScope(a) {
		m.mu.Unlock()
		return LanzouRecycleFolderPreview{}, errors.New("账号来源已变化，请重新读取回收站")
	}
	receipt := m.lanzouFolderInventoryLocked(a, displayID, name)
	m.mu.Unlock()
	if receipt.LanzouContents == nil {
		return preview, nil
	}
	var comparison *LanzouRecycleFolderComparison
	if !preview.HasAdditionalEntries {
		comparison = compareLanzouFolderInventory(ctx, client, a, receipt.Source.Original.ID, preview.RemoteFiles, receipt.LanzouContents, false)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(accountID)) != downloadScope(a) {
		return LanzouRecycleFolderPreview{}, errors.New("账号来源已变化，请重新读取回收站")
	}
	if current := m.lanzouFolderInventoryLocked(a, displayID, name); current.Receipt.ID != receipt.Receipt.ID {
		return preview, nil
	}
	preview.Comparison = comparison
	preview.HasOriginal = true
	preview.OriginalFiles = make([]string, 0, len(receipt.LanzouContents.Files))
	for _, file := range receipt.LanzouContents.Files {
		preview.OriginalFiles = append(preview.OriginalFiles, file.Name)
	}
	return preview, nil
}

func (m *Manager) lanzouFolderInventoryLocked(a Account, displayID, name string) diskTrash {
	for _, op := range m.trashes {
		d := op.saved
		if d.Source.Purpose != lanzouFolderTrashPurpose || !d.Submitted || d.Receipt.State != "confirmed" ||
			d.LanzouContents == nil || !trashAccount(a, d.Source) ||
			displayID != "folder:"+d.Source.Original.ID || name != d.Source.Original.Name {
			continue
		}
		return d
	}
	return diskTrash{}
}

func compareLanzouFolderInventory(ctx context.Context, client *http.Client, a Account, folderID string, visibleNames []string, inventory *lanzouFolderContents, strictNames bool) *LanzouRecycleFolderComparison {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	files, err := readLanzouFolderFilesWithIDs(ctx, client, a, folderID, visibleNames)
	if err != nil {
		return nil
	}
	seen := make(map[string]string, len(files))
	for _, file := range files {
		seen[file.ID] = file.Name
	}
	comparison := &LanzouRecycleFolderComparison{}
	for _, file := range inventory.Files {
		if name, found := seen[file.ID]; found {
			if strictNames && !matchingLanzouPurgeName(name, file.Name) {
				return nil
			}
			comparison.Matching++
			delete(seen, file.ID)
		} else {
			comparison.OriginalOnly++
		}
	}
	// Strict comparisons also require all visible records to match the inventory.
	if strictNames && len(seen) != 0 {
		return nil
	}
	comparison.CurrentOnly = len(seen)
	return comparison
}

func readLanzouFolderFilesWithIDs(ctx context.Context, client *http.Client, a Account, folderID string, visibleNames []string) ([]lanzouFolderFile, error) {
	unverified := errors.New("蓝奏文件夹内容或文件标识无法核对")
	query := url.Values{"item": {"recycle"}, "action": {"folder_restore"}, "folder_id": {folderID}}
	body, err := readLanzouRecyclePage(ctx, client, a, query)
	if err != nil {
		return nil, err
	}
	if validateLanzouRecycleConfirmation(body, "folder_restore", "folder_id", folderID) != nil {
		return nil, unverified
	}
	names, err := parseLanzouRecycleFolder(body)
	if lanzouRecycleFolderHasChildren(body, folderID) {
		return nil, errLanzouRecycleFolderMarkers
	}
	if err != nil || len(names) != len(visibleNames) {
		return nil, unverified
	}
	nameCounts := map[string]int{}
	for _, name := range visibleNames {
		nameCounts[name]++
	}
	for _, name := range names {
		if nameCounts[name] == 0 {
			return nil, unverified
		}
		nameCounts[name]--
	}
	seen := map[string]bool{}
	files := make([]lanzouFolderFile, 0, len(names))
	for _, link := range lanzouRecycleLink.FindAllStringSubmatch(lanzouRecycleHidden.ReplaceAllString(body, ""), -1) {
		if !lanzouRecycleFileIcon(link[2]) {
			continue
		}
		href, err := url.Parse(lanzouRecycleAttributes(link[1])["href"])
		if err != nil || (href.Scheme != "https" && href.Scheme != "http") || href.User != nil || href.Port() != "" ||
			!lanzouShareHost.MatchString(strings.ToLower(href.Hostname())) || href.RawQuery != "" || href.Fragment != "" || href.RawPath != "" {
			return nil, unverified
		}
		id := strings.TrimPrefix(href.Path, "/")
		if !valid123Folder(id) || id == "0" || seen[id] || len(files) >= len(names) {
			return nil, unverified
		}
		files = append(files, lanzouFolderFile{ID: id, Name: names[len(files)]})
		seen[id] = true
	}
	if len(seen) != len(visibleNames) {
		return nil, unverified
	}
	return files, nil
}

func readLanzouRecycleFolder(ctx context.Context, client *http.Client, a Account, displayID, name string) (LanzouRecycleFolderPreview, error) {
	id, ok := strings.CutPrefix(displayID, "folder:")
	if !ok || !validLanzouRecycleID(id) || name == "" || len(name) > 4096 {
		return LanzouRecycleFolderPreview{}, errors.New("蓝奏回收站文件夹无效")
	}
	root, err := readLanzouRecyclePage(ctx, client, a, url.Values{"item": {"recycle"}, "action": {"files"}})
	if err != nil {
		return LanzouRecycleFolderPreview{}, err
	}
	rows, err := parseLanzouRecycle(root)
	if err != nil {
		return LanzouRecycleFolderPreview{}, err
	}
	for _, row := range rows {
		if row.ID == displayID && row.Kind == "folder" && row.Name == name {
			body, err := readLanzouRecyclePage(ctx, client, a, url.Values{"item": {"recycle"}, "action": {"show_files"}, "folder_id": {id}})
			if err != nil {
				return LanzouRecycleFolderPreview{}, err
			}
			files, err := parseLanzouRecycleFolder(body)
			if err != nil {
				return LanzouRecycleFolderPreview{}, err
			}
			folders, folderIDs := lanzouRecycleFolderRows(body, displayID, lanzouPurgeRootNames(rows))
			return LanzouRecycleFolderPreview{
				RemoteFiles:          files,
				RemoteFolders:        folders,
				RemoteFolderIDs:      folderIDs,
				HasAdditionalEntries: lanzouRecycleFolderHasChildren(body, id),
			}, nil
		}
	}
	return LanzouRecycleFolderPreview{}, errors.New("蓝奏回收站文件夹已变化，请刷新后重选")
}

func lanzouRecycleFolderRows(body, displayID string, rootNames map[string]string) ([]string, []string) {
	names := []string{}
	ids := []string{}
	indices := map[string]int{}
	for _, row := range lanzouRecycleRows.FindAllStringSubmatch(lanzouRecycleHidden.ReplaceAllString(body, ""), -1) {
		entry, err := lanzouRecycleRow(row[1])
		if err != nil || entry.Kind != "folder" || entry.ID == displayID {
			continue
		}
		if index, seen := indices[entry.ID]; seen {
			ids[index] = ""
			continue
		}
		if len(names) >= 1000 {
			continue
		}
		indices[entry.ID] = len(names)
		names = append(names, entry.Name)
		linkID := ""
		if rootNames[entry.ID] == entry.Name && lanzouRecycleHasBrowseLink(row[1], entry.ID) {
			linkID = entry.ID
		}
		ids = append(ids, linkID)
	}
	return names, ids
}

func lanzouRecycleHasBrowseLink(row, displayID string) bool {
	for _, link := range lanzouRecycleLink.FindAllStringSubmatch(row, -1) {
		href, err := url.Parse(lanzouRecycleAttributes(link[1])["href"])
		if err != nil || href.Query().Get("item") != "recycle" || href.Query().Get("action") != "show_files" {
			continue
		}
		if id, ok := lanzouRecycleFolderLink(href); ok && "folder:"+id == displayID {
			return true
		}
	}
	return false
}

func parseLanzouRecycleFolder(body string) ([]string, error) {
	body = lanzouRecycleHidden.ReplaceAllString(body, "")
	visibleName := func(markup string) string {
		return strings.TrimSpace(html.UnescapeString(lanzouRecycleTag.ReplaceAllString(markup, "")))
	}
	bad := errors.New("蓝奏回收站文件夹内容无法确认，请到官网核对")
	files := []string{}
	for _, link := range lanzouRecycleLink.FindAllStringSubmatch(body, -1) {
		if !lanzouRecycleFileIcon(link[2]) {
			continue
		}
		name := visibleName(link[2])
		if name == "" || len(name) > 4096 || strings.ContainsAny(name, "\x00\r\n") || len(files) >= 1000 {
			return nil, bad
		}
		files = append(files, name)
	}
	empty := false
	for _, cell := range lanzouRecycleCells.FindAllStringSubmatch(body, -1) {
		name := visibleName(cell[1])
		if name == "该文件夹里没有包含文件" || name == "此文件夹没有包含文件" {
			empty = true
			break
		}
	}
	if empty != (len(files) == 0) {
		return nil, bad
	}
	return files, nil
}

func lanzouRecycleFileIcon(markup string) bool {
	for _, tag := range lanzouRecycleImages.FindAllString(markup, -1) {
		src := strings.TrimPrefix(lanzouRecycleAttributes(tag)["src"], "/")
		if strings.HasPrefix(src, "filetype/") {
			return true
		}
	}
	return false
}
