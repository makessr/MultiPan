package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedFileBrowserProviders(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu", "guangya", "ctfile"} {
		t.Run(kind, func(t *testing.T) {
			a := Account{Kind: kind, Secret: "synthetic-secret", Username: "synthetic-device"}
			a.Endpoint = map[string]string{"quark": QuarkAPI, "pan123": Pan123API, "baidu": BaiduAPI, "guangya": GuangyaAPI, "ctfile": CtFileAPI}[kind]
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !strings.Contains(r.URL.Path, "list") && !strings.HasSuffix(r.URL.Path, "/file/sort") {
					t.Fatalf("unexpected write or endpoint: %s", r.URL.Path)
				}
				switch kind {
				case "quark":
					return quarkResponse(`{"status":200,"code":0,"data":{"list":[{"fid":"dir","file_name":"资料","dir":true},{"fid":"file","file_name":"测试.zip","size":12}]}}`, nil), nil
				case "pan123":
					return quarkResponse(`{"code":0,"data":{"Next":"-1","Total":2,"InfoList":[{"FileId":10,"FileName":"资料","Type":1},{"FileId":11,"FileName":"测试.zip","Type":0,"Size":12}]}}`, nil), nil
				case "baidu":
					return quarkResponse(`{"errno":0,"list":[{"fs_id":10,"path":"/资料","server_filename":"资料","isdir":1},{"fs_id":11,"path":"/测试.zip","server_filename":"测试.zip","isdir":0,"size":12}]}`, nil), nil
				case "guangya":
					return quarkResponse(`{"data":{"total":2,"list":[{"fileId":"dir","fileName":"资料","resType":2},{"fileId":"file","fileName":"测试.zip","resType":1,"fileSize":12}]}}`, nil), nil
				case "ctfile":
					if strings.Contains(r.URL.Path, "/folder/") {
						return quarkResponse(`{"code":200,"results":[{"key":"d10","name":"资料"}]}`, nil), nil
					}
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["start"].(float64) > 0 {
						return quarkResponse(`{"code":200,"results":[]}`, nil), nil
					}
					return quarkResponse(`{"code":200,"results":[{"key":"f11","name":"测试.zip","size":12,"status":1}]}`, nil), nil
				}
				panic("unexpected kind")
			})}
			p, e := ListAccountFiles(context.Background(), client, a, "")
			if e != nil {
				t.Fatal(e)
			}
			if p.CurrentID != cloudRoot(kind) || len(p.Breadcrumbs) != 1 || len(p.Entries) != 2 || !p.Entries[0].Folder || p.Entries[1].Folder || p.Entries[1].Name != "测试.zip" || p.Entries[1].Size == nil || *p.Entries[1].Size != 12 {
				t.Fatalf("wrong mapped files: %+v", p)
			}
			encoded, _ := json.Marshal(p)
			if strings.Contains(string(encoded), a.Secret) || calls > 3 {
				t.Fatal("credential exposed or redundant requests")
			}
			client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
				res := quarkResponse("<html>expired</html>", nil)
				res.StatusCode = 401
				return res, nil
			})
			if _, e = ListAccountFiles(context.Background(), client, a, ""); e == nil {
				t.Fatal("expired login presented as empty directory")
			}
		})
	}
}

