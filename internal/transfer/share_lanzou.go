package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const lanzouDirectoryShareGroupLimit = 5

type LanzouDirectoryShareEntry struct {
	ParentID string `json:"parentId"`
	EntryID  string `json:"entryId"`
	Name     string `json:"name"`
	Folder   bool   `json:"folder"`
}

// ReadLanzouDirectoryShares reads up to five selected links. Items sharing a
// directory and type reuse inventory reads; unmatched items have no result.
func (m *Manager) ReadLanzouDirectoryShares(ctx context.Context, accountID string, entries []LanzouDirectoryShareEntry) ([]*ManagedShareLink, error) {
	if len(entries) == 0 || len(entries) > lanzouDirectoryShareGroupLimit {
		return nil, errors.New("每组蓝奏分享请选择 1 至 5 项")
	}
	m.mu.Lock()
	a := m.downloadAccountLocked(accountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || a.ID == "" || a.Kind != "lanzou" {
		return nil, errors.New("蓝奏账号已变化，请重新选择目录项目")
	}
	result, err := readLanzouDirectoryShares(ctx, NewHTTPUploader().Client, a, entries)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(accountID)) != downloadScope(a) {
		return nil, errors.New("蓝奏账号已变化，请重新选择目录项目")
	}
	return result, nil
}

