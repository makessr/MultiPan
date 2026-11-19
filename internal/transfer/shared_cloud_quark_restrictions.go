package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

const quarkRestrictionLimit = 100

type QuarkShareRestriction struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
	Reason string `json:"reason"`
}

type QuarkShareRestrictions struct {
	Reported int64                   `json:"reported"`
	Items    []QuarkShareRestriction `json:"items"`
}

func (row quarkShareRecord) restriction(now time.Time) (int64, string) {
	if row.unavailableStatus(now) != "" {
		return 0, ""
	}
	if string(row.Partial) != "true" && string(row.Partial) != "1" {
		return 0, ""
	}
	var total int64
	var token string
	if json.Unmarshal(row.Restricted, &total) != nil || total < 1 ||
		json.Unmarshal(row.DetailToken, &token) != nil || len(token) == 0 || len(token) > 4096 ||
		strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 ||
		shareScalar(row.Key) == "" {
		return 0, ""
	}
	return total, token
}

func (m *Manager) ManagedQuarkShareRestrictions(ctx context.Context, handle string) (QuarkShareRestrictions, error) {
	h, err := decodeManagedShareHandle(handle)
	if err != nil {
		return QuarkShareRestrictions{}, err
	}
	if h.Kind != "quark" || h.Page < 1 || h.Page > 1000 || h.QuarkLinkKey == "" {
		return QuarkShareRestrictions{}, errors.New("原夸克分享来源无法核对，请刷新列表")
	}
	a, err := m.managedShareAccount(h.AccountID)
	if err != nil || a.Kind != "quark" || downloadScope(a) != h.Scope || m.managedShareRevoking(h) {
		return QuarkShareRestrictions{}, errors.New("原夸克分享来源无法核对，请刷新列表")
	}
	result, err := readQuarkShareRestrictions(ctx, newQuarkClient(NewHTTPUploader().Client, a), h)
	if err != nil {
		return QuarkShareRestrictions{}, err
	}
	if current, err := m.managedShareAccount(h.AccountID); err != nil || downloadScope(current) != h.Scope || m.managedShareRevoking(h) {
		return QuarkShareRestrictions{}, errors.New("原夸克分享来源无法核对，请刷新列表")
	}
	return result, nil
}

func readQuarkShareRestrictions(ctx context.Context, c *quarkClient, h managedShareHandle) (QuarkShareRestrictions, error) {
	page, err := quarkManagedSharesFromClient(ctx, c, h.Page, h.QuarkFilter)
	if err != nil {
		return QuarkShareRestrictions{}, err
	}
	for i, row := range page.quarkRows {
		if shareScalar(row.ID) != h.ProviderID {
			continue
		}
		current, err := decodeManagedShareHandle(page.Items[i].ID)
		total, token := row.restriction(time.Now())
		if err == nil && current.Name == h.Name && current.URL == h.URL && current.QuarkLinkKey == h.QuarkLinkKey && total > 0 {
			return quarkShareRestrictionFiles(ctx, c, current.QuarkLinkKey, token, total)
		}
		break
	}
	return QuarkShareRestrictions{}, errors.New("原分享已变化或网盘未确认部分受限，请刷新列表")
}

func quarkShareRestrictionFiles(ctx context.Context, c *quarkClient, key, token string, total int64) (QuarkShareRestrictions, error) {
	var data struct {
		Files *[]struct {
			ID     json.RawMessage `json:"fid"`
			Name   string          `json:"file_name"`
			Parent string          `json:"pdir_name"`
		} `json:"file_list"`
		Reasons map[string]struct {
			Message string `json:"reason_message"`
		} `json:"invalid_fid_reasons"`
	}
	_, err := c.request(ctx, "POST", "/share/invalid/file/list", nil,
		map[string]any{"pwd_id": key, "stoken": token, "size": quarkRestrictionLimit}, &data)
	if err != nil {
		return QuarkShareRestrictions{}, err
	}
	if data.Files == nil || len(*data.Files) > quarkRestrictionLimit || int64(len(*data.Files)) > total {
		return QuarkShareRestrictions{}, errors.New("夸克受限文件清单格式或数量异常，请刷新后重试")
	}
	result := QuarkShareRestrictions{Reported: total, Items: []QuarkShareRestriction{}}
	seen := map[string]bool{}
	for _, file := range *data.Files {
		id := shareScalar(file.ID)
		reason := data.Reasons[id].Message
		if id == "" || id == "0" || seen[id] || strings.TrimSpace(file.Name) == "" ||
			len(file.Name) > 2048 || len(file.Parent) > 2048 || len(reason) > 2048 {
			return QuarkShareRestrictions{}, errors.New("夸克受限文件明细不完整，请刷新后重试")
		}
		seen[id] = true
		result.Items = append(result.Items, QuarkShareRestriction{Name: file.Name, Parent: file.Parent, Reason: reason})
	}
	return result, nil
}
