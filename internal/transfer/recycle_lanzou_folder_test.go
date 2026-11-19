package transfer

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestLanzouRecycleFolderPreviewIsReadOnly(t *testing.T) {
	for _, mode := range []string{"files", "empty", "folder-rows", "wrong-folder", "redirect", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != "up.woozooo.com" || r.URL.Path != "/mydisk.php" ||
					r.URL.Query().Get("item") != "recycle" || r.Header.Get("Cookie") != "fixture=auth" {
					t.Fatal("unexpected request", r.Method, r.URL)
				}
				status, headers := http.StatusOK, http.Header{}
				var body string
				switch r.URL.Query().Get("action") {
				case "files":
					body = `<tr><input name="fd_sel_ids[]" value="42"><a href="?item=recycle&amp;action=show_files&amp;folder_id=42">资料</a></tr>`
				case "show_files":
					if r.URL.Query().Get("folder_id") != "42" {
						t.Fatal("incorrect folder", r.URL)
					}
					body = `<tr><td><a href="https://www.lanzoui.com/123"><img src="filetype/txt.gif">&nbsp;旧 &amp; 文件...</a></td></tr>`
					if mode == "empty" {
						body = `<tr><td>该文件夹里没有包含文件</td></tr>`
					} else if mode == "folder-rows" {
						body += `<tr><td><a href="?item=recycle&amp;action=show_files&amp;folder_id=99">目录导航</a></td></tr>` +
							`<tr><td><input name="fd_sel_ids[]" value="43"><a href="?item=recycle&amp;action=show_files&amp;folder_id=43">目录甲</a></td></tr>` +
							`<tr><td><input name="fd_sel_ids[]" value="44"><a href="https://elsewhere.invalid/mydisk.php?item=recycle&amp;action=show_files&amp;folder_id=44">外站目录</a></td></tr>`
					} else if mode == "ambiguous" {
						body = `<script>该文件夹里没有包含文件</script>`
					} else if mode == "redirect" {
						status = http.StatusFound
						headers.Set("Location", "https://elsewhere.invalid/")
					}
				default:
					t.Fatal("unexpected action", r.URL)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			a := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture=auth"}
			name := "资料"
			if mode == "wrong-folder" {
				name = "别的资料"
			}
			preview, err := readLanzouRecycleFolder(context.Background(), client, a, "folder:42", name)
			if mode == "files" || mode == "empty" || mode == "folder-rows" {
				var want []string
				var wantFolders []string
				if mode != "empty" {
					want = []string{"旧 & 文件..."}
				}
				if mode == "folder-rows" {
					wantFolders = []string{"目录甲"}
				}
				if err != nil || !slices.Equal(preview.RemoteFiles, want) || !slices.Equal(preview.RemoteFolders, wantFolders) ||
					preview.HasAdditionalEntries != (mode == "folder-rows") {
					t.Fatal(preview, err)
				}
			} else if err == nil {
				t.Fatal("ambiguous folder accepted", preview)
			}
			wantCalls := 2
			if mode == "wrong-folder" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("unexpected requests", calls)
			}
		})
	}
}

func TestLanzouRecycleFolderLinksRequireMatchingVisibleRootRow(t *testing.T) {
	folder := func(id, name string) string {
		return `<tr><td><input name="fd_sel_ids[]" value="` + id + `"><a href="?item=recycle&amp;action=show_files&amp;folder_id=` + id + `">` + name + `</a></td></tr>`
	}
	body := folder("41", "原页") + folder("42", "可浏览") + folder("43", "仅此页") +
		folder("44", "改名") + folder("45", "重复") + folder("45", "重复") +
		`<tr><td><a href="?item=recycle&amp;action=folder_restore&amp;folder_id=46">旧表单</a></td></tr>`
	names, ids := lanzouRecycleFolderRows(body, "folder:41", map[string]string{
		"folder:41": "原页", "folder:42": "可浏览", "folder:44": "旧名",
		"folder:45": "重复", "folder:46": "旧表单",
	})
	if !slices.Equal(names, []string{"可浏览", "仅此页", "改名", "重复", "旧表单"}) ||
		!slices.Equal(ids, []string{"folder:42", "", "", "", ""}) {
		t.Fatalf("names = %v, IDs = %v", names, ids)
	}
}

func TestLanzouRecycleInventoryComparison(t *testing.T) {
	file := func(target, name string) string {
		return `<tr><td><a href="` + target + `"><img src="/filetype/txt.gif">` + name + `</a></td></tr>`
	}
	form := testLanzouConfirmation("folder_restore", "folder_id", "42")
	first, second := file("https://www.lanzoui.com/81", "重复..."), file("https://www.lanzoui.com/82", "重复...")
	for _, tt := range []struct {
		name, body string
		ok         bool
	}{
		{"same visible names, different identities", form + first + second, true},
		{"duplicate ID", form + first + first, false},
		{"external anchor", form + first + file("https://example.invalid/82", "重复..."), false},
		{"different visible name", form + first + file("https://www.lanzoui.com/82", "已变"), false},
		{"wrong folder", testLanzouConfirmation("folder_restore", "folder_id", "43") + first + second, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Host != "up.woozooo.com" ||
					r.URL.Query().Get("action") != "folder_restore" || r.URL.Query().Get("folder_id") != "42" {
					t.Fatal(r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}
			a := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
			local := &lanzouFolderContents{Files: []lanzouFolderFile{{"81", "原始完整名字.txt"}, {"80", "另一份文件.txt"}}}
			got := compareLanzouFolderInventory(context.Background(), client, a, "42", []string{"重复...", "重复..."}, local, false)
			if tt.ok && (got == nil || *got != (LanzouRecycleFolderComparison{1, 1, 1})) || !tt.ok && got != nil {
				t.Fatal("unexpected comparison", got)
			}
		})
	}
}
