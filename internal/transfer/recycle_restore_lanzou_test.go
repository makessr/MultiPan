package transfer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLanzouRestoreUsesConfirmedMoveAndExactForm(t *testing.T) {
	for _, mode := range []string{"confirmed", "folder-confirmed", "folder-contents", "folder-delayed", "folder-web-contents", "folder-web-delayed", "folder-wrong-id", "folder-renamed", "folder-extra", "folder-nested", "wrong-form", "missing-receipt", "ambiguous-receipt", "no-origin", "external-folder", "external-truncated", "external-conflict", "non-root-file", "root-conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a := Account{ID: "a", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture=auth"}
			m.accounts = []Account{a}
			parent := "-1"
			folder := strings.HasPrefix(mode, "folder-") || mode == "external-folder"
			fromRecycle := strings.HasPrefix(mode, "folder-web-")
			hasContents := folder && strings.HasPrefix(mode, "folder-") && mode != "folder-confirmed" && !fromRecycle
			verifyContents := hasContents || fromRecycle
			delayed := mode == "folder-delayed" || mode == "folder-web-delayed"
			external := mode == "no-origin" || strings.HasPrefix(mode, "external-")
			if mode == "non-root-file" || mode == "root-conflict" || folder {
				parent = "12"
			}
			destination := "-1"
			if folder {
				destination = parent
			}
			action, idField, purpose := "file_restore", "file_id", lanzouTrashPurpose
			if folder {
				action, idField, purpose = "folder_restore", "folder_id", lanzouFolderTrashPurpose
			}
			if !external {
				item := CloudEntry{ID: "42", Name: "file.txt", Folder: folder}
				s := trashSource{Purpose: purpose, Scope: downloadScope(a), Input: TrashInput{AccountID: a.ID, ParentID: parent, Entry: item}, Original: lanzouTrashEntry(parent, item)}
				m.trashes = map[string]*trashOperation{"moved": {saved: diskTrash{Source: s, Submitted: true, Receipt: TrashReceipt{
					ID: "moved", AccountID: a.ID, Name: item.Name, Original: s.Original.OriginalPath, State: "confirmed", Created: time.Now(),
				}}}}
				if hasContents {
					m.trashes["moved"].saved.LanzouContents = &lanzouFolderContents{Files: []lanzouFolderFile{{ID: "61", Name: "完整文件名.txt"}}}
				}
			}
			var writes atomic.Int32
			var filesReady atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == "files":
					if folder {
						fmt.Fprint(w, `<tr><input name="fd_sel_ids[]" value="42"><a href="?item=recycle&amp;action=show_files&amp;folder_id=42">file.txt</a><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=42"><img src="restore_icon.gif"></a></tr>`)
						if fromRecycle {
							fmt.Fprint(w, `<tr><input name="fl_sel_ids[]" value="61"><a href="/file"><img src="/filetype/txt.gif">完整文件名.txt</a></tr>`)
						}
					} else {
						name := "file.txt"
						if mode == "external-truncated" {
							name = "original-name…"
						}
						fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="42"><a href="/file"><img src="/filetype/txt.gif">%s</a></tr>`, name)
					}
				case r.Method == http.MethodGet && folder && r.URL.Query().Get("action") == "show_files":
					if fromRecycle {
						fmt.Fprint(w, `<tr><td><a href="/file"><img src="/filetype/txt.gif">完整文件名.txt</a></td></tr>`)
					}
				case r.Method == http.MethodGet && r.URL.Query().Get("action") == action:
					id := "42"
					if mode == "wrong-form" {
						id = "43"
					}
					fmt.Fprint(w, testLanzouConfirmation(action, idField, id))
					if fromRecycle {
						fmt.Fprint(w, `<tr><td><a href="https://fixture.lanzoub.com/61"><img src="/filetype/txt.gif">完整文件名.txt</a></td></tr>`)
					}
				case r.Method == http.MethodPost && r.URL.Path == "/mydisk.php":
					writes.Add(1)
					if r.URL.RawQuery != "item=recycle" || r.ParseForm() != nil || r.PostForm.Get(idField) != "42" || r.PostForm.Get("task") != action || r.PostForm.Get("action") != action || r.PostForm.Get("formhash") == "" {
						t.Error("invalid restore form")
					}
					d, err := readState(statePath)
					if err != nil || len(d.RecycleRestores) != 1 || !d.RecycleRestores[0].Submitted {
						t.Error("restore submitted before durable intent", err)
					} else if (d.RecycleRestores[0].LanzouContents != nil) != verifyContents || d.RecycleRestores[0].LanzouContentsFromRecycle != fromRecycle {
						t.Error("inventory and provenance were not persisted before restore")
					}
					if mode == "missing-receipt" {
						w.WriteHeader(http.StatusBadGateway)
					} else if mode == "ambiguous-receipt" {
						fmt.Fprint(w, "<p>未恢复成功，请重试</p><script>'恢复成功'</script>")
					} else {
						fmt.Fprint(w, "<p>恢复成功</p>")
					}
				case r.Method == http.MethodPost && r.URL.Path == "/doupload.php" && r.ParseForm() == nil:
					if verifyContents && r.Form.Get("folder_id") == "42" {
						switch r.Form.Get("task") {
						case "47":
							items := "[]"
							if mode == "folder-nested" {
								items = `[{"fol_id":60,"name":"nested"}]`
							}
							fmt.Fprintf(w, `{"zt":1,"info":[{"folderid":12,"name":"source"},{"folderid":42,"name":"file.txt"}],"text":%s}`, items)
						case "5":
							if r.Form.Get("pg") != "1" || delayed && !filesReady.Load() {
								fmt.Fprint(w, `{"zt":2,"text":[]}`)
								return
							}
							items := `[{"id":"61","name_all":"完整文件名.txt","size":"1K"}]`
							switch mode {
							case "folder-wrong-id":
								items = strings.Replace(items, `"61"`, `"62"`, 1)
							case "folder-renamed":
								items = strings.Replace(items, "完整文件名.txt", "改名.txt", 1)
							case "folder-extra":
								items = strings.TrimSuffix(items, "]") + `,{"id":"62","name_all":"多余文件.txt","size":"1K"}]`
							}
							fmt.Fprintf(w, `{"zt":1,"text":%s}`, items)
						default:
							t.Error("unexpected inventory operation", r.Form)
						}
						return
					}
					if r.Form.Get("folder_id") != destination {
						t.Error("unexpected target directory", r.Form)
					}
					switch r.Form.Get("task") {
					case "47":
						if folder {
							if writes.Load() > 0 {
								fmt.Fprint(w, `{"zt":1,"info":[{"folderid":12,"name":"source"}],"text":[{"fol_id":42,"name":"file.txt"}]}`)
							} else {
								fmt.Fprint(w, `{"zt":1,"info":[{"folderid":12,"name":"source"}],"text":[]}`)
							}
						} else {
							fmt.Fprint(w, `{"zt":1,"info":[{"folderid":-1,"name":"首页"}],"text":[]}`)
						}
					case "5":
						if mode == "root-conflict" || mode == "external-conflict" {
							fmt.Fprint(w, `{"zt":1,"text":[{"id":"43","name_all":"FILE.txt","size":"1K"}]}`)
						} else if !folder && writes.Load() > 0 {
							fmt.Fprint(w, `{"zt":1,"text":[{"id":"42","name_all":"file.txt","size":"1K"}]}`)
						} else {
							fmt.Fprint(w, `{"zt":2,"text":[]}`)
						}
					default:
						t.Error("unexpected operation", r.Form)
					}
				default:
					t.Error("unexpected endpoint", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			before := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = before }()

			list, err := m.ListRecycle(ctx, a.ID, 1)
			wantItems := 1
			if fromRecycle {
				wantItems = 2
			}
			if err != nil || len(list.Items) != wantItems || list.Items[0].ID != map[bool]string{false: "file:42", true: "folder:42"}[folder] {
				t.Fatalf("expected %d rows with the target first, got %d rows, error: %v", wantItems, len(list.Items), err)
			}
			handle := list.Items[0].RestoreHandle
			if mode == "external-folder" || mode == "external-truncated" {
				if handle != "" {
					t.Fatal("unexpected restore eligibility")
				}
				return
			}
			recordedParent := parent
			if external {
				recordedParent = ""
			}
			if handle == "" || list.Items[0].ParentID != recordedParent {
				t.Fatal("expected restore handle without changing the recorded original directory")
			}
			if !folder {
				preview, err := m.PreviewLanzouRecycleRestore(ctx, handle)
				blocked := mode == "wrong-form" || mode == "root-conflict" || mode == "external-conflict"
				if (err != nil) != blocked || !blocked && preview.Evidence != "root" ||
					writes.Load() != 0 || len(m.RecycleRestoreHistory(a.ID)) != 0 {
					t.Fatal("unexpected file preview result or writes", preview, err)
				}
			}
			if folder {
				preview, err := m.PreviewLanzouRecycleRestore(ctx, handle)
				if err != nil || writes.Load() != 0 || len(m.RecycleRestoreHistory(a.ID)) != 0 {
					t.Fatal("folder preview failed or wrote state", preview, err)
				}
				wantEvidence := "position"
				if fromRecycle {
					wantEvidence = "recycle"
				} else if hasContents {
					wantEvidence = "local"
				}
				if preview.Evidence != wantEvidence ||
					verifyContents && (len(preview.Files) != 1 || preview.Files[0] != "完整文件名.txt") ||
					!verifyContents && len(preview.Files) != 0 {
					t.Fatal("folder preview misstated the source or contents", preview)
				}
			}
			if external {
				source, err := decodeRecycleRestore(handle)
				if err != nil || source.Purpose != lanzouListedFileRestorePurpose || source.Entry.OriginalPath != "" || validLanzouRestoreEntry(source.Entry) {
					t.Fatal("web-listed restore did not stay separate from verified local origin", err)
				}
			}
			if mode == "non-root-file" || mode == "root-conflict" || external {
				err := m.ValidateRecycleRestoreBatch(ctx, []string{handle})
				if (err != nil) != (mode == "root-conflict" || mode == "external-conflict") || writes.Load() != 0 {
					t.Fatal("unexpected batch preflight result", err)
				}
			}
			got, err := m.RestoreRecycle(ctx, handle)
			want, wantWrites := "confirmed", int32(1)
			if mode == "wrong-form" || mode == "root-conflict" || mode == "external-conflict" {
				want, wantWrites = "not_sent", 0
			} else if mode == "missing-receipt" || mode == "ambiguous-receipt" {
				want = "unknown"
			}
			if verifyContents && mode != "folder-contents" && mode != "folder-web-contents" {
				want = "unknown"
			}
			if err != nil || got.State != want {
				t.Fatal(got, err)
			}
			if mode == "folder-web-contents" && !strings.Contains(got.Message, "还原前回收站网页清单") {
				t.Fatal("web inventory confirmation was not distinguished", got)
			}
			if mode == "non-root-file" || mode == "folder-confirmed" || mode == "no-origin" {
				location, err := m.LocateRecycleRestore(ctx, a.ID, got.ID)
				if err != nil || location.AccountID != a.ID || location.ParentID != destination {
					t.Fatal("unexpected restore directory", location, err)
				}
			}
			if mode == "no-origin" {
				m.Close()
				reopened, err := NewManager(statePath, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				history := reopened.RecycleRestoreHistory(a.ID)
				if len(history) != 1 || history[0].State != "confirmed" {
					t.Fatal("web-listed restore receipt lost on restart", history)
				}
				repeated, err := reopened.RestoreRecycle(ctx, handle)
				if err != nil || repeated.ID != got.ID {
					t.Fatal("web-listed restore did not reuse its receipt", repeated, err)
				}
			} else if delayed {
				if count, err := m.ClearTrashHistory(a.ID); err != nil || count != 1 {
					t.Fatal("original move receipt was not cleared", count, err)
				}
				m.Close()
				reopened, err := NewManager(statePath, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				filesReady.Store(true)
				checked, err := reopened.CheckRecycleRestore(ctx, got.ID)
				if err != nil || checked.State != "confirmed" {
					t.Fatal("read-only verification did not use the saved restore inventory", checked, err)
				}
				if fromRecycle && !strings.Contains(checked.Message, "还原前回收站网页清单") {
					t.Fatal("web inventory provenance was lost after restart", checked)
				}
			} else if _, err = m.CheckRecycleRestore(ctx, got.ID); err != nil {
				t.Fatal(err)
			}
			if writes.Load() != wantWrites {
				t.Fatal("unexpected write count", writes.Load())
			}
		})
	}
}
