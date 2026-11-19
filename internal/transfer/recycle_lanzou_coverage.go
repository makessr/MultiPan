package transfer

import (
	"context"
	"errors"
)

type LanzouRecycleCoverage struct {
	Rows             []LanzouRecycleCoverageRow `json:"rows"`
	CheckedFolders   int                        `json:"checkedFolders"`
	VisibleFolders   int                        `json:"visibleFolders"`
	IncompleteReason string                     `json:"incompleteReason,omitempty"`
}

type LanzouRecycleCoverageRow struct {
	Name       string                       `json:"name"`
	Kind       string                       `json:"kind"`
	Page       int                          `json:"page"`
	State      string                       `json:"state"`
	Entry      *RecycleEntry                `json:"entry,omitempty"`
	Folder     *LanzouRecycleCoverageFolder `json:"folder,omitempty"`
	Limitation string                       `json:"limitation,omitempty"`
}

type LanzouRecycleCoverageFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Manager) PreviewLanzouRecycleCoverage(ctx context.Context, accountID string) (LanzouRecycleCoverage, error) {
	a, err := m.recycleListingAccount(accountID)
	if err != nil || !lanzouRecyclePurgeAccount(a) {
		return LanzouRecycleCoverage{}, errors.New("只能预览当前蓝奏账号的回收站范围")
	}
	client := NewHTTPUploader().Client
	items, err := readLanzouRecycleRows(ctx, client, a)
	if err != nil {
		return LanzouRecycleCoverage{}, err
	}
	// If the folder inventory cannot be read, retain existing local candidates.
	scan := lanzouPurgeFolderScan{files: make(map[string][]lanzouFolderFile), errors: make(map[string]error)}
	members, scanErr := readLanzouPurgeFolderMembers(ctx, client, a, items, &scan)
	if errors.Is(scanErr, errLanzouRecycleLogin) {
		return LanzouRecycleCoverage{}, scanErr
	}
	if ctx.Err() != nil {
		return LanzouRecycleCoverage{}, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(accountID)) != downloadScope(a) {
		return LanzouRecycleCoverage{}, errors.New("账号来源已变化，请重新读取回收站")
	}
	result := LanzouRecycleCoverage{
		Rows:           m.lanzouRecycleCoverageLocked(a, items, members, scan),
		CheckedFolders: len(scan.files),
	}
	for _, item := range items {
		if item.Kind == "folder" {
			result.VisibleFolders++
		}
	}
	switch {
	case errors.Is(scanErr, context.DeadlineExceeded):
		result.IncompleteReason = "timeout"
	case errors.Is(scanErr, errLanzouPurgeFolderLimit):
		result.IncompleteReason = "folder-limit"
	case scanErr != nil:
		result.IncompleteReason = "uncertain-inventory"
	}
	return result, nil
}

func (m *Manager) lanzouRecycleCoverageLocked(a Account, items []RecycleEntry, members map[string]bool, scan lanzouPurgeFolderScan) []LanzouRecycleCoverageRow {
	folders := scan.files
	scope := downloadScope(a)
	history := map[string]bool{}
	for _, op := range m.recyclePurges {
		if op.saved.Source.Scope == scope {
			history[op.saved.Source.Entry.ID] = true
		}
	}
	for _, op := range m.recycleRestores {
		if op.saved.Source.Scope == scope {
			history[op.saved.Source.Entry.ID] = true
		}
	}
	rows := make([]LanzouRecycleCoverageRow, 0, len(items))
	rootNames := lanzouPurgeRootNames(items)
	parentByFile := make(map[string]*LanzouRecycleCoverageFolder)
	if members != nil {
		for _, item := range items {
			if item.Kind != "folder" {
				continue
			}
			folder := &LanzouRecycleCoverageFolder{ID: item.ID, Name: item.Name}
			for _, file := range folders[item.ID] {
				id := "file:" + file.ID
				if members[file.ID] && rootNames[id] == file.Name {
					parentByFile[id] = folder
				}
			}
		}
	}
	for i, item := range items {
		row := LanzouRecycleCoverageRow{Name: item.Name, Kind: item.Kind, Page: i/recyclePageSize + 1, State: "website"}
		if item.Kind == "folder" {
			row.Folder = &LanzouRecycleCoverageFolder{ID: item.ID, Name: item.Name}
		} else {
			row.Folder = parentByFile[item.ID]
		}
		switch {
		case m.pendingRestoreLocked(scope, item.ID), m.pendingPurgeLocked(scope, item.ID):
			row.State = "pending"
		case history[item.ID]:
			row.State = "history"
		default:
			if entry, ok := m.lanzouRestoreOriginLocked(a, item); ok {
				row.State = "candidate"
				row.Entry = &entry
			} else if files, found := folders[item.ID]; (item.Kind == "file" && members != nil) ||
				(found && (len(files) == 0 || members != nil && validLanzouListedPurgeFiles(rootNames, files))) {
				if entry, ok := lanzouListedRecycleEntry(item, item.Kind); ok && (entry.Kind == "folder" || !members[entry.FileID]) {
					if m.lanzouPendingMoveLocked(scope, entry) {
						row.State = "pending"
					} else {
						row.State = "candidate"
						row.Entry = &entry
					}
				}
			}
		}
		if row.State == "website" {
			switch {
			case item.Kind == "folder":
				files, checked := folders[item.ID]
				err := scan.errors[item.ID]
				switch {
				case errors.Is(err, errLanzouRecycleFolderMarkers):
					row.Limitation = "folder-markers"
				case errors.Is(err, context.DeadlineExceeded):
					row.Limitation = "folder-timeout"
				case err != nil:
					row.Limitation = "folder-inventory"
				case !checked:
					row.Limitation = "folder-not-checked"
				case len(files) > 0 && (members == nil || !validLanzouListedPurgeFiles(rootNames, files)):
					row.Limitation = "folder-membership"
				default:
					row.Limitation = "name-unconfirmed"
				}
			case members == nil:
				row.Limitation = "folder-membership"
			case row.Folder != nil:
				row.Limitation = "folder-member"
			default:
				row.Limitation = "name-unconfirmed"
			}
		}
		rows = append(rows, row)
	}
	return rows
}
