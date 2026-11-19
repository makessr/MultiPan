package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLanzouRecycleReadOnlyAndCredentialBoundary(t *testing.T) {
	for _, mode := range []string{"rows", "redirect", "login-redirect", "external-login-redirect", "login", "malformed", "duplicate", "other-origin", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			account := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "phpdisk_info=synthetic"}
			if mode == "other-origin" {
				account.Endpoint = "https://pc.woozooo.com"
			}
			file := `<tr><td><input name="fl_sel_ids[]" type="checkbox" value="123"></td><td><a href="/file"><img src="/filetype/txt.gif">例 &amp; 文件...</a></td></tr>`
			folder := `<tr><td><a href="mydisk.php?item=recycle&amp;action=folder_restore&amp;folder_id=123">&nbsp;旧目录</a></td></tr>`
			body := "<table><!--" + file + "-->" + file + folder + "</table>"
			status := http.StatusOK
			headers := http.Header{}
			switch mode {
			case "redirect":
				status = http.StatusFound
				headers.Set("Location", "https://pc.woozooo.com/mydisk.php")
			case "login-redirect":
				status = http.StatusFound
				headers.Set("Location", "/acc.php")
			case "external-login-redirect":
				status = http.StatusFound
				headers.Set("Location", "https://pc.woozooo.com/acc.php")
			case "login":
				body = "<html>网盘用户登录</html>"
			case "malformed":
				body = strings.Replace(file, `value="123"`, `value="-1"`, 1)
			case "duplicate":
				body = file + file
			case "oversized":
				body = strings.Repeat("x", (2<<20)+1)
			}
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != "up.woozooo.com" || r.URL.Path != "/mydisk.php" ||
					r.URL.Query().Get("item") != "recycle" || r.URL.Query().Get("action") != "files" ||
					r.Header.Get("Cookie") != account.Secret || r.Body != nil {
					t.Fatal("unexpected blue recycle request", r.Method, r.URL)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			page, err := listAccountRecycle(context.Background(), client, account, 1)
			if mode == "other-origin" {
				if calls != 0 || err == nil {
					t.Fatal("wrong origin dispatched")
				}
				return
			}
			if calls != 1 {
				t.Fatal("unexpected request count", calls)
			}
			if mode != "rows" {
				if err == nil || len(page.Items) != 0 {
					t.Fatal("ambiguous response was accepted", mode, err)
				}
				if errors.Is(err, errLanzouRecycleLogin) != (mode == "login-redirect") {
					t.Fatal("incorrect login classification", err)
				}
				return
			}
			if err != nil || len(page.Items) != 2 || page.Total != nil || page.HasMore {
				t.Fatal(page, err)
			}
			if page.Items[0] != (RecycleEntry{ID: "file:123", Kind: "file", Name: "例 & 文件..."}) ||
				page.Items[1] != (RecycleEntry{ID: "folder:123", Kind: "folder", Name: "旧目录"}) {
				t.Fatal("unexpected display metadata", page.Items)
			}
		})
	}
}

func TestLanzouRecyclePagesVisibleRows(t *testing.T) {
	var content strings.Builder
	for i := 1; i <= 101; i++ {
		fmt.Fprintf(&content, `<tr><td><input name="fl_sel_ids[]" value="%d"><a href="/file"><img src="/filetype/txt.gif">item %d</a></td></tr>`, i, i)
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(content.String())), Request: r}, nil
	})}
	a := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "synthetic"}
	first, err := listAccountRecycle(context.Background(), client, a, 1)
	if err != nil || len(first.Items) != 100 || !first.HasMore {
		t.Fatal("unexpected first page", err)
	}
	next, err := listAccountRecycle(context.Background(), client, a, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "file:101" || next.HasMore {
		t.Fatal("unexpected last page", next, err)
	}
}

