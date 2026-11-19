package transfer

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanzouCreateFolderAndVerify(t *testing.T) {
	for _, mode := range []string{"success", "duplicate", "refused", "expired", "html", "network", "wrong-name", "verify-failed", "ambiguous", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, reads := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/doupload.php" || r.Header.Get("Cookie") != "phpdisk_info=synthetic" {
					t.Fatal("incorrect request")
				}
				r.ParseForm()
				switch r.Form.Get("task") {
				case "47":
					reads++
					if r.Form.Get("folder_id") != "42" {
						t.Fatal("wrong parent listed")
					}
					if mode == "duplicate" {
						return quarkResponse(`{"zt":1,"text":[{"fol_id":43,"name":"资料&amp;备份"}]}`, nil), nil
					}
					if writes == 0 {
						return quarkResponse(`{"zt":1,"text":[{"fol_id":40,"name":"已有"}]}`, nil), nil
					}
					if mode == "verify-failed" {
						return quarkResponse(`<html>phpdisk_info=synthetic</html>`, nil), nil
					}
					if mode == "wrong-name" {
						return quarkResponse(`{"zt":1,"text":[{"fol_id":43,"name":"其他"}]}`, nil), nil
					}
					if mode == "ambiguous" {
						return quarkResponse(`{"zt":1,"text":[{"fol_id":43,"name":"资料&amp;备份"},{"fol_id":44,"name":"资料&amp;备份"}]}`, nil), nil
					}
					return quarkResponse(`{"zt":1,"text":[{"fol_id":"123456789012345678","name":"资料&amp;备份"}]}`, nil), nil
				case "18":
					return quarkResponse(`{"zt":1,"info":{"name":"资料&amp;备份","onof":"0","pwd":"ignored-stale"}}`, nil), nil
				case "2":
					writes++
					if r.Form.Get("parent_id") != "42" || r.Form.Get("folder_name") != "资料&备份" || r.Form.Get("folder_description") != "" {
						t.Fatal("wrong mutation fields")
					}
					switch mode {
					case "refused":
						return quarkResponse(`{"zt":0,"info":"phpdisk_info=synthetic"}`, nil), nil
					case "expired":
						return quarkResponse(`{"zt":9}`, nil), nil
					case "html":
						return quarkResponse(`<html>phpdisk_info=synthetic</html>`, nil), nil
					case "network":
						return nil, errors.New("private response phpdisk_info=synthetic")
					case "cancel":
						cancel()
					}
					return quarkResponse(`{"zt":"1","text":"not a reliable identifier"}`, nil), nil
				default:
					t.Fatal("unexpected task")
					return nil, nil
				}
			})}
			folder, e := CreateLanzouFolder(ctx, client, Account{Endpoint: "https://lanzou.example", Secret: "phpdisk_info=synthetic"}, "42", "资料&备份")
			if mode == "success" {
				if e != nil || folder.ID != "123456789012345678" || folder.Name != "资料&备份" || reads != 2 || writes != 1 {
					t.Fatalf("incorrect create/confirmation: %+v %v reads=%d writes=%d", folder, e, reads, writes)
				}
				return
			}
			if e == nil || writes > 1 || strings.Contains(e.Error(), "phpdisk_info=synthetic") {
				t.Fatalf("failure accepted/replayed/leaked %v writes=%d", e, writes)
			}
			if mode == "duplicate" && writes != 0 {
				t.Fatal("same-name folder caused write")
			}
		})
	}
}

func TestLanzouFolderNameValidationBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid input reached network")
		return nil, nil
	})}
	for _, name := range []string{"", ".", "..", "有 空格", "带(括号)", "含\n换行", "../越界", "a\\b", "a\x00b", strings.Repeat("中", 51)} {
		if _, e := CreateLanzouFolder(context.Background(), client, Account{}, "42", name); e == nil {
			t.Errorf("invalid name accepted: %q", name)
		}
	}
	if _, e := CreateLanzouFolder(context.Background(), client, Account{}, "invalid", "资料"); e == nil {
		t.Fatal("invalid parent accepted")
	}
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.CreateFolder(context.Background(), "missing", "42", "资料"); e == nil {
		t.Fatal("missing account accepted")
	}
	a, e := m.SaveAccount(AccountInput{Kind: "ctfile", Name: "OpenAPI", Username: "public", Secret: "synthetic-session-token", Folder: "d0"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.CreateFolder(context.Background(), a.ID, "/", "资料"); e == nil {
		t.Fatal("unsupported provider mutated")
	}
}

func TestLanzouNewFolderDefaultsToNoPassword(t *testing.T) {
	for _, mode := range []string{"protected", "already-public", "stale-password", "lost-write-ack", "still-protected", "rejected", "missing-switch", "changed-name", "verification-failed"} {
		t.Run(mode, func(t *testing.T) {
			creates, settings, details := 0, 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/doupload.php" || r.Header.Get("Cookie") != "phpdisk_info=synthetic" {
					t.Fatal("wrong request boundary")
				}
				_ = r.ParseForm()
				switch r.Form.Get("task") {
				case "47":
					if creates == 0 {
						return quarkResponse(`{"zt":1,"text":[{"fol_id":40,"name":"已有加密目录"}]}`, nil), nil
					}
					return quarkResponse(`{"zt":1,"text":[{"fol_id":40,"name":"已有加密目录"},{"fol_id":77,"name":"新目录"}]}`, nil), nil
				case "2":
					creates++
					return quarkResponse(`{"zt":1}`, nil), nil
				case "18":
					details++
					if r.Form.Get("folder_id") != "77" {
						t.Fatal("touched existing folder")
					}
					if mode == "verification-failed" && settings > 0 {
						return nil, errors.New("private phpdisk_info=synthetic")
					}
					name := "新目录"
					if mode == "changed-name" {
						name = "其他目录"
					}
					on := "1"
					if mode == "already-public" || mode == "stale-password" || settings > 0 && mode != "still-protected" && mode != "rejected" {
						on = "0"
					}
					if mode == "missing-switch" {
						return quarkResponse(`{"zt":1,"info":{"name":"新目录"}}`, nil), nil
					}
					return jsonResponse(map[string]any{"zt": 1, "info": map[string]any{"name": name, "onof": on, "pwd": "stale-reference"}}), nil
				case "16":
					settings++
					if r.Form.Get("folder_id") != "77" || r.Form.Get("shows") != "0" || r.Form.Get("shownames") != "" || len(r.PostForm["shownames"]) != 1 {
						t.Fatal("wrong clear-password payload", r.PostForm)
					}
					if mode == "lost-write-ack" {
						return nil, errors.New("private phpdisk_info=synthetic")
					}
					if mode == "rejected" {
						return quarkResponse(`{"zt":0}`, nil), nil
					}
					return quarkResponse(`{"zt":1}`, nil), nil
				default:
					t.Fatal("unexpected operation", r.Form)
					return nil, nil
				}
			})}
			folder, e := CreateAccountFolder(context.Background(), client, Account{Kind: "lanzou", Endpoint: "https://lanzou.example", Secret: "phpdisk_info=synthetic"}, "42", "新目录")
			success := mode == "protected" || mode == "already-public" || mode == "stale-password" || mode == "lost-write-ack"
			if success {
				if e != nil || folder.ID != "77" {
					t.Fatal(mode, folder, e)
				}
			} else {
				if e == nil || !strings.Contains(e.Error(), "已创建") || !strings.Contains(e.Error(), "不要重复创建") || strings.Contains(e.Error(), "phpdisk_info") {
					t.Fatal("false success or leaked error", mode, e)
				}
			}
			if creates != 1 || settings > 1 {
				t.Fatal("mutation replay", creates, settings)
			}
			want := 1
			if mode == "already-public" || mode == "stale-password" || mode == "missing-switch" || mode == "changed-name" {
				want = 0
			}
			if settings != want {
				t.Fatal("wrong write count", mode, settings)
			}
			if want == 1 && details != 2 {
				t.Fatal("no readback", details)
			}
		})
	}
}
