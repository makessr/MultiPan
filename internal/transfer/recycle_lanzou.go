package transfer

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const lanzouRecycleOrigin = "https://up.woozooo.com"

var errLanzouRecycleLogin = errors.New("蓝奏云要求重新登录，请更新账号登录后刷新回收站")

var (
	lanzouRecycleHidden = regexp.MustCompile("(?is)<!--.*?-->|<(?:script|style)\\b[^>]*>.*?</(?:script|style)\\s*>")
	lanzouRecycleRows   = regexp.MustCompile("(?is)<tr\\b[^>]*>(.*?)</tr\\s*>")
	lanzouRecycleInput  = regexp.MustCompile("(?is)<input\\b[^>]*>")
	lanzouRecycleLink   = regexp.MustCompile("(?is)<a\\b([^>]*)>(.*?)</a\\s*>")
	lanzouRecycleTag    = regexp.MustCompile("(?s)<[^>]*>")
	lanzouRecycleAttr   = regexp.MustCompile(`(?i)([a-z][a-z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
)

func lanzouRecycleAttributes(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range lanzouRecycleAttr.FindAllStringSubmatch(tag, -1) {
		key := strings.ToLower(m[1])
		if _, exists := attrs[key]; exists {
			return nil
		}
		attrs[key] = html.UnescapeString(m[2] + m[3] + m[4])
	}
	return attrs
}

func readLanzouRecycle(ctx context.Context, client *http.Client, a Account, page int) (RecyclePage, error) {
	items, err := readLanzouRecycleRows(ctx, client, a)
	if err != nil {
		return RecyclePage{}, err
	}
	start := (page - 1) * recyclePageSize
	if start > len(items) {
		start = len(items)
	}
	end := min(start+recyclePageSize, len(items))
	// Only paginate the recognizable rows visible in the returned document.
	// No server total, original path or write handle can be inferred.
	return RecyclePage{Page: page, Items: items[start:end], HasMore: end < len(items)}, nil
}

func readLanzouRecycleRows(ctx context.Context, client *http.Client, a Account) ([]RecycleEntry, error) {
	body, err := readLanzouRecyclePage(ctx, client, a, url.Values{"item": {"recycle"}, "action": {"files"}})
	if err != nil {
		return nil, err
	}
	return parseLanzouRecycle(body)
}

func readLanzouRecyclePage(ctx context.Context, client *http.Client, a Account, query url.Values) (string, error) {
	if a.Kind != "lanzou" || a.Endpoint != lanzouRecycleOrigin || a.Secret == "" {
		return "", errors.New("蓝奏回收站需要现有登录地址和有效登录")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lanzouRecycleOrigin+"/mydisk.php?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", a.UserAgent)
	req.Header.Set("Referer", lanzouRecycleOrigin+"/mydisk.php")
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return "", errors.New("读取蓝奏回收站失败，请检查登录和网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusFound && lanzouRecycleLoginRedirect(req.URL, resp.Header.Get("Location")) {
		return "", errLanzouRecycleLogin
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("蓝奏当前登录地址没有返回可读回收站页面")
	}
	const maxBody = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return "", errors.New("蓝奏回收站响应读取失败或超出大小限制")
	}
	return string(body), nil
}

func lanzouRecycleLoginRedirect(origin *url.URL, location string) bool {
	target, err := origin.Parse(location)
	return err == nil && target.User == nil && target.Opaque == "" &&
		target.Scheme == origin.Scheme && target.Host == origin.Host &&
		target.EscapedPath() == "/acc.php"
}

func parseLanzouRecycle(body string) ([]RecycleEntry, error) {
	body = lanzouRecycleHidden.ReplaceAllString(body, "")
	formatErr := errors.New("蓝奏回收站页面未能识别，请到官网核对")
	items := []RecycleEntry{}
	seen := map[string]bool{}
	for _, row := range lanzouRecycleRows.FindAllStringSubmatch(body, -1) {
		read, err := lanzouRecycleRow(row[1])
		if err != nil {
			return nil, formatErr
		}
		if read.ID == "" {
			continue
		}
		if seen[read.ID] || len(items) >= 1000 {
			return nil, formatErr
		}
		seen[read.ID] = true
		items = append(items, read)
	}
	// An empty or changed page, login screen, and iframe shell are ambiguous.
	if len(items) == 0 {
		return nil, formatErr
	}
	return items, nil
}

func lanzouRecycleRow(row string) (RecycleEntry, error) {
	var id, kind, name string
	var legacyFolderID, legacyName string
	bad := errors.New("蓝奏回收站条目身份或名称不明确")
	for _, input := range lanzouRecycleInput.FindAllString(row, -1) {
		a := lanzouRecycleAttributes(input)
		switch a["name"] {
		case "fl_sel_ids", "fl_sel_ids[]", "fd_sel_ids", "fd_sel_ids[]":
			if id != "" || !validLanzouRecycleID(a["value"]) {
				return RecycleEntry{}, bad
			}
			id, kind = a["value"], "file"
			if strings.HasPrefix(a["name"], "fd_") {
				kind = "folder"
			}
		}
	}
	for _, link := range lanzouRecycleLink.FindAllStringSubmatch(row, -1) {
		a := lanzouRecycleAttributes(link[1])
		href, err := url.Parse(a["href"])
		if err != nil {
			continue
		}
		q := href.Query()
		label := strings.TrimSpace(html.UnescapeString(lanzouRecycleTag.ReplaceAllString(link[2], "")))
		action := q.Get("action")
		if q.Get("item") == "recycle" && (action == "show_files" || action == "folder_restore" || action == "folder_delete_complete") {
			folderID, ok := lanzouRecycleFolderLink(href)
			if !ok || kind != "" && (kind != "folder" || id != folderID) {
				return RecycleEntry{}, bad
			}
			if action == "show_files" && kind == "folder" {
				if name != "" || label == "" {
					return RecycleEntry{}, bad
				}
				name = label
			} else if action == "folder_restore" && kind == "" && label != "" && label != "恢复" && label != "还原" && label != "复原" {
				if legacyFolderID != "" {
					return RecycleEntry{}, bad
				}
				legacyFolderID, legacyName = folderID, label
			}
		} else if kind == "file" && strings.Contains(strings.ToLower(link[2]), "filetype/") {
			if name != "" || label == "" {
				return RecycleEntry{}, bad
			}
			name = label
		}
	}
	if id == "" && legacyFolderID != "" {
		id, kind, name = legacyFolderID, "folder", legacyName
	}
	if id == "" {
		return RecycleEntry{}, nil
	}
	if id == "0" || name == "" || len(name) > 4096 || strings.ContainsAny(name, "\x00\r\n") {
		return RecycleEntry{}, bad
	}
	return RecycleEntry{ID: kind + ":" + id, Kind: kind, Name: name}, nil
}

func lanzouRecycleFolderLink(href *url.URL) (string, bool) {
	base, _ := url.Parse(lanzouRecycleOrigin + "/mydisk.php")
	target := base.ResolveReference(href)
	q, err := url.ParseQuery(target.RawQuery)
	id := q.Get("folder_id")
	return id, err == nil && target.Scheme == base.Scheme && target.Host == base.Host &&
		target.EscapedPath() == base.Path && target.User == nil && target.Opaque == "" && target.Fragment == "" &&
		len(q["item"]) == 1 && len(q["action"]) == 1 && len(q["folder_id"]) == 1 && validLanzouRecycleID(id)
}

func validLanzouRecycleID(id string) bool {
	return validFolderID(id) && id[0] >= '1' && id[0] <= '9'
}