func TestLanzouRecycleCoverageClassifiesVisibleRowsOnly(t *testing.T) {
	account := Account{ID: "local", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
	other := Account{ID: "other", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "different"}
	scope := downloadScope(account)
	m := &Manager{
		trashes: map[string]*trashOperation{},
		recyclePurges: map[string]*recyclePurgeOperation{
			"pending-folder": {saved: diskRecyclePurge{
				Source:  recyclePurgeSource{Scope: scope, Entry: RecycleEntry{ID: "folder:2"}, FolderFileIDs: []string{"3"}},
				Receipt: TrashReceipt{State: "unknown"}, Submitted: true,
			}},
			"other-account": {saved: diskRecyclePurge{
				Source:  recyclePurgeSource{Scope: downloadScope(other), Entry: RecycleEntry{ID: "file:5"}},
				Receipt: TrashReceipt{State: "unknown"}, Submitted: true,
			}},
		},
		recycleRestores: map[string]*recycleRestore{
			"confirmed": {saved: diskRecycleRestore{Source: recycleRestoreSource{Scope: scope, Entry: RecycleEntry{ID: "file:4"}}, Result: RecycleRestoreResult{State: "confirmed"}, Submitted: true}},
		},
	}
	original := CloudEntry{ID: "1", Name: "完整名称.txt"}
	m.trashes["original"] = &trashOperation{saved: diskTrash{
		Source:  trashSource{Purpose: lanzouTrashPurpose, Scope: scope, Input: TrashInput{AccountID: account.ID, ParentID: "-1", Entry: original}, Original: lanzouTrashEntry("-1", original)},
		Receipt: TrashReceipt{State: "confirmed"}, Submitted: true,
	}}
	items := []RecycleEntry{
		{ID: "file:1", Kind: "file", Name: original.Name},
		{ID: "folder:2", Kind: "folder", Name: "原目录"},
		{ID: "file:3", Kind: "file", Name: "目录里的文件"},
		{ID: "file:4", Kind: "file", Name: "有回执"},
		{ID: "file:5", Kind: "file", Name: "其他账号待核对"},
	}
	for i := 5; i < 101; i++ {
		items = append(items, RecycleEntry{ID: fmt.Sprintf("file:%d", i+1), Kind: "file", Name: fmt.Sprintf("可见文件%d", i+1)})
	}
	rows := m.lanzouRecycleCoverageLocked(account, items, nil, lanzouPurgeFolderScan{})
	want := []string{"candidate", "pending", "pending", "history", "website"}
	for i, state := range want {
		if rows[i].State != state || rows[i].Name != items[i].Name || rows[i].Page != 1 {
			t.Fatal("wrong coverage classification", i, rows[i])
		}
	}
	if len(rows) != 101 || rows[100].Page != 2 {
		t.Fatal("coverage differs from list pagination")
	}
	if rows[1].Folder == nil || rows[1].Folder.ID != "folder:2" ||
		rows[4].Folder != nil || rows[4].Limitation != "folder-membership" {
		t.Fatal("coverage linked an unverified file or missed the visible folder", rows[1], rows[4])
	}
	folders := map[string][]lanzouFolderFile{
		"folder:2": {{ID: "5", Name: items[4].Name}, {ID: "6", Name: "名称对不上"}},
	}
	rows = m.lanzouRecycleCoverageLocked(account, items, map[string]bool{"5": true, "6": true}, lanzouPurgeFolderScan{files: folders})
	if rows[4].State != "website" || rows[4].Entry != nil || rows[4].Limitation != "folder-member" ||
		rows[4].Folder == nil || rows[4].Folder.ID != items[1].ID || rows[4].Folder.Name != items[1].Name ||
		rows[5].Folder != nil {
		t.Fatal("coverage did not preserve read-only folder identity", rows[4], rows[5])
	}
}

func TestLanzouRecycleFolderCheckboxAndBrowseLink(t *testing.T) {
	const checkbox = `<input name="fd_sel_ids[]" type="checkbox" value="42">`
	const browse = `<a href="?item=recycle&amp;action=show_files&amp;folder_id=42"><img src="images/folder.gif">&nbsp;蓝奏 &amp; 资料</a>`
	const restore = `<a href="mydisk.php?item=recycle&amp;action=folder_restore&amp;folder_id=42"><img src="images/restore_icon.gif"></a>`
	check := func(row string, accepted bool) {
		t.Helper()
		items, err := parseLanzouRecycle("<tr>" + row + "</tr>")
		if accepted {
			if err != nil || len(items) != 1 || items[0] != (RecycleEntry{ID: "folder:42", Kind: "folder", Name: "蓝奏 & 资料"}) {
				t.Fatal(items, err)
			}
		} else if err == nil {
			t.Fatal("ambiguous folder row accepted", items)
		}
	}
	check(restore+checkbox+browse, true)
	check(checkbox+restore, false)
	check(checkbox+strings.Replace(browse, "folder_id=42", "folder_id=43", 1)+restore, false)
	check(checkbox+browse+browse, false)
	check(checkbox+strings.Replace(browse, "href=\"?", "href=\"https://other.invalid/mydisk.php?", 1), false)
	check(checkbox+browse+strings.Replace(restore, "folder_id=42", "folder_id=43", 1), false)
}