func readLanzouDirectoryShares(ctx context.Context, client *http.Client, a Account, entries []LanzouDirectoryShareEntry) ([]*ManagedShareLink, error) {
	type directory struct {
		id     string
		folder bool
	}
	groups := make(map[directory][]int)
	order := make([]directory, 0, len(entries))
	inputs := make([]ShareInput, len(entries))
	for i, entry := range entries {
		in := ShareInput{AccountID: a.ID, ParentID: entry.ParentID, EntryID: entry.EntryID, Name: entry.Name, Folder: entry.Folder, Action: "get"}
		if err := validateShare(a, in); err != nil {
			return nil, err
		}
		key := directory{entry.ParentID, entry.Folder}
		for _, existing := range groups[key] {
			if inputs[existing].EntryID == in.EntryID {
				return nil, errors.New("蓝奏分享读取组包含重复项目")
			}
		}
		if len(groups[key]) == 0 {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
		inputs[i] = in
	}
	results := make([]*ManagedShareLink, len(entries))
	for _, key := range order {
		indices := groups[key]
		if len(indices) == 1 {
			i := indices[0]
			if link, err := readLanzouDirectoryShare(ctx, client, a, inputs[i]); err == nil {
				results[i] = &link
			}
			continue
		}
		before, err := readLanzouDirectoryNames(ctx, client, a, key.id, key.folder)
		if err != nil {
			continue
		}
		anyRead := false
		for _, i := range indices {
			in := inputs[i]
			if before[in.EntryID] == in.Name {
				if link, err := readLanzouDirectoryLink(ctx, client, a, in); err == nil {
					results[i] = &link
					anyRead = true
				}
			}
		}
		if !anyRead {
			continue
		}
		after, err := readLanzouDirectoryNames(ctx, client, a, key.id, key.folder)
		for _, i := range indices {
			if err != nil || after[inputs[i].EntryID] != inputs[i].Name {
				results[i] = nil
			}
		}
	}
	return results, ctx.Err()
}

// ReadLanzouDirectoryShare checks the directory entry and reads the existing
// link without creating a local share operation.
func (m *Manager) ReadLanzouDirectoryShare(ctx context.Context, accountID, parentID, entryID, name string, folder bool) (ManagedShareLink, error) {
	m.mu.Lock()
	a := m.downloadAccountLocked(accountID)
	closing := m.closing
	m.mu.Unlock()
	if closing || a.ID == "" || a.Kind != "lanzou" {
		return ManagedShareLink{}, errors.New("蓝奏账号已变化，请重新选择目录项目")
	}
	in := ShareInput{AccountID: accountID, ParentID: parentID, EntryID: entryID, Name: name, Folder: folder, Action: "get"}
	if err := validateShare(a, in); err != nil {
		return ManagedShareLink{}, err
	}
	result, err := readLanzouDirectoryShare(ctx, NewHTTPUploader().Client, a, in)
	if err != nil {
		return ManagedShareLink{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(accountID)) != downloadScope(a) {
		return ManagedShareLink{}, errors.New("蓝奏账号已变化，请重新选择目录项目")
	}
	return result, nil
}

func (m *Manager) ReadLanzouShareReceiptLink(ctx context.Context, accountID, receiptID string) (ManagedShareLink, error) {
	return m.readLanzouShareReceiptLink(ctx, NewHTTPUploader().Client, accountID, receiptID)
}

func (m *Manager) readLanzouShareReceiptLink(ctx context.Context, client *http.Client, accountID, receiptID string) (ManagedShareLink, error) {
	m.mu.Lock()
	account := m.downloadAccountLocked(accountID)
	op := m.shares[receiptID]
	if m.closing || !canLocateLanzouShare(account, op) {
		m.mu.Unlock()
		return ManagedShareLink{}, errors.New("蓝奏回执或账号来源已变化")
	}
	recorded, password := op.result.URL, op.result.Password
	expected, err := shareURL("lanzou", recorded)
	_, blocked := m.shareRevocationOverlayLocked(op)
	input := op.in
	input.Action, input.Password, input.ClearPassword = "get", "", false
	m.mu.Unlock()
	if blocked || err != nil {
		return ManagedShareLink{}, errors.New("原回执没有可核对的蓝奏链接")
	}

	result, err := readLanzouDirectoryShare(ctx, client, account, input)
	if err != nil {
		return ManagedShareLink{}, err
	}
	if result.URL != expected {
		return ManagedShareLink{}, errors.New("原分享链接已变化，请到原目录核对")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.shares[receiptID] != op || !canLocateLanzouShare(m.downloadAccountLocked(accountID), op) || op.result.URL != recorded || op.result.Password != password {
		return ManagedShareLink{}, errors.New("读取期间蓝奏回执或账号来源已变化")
	}
	if _, blocked = m.shareRevocationOverlayLocked(op); blocked {
		return ManagedShareLink{}, errors.New("原分享结果待核对，暂不能读取")
	}
	result.Message = lanzouReceiptPasswordMessage(password, result.Password)
	return result, nil
}

func lanzouReceiptPasswordMessage(recorded, current string) string {
	if recorded == current {
		return "提取码与本机回执一致"
	}
	return "当前提取码与本机回执不同"
}

func (m *Manager) ReadLanzouShareReceiptLinks(ctx context.Context, accountID string, ids []string) ([]*ManagedShareLink, error) {
	return m.readLanzouShareReceiptLinks(ctx, NewHTTPUploader().Client, accountID, ids)
}

func (m *Manager) readLanzouShareReceiptLinks(ctx context.Context, client *http.Client, accountID string, ids []string) ([]*ManagedShareLink, error) {
	if len(ids) < 1 || len(ids) > lanzouDirectoryShareGroupLimit {
		return nil, errors.New("每组蓝奏分享请选择 1 至 5 条回执")
	}
	type receipt struct {
		op            *shareOperation
		url, password string
	}
	snapshots := make([]receipt, 0, len(ids))
	entries := make([]LanzouDirectoryShareEntry, 0, len(ids))
	m.mu.Lock()
	account := m.downloadAccountLocked(accountID)
	for _, id := range ids {
		op := m.shares[id]
		if m.closing || !canLocateLanzouShare(account, op) {
			m.mu.Unlock()
			return nil, errors.New("蓝奏回执或账号来源已变化")
		}
		link, err := shareURL("lanzou", op.result.URL)
		if _, blocked := m.shareRevocationOverlayLocked(op); blocked || err != nil {
			m.mu.Unlock()
			return nil, errors.New("原回执没有可核对的蓝奏链接")
		}
		snapshots = append(snapshots, receipt{op, link, op.result.Password})
		entries = append(entries, LanzouDirectoryShareEntry{ParentID: op.in.ParentID, EntryID: op.in.EntryID, Name: op.in.Name, Folder: op.in.Folder})
	}
	m.mu.Unlock()

	links, err := readLanzouDirectoryShares(ctx, client, account, entries)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.downloadAccountLocked(accountID)
	if m.closing || downloadScope(current) != downloadScope(account) {
		return nil, errors.New("读取期间蓝奏账号来源已变化")
	}
	for i, snapshot := range snapshots {
		op := m.shares[ids[i]]
		link := links[i]
		if link == nil || link.URL != snapshot.url || op != snapshot.op || !canLocateLanzouShare(current, op) {
			links[i] = nil
			continue
		}
		expected, err := shareURL("lanzou", op.result.URL)
		if _, blocked := m.shareRevocationOverlayLocked(op); blocked || err != nil || expected != snapshot.url || op.result.Password != snapshot.password {
			links[i] = nil
			continue
		}
		link.Message = lanzouReceiptPasswordMessage(snapshot.password, link.Password)
	}
	return links, nil
}

func readLanzouDirectoryShare(ctx context.Context, client *http.Client, a Account, in ShareInput) (ManagedShareLink, error) {
	if err := checkLanzouDirectoryEntry(ctx, client, a, in); err != nil {
		return ManagedShareLink{}, err
	}
	return readLanzouDirectoryLink(ctx, client, a, in)
}

func readLanzouDirectoryLink(ctx context.Context, client *http.Client, a Account, in ShareInput) (ManagedShareLink, error) {
	result, err := readLanzouShare(ctx, client, a, in)
	if err != nil {
		return ManagedShareLink{}, err
	}
	return ManagedShareLink{Name: in.Name, URL: result.URL, Password: result.Password, Expiry: "有效期沿用网盘设置", Message: result.Message}, nil
}

func checkLanzouDirectoryEntry(ctx context.Context, client *http.Client, a Account, in ShareInput) error {
	names, err := readLanzouDirectoryNames(ctx, client, a, in.ParentID, in.Folder)
	if err != nil {
		return err
	}
	if names[in.EntryID] != in.Name {
		return errors.New("原目录项目已变化或无法唯一确认，请重新选择")
	}
	return nil
}

func readLanzouDirectoryNames(ctx context.Context, client *http.Client, a Account, parentID string, folder bool) (map[string]string, error) {
	names := make(map[string]string)
	if folder {
		page, err := listLanzouFolders(ctx, client, a, parentID, true)
		if err != nil {
			return nil, err
		}
		for _, folder := range page.Folders {
			names[folder.ID] = folder.Name
		}
	} else {
		files, err := readLanzouFiles(ctx, client, a, parentID, true)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			names[file.ID] = file.Name
		}
	}
	return names, nil
}

func (m *Manager) UpdateLanzouDirectorySharePassword(ctx context.Context, accountID, parentID, entryID, name string, folder bool, expectedURL, expectedPassword, password string) (ShareResult, error) {
	return m.editLanzouDirectorySharePassword(ctx, accountID, parentID, entryID, name, folder, expectedURL, expectedPassword, password, false)
}

func (m *Manager) ClearLanzouDirectorySharePassword(ctx context.Context, accountID, parentID, entryID, name string, folder bool, expectedURL, expectedPassword string) (ShareResult, error) {
	return m.editLanzouDirectorySharePassword(ctx, accountID, parentID, entryID, name, folder, expectedURL, expectedPassword, "", true)
}

func (m *Manager) EditLanzouShareReceiptPassword(ctx context.Context, accountID, receiptID, expectedURL, expectedPassword, password string, clear bool) (ShareResult, error) {
	if receiptID == "" {
		return ShareResult{}, errors.New("缺少原蓝奏分享回执")
	}
	link, err := shareURL("lanzou", expectedURL)
	if err != nil || link != expectedURL {
		return ShareResult{}, errors.New("原分享链接未通过预检，请重新读取")
	}
	return m.ShareBatchEntry(ctx, ShareInput{
		AccountID: accountID, Action: "password", Password: password, ClearPassword: clear,
		expectedLanzouURL: link, expectedLanzouPassword: expectedPassword, lanzouReceiptID: receiptID,
	})
}

func (m *Manager) editLanzouDirectorySharePassword(ctx context.Context, accountID, parentID, entryID, name string, folder bool, expectedURL, expectedPassword, password string, clear bool) (ShareResult, error) {
	link, err := shareURL("lanzou", expectedURL)
	if err != nil || link != expectedURL {
		return ShareResult{}, errors.New("原分享链接未通过预检，请重新读取")
	}
	in := ShareInput{
		AccountID: accountID, ParentID: parentID, EntryID: entryID, Name: name, Folder: folder,
		Action: "password", Password: password, ClearPassword: clear,
		expectedLanzouURL: link, expectedLanzouPassword: expectedPassword,
	}
	return m.ShareBatchEntry(ctx, in)
}

func readLanzouShare(ctx context.Context, client *http.Client, a Account, in ShareInput) (ShareResult, error) {
	form := url.Values{"task": {"22"}, "file_id": {in.EntryID}}
	if in.Folder {
		form = url.Values{"task": {"18"}, "folder_id": {in.EntryID}}
	}
	var response struct {
		Info struct {
			Domain   string          `json:"is_newd"`
			File     string          `json:"f_id"`
			URL      string          `json:"new_url"`
			On       json.RawMessage `json:"onof"`
			Password string          `json:"pwd"`
		} `json:"info"`
	}
	if e := lanzouEditRequest(ctx, client, a, form, &response); e != nil {
		return ShareResult{}, errors.New("读取蓝奏分享信息失败，请检查登录或在官网生成分享链接后重试 [SHARE-LZ/read]")
	}
	d := response.Info
	link := d.URL
	if !in.Folder {
		if !shareToken.MatchString(d.File) || d.File == "i" {
			return ShareResult{}, errors.New("蓝奏未返回完整分享链接，请在官网分享管理核对 [SHARE-LZ/link]")
		}
		link = strings.TrimRight(d.Domain, "/") + "/" + d.File
	}
	link, e := shareURL("lanzou", link)
	if e != nil {
		return ShareResult{}, e
	}
	on := strings.Trim(string(d.On), "\"")
	password := ""
	if on == "1" {
		if len(d.Password) == 0 || len(d.Password) > 64 || strings.IndexFunc(d.Password, unicode.IsControl) >= 0 {
			return ShareResult{}, errors.New("无法确认蓝奏现有提取码，请到官网核对")
		}
		password = d.Password
	} else if on != "0" {
		return ShareResult{}, errors.New("无法确认蓝奏分享密码开关，未返回可复制结果")
	}
	return ShareResult{State: "ready", URL: link, Password: password, Message: "已读取现有分享链接，未更改原分享设置"}, nil
}

func shareLanzou(ctx context.Context, client *http.Client, a Account, op *shareOperation, checkOnly bool) ShareResult {
	in := op.in
	current, e := readLanzouShare(ctx, client, a, in)
	if e != nil {
		state := "failed"
		if checkOnly && in.Action == "password" {
			state = "unknown"
		}
		return ShareResult{State: state, Message: e.Error()}
	}
	if in.Action == "get" {
		return current
	}
	unknown := ShareResult{State: "unknown", Message: "蓝奏提取码设置结果未确认，请先点击“检查原操作结果”或到官网核对，不要重复设置"}
	if checkOnly {
		if op.lanzouURL == "" || current.URL != op.lanzouURL || current.Password != in.Password {
			unknown.Message = "无法确认原链接和提取码均符合设置，请到官网核对原操作"
			return unknown
		}
		current.Message = "已核对原链接和提取码，未重新提交设置"
		return current
	}
	if in.expectedLanzouURL != "" && current.URL != in.expectedLanzouURL {
		return ShareResult{State: "failed", Message: "原链接在预检后已变化，请重新选择项目。未提交改码。"}
	}
	if current.Password == in.Password {
		op.lanzouURL = current.URL
		current.Message = "已核对当前提取码与设定一致；未重复修改"
		if in.ClearPassword {
			current.Message = "已核对原链接没有提取码，未提交修改"
		}
		return current
	}
	if in.expectedLanzouURL != "" && current.Password != in.expectedLanzouPassword {
		return ShareResult{State: "failed", Message: "提取码在预检后已变化，本项未修改。请重新核对后再试。"}
	}
	op.lanzouURL = current.URL
	if op.settingsCheckpoint == nil {
		e = op.saveCheckpoint()
	} else {
		e = op.settingsCheckpoint(managedShareHandle{})
	}
	if e != nil {
		return ShareResult{State: "failed", Message: "无法保存蓝奏原分享链接，未提交提取码修改"}
	}
	form := url.Values{"task": {"23"}, "file_id": {in.EntryID}, "shows": {"1"}, "shownames": {in.Password}}
	if in.Folder {
		form = url.Values{"task": {"16"}, "folder_id": {in.EntryID}, "shows": {"1"}, "shownames": {in.Password}}
	}
	if in.ClearPassword {
		form.Set("shows", "0")
	}
	if e = lanzouEditRequest(ctx, client, a, form, nil); e != nil {
		return unknown
	}
	after, e := readLanzouShare(ctx, client, a, in)
	if e != nil || after.URL != op.lanzouURL || after.Password != in.Password {
		return unknown
	}
	after.Message = "已设置并重新读取核对提取码；已有分享链接也使用新提取码"
	if in.ClearPassword {
		after.Message = "已核对原链接的提取码已关闭"
	}
	return after
}
