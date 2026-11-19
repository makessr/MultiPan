package transfer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLanzouRecycleKeepsVerifiedEmptyFoldersAfterIncompleteInventory(t *testing.T) {
	account := Account{ID: "fixture", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
	const empty = `<tr><td>该文件夹里没有包含文件</td></tr>`
	const root = `<tr><input name="fd_sel_ids[]" value="41"><a href="?item=recycle&amp;action=show_files&amp;folder_id=41">有子目录</a></tr>` +
		`<tr><input name="fd_sel_ids[]" value="42"><a href="?item=recycle&amp;action=show_files&amp;folder_id=42">空目录</a></tr>` +
		`<tr><input name="fl_sel_ids[]" value="61"><a href="/file"><img src="/filetype/txt.gif">文件</a></tr>`
	emptyReads := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("item") != "recycle" {
			t.Fatal("unexpected request during inventory check", r.Method, r.URL)
		}
		var body string
		switch q.Get("action") + ":" + q.Get("folder_id") {
		case "files:":
			body = root
		case "show_files:41":
			body = empty + `<tr><td><a href="?item=recycle&amp;action=show_files&amp;folder_id=99">子目录</a></td></tr>`
		case "show_files:42":
			emptyReads++
			body = empty
		case "folder_restore:42":
			body = testLanzouConfirmation("folder_restore", "folder_id", "42") + empty
		default:
			t.Fatal("unexpected folder check", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ctx := context.Background()
	items, err := parseLanzouRecycle(root)
	if err != nil {
		t.Fatal(err)
	}
	scan := lanzouPurgeFolderScan{files: make(map[string][]lanzouFolderFile), errors: make(map[string]error)}
	members, err := readLanzouPurgeFolderMembers(ctx, client, account, items, &scan)
	if err == nil || members != nil || emptyReads != 1 {
		t.Fatal("incomplete membership was accepted or later inventory was skipped", err, emptyReads)
	}
	rows := (&Manager{}).lanzouRecycleCoverageLocked(account, items, members, scan)
	if rows[0].Entry != nil || rows[0].Limitation != "folder-markers" || rows[1].Entry == nil ||
		rows[2].Entry != nil || rows[2].Limitation != "folder-membership" {
		t.Fatal("incorrect eligibility with incomplete membership", rows)
	}
	contents, err := readLanzouListedFolderPurgeContents(ctx, client, account, *rows[1].Entry)
	if err != nil || len(contents.ids) != 0 {
		t.Fatal("independently verified empty folder could not be prepared", err)
	}
}

func TestLanzouRecycleCoveragePreparesSelectedItemsAcrossPages(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "fixture", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
	m := &Manager{accounts: []Account{a}, trashes: map[string]*trashOperation{}}
	entry := CloudEntry{ID: "1", Name: "same-name.txt"}
	m.trashes[entry.ID] = &trashOperation{saved: diskTrash{
		Source: trashSource{Purpose: lanzouTrashPurpose, Scope: downloadScope(a),
			Original: lanzouTrashEntry("17", entry), Input: TrashInput{AccountID: a.ID, ParentID: "17", Entry: entry}},
		Receipt: TrashReceipt{State: "confirmed"}, Submitted: true,
	}}

	lastName := "same-name.txt"
	listReads := 0
	var forms []string
	var fixtureMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != "up.woozooo.com" || r.URL.Path != "/mydisk.php" || r.URL.Query().Get("item") != "recycle" {
			t.Errorf("unexpected preview request %s host=%q URL=%v", r.Method, r.Host, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		switch r.URL.Query().Get("action") {
		case "files":
			listReads++
			for i := 1; i <= 101; i++ {
				name := "unselected…"
				if i == 1 {
					name = "same-name.txt"
				} else if i == 101 {
					name = lastName
				}
				fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="%d"><a href="/file"><img src="/filetype/txt.gif">%s</a></tr>`, i, name)
			}
		case "file_delete_complete":
			id := r.URL.Query().Get("file_id")
			forms = append(forms, id)
			fmt.Fprint(w, testLanzouConfirmation("file_delete_complete", "file_id", id))
		default:
			t.Error("unexpected action", r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	previous := http.DefaultTransport
	http.DefaultTransport = tr
	defer func() { http.DefaultTransport = previous; tr.CloseIdleConnections() }()
	report, err := m.PreviewLanzouRecycleCoverage(ctx, a.ID)
	rows := report.Rows
	if err != nil || len(rows) != 101 || rows[0].Entry == nil || rows[100].Entry == nil || rows[100].Page != 2 || rows[1].Entry != nil {
		t.Fatalf("unexpected preview coverage: %d, %v", len(rows), err)
	}
	selected := []RecycleEntry{*rows[0].Entry, *rows[100].Entry}
	if selected[0].RestoreHandle != "" || selected[1].RestoreHandle != "" {
		t.Fatal("preview included a restore handle")
	}
	if selected[1].FileID != "101" || selected[1].ParentID != "" || selected[1].OriginalPath != "" {
		t.Fatal("preview inferred an unknown original location")
	}
	prepared, err := m.PrepareRecyclePurge(ctx, a.ID, 0, selected)
	fixtureMu.Lock()
	reads, selectedForms := listReads, strings.Join(forms, ",")
	lastName = "renamed.txt"
	fixtureMu.Unlock()
	if err != nil || len(prepared) != 2 || reads != 2 || selectedForms != "1,101" {
		t.Fatalf("unexpected preparation: %d, reads=%d, forms=%s, err=%v", len(prepared), reads, selectedForms, err)
	}
	if stale, err := m.PrepareRecyclePurge(ctx, a.ID, 0, selected); err == nil || len(stale) != 0 {
		t.Fatal("stale cross-page selection prepared writable handles", err)
	}
}

func TestLanzouPurgeSelectedItem(t *testing.T) {
	for _, mode := range []string{"confirmed", "delayed-refresh", "no-local-receipt", "listed-confirmed", "listed-folder-present", "listed-folder-before-write", "listed-folder-after-write", "listed-lost-response", "listed-truncated", "listed-last-record", "changed", "wrong-form", "lost-response", "false-ack", "ambiguous-empty", "still-present",
		"listed-sibling", "listed-member", "listed-member-before-write", "listed-member-after-write", "listed-folder-disagreement", "listed-folder-nested",
		"listed-empty-folder", "listed-empty-folder-changed", "listed-empty-folder-disagreement", "listed-empty-folder-wrong-form", "listed-empty-folder-lost",
		"listed-filled-folder", "listed-filled-folder-renamed", "listed-filled-folder-root-missing", "listed-filled-folder-truncated", "listed-filled-folder-moved-after",
		"folder-empty", "folder-no-local-receipt", "folder-files", "folder-children", "folder-ambiguous", "folder-changed", "folder-lost-response",
		"folder-inventory", "folder-inventory-changed", "folder-inventory-renamed", "folder-inventory-child-remains", "folder-inventory-local-changed", "folder-inventory-root-missing"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a := Account{ID: "fixture", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture=auth"}
			m.accounts = []Account{a}
			listedFilled := strings.HasPrefix(mode, "listed-filled-folder")
			listedFolder := strings.HasPrefix(mode, "listed-empty-folder") || listedFilled
			entry := CloudEntry{ID: "42", Name: "disposable.txt", Folder: strings.HasPrefix(mode, "folder-") || listedFolder}
			listed := strings.HasPrefix(mode, "listed-") && !listedFolder
			if mode == "listed-truncated" {
				entry.Name = "dispos…"
			}
			inventory := strings.HasPrefix(mode, "folder-inventory")
			const fullName = "nested file with full name.txt"
			original := lanzouTrashEntry("17", entry)
			action, idField, otherID := "file_delete_complete", "file_id", "43"
			if entry.Folder {
				action, idField, otherID = "folder_delete_complete", "folder_id", "42"
			}
			if !listed && !listedFolder && mode != "no-local-receipt" && mode != "folder-no-local-receipt" {
				purpose := lanzouTrashPurpose
				if entry.Folder {
					purpose = lanzouFolderTrashPurpose
				}
				m.trashes["move"] = &trashOperation{saved: diskTrash{
					Source: trashSource{Purpose: purpose, Scope: downloadScope(a), Original: original,
						Input: TrashInput{AccountID: a.ID, ParentID: "17", Entry: entry}},
					Receipt:   TrashReceipt{ID: "move", AccountID: a.ID, Name: entry.Name, Original: original.OriginalPath, State: "confirmed", Created: time.Now()},
					Submitted: true,
				}}
				if inventory {
					m.trashes["move"].saved.LanzouContents = &lanzouFolderContents{Files: []lanzouFolderFile{{ID: "61", Name: fullName}}}
				}
			}
			writes, forms, previews := 0, 0, 0
			present, changed, listReadable := true, false, true
			folderVisible := mode == "listed-folder-present" || mode == "listed-sibling" || mode == "listed-member" ||
				mode == "listed-member-before-write" || mode == "listed-folder-disagreement" || mode == "listed-folder-nested"
			const folderFile = `<tr><td><a href="https://www.lanzoui.com/%s"><img src="/filetype/txt.gif">%s</a></td></tr>`
			folderFileID := "43"
			filledChildName := "child.txt"
			if mode == "listed-filled-folder-truncated" {
				filledChildName = "child..."
			}
			movedChild := false
			if mode == "listed-member" {
				folderFileID = "42"
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "up.woozooo.com" || r.Header.Get("Cookie") != "fixture=auth" ||
					r.URL.Path != "/mydisk.php" || r.URL.Query().Get("item") != "recycle" {
					t.Error("unexpected origin or account", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == "files":
					if !listReadable {
						fmt.Fprint(w, "<html><p>unrecognized page</p></html>")
						return
					}
					if present && entry.Folder {
						fmt.Fprint(w, `<tr><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=42">disposable.txt</a></tr>`)
					} else if present {
						name := entry.Name
						if changed {
							name = "dis..."
						}
						fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="42"><a href="/file"><img src="/filetype/txt.gif">%s</a></tr>`, name)
						if mode == "delayed-refresh" && writes != 0 {
							present = false
						}
					}
					if mode != "listed-last-record" || writes == 0 {
						fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="%s"><a href="/file"><img src="/filetype/txt.gif">keep.txt</a></tr>`, otherID)
					}
					if folderVisible {
						fmt.Fprint(w, `<tr><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=42">recycled folder</a></tr>`)
					}
					if listedFilled && present && mode != "listed-filled-folder-root-missing" {
						fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="61"><a href="/file"><img src="/filetype/txt.gif">%s</a></tr>`, filledChildName)
					}
					if movedChild {
						fmt.Fprint(w, `<tr><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=99">another folder</a></tr>`)
					}
					if inventory && mode != "folder-inventory-root-missing" && (writes == 0 || mode == "folder-inventory-child-remains" && !changed) {
						fmt.Fprint(w, `<tr><input name="fl_sel_ids[]" value="61"><a href="/file"><img src="/filetype/txt.gif">nested...</a></tr>`)
					}
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == "show_files" && listed && folderVisible:
					if r.URL.Query().Get("folder_id") != "42" {
						t.Error("unexpected listed-file folder", r.URL)
					}
					switch mode {
					case "listed-folder-present", "listed-folder-before-write", "listed-folder-after-write":
						fmt.Fprint(w, "<p>unrecognized folder page</p>")
					case "listed-folder-nested":
						fmt.Fprint(w, `<tr><td>该文件夹里没有包含文件</td><td><a href="?item=recycle&amp;action=show_files&amp;folder_id=99">nested folder</a></td></tr>`)
					default:
						fmt.Fprintf(w, folderFile, folderFileID, entry.Name)
					}
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == "folder_restore" && listed && folderVisible:
					formID := r.URL.Query().Get("folder_id")
					if formID != "42" {
						t.Error("unexpected folder confirmation target", r.URL)
					}
					name := entry.Name
					if mode == "listed-folder-disagreement" {
						name = "another.txt"
					}
					fmt.Fprint(w, testLanzouConfirmation("folder_restore", "folder_id", formID))
					fmt.Fprintf(w, folderFile, folderFileID, name)
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == "show_files" && entry.Folder:
					previews++
					if r.URL.Query().Get("folder_id") != "42" && !(movedChild && r.URL.Query().Get("folder_id") == "99") {
						t.Error("unexpected preview folder", r.URL)
					}
					const empty = `<tr><td>该文件夹里没有包含文件</td></tr>`
					switch {
					case listedFilled:
						fmt.Fprintf(w, folderFile, "61", filledChildName)
					case inventory:
						name := "nested..."
						if mode == "folder-inventory-renamed" {
							name = "renamed.txt"
						}
						fmt.Fprintf(w, `<tr><td><a href="/file"><img src="/filetype/txt.gif">%s</a></td></tr>`, name)
					case mode == "folder-files" || mode == "folder-changed" && previews > 1:
						fmt.Fprint(w, `<tr><td><a href="/file"><img src="/filetype/txt.gif">nested.txt</a></td></tr>`)
					case mode == "listed-empty-folder-changed" && changed:
						fmt.Fprintf(w, folderFile, "61", "new-file.txt")
					case mode == "folder-children":
						fmt.Fprint(w, empty+`<tr><td><a href="?item=recycle&amp;action=show_files&amp;folder_id=99">nested folder</a></td></tr>`)
					case mode == "folder-ambiguous":
						fmt.Fprint(w, `<script>该文件夹里没有包含文件</script>`)
					default:
						fmt.Fprint(w, empty)
					}
				case r.Method == http.MethodGet && listedFolder && r.URL.Query().Get("action") == "folder_restore":
					id := "42"
					if mode == "listed-empty-folder-wrong-form" {
						id = "43"
					} else if movedChild {
						id = "99"
					}
					fmt.Fprint(w, testLanzouConfirmation("folder_restore", "folder_id", id))
					if listedFilled {
						fmt.Fprintf(w, folderFile, "61", filledChildName)
					} else if mode == "listed-empty-folder-disagreement" {
						fmt.Fprintf(w, folderFile, "61", "hidden-file.txt")
					} else {
						fmt.Fprint(w, `<tr><td>此文件夹没有包含文件</td></tr>`)
					}
				case r.Method == http.MethodGet && inventory && r.URL.Query().Get("action") == "folder_restore":
					id, name := "61", "nested..."
					if mode == "folder-inventory-changed" && previews == 2 {
						id = "62"
					}
					if mode == "folder-inventory-renamed" {
						name = "renamed.txt"
					}
					fmt.Fprint(w, testLanzouConfirmation("folder_restore", "folder_id", "42"))
					fmt.Fprintf(w, `<tr><td><a href="https://www.lanzoui.com/%s"><img src="/filetype/txt.gif">%s</a></td></tr>`, id, name)
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == action:
					forms++
					id := r.URL.Query().Get(idField)
					if id != "42" {
						t.Error("unexpected confirmation target", id)
					}
					if mode == "wrong-form" && forms == 2 {
						id = "43"
					}
					fmt.Fprintf(w, `<form method="post" action="?item=recycle"><input type="hidden" name="action" value="%s"><input type="hidden" name="task" value="%s"><input type="hidden" name="%s" value="%s"><input type="hidden" name="formhash" value="proof%d"></form>`, action, action, idField, id, forms)
				case r.Method == http.MethodPost:
					writes++
					if r.ParseForm() != nil || len(r.PostForm) != 4 || r.PostForm.Get(idField) != "42" ||
						r.PostForm.Get("action") != action || r.PostForm.Get("task") != action ||
						r.PostForm.Get("formhash") != "proof2" || len(r.URL.Query()) != 1 {
						t.Error("unexpected deletion scope", r.PostForm)
					}
					state, err := readState(statePath)
					if err != nil || len(state.RecyclePurges) != 1 || !state.RecyclePurges[0].Submitted {
						t.Error("request sent without saved intent", err)
					} else if inventory && (len(state.RecyclePurges[0].Source.FolderFileIDs) != 1 || state.RecyclePurges[0].Source.FolderFileIDs[0] != "61") {
						t.Error("request sent without saved child inventory")
					} else if listed && state.RecyclePurges[0].Source.Purpose != lanzouListedFilePurgePurpose {
						t.Error("request sent without listed-file provenance")
					} else if listedFilled && (state.RecyclePurges[0].Source.Purpose != lanzouListedFolderPurgePurpose ||
						state.RecyclePurges[0].Source.Entry.OriginalPath != "" || strings.Join(state.RecyclePurges[0].Source.FolderFileIDs, ",") != "61" ||
						state.RecyclePurges[0].Source.FolderInventoryHash == "") {
						t.Error("request sent without saved webpage inventory")
					} else if listedFolder && !listedFilled && (state.RecyclePurges[0].Source.Purpose != lanzouListedEmptyFolderPurgePurpose || state.RecyclePurges[0].Source.Entry.OriginalPath != "" || len(state.RecyclePurges[0].Source.FolderFileIDs) != 0) {
						t.Error("request sent without valid listed-empty-folder provenance")
					}
					present = mode == "still-present" || mode == "delayed-refresh"
					movedChild = mode == "listed-filled-folder-moved-after"
					folderVisible = mode == "listed-folder-after-write" || mode == "listed-sibling" || mode == "listed-member-after-write"
					if mode == "listed-member-after-write" {
						folderFileID = "42"
					}
					listReadable = mode != "ambiguous-empty"
					switch mode {
					case "lost-response", "folder-lost-response", "listed-lost-response", "listed-empty-folder-lost":
						w.WriteHeader(http.StatusBadGateway)
					case "false-ack":
						fmt.Fprint(w, "<script>删除成功</script><p>无法确认是否删除成功</p>")
					default:
						fmt.Fprint(w, "<p>删除成功。</p>")
					}
				default:
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			before := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = before; tr.CloseIdleConnections() }()

			if _, err := m.PreviewRecycleEmpty(ctx, a.ID); err == nil {
				t.Fatal("selected purge must not allow full recycle-bin deletion")
			}
			page, err := m.ListRecycle(ctx, a.ID, 1)
			wantRows := 2
			if folderVisible {
				wantRows++
			}
			if inventory && mode != "folder-inventory-root-missing" {
				wantRows++
			}
			if listedFilled && mode != "listed-filled-folder-root-missing" {
				wantRows++
			}
			if err != nil || len(page.Items) != wantRows {
				t.Fatal(page, err)
			}
			selectedPage := 1
			if listed {
				selectedPage = 0
			}
			selected := page.Items[:1]
			if listedFolder {
				selectedPage = 0
				report, e := m.PreviewLanzouRecycleCoverage(ctx, a.ID)
				coverage := report.Rows
				eligible := mode != "listed-empty-folder-disagreement" && mode != "listed-empty-folder-wrong-form"
				eligible = eligible && mode != "listed-filled-folder-root-missing" && mode != "listed-filled-folder-truncated"
				if e != nil || len(coverage) != wantRows || (coverage[0].Entry != nil) != eligible || writes != 0 || forms != 0 {
					t.Fatal("folder coverage disagrees with the webpage inventory", coverage, e)
				}
				if eligible {
					selected = []RecycleEntry{*coverage[0].Entry}
				} else {
					entry, _ := lanzouListedRecycleEntry(selected[0], "folder")
					selected = []RecycleEntry{entry}
				}
			}
			if mode == "listed-sibling" || mode == "listed-member" || mode == "listed-folder-disagreement" {
				report, err := m.PreviewLanzouRecycleCoverage(ctx, a.ID)
				coverage := report.Rows
				if err != nil || len(coverage) != wantRows || (coverage[0].State == "candidate") != (mode == "listed-sibling") || writes != 0 {
					t.Fatal("coverage disagrees with folder membership", coverage, err)
				}
				if mode == "listed-folder-disagreement" && (report.IncompleteReason != "uncertain-inventory" ||
					report.VisibleFolders != 1 || report.CheckedFolders != 0) {
					t.Fatal("incomplete folder inventory was not reported", report)
				}
			}
			prepared, err := m.PrepareRecyclePurge(ctx, a.ID, selectedPage, selected)
			switch mode {
			case "no-local-receipt", "listed-folder-present", "listed-truncated", "listed-member", "listed-folder-disagreement", "listed-folder-nested",
				"listed-empty-folder-disagreement", "listed-empty-folder-wrong-form",
				"listed-filled-folder-root-missing", "listed-filled-folder-truncated",
				"folder-no-local-receipt", "folder-files", "folder-children", "folder-ambiguous", "folder-inventory-renamed", "folder-inventory-root-missing":
				if err == nil || len(prepared) != 0 || forms != 0 || writes != 0 {
					t.Fatal("unverified file or folder accepted", prepared, err, forms, writes)
				}
				return
			}
			if err != nil || len(prepared) != 1 || forms != 1 || writes != 0 {
				t.Fatal("invalid read-only preparation", prepared, err, forms, writes)
			}
			if inventory && (len(prepared[0].FolderFiles) != 1 || prepared[0].FolderFiles[0] != fullName) {
				t.Fatal("expected full filename in preview", prepared[0].FolderFiles)
			}
			if listedFilled && strings.Join(prepared[0].FolderFiles, ",") != "child.txt" {
				t.Fatal("webpage file was omitted from the preview", prepared[0].FolderFiles)
			}
			if mode == "folder-inventory-local-changed" {
				m.trashes["move"].saved.LanzouContents.Files[0].Name = "changed after preview.txt"
				if _, err := m.PurgeRecycle(ctx, prepared[0].Handle, "永久删除"); err == nil || writes != 0 {
					t.Fatal("stale local inventory was accepted", err, writes)
				}
				return
			}
			changed = mode == "changed" || mode == "listed-empty-folder-changed"
			if mode == "listed-filled-folder-renamed" {
				filledChildName = "renamed.txt"
			}
			folderVisible = mode == "listed-folder-before-write" || mode == "listed-sibling" || mode == "listed-member-before-write"
			if mode == "listed-member-before-write" {
				folderFileID = "42"
			}
			result, err := m.PurgeRecycle(ctx, prepared[0].Handle, "永久删除")
			want, wantWrites := "unknown", 1
			switch mode {
			case "changed", "wrong-form", "listed-folder-before-write", "listed-member-before-write", "listed-empty-folder-changed", "listed-filled-folder-renamed", "folder-changed", "folder-inventory-changed":
				want, wantWrites = "not_sent", 0
			case "confirmed", "delayed-refresh", "listed-confirmed", "listed-sibling", "listed-empty-folder", "listed-filled-folder", "folder-empty", "folder-inventory":
				want = "confirmed"
			}
			if err != nil || result.State != want || writes != wantWrites {
				t.Fatal(result, err, writes)
			}
			wantPreviews := 2
			if listedFolder {
				wantPreviews = 3
			}
			if entry.Folder && mode != "listed-filled-folder-moved-after" && previews != wantPreviews {
				t.Fatal("folder contents were not checked before preparing and sending", previews)
			}
			if want != "unknown" {
				return
			}
			m.Close()
			reopened, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if _, err := reopened.PurgeRecycle(ctx, prepared[0].Handle, "永久删除"); err == nil {
				t.Fatal("pending deletion was replayed")
			}
			present, listReadable = false, true
			folderVisible = false
			movedChild = false
			if mode == "ambiguous-empty" || mode == "still-present" || mode == "folder-inventory-child-remains" || mode == "listed-folder-after-write" || mode == "listed-member-after-write" || mode == "listed-filled-folder-moved-after" {
				want = "confirmed"
			}
			changed = true
			checked, err := reopened.CheckRecyclePurge(ctx, result.ID)
			if err != nil || checked.State != want || writes != 1 {
				t.Fatal("unexpected result after read-only restart check", checked, err, writes)
			}
		})
	}
}
