package transfer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLanzouTrashAndReadOnlyRecovery(t *testing.T) {
	for _, mode := range []string{"confirmed", "name-truncated", "changed-source", "folder-confirmed", "folder-no-inventory", "folder-nested", "folder-nested-after-preview", "folder-missing-list", "folder-wrong-recycle-type"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a := Account{ID: "fixture", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture=auth"}
			m.accounts = []Account{a}
			var mu sync.Mutex
			writes := 0
			didChange := false
			folder := strings.HasPrefix(mode, "folder-")
			const originalName = "完整名称但蓝奏回收站只显示开头的长文件.txt"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host != "up.woozooo.com" || r.Header.Get("Cookie") != "fixture=auth" {
					t.Error("wrong account or host", r.Host)
				}
				if r.Method == http.MethodGet && r.URL.Path == "/mydisk.php" && r.URL.Query().Get("action") == "show_files" && r.URL.Query().Get("folder_id") == "42" {
					fmt.Fprint(w, `<tr><td><a href="/123"><img src="/filetype/txt.gif">&nbsp;完整名称...</a></td></tr>`)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/mydisk.php" && r.URL.Query().Get("action") == "folder_restore" && r.URL.Query().Get("folder_id") == "42" {
					fmt.Fprint(w, testLanzouConfirmation("folder_restore", "folder_id", "42"),
						`<tr><td><a href="https://www.lanzoui.com/61"><img src="/filetype/txt.gif">&nbsp;完整名称...</a></td></tr>`)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/mydisk.php" && r.URL.Query().Get("action") == "files" {
					if folder && mode != "folder-wrong-recycle-type" {
						fmt.Fprint(w, `<tr><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=42">file.txt</a></tr>`)
						return
					}
					name := "file.txt"
					if mode == "name-truncated" {
						name = "file..."
					}
					fmt.Fprintf(w, `<tr><input name="fl_sel_ids[]" value="42"><a href="/file"><img src="/filetype/txt.gif">%s</a></tr>`, name)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/doupload.php" || r.ParseForm() != nil {
					t.Error("unexpected endpoint", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch r.Form.Get("task") {
				case "5":
					if folder && r.Form.Get("folder_id") == "42" {
						switch {
						case mode == "folder-no-inventory":
							w.WriteHeader(http.StatusBadGateway)
						case r.Form.Get("pg") == "1":
							fmt.Fprintf(w, `{"zt":1,"text":[{"id":"61","name_all":%q,"size":"1K"}]}`, originalName)
						default:
							fmt.Fprint(w, `{"zt":2,"text":[]}`)
						}
						return
					}
					if r.Form.Get("folder_id") != "17" || r.Form.Get("pg") != "1" {
						t.Error("wrong original folder", r.Form)
					}
					if writes == 0 && !didChange {
						fmt.Fprint(w, `{"zt":1,"text":[{"id":"42","name_all":"file.txt","size":"1K"}]}`)
					} else {
						fmt.Fprint(w, `{"zt":2,"text":[]}`)
					}
				case "47":
					switch r.Form.Get("folder_id") {
					case "17":
						if writes == 0 {
							fmt.Fprint(w, `{"zt":1,"info":[{"folderid":17,"name":"source"}],"text":[{"fol_id":42,"name":"file.txt"}]}`)
						} else {
							fmt.Fprint(w, `{"zt":1,"info":[{"folderid":17,"name":"source"}],"text":[]}`)
						}
					case "42":
						const prefix = `{"zt":1,"info":[{"folderid":17,"name":"source"},{"folderid":42,"name":"file.txt"}]`
						if mode == "folder-nested" || mode == "folder-nested-after-preview" && didChange {
							fmt.Fprint(w, prefix+`,"text":[{"fol_id":43,"name":"nested"}]}`)
						} else if mode == "folder-missing-list" {
							fmt.Fprint(w, prefix+`}`)
						} else {
							fmt.Fprint(w, prefix+`,"text":null}`)
						}
					default:
						t.Error("unexpected folder listing", r.Form)
					}
				case "3", "6":
					writes++
					task, idField := "6", "file_id"
					if folder {
						task, idField = "3", "folder_id"
					}
					if r.Form.Get("task") != task || r.Form.Get(idField) != "42" || len(r.Form) != 2 {
						t.Error("unexpected delete payload", r.Form)
					}
					persisted, err := readState(statePath)
					if err != nil || len(persisted.Trashes) != 1 || !persisted.Trashes[0].Submitted {
						t.Error("delete was sent without durable intent", err)
					} else if folder && mode != "folder-no-inventory" {
						contents := persisted.Trashes[0].LanzouContents
						if contents == nil || len(contents.Files) != 1 || contents.Files[0] != (lanzouFolderFile{ID: "61", Name: originalName}) {
							t.Error("original directory inventory was not saved before sending")
						}
					} else if persisted.Trashes[0].LanzouContents != nil {
						t.Error("unavailable or inapplicable directory produced an inventory")
					}
					w.WriteHeader(http.StatusBadGateway) // The write response can be lost.
				default:
					t.Error("unexpected task", r.Form)
					w.WriteHeader(http.StatusBadRequest)
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

			input := TrashInput{AccountID: a.ID, ParentID: "17", Entry: CloudEntry{ID: "42", Name: "file.txt", Folder: folder}}
			prepared, err := m.PrepareTrash(context.Background(), []TrashInput{input})
			if mode == "folder-nested" || mode == "folder-missing-list" {
				mu.Lock()
				defer mu.Unlock()
				if err == nil || len(prepared) != 0 || writes != 0 || recyclePurgeProvider(a) {
					t.Fatal("unexpected write eligibility", prepared, err)
				}
				return
			}
			if err != nil || len(prepared) != 1 {
				t.Fatal(prepared, err)
			}
			if mode == "changed-source" || mode == "folder-nested-after-preview" {
				mu.Lock()
				didChange = true
				mu.Unlock()
			}
			got, err := m.TrashEntry(context.Background(), prepared[0].Handle)
			expected, expectedWrites := "confirmed", 1
			if mode == "name-truncated" || mode == "folder-wrong-recycle-type" {
				expected = "unknown"
			} else if mode == "changed-source" || mode == "folder-nested-after-preview" {
				expected, expectedWrites = "not_sent", 0
			}
			if err != nil || got.State != expected {
				t.Fatal(got, err)
			}
			if _, err := m.CheckTrash(context.Background(), got.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "folder-confirmed" || mode == "folder-no-inventory" {
				m.Close()
				reopened, err := NewManager(statePath, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				preview, err := reopened.ListLanzouRecycleFolder(context.Background(), a.ID, "folder:42", input.Entry.Name)
				if err != nil || len(preview.RemoteFiles) != 1 || preview.RemoteFiles[0] != "完整名称..." {
					t.Fatal("unexpected remote preview", preview, err)
				}
				if mode == "folder-confirmed" {
					if !preview.HasOriginal || len(preview.OriginalFiles) != 1 || preview.OriginalFiles[0] != originalName {
						t.Fatal("original list unavailable after restart", preview)
					}
					if preview.Comparison == nil || *preview.Comparison != (LanzouRecycleFolderComparison{Matching: 1}) {
						t.Fatal("file IDs were not compared after restart", preview.Comparison)
					}
				} else if preview.HasOriginal {
					t.Fatal("failed capture was presented as an original inventory")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if writes != expectedWrites {
				t.Fatal("unexpected write count", writes)
			}
		})
	}
}
