package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func verify123RestoreDestination(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource, trail []CloudFolder) error {
	_, _, actual, err := verifiedMoveTrails(ctx, client, a, MoveFileInput{
		ParentID: "0", SourceTrail: []CloudFolder{{ID: "0"}}, EntryID: h.Entry.ID,
		DestinationID: h.DestinationID, DestinationTrail: trail,
	})
	if err != nil {
		return err
	}
	names := []string{"根目录"}
	for _, folder := range trail[1:] {
		names = append(names, folder.Name)
	}
	if actual != strings.Join(names, " / ") {
		return errors.New("123 目标路径已变化，请重新选择")
	}
	return nil
}

func submit123RestoreTo(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource) error {
	return (pan123Client{client, a}).request(ctx, "POST", "/file/recover/by_path", nil, struct {
		ParentID json.Number   `json:"parentFileId"`
		Files    []json.Number `json:"fileIds"`
	}{ParentID: json.Number(h.DestinationID), Files: []json.Number{json.Number(h.Entry.ID)}}, nil)
}

func validate123RestoreBatchDestination(ctx context.Context, client *http.Client, a Account, sources []recycleRestoreSource, trail []CloudFolder) error {
	target := sources[0]
	if err := verify123RestoreDestination(ctx, client, a, target, trail); err != nil {
		return errors.New("目标目录或路径已变化，整批未提交")
	}
	return scan123RestoreParent(ctx, client, a, target.DestinationID, true, func(row pan123RestoreRow) error {
		for _, source := range sources {
			if strings.EqualFold(source.Entry.Name, row.Name) {
				return errors.New("目标目录已有同名项目，整批未提交")
			}
		}
		return nil
	})
}

// Only a matching live entry in the recorded parent confirms restore. A missing
// recycle row or a successful POST does not. Bound directory scans to 5000 entries.
func pan123RestoreOriginal(ctx context.Context, client *http.Client, a Account, h recycleRestoreSource) (bool, bool, error) {
	same, occupied := false, false
	err := scan123RestoreParent(ctx, client, a, recycleRestoreParent(h), h.DestinationID != "", func(row pan123RestoreRow) error {
		if row.Name != h.Entry.Name && (h.DestinationID == "" || !strings.EqualFold(row.Name, h.Entry.Name)) {
			return nil
		}
		if occupied {
			same = false
			return errors.New("123还原目录存在多个同名项目")
		}
		occupied = true
		same = row.Name == h.Entry.Name && string(row.ID) == h.Entry.ID && (*row.Type == 1) == (h.Entry.Kind == "folder") &&
			(h.Entry.Kind == "folder" || row.Size != nil && h.Entry.Size != nil && *row.Size == *h.Entry.Size)
		return nil
	})
	return same && err == nil, occupied, err
}

type pan123RestoreRow struct {
	ID      json.Number `json:"FileId"`
	Parent  json.Number `json:"ParentFileId"`
	Name    string      `json:"FileName"`
	Type    *int        `json:"Type"`
	Size    *int64      `json:"Size"`
	Trashed *bool       `json:"Trashed"`
}

func scan123RestoreParent(ctx context.Context, client *http.Client, a Account, parent string, strict bool, visit func(pan123RestoreRow) error) error {
	seen := map[string]bool{}
	expectedTotal := -1
	for page := 1; page <= 50; page++ {
		var data struct {
			Rows  []pan123RestoreRow `json:"InfoList"`
			Total *int               `json:"Total"`
			Next  json.RawMessage    `json:"Next"`
		}
		q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {"0"}, "parentFileId": {parent}, "trashed": {"false"}, "Page": {strconv.Itoa(page)}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "SearchData": {""}, "inDirectSpace": {"false"}}
		if e := (pan123Client{client, a}).request(ctx, "GET", "/file/list/new", q, nil, &data); e != nil {
			return e
		}
		if data.Rows == nil || len(data.Rows) > 100 {
			return errors.New("123还原目录列表格式未确认")
		}
		for _, r := range data.Rows {
			id := string(r.ID)
			if !valid123Folder(id) || id == "0" || seen[id] || string(r.Parent) != parent || r.Type == nil || (*r.Type != 0 && *r.Type != 1) || r.Name == "" || r.Size != nil && *r.Size < 0 || r.Trashed != nil && *r.Trashed {
				return errors.New("123还原目录条目来源或类型未确认")
			}
			seen[id] = true
			if err := visit(r); err != nil {
				return err
			}
		}
		lastPage := len(data.Rows) == 0 || pan123RecycleFieldKind(data.Next) == "end"
		if strict {
			if data.Total == nil || *data.Total < len(seen) || expectedTotal >= 0 && *data.Total != expectedTotal ||
				lastPage && *data.Total != len(seen) {
				return errors.New("123 目标目录列表不完整")
			}
			expectedTotal = *data.Total
		}
		if lastPage || data.Total != nil && *data.Total >= 0 && len(seen) >= *data.Total {
			return nil
		}
	}
	return errors.New("123还原目录核对超过5000项，请到官网核对")
}