func TestLanzouFileBrowserPaginationAndFailure(t *testing.T) {
	for _, mode := range []string{"pages", "repeat", "expired", "bad-data"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/doupload.php" || r.Header.Get("Cookie") != "phpdisk_info=synthetic" {
					t.Fatal("wrong read-only request")
				}
				r.ParseForm()
				if r.Form.Get("folder_id") != "42" {
					t.Fatal("wrong selected folder")
				}
				if r.Form.Get("task") == "47" {
					return quarkResponse(`{"zt":1,"text":[{"fol_id":43,"name":"资料"}],"info":[{"folderid":42,"name":"测试"}]}`, nil), nil
				}
				if r.Form.Get("task") != "5" {
					t.Fatal("write task used")
				}
				calls++
				if mode == "expired" {
					return quarkResponse(`{"zt":9}`, nil), nil
				}
				if mode == "bad-data" {
					return quarkResponse(`{"zt":1,"text":"unexpected"}`, nil), nil
				}
				if calls == 1 || mode == "repeat" {
					items := []any{}
					for i := 0; i < 18; i++ {
						items = append(items, map[string]any{"id": 100 + i, "name_all": fmt.Sprintf("测试 &amp; %d.zip", i), "size": "1.2 M"})
					}
					b, _ := json.Marshal(map[string]any{"zt": 1, "text": items})
					return quarkResponse(string(b), nil), nil
				}
				return quarkResponse(`{"zt":1,"text":[{"id":"999","name_all":"末页.zip","size":"0 B"}]}`, nil), nil
			})}
			p, e := ListAccountFiles(context.Background(), client, Account{Kind: "lanzou", Endpoint: "https://lanzou.example", Secret: "phpdisk_info=synthetic"}, "42")
			if mode != "pages" {
				if e == nil {
					t.Fatal("failure accepted")
				}
				return
			}
			if e != nil || calls != 2 || len(p.Entries) != 20 || p.Entries[1].Name != "测试 & 0.zip" || p.Entries[1].SizeText != "1.2 M" || p.Entries[19].ID != "999" || p.Breadcrumbs[len(p.Breadcrumbs)-1].Name != "测试" {
				t.Fatalf("paging %v %+v", e, p)
			}
		})
	}
}

func TestDAVFileBrowserBoundaries(t *testing.T) {
	const self = `<d:response><d:href>/dav/资料/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	const file = `<d:response><d:href>/dav/资料/%E6%B5%8B%E8%AF%95.zip</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>0</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	bad := []string{"https://other.example/dav/资料/泄漏.zip", "/dav/其他/越界.zip", "/dav/资料/下级/非直接子文件.zip", "/outside.zip"}
	body := self + file
	for _, href := range bad {
		body += `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" || r.URL.Path != "/dav/资料/" {
			t.Fatal("unexpected request")
		}
		return quarkResponse(`<d:multistatus xmlns:d="DAV:">`+body+`</d:multistatus>`, nil), nil
	})}
	a := Account{Kind: "ctdav", Endpoint: "https://dav.example/dav", Username: "user", Secret: "synthetic"}
	p, e := ListAccountFiles(context.Background(), client, a, "/资料")
	if e != nil || len(p.Entries) != 1 || p.Entries[0].ID != "/资料/测试.zip" || p.Entries[0].Folder || p.Entries[0].Size == nil || *p.Entries[0].Size != 0 || len(p.Breadcrumbs) != 2 {
		t.Fatalf("DAV file metadata/boundaries %v %+v", e, p)
	}
	body = strings.ReplaceAll(self+file, "<d:getcontentlength>0</d:getcontentlength>", "<d:getcontentlength/>")
	p, e = ListAccountFiles(context.Background(), client, a, "/资料")
	if e != nil || len(p.Entries) != 1 || p.Entries[0].Size != nil {
		t.Fatalf("optional DAV length must not break browsing: %v %+v", e, p)
	}
	body = file
	if _, e = ListAccountFiles(context.Background(), client, a, "/资料"); e == nil {
		t.Fatal("missing self collection accepted")
	}
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.ListFiles(context.Background(), "missing", ""); e == nil {
		t.Fatal("missing account accepted")
	}
	account, e := m.SaveAccount(AccountInput{Name: "DAV", Kind: "webdav", Endpoint: a.Endpoint, Username: a.Username, Secret: a.Secret, Folder: "/"})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.SetDefaultFolder(account.ID, "/资料", "根目录 / 资料"); e != nil {
		t.Fatal(e)
	}
	if m.Snapshot().Accounts[0].Folder != "/资料" {
		t.Fatal("browser selection did not update DAV default")
	}
	if e = m.SetDefaultFolder(account.ID, "/../escape", "越界"); e == nil {
		t.Fatal("invalid DAV path persisted")
	}
}
