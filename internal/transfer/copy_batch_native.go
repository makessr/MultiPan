package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// This preflight is read-only. Each copy still validates its source and
// destination before sending a write.
func (m *Manager) validateNativeCopyBatch(ctx context.Context, client *http.Client, rows []MoveBatchEntry, kind string, direct bool) error {
	limit := 50
	if kind == "lanzou" {
		limit = 20
	}
	if len(rows) < 1 || len(rows) > limit {
		return fmt.Errorf("批量复制请选择 1 至 %d 个项目", limit)
	}
	endpoint, validInput, list := Pan123API, validPan123CopyInput, relayDirectoryPage
	if kind == "guangya" {
		endpoint, validInput, list = GuangyaAPI, validGuangyaCopyInput, relayGuangyaPage
	} else if kind == "lanzou" {
		endpoint, validInput = "https://up.woozooo.com", validLanzouCopyInput
	}
	first := rows[0].Input
	seen := make(map[string]bool, len(rows))
	var directNames []string
	for _, row := range rows {
		in := row.Input
		if !validInput(row) || in.AccountID != first.AccountID || in.ParentID != first.ParentID ||
			in.DestinationID != first.DestinationID || seen[in.EntryID] ||
			!slices.Equal(in.SourceTrail, first.SourceTrail) || !slices.Equal(in.DestinationTrail, first.DestinationTrail) {
			return errors.New("请选择同一来源目录下支持复制的不同项目，并选择同一目标")
		}
		seen[in.EntryID] = true
		if direct {
			if kind == "guangya" && !row.Folder {
				return errors.New("光鸭批量直接复制仅支持文件夹")
			}
			if row.Folder && validateCloudFolderName(in.Name) != nil {
				return fmt.Errorf("文件夹名称「%s」不支持直接复制", in.Name)
			}
			if slices.ContainsFunc(directNames, func(name string) bool { return strings.EqualFold(name, in.Name) }) {
				return fmt.Errorf("所选项目有同名项「%s」", in.Name)
			}
			directNames = append(directNames, in.Name)
		}
	}
	m.folderMutationMu.Lock()
	defer m.folderMutationMu.Unlock()
	m.mu.Lock()
	a := m.downloadAccountLocked(first.AccountID)
	if m.closing || a.ID == "" || a.Kind != kind || a.Endpoint != endpoint || len(m.copies)+len(rows) > 1000 {
		m.mu.Unlock()
		return errors.New("账号不可用或本机复制记录空间不足")
	}
	scope := downloadScope(a)
	for _, op := range m.copies {
		in := op.saved.Input.Input
		if op.saved.Scope == scope && in.DestinationID == first.DestinationID && seen[in.EntryID] {
			m.mu.Unlock()
			return errors.New("所选项目已有同源同目标复制记录，请先核对")
		}
	}
	m.mu.Unlock()

	if kind == "lanzou" {
		if err := validateLanzouCopyBatchContents(ctx, client, a, rows); err != nil {
			return err
		}
	} else if err := validateNativeCopyBatchContents(ctx, client, a, rows, direct, list, directNames); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != scope {
		return errors.New("账号来源已变化，请重新选择")
	}
	return ctx.Err()
}

func validateNativeCopyBatchContents(ctx context.Context, client *http.Client, a Account, rows []MoveBatchEntry, direct bool,
	list func(context.Context, *http.Client, Account, string) (FilePage, error), directNames []string) error {
	first := rows[0].Input
	if _, _, _, err := verifiedMoveTrails(ctx, client, a, first); err != nil {
		return errors.New("复制目录层级无法确认，请重新浏览目录")
	}
	source, err := list(ctx, client, a, first.ParentID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var sourceErr error
		if a.Kind == "guangya" {
			var original CloudEntry
			original, sourceErr = selectGuangyaCopyEntry(source, row, nil)
			if row.Folder && sourceErr == nil {
				var manifest []CopyManifestItem
				manifest, _, sourceErr = readGuangyaCopyTree(ctx, client, a, row.Input.EntryID, row.Input.DestinationID, false)
				if direct && sourceErr == nil {
					_, sourceErr = guangyaDirectFolderChildren(diskCopy{Input: row, Source: original, Manifest: manifest})
				}
			}
		} else {
			sourceErr = validatePan123CopyBatchSource(ctx, client, a, source, row, direct)
		}
		if sourceErr != nil {
			return fmt.Errorf("%s：%w", row.Input.Name, sourceErr)
		}
	}
	target, err := list(ctx, client, a, first.DestinationID)
	if err != nil {
		return err
	}
	for _, name := range directNames {
		if err := checkDirectCopyName(target, name); err != nil {
			return err
		}
	}
	return nil
}
