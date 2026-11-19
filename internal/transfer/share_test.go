package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareHistoryIsReadOnlyAndHidesPrivateOperationData(t *testing.T) {
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "never-session"}
	older := &shareOperation{in: ShareInput{AccountID: "a", ParentID: "never-parent", Action: "create"}, scope: downloadScope(a), key: "never-key", task: "never-task", share: "never-share", key123: "never-key123", baiduSource: "never-source", created: time.Unix(1, 0), result: ShareResult{ID: "receipt-1", Kind: "quark", Name: "fixture", State: "ready", URL: "https://pan.quark.cn/s/fixture", Password: "A1b2", CanCheck: true}}
	newer := &shareOperation{in: ShareInput{AccountID: "a"}, scope: downloadScope(a), created: time.Unix(2, 0), running: true, result: ShareResult{ID: "receipt-2", Kind: "quark", State: "working", URL: "never-old-url", Password: "never-old-password"}}
	m := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{"first": older, "second": newer}}
	rows := m.ShareHistory()
	if len(rows) != 2 || rows[0].Result.ID != "receipt-2" || rows[1].Result.ID != "receipt-1" {
		t.Fatal("operation order changed", rows)
	}
	if rows[0].Result.URL != "" || rows[0].Result.Password != "" || rows[1].Result.URL == "" {
		t.Fatal("non-ready link exposed or ready link hidden")
	}
	if rows[0].Action != "" || rows[1].Action != "create" {
		t.Fatal("unexpected legacy or known operation classification", rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "never-") {
		t.Fatal("private operation fields exposed", string(raw))
	}
	rows[1].Result.URL = "changed"
	if older.result.URL == "changed" {
		t.Fatal("history view mutated receipt")
	}
	m.accounts[0].Secret = "changed"
	for _, row := range m.ShareHistory() {
		if row.Result.State != "unknown" || row.Result.URL != "" || row.Result.Password != "" || row.Result.CanCheck {
			t.Fatal("changed account reused cached link", row)
		}
	}
	if older.result.State != "ready" {
		t.Fatal("read-only history changed underlying receipt")
	}
	m.accounts = nil
	for _, row := range m.ShareHistory() {
		if row.Result.CanCheck || row.Result.URL != "" {
			t.Fatal("removed account exposed cached link")
		}
	}
}

func TestLocateLanzouShareReceiptBindsAccountAndSource(t *testing.T) {
	a := Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "never-session"}
	b := a
	b.ID = "b"
	op := &shareOperation{in: ShareInput{AccountID: "a", ParentID: "30", EntryID: "20", Name: "资料", Folder: true, Action: "password", Password: "never"},
		scope: downloadScope(a), result: ShareResult{ID: "receipt", Kind: "lanzou", State: "ready"}}
	m := &Manager{accounts: []Account{a, b}, shares: map[string]*shareOperation{"receipt": op}}
	got, err := m.LanzouShareLocation("a", "receipt")
	if err != nil || got.AccountID != "a" || got.ParentID != "30" || got.Entry.ID != "20" || !got.Entry.Folder || !m.ShareHistory()[0].CanLocate {
		t.Fatal(got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "never") || strings.Contains(string(raw), "password") {
		t.Fatal("exposed sensitive operation data")
	}
	if _, err := m.LanzouShareLocation("b", "receipt"); err == nil {
		t.Fatal("same-provider account crossed")
	}
	m.accounts[0].Secret = "new-session"
	if _, err := m.LanzouShareLocation("a", "receipt"); err == nil || m.ShareHistory()[0].CanLocate {
		t.Fatal("stale scope still allowed")
	}
}

func TestShareValidationAndPublicURLBoundary(t *testing.T) {
	a := Account{Kind: "lanzou", Endpoint: "https://up.woozooo.com"}
	in := ShareInput{ParentID: "-1", EntryID: "123", Name: "fixture.txt", Action: "password", Password: "A1b2"}
	if e := validateShare(a, in); e != nil {
		t.Fatal(e)
	}
	for _, code := range []string{"", "too-long", "a b", "<bad>", "中文"} {
		bad := in
		bad.Password = code
		if validateShare(a, bad) == nil {
			t.Fatal("invalid password accepted")
		}
	}
	clear := in
	clear.ClearPassword = true
	if validateShare(a, clear) == nil {
		t.Fatal("accepted both set and clear")
	}
	clear.Password = ""
	if err := validateShare(a, clear); err != nil {
		t.Fatal("explicit clear rejected", err)
	}
	clear.Action = "get"
	if validateShare(a, clear) == nil {
		t.Fatal("accepted clear on read")
	}
	if _, e := shareURL("lanzou", "http://multipan-example.lanzoub.com/i123"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"https://user:credential@pan.quark.cn/s/123", "https://pan.quark.cn.evil.example/s/123", "https://pan.quark.cn/s/123?token=secret", "https://127.0.0.1/s/123", "javascript:alert(1)", "https://pan.quark.cn:8443/s/123", "https://pan.quark.cn/s/a/b"} {
		if _, e := shareURL("quark", raw); e == nil {
			t.Fatal("unsafe share URL accepted")
		}
	}
	for _, raw := range []string{`true`, `null`, `1.2`, `1e3`, `"a/b"`} {
		if shareScalar(json.RawMessage(raw)) != "" {
			t.Fatal("non-ID accepted", raw)
		}
	}
	if shareScalar(json.RawMessage(`9007199254740993123`)) != "9007199254740993123" {
		t.Fatal("large ID rounded")
	}
	a.Kind = "webdav"
	if validateShare(a, in) == nil {
		t.Fatal("DAV endpoint presented as share")
	}
}

func TestReadLanzouShareReceiptLinkKeepsOriginalReceipt(t *testing.T) {
	a := Account{ID: "blue", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
	for _, mode := range []string{"same", "changed", "stale-account"} {
		t.Run(mode, func(t *testing.T) {
			op := &shareOperation{in: ShareInput{AccountID: a.ID, ParentID: "42", EntryID: "123", Name: "fixture", Action: "password", Password: "Old"},
				scope: downloadScope(a), result: ShareResult{ID: "receipt", State: "ready", Kind: "lanzou", URL: "https://fixture.lanzoub.com/i123", Password: "Old"}}
			manager := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{"receipt": op}}
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.ParseForm() != nil {
					t.Fatal("invalid read request")
				}
				switch r.Form.Get("task") {
				case "5":
					return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"fixture"}]}`, nil), nil
				case "22":
					id := "i123"
					if mode == "changed" {
						id = "i456"
					}
					if mode == "stale-account" {
						manager.accounts[0].Secret = "other-session"
					}
					return quarkResponse(fmt.Sprintf(`{"zt":1,"info":{"is_newd":"https://fixture.lanzoub.com","f_id":%q,"onof":"1","pwd":"Fresh"}}`, id), nil), nil
				}
				t.Fatalf("unexpected write or lookup: %s", r.Form.Get("task"))
				return nil, nil
			})}
			got, err := manager.readLanzouShareReceiptLink(context.Background(), client, a.ID, "receipt")
			if (err == nil) != (mode == "same") || calls != 2 || (mode == "same" && got.Password != "Fresh") ||
				(mode != "same" && got.URL != "") || op.result.Password != "Old" || len(manager.shares) != 1 {
				t.Fatal("unexpected read or receipt mutation", got, err)
			}
		})
	}
}

func TestEditLanzouShareReceiptPasswordRequiresOriginalReceipt(t *testing.T) {
	a := Account{ID: "blue", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
	const link = "https://fixture.lanzoub.com/i123"
	original := &shareOperation{in: ShareInput{AccountID: a.ID, ParentID: "42", EntryID: "123", Name: "fixture", Action: "get"},
		scope: downloadScope(a), result: ShareResult{ID: "receipt", State: "ready", Kind: "lanzou", URL: link}}
	pending := &shareOperation{in: ShareInput{AccountID: a.ID, ParentID: "42", EntryID: "123", Name: "fixture", Action: "password", Password: "Old"},
		scope: downloadScope(a), result: ShareResult{ID: "pending", State: "unknown", Kind: "lanzou"}}
	manager := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{"receipt": original, "pending": pending}}
	for _, tc := range []struct{ id, url string }{
		{"", link}, {"missing", link}, {"pending", link}, {"receipt", "https://fixture.lanzoub.com/i456"},
	} {
		if _, err := manager.EditLanzouShareReceiptPassword(context.Background(), a.ID, tc.id, tc.url, "", "New", false); err == nil {
			t.Fatalf("unexpected acceptance of %q and %q", tc.id, tc.url)
		}
	}
	result, err := manager.EditLanzouShareReceiptPassword(context.Background(), a.ID, "receipt", link, "", "New", false)
	if err != nil || result.ID != pending.result.ID || result.State != "unknown" {
		t.Fatalf("expected the unresolved operation for the original item, got %#v, %v", result, err)
	}
	manager.accounts[0].Secret = "different-session"
	if _, err := manager.EditLanzouShareReceiptPassword(context.Background(), a.ID, "receipt", link, "", "", true); err == nil {
		t.Fatal("accepted a receipt from a different login")
	}
	if len(manager.shares) != 2 || original.result.URL != link {
		t.Fatal("receipt validation changed share history")
	}
}

func TestReadLanzouShareReceiptLinksSharesDirectoryChecks(t *testing.T) {
	a := Account{ID: "blue", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
	manager := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{}}
	ids := []string{"101", "102", "103"}
	for _, id := range ids {
		manager.shares[id] = &shareOperation{in: ShareInput{AccountID: a.ID, ParentID: "42", EntryID: id, Name: "file" + id, Action: "get"},
			scope: downloadScope(a), result: ShareResult{ID: id, State: "ready", Kind: "lanzou", URL: "https://fixture.lanzoub.com/i" + id, Password: "Old"}}
	}
	directoryReads, linkReads := 0, 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.ParseForm() != nil {
			t.Fatal("invalid request")
		}
		switch r.Form.Get("task") {
		case "5":
			directoryReads++
			return quarkResponse(`{"zt":1,"text":[{"id":101,"name_all":"file101"},{"id":102,"name_all":"file102"},{"id":103,"name_all":"file103"}]}`, nil), nil
		case "22":
			linkReads++
			id := r.Form.Get("file_id")
			password := "Old"
			if id == "102" {
				password = "New"
			}
			if id == "103" {
				id = "999"
			}
			return quarkResponse(fmt.Sprintf(`{"zt":1,"info":{"is_newd":"https://fixture.lanzoub.com","f_id":"i%s","onof":"1","pwd":%q}}`, id, password), nil), nil
		default:
			t.Fatal("unexpected write or lookup")
			return nil, nil
		}
	})}
	got, err := manager.readLanzouShareReceiptLinks(context.Background(), client, a.ID, ids)
	if err != nil || len(got) != 3 || got[0] == nil || got[1] == nil || got[2] != nil || directoryReads != 2 || linkReads != 3 {
		t.Fatal("results or call counts incorrect", directoryReads, linkReads, err)
	}
	if got[0].Message != "提取码与本机回执一致" || got[1].Message != "当前提取码与本机回执不同" || manager.shares["102"].result.Password != "Old" {
		t.Fatal("change indicator or original receipt invalid")
	}
}

func TestLanzouDirectoryShareChecksOriginalLocation(t *testing.T) {
	for _, mode := range []string{"file", "folder", "renamed", "moved", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			a := Account{Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture-cookie"}
			in := ShareInput{ParentID: "42", EntryID: "123", Name: "fixture", Folder: mode == "folder", Action: "get"}
			var requests []string
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "up.woozooo.com" || r.Header.Get("Cookie") != a.Secret || r.ParseForm() != nil {
					t.Fatal("wrong read request")
				}
				task := r.Form.Get("task")
				requests = append(requests, task)
				switch task {
				case "5":
					if r.Form.Get("folder_id") != in.ParentID || r.Form.Get("pg") != "1" {
						t.Fatal("wrong source directory")
					}
					switch mode {
					case "renamed":
						return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"new name"}]}`, nil), nil
					case "moved":
						return quarkResponse(`{"zt":2,"text":[]}`, nil), nil
					case "duplicate":
						return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"fixture"},{"id":123,"name_all":"fixture"}]}`, nil), nil
					default:
						return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"fixture"}]}`, nil), nil
					}
				case "47":
					if r.Form.Get("folder_id") != in.ParentID {
						t.Fatal("wrong source directory")
					}
					return quarkResponse(`{"zt":1,"text":[{"fol_id":123,"name":"fixture"}],"info":[{"folderid":42,"name":"parent"}]}`, nil), nil
				case "22":
					if r.Form.Get("file_id") != in.EntryID {
						t.Fatal("wrong file ID")
					}
				case "18":
					if r.Form.Get("folder_id") != in.EntryID {
						t.Fatal("wrong folder ID")
					}
				default:
					t.Fatal("unexpected task", task)
				}
				return quarkResponse(`{"zt":1,"info":{"is_newd":"https://fixture.lanzoub.com","f_id":"i123","new_url":"https://fixture.lanzoub.com/b123","onof":"1","pwd":"fixture-code"}}`, nil), nil
			})}
			result, err := readLanzouDirectoryShare(context.Background(), client, a, in)
			want := "5,22"
			if mode == "folder" {
				want = "47,18"
			}
			if mode != "file" && mode != "folder" {
				if err == nil || result.URL != "" || strings.Join(requests, ",") != "5" {
					t.Fatal("stale selection reached share read", requests, err)
				}
				return
			}
			if err != nil || strings.Join(requests, ",") != want || result.Password != "fixture-code" || result.URL == "" {
				t.Fatal("confirmed link was not read", requests, err)
			}
		})
	}
}

func TestLanzouDirectoryShareGroupRechecksSharedDirectory(t *testing.T) {
	a := Account{ID: "a", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
	entries := []LanzouDirectoryShareEntry{
		{ParentID: "42", EntryID: "123", Name: "first"},
		{ParentID: "42", EntryID: "124", Name: "second"},
		{ParentID: "42", EntryID: "125", Name: "stale"},
	}
	var tasks []string
	listReads := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.ParseForm() != nil {
			t.Fatal("invalid provider request")
		}
		task := r.Form.Get("task")
		tasks = append(tasks, task)
		switch task {
		case "5":
			if r.Form.Get("folder_id") != "42" {
				t.Fatal("wrong parent")
			}
			listReads++
			if listReads == 1 {
				return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"first"},{"id":124,"name_all":"second"},{"id":125,"name_all":"renamed"}]}`, nil), nil
			}
			return quarkResponse(`{"zt":1,"text":[{"id":123,"name_all":"first"},{"id":125,"name_all":"renamed"}]}`, nil), nil
		case "22":
			id := r.Form.Get("file_id")
			if id != "123" && id != "124" {
				t.Fatal("unmatched entry reached link lookup")
			}
			return quarkResponse(`{"zt":1,"info":{"is_newd":"https://fixture.lanzoub.com","f_id":"i`+id+`","onof":"0"}}`, nil), nil
		default:
			t.Fatal("unexpected provider request", task)
			return nil, nil
		}
	})}
	results, err := readLanzouDirectoryShares(context.Background(), client, a, entries)
	if err != nil || len(results) != 3 || results[0] == nil || results[0].Name != "first" || results[1] != nil || results[2] != nil {
		t.Fatalf("unexpected group result: %+v, %v", results, err)
	}
	if strings.Join(tasks, ",") != "5,22,22,5" {
		t.Fatal("expected two shared list reads and two verified link reads", tasks)
	}
}

func TestLanzouBatchPasswordStopsAtExistingUncertainTarget(t *testing.T) {
	account := Account{ID: "fixture", Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture"}
	original := ShareInput{AccountID: account.ID, ParentID: "42", EntryID: "123", Name: "old name", Action: "password", Password: "Old1"}
	receipt := ShareResult{ID: "existing", Kind: account.Kind, State: "unknown"}
	m := &Manager{
		accounts: []Account{account},
		shares: map[string]*shareOperation{
			receipt.ID: {in: original, scope: downloadScope(account), result: receipt},
		},
	}
	got, err := m.UpdateLanzouDirectorySharePassword(context.Background(), account.ID, "43", "123", "new name", false, "https://fixture.lanzoub.com/i123", "old1", "New1")
	if err != nil || got != receipt || len(m.shares) != 1 {
		t.Fatal("another password change advanced past an unresolved receipt", got, err)
	}
}

func TestLanzouExplicitClearVerifiesOriginalLinkAndProtection(t *testing.T) {
	for _, mode := range []string{"file", "folder", "already-public", "refused", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			a := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
			in := ShareInput{EntryID: "42", Action: "password", ClearPassword: true, Folder: mode == "folder"}
			op := &shareOperation{in: in}
			protected := mode != "already-public"
			writes := 0
			link := "https://fixture.lanzoub.com/i42"
			if in.Folder {
				link = "https://fixture.lanzoub.com/b42"
			}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				switch task := r.Form.Get("task"); task {
				case "16", "23":
					if in.Folder != (task == "16") || r.Form.Get("shows") != "0" || len(r.Form["shownames"]) != 1 || r.Form.Get("shownames") != "" || op.lanzouURL != link {
						t.Fatal("wrong clear request or missing original link checkpoint")
					}
					if task == "16" && r.Form.Get("folder_id") != in.EntryID || task == "23" && r.Form.Get("file_id") != in.EntryID {
						t.Fatal("wrong clear target")
					}
					writes++
					protected = mode == "refused"
					if mode == "lost-response" {
						return nil, errors.New("fixture response lost")
					}
					return quarkResponse(`{"zt":1}`, nil), nil
				case "18", "22":
					on := "0"
					if protected {
						on = "1"
					}
					return jsonResponse(map[string]any{"zt": 1, "info": map[string]string{
						"is_newd": "https://fixture.lanzoub.com", "f_id": "i42", "new_url": link, "onof": on, "pwd": "stale-code",
					}}), nil
				default:
					t.Fatal("unexpected request", task)
					return nil, errors.New("unexpected request")
				}
			})}
			result := shareLanzou(context.Background(), client, a, op, false)
			if mode == "already-public" {
				if result.State != "ready" || result.URL != link || result.Password != "" || writes != 0 {
					t.Fatal("public share was rewritten or exposed a residual code")
				}
				return
			}
			if mode == "refused" || mode == "lost-response" {
				if result.State != "unknown" || result.URL != "" || writes != 1 {
					t.Fatal("uncertain clear was reported as complete")
				}
				if mode == "refused" {
					return
				}
				result = shareLanzou(context.Background(), client, a, op, true)
			}
			if result.State != "ready" || result.URL != link || result.Password != "" || writes != 1 {
				t.Fatal("clear did not confirm the original unprotected link")
			}
		})
	}
}

func TestLanzouShareReadsAndVerifiesWithoutReplayingPasswordWrites(t *testing.T) {
	for _, mode := range []string{"get", "unprotected", "folder", "same-password", "write-ack-lost", "verification-lost", "changed-link", "changed-before-batch-write", "verified-batch-write", "changed-code-before-edit", "protection-enabled-before-edit", "changed-code-before-clear", "already-matches-batch", "checkpoint-failed", "legacy-unknown", "bad-link", "missing-password"} {
		t.Run(mode, func(t *testing.T) {
			password := "old1"
			writes := 0
			reads := 0
			recoverRead := false
			in := ShareInput{EntryID: "123", Name: "fixture", Action: "password", Password: "New1", Folder: mode == "folder"}
			if mode == "get" || mode == "unprotected" || mode == "bad-link" || mode == "missing-password" {
				in.Action = "get"
				in.Password = ""
			}
			if mode == "same-password" {
				in.Password = password
			}
			if mode == "legacy-unknown" {
				password = in.Password
			}
			switch mode {
			case "verified-batch-write", "already-matches-batch", "changed-code-before-edit", "changed-code-before-clear", "protection-enabled-before-edit":
				in.expectedLanzouURL = "https://multipan-example.lanzoub.com/i123"
				in.expectedLanzouPassword = password
			case "changed-before-batch-write":
				in.expectedLanzouURL = "https://multipan-example.lanzoub.com/i456"
			}
			switch mode {
			case "changed-code-before-edit":
				password = "External"
			case "changed-code-before-clear":
				in.Password, in.ClearPassword = "", true
				password = "External"
			case "protection-enabled-before-edit":
				in.expectedLanzouPassword = ""
			case "already-matches-batch":
				password = in.Password
			}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "up.woozooo.com" || r.Header.Get("Cookie") != "fixture-cookie" {
					t.Error("wrong authenticated boundary")
				}
				r.ParseForm()
				task := r.Form.Get("task")
				if task == "16" || task == "23" {
					writes++
					if r.Form.Get("shows") != "1" || r.Form.Get("shownames") != "New1" {
						t.Error("wrong password settings")
					}
					if in.Folder && r.Form.Get("folder_id") != "123" || !in.Folder && r.Form.Get("file_id") != "123" {
						t.Error("wrong entry namespace")
					}
					password = "New1"
					if mode == "write-ack-lost" {
						return nil, errors.New("fixture response lost")
					}
					return quarkResponse(`{"zt":1}`, nil), nil
				}
				reads++
				if mode == "verification-lost" && writes > 0 && !recoverRead {
					return nil, errors.New("fixture read failed")
				}
				if in.Folder && task != "18" || !in.Folder && task != "22" {
					t.Error("wrong detail request")
				}
				on := "1"
				pw := password
				if mode == "unprotected" {
					on = "0"
				}
				if mode == "missing-password" {
					pw = ""
				}
				domain := "https://multipan-example.lanzoub.com"
				if mode == "bad-link" {
					domain = "https://evil.example"
				}
				linkID := "i123"
				if mode == "changed-link" && writes > 0 {
					linkID = "i456"
				}
				return jsonResponse(map[string]any{"zt": 1, "info": map[string]any{"is_newd": domain, "f_id": linkID, "new_url": domain + "/b123", "onof": on, "pwd": pw}}), nil
			})}
			a := Account{Kind: "lanzou", Endpoint: "https://up.woozooo.com", Secret: "fixture-cookie"}
			op := &shareOperation{in: in}
			if mode == "checkpoint-failed" {
				op.checkpoint = func() error { return errors.New("fixture disk failure") }
			}
			result := shareLanzou(context.Background(), client, a, op, mode == "legacy-unknown")
			if mode == "write-ack-lost" || mode == "verification-lost" {
				if result.State != "unknown" || result.URL != "" || writes != 1 {
					t.Fatalf("uncertain %+v writes%d", result, writes)
				}
				recoverRead = true
				result = shareLanzou(context.Background(), client, a, op, true)
				if result.State != "ready" || writes != 1 || result.Password != "New1" {
					t.Fatal("recheck replayed mutation or failed to recover")
				}
			} else if mode == "changed-link" {
				if result.State != "unknown" || result.URL != "" || writes != 1 {
					t.Fatal("a changed public link was reported as the original share", result, writes)
				}
				result = shareLanzou(context.Background(), client, a, op, true)
				if result.State != "unknown" || result.URL != "" || writes != 1 {
					t.Fatal("read-only recheck adopted a new link or replayed the write", result, writes)
				}
			} else if mode == "legacy-unknown" {
				if result.State != "unknown" || result.URL != "" || writes != 0 {
					t.Fatal("legacy record was confirmed without its original link", result, writes)
				}
			} else if mode == "checkpoint-failed" {
				if result.State != "failed" || result.URL != "" || writes != 0 {
					t.Fatal("write occurred without a durable original link", result, writes)
				}
			} else if mode == "changed-before-batch-write" {
				if result.State != "failed" || result.URL != "" || writes != 0 || reads != 1 {
					t.Fatal("changed original link reached password write", result.State, writes, reads)
				}
			} else if mode == "changed-code-before-edit" || mode == "changed-code-before-clear" || mode == "protection-enabled-before-edit" {
				if result.State != "failed" || result.Password != "" || writes != 0 || reads != 1 || op.lanzouURL != "" {
					t.Fatal("changed original access settings reached password write", result.State, writes, reads)
				}
			} else if mode == "already-matches-batch" {
				if result.State != "ready" || result.Password != in.Password || writes != 0 || reads != 1 {
					t.Fatal("already matching settings were not handled without a write", result.State, writes, reads)
				}
			} else if mode == "verified-batch-write" {
				if result.State != "ready" || result.URL != in.expectedLanzouURL || result.Password != in.Password || writes != 1 || reads != 2 {
					t.Fatal("password write was not verified", result.State, writes, reads)
				}
			} else if mode == "bad-link" || mode == "missing-password" {
				if result.State != "failed" || result.URL != "" {
					t.Fatal("invalid result exposed", result)
				}
			} else if result.State != "ready" {
				t.Fatal(result)
			}
			if in.Action == "get" || mode == "same-password" {
				if writes != 0 {
					t.Fatal("read or same-password wrote state")
				}
			}
			if mode == "unprotected" && result.Password != "" {
				t.Fatal("unused password leaked")
			}
			if reads == 0 {
				t.Fatal("no read verification")
			}
		})
	}
}

func TestQuarkShareCreateOnceAndRecheckOriginalTask(t *testing.T) {
	for _, mode := range []string{"normal", "expiry1", "expiry30", "permanent", "poll-once", "link-read-lost", "create-lost", "wrong-password", "missing-password", "foreign-link", "task-failed", "expired", "no-password", "no-password-missing", "no-password-null", "no-password-empty", "no-password-contradiction", "protected-public", "large-id"} {
		t.Run(mode, func(t *testing.T) {
			creates, polls, links := 0, 0, 0
			in := ShareInput{EntryID: "fid", Name: "fixture.txt", Action: "create", Password: "A1b2", Days: 7}
			expectedExpiry := float64(3)
			switch mode {
			case "expiry1":
				in.Days = 1
				expectedExpiry = 2
			case "expiry30":
				in.Days = 30
				expectedExpiry = 4
			case "permanent":
				in.Days = 0
				expectedExpiry = 1
			}
			if strings.HasPrefix(mode, "no-password-") {
				in.Days = 0
				expectedExpiry = 1
			}
			if mode == "no-password" || strings.HasPrefix(mode, "no-password-") {
				in.Password = ""
			}
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "drive.quark.cn" || r.Header.Get("Cookie") != "fixture-cookie" {
					t.Error("wrong API credential boundary")
				}
				switch r.URL.Path {
				case "/1/clouddrive/share":
					creates++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					expectedMode := float64(2)
					if in.Password == "" {
						expectedMode = 1
					}
					if body["expired_type"] != expectedExpiry || body["passcode"] != in.Password || body["url_type"] != expectedMode {
						t.Error("wrong share options")
					}
					if mode == "create-lost" {
						return nil, errors.New("fixture lost response")
					}
					return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"task-fixture"},"metadata":{"tq_gap":1}}`, nil), nil
				case "/1/clouddrive/task":
					polls++
					if r.URL.Query().Get("task_id") != "task-fixture" {
						t.Error("not checking original task")
					}
					status := 2
					if mode == "poll-once" && polls == 1 {
						status = 1
					}
					if mode == "task-failed" {
						status = 3
					}
					id := any("share-fixture")
					if mode == "large-id" {
						id = json.RawMessage(`9007199254740993123`)
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"status": status, "share_id": id}}), nil
				case "/1/clouddrive/share/password":
					links++
					if mode == "link-read-lost" && links == 1 {
						return nil, errors.New("fixture read failed")
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if mode == "large-id" && body["share_id"] != "9007199254740993123" {
						t.Error("large ID changed")
					}
					data := map[string]any{"pwd_id": "public-fixture", "passcode": in.Password, "audit_status": 1}
					if mode == "expired" {
						data["expired_type"] = 2
						data["expired_at"] = time.Now().Add(-time.Hour).UnixMilli()
					}
					if mode == "wrong-password" {
						data["passcode"] = "wrong"
					}
					if mode == "no-password-null" {
						data["passcode"] = nil
					}
					if mode == "no-password-empty" {
						data["url_type"] = 1
					}
					if mode == "no-password-contradiction" {
						data["url_type"] = 2
					}
					if mode == "protected-public" {
						data["url_type"] = 1
					}
					if mode == "missing-password" || mode == "no-password-missing" {
						delete(data, "passcode")
					}
					if mode == "foreign-link" {
						data["share_url"] = "https://evil.example/s/fixture"
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": data}), nil
				default:
					t.Error("unexpected API request")
					return nil, errors.New("unexpected")
				}
			})}
			a := Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-cookie"}
			op := &shareOperation{in: in}
			result := shareQuark(context.Background(), client, a, op, false)
			if mode == "link-read-lost" {
				if result.State != "unknown" {
					t.Fatal(result)
				}
				result = shareQuark(context.Background(), client, a, op, true)
			}
			want := "ready"
			if mode == "create-lost" || mode == "wrong-password" || mode == "missing-password" || mode == "foreign-link" || mode == "no-password-contradiction" || mode == "protected-public" {
				want = "unknown"
			}
			if mode == "task-failed" || mode == "expired" {
				want = "failed"
			}
			if result.State != want || creates != 1 {
				t.Fatalf("%+v creates%d want%s", result, creates, want)
			}
			if mode == "create-lost" {
				shareQuark(context.Background(), client, a, op, true)
				if creates != 1 {
					t.Fatal("uncertain create replayed")
				}
			}
			if result.State != "ready" && result.URL != "" {
				t.Fatal("unverified result exposed URL")
			}
			if strings.Contains(result.Message, "fixture-cookie") {
				t.Fatal("credential in error")
			}
		})
	}
}

func TestQuarkCombinedShareRequiresCompleteScopeAndNeverReplays(t *testing.T) {
	a := Account{ID: "fixture", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-cookie"}
	in := ShareInput{AccountID: a.ID, ParentID: "0", EntryID: "one", Name: "资料", Action: "create", Days: 7,
		Targets: []ShareTarget{{ID: "one", Name: "资料"}, {ID: "two", Name: "文件夹", Folder: true}}}
	if err := validateShare(a, in); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"task-count", "detail-count", "partial-task", "partial-detail", "missing-count", "wrong-expiry"} {
		t.Run(mode, func(t *testing.T) {
			writes, polls := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/1/clouddrive/share":
					writes++
					var body struct {
						IDs []string `json:"fid_list"`
					}
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || strings.Join(body.IDs, ",") != "one,two" {
						t.Fatal("combined share did not submit exactly the selected IDs")
					}
					return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"group-task"}}`, nil), nil
				case "/1/clouddrive/task":
					polls++
					data := map[string]any{"status": 2, "share_id": "group-share"}
					if mode == "task-count" || mode == "partial-detail" || mode == "wrong-expiry" {
						data["creation_snapshot"] = map[string]int{"success_count": 2, "failed_count": 0}
					} else if mode == "partial-task" {
						data["creation_snapshot"] = map[string]int{"success_count": 1, "failed_count": 1}
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": data}), nil
				case "/1/clouddrive/share/password":
					data := map[string]any{"pwd_id": "group-public", "expired_type": 3}
					if mode == "detail-count" || mode == "partial-task" {
						data["all_file_num"], data["violation_cnt"] = 2, 0
					}
					if mode == "partial-detail" {
						data["violation_cnt"] = 1
					}
					if mode == "wrong-expiry" {
						data["expired_type"] = 2
					}
					return jsonResponse(map[string]any{"status": 200, "code": 0, "data": data}), nil
				default:
					t.Fatal("unexpected route", r.URL.Path)
					return nil, nil
				}
			})}
			op := &shareOperation{in: in}
			first := shareQuark(context.Background(), client, a, op, false)
			want := "unknown"
			if mode == "task-count" || mode == "detail-count" {
				want = "ready"
			}
			if first.State != want || (first.URL != "") != (want == "ready") {
				t.Fatal("unexpected combined share", first)
			}
			saved := snapshotShare(op)
			reopened := &shareOperation{in: saved.Input, task: saved.Task, share: saved.Share, quarkGroupVerified: saved.QuarkGroupVerified}
			checked := shareQuark(context.Background(), client, a, reopened, true)
			if checked.State != want || writes != 1 || (polls == 1) != (want == "ready") {
				t.Fatal("recheck lost scope or replayed creation", checked, writes, polls)
			}
		})
	}
}

func TestManagerShareReceiptDeduplicationScopeAndStaleSource(t *testing.T) {
	creates := 0
	wrongSource := false
	lostLink := true
	expiredLink := false
	blockCreates := false
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			name := "fixture.txt"
			if wrongSource {
				name = "changed.txt"
			}
			fmt.Fprintf(w, `{"code":0,"data":{"list":[{"fid":"fid","file_name":%q,"file_type":1,"size":1}]}}`, name)
		case "/1/clouddrive/share":
			creates++
			if blockCreates {
				io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, `{"code":0,"data":{"task_id":"task"}}`)
		case "/1/clouddrive/task":
			fmt.Fprint(w, `{"code":0,"data":{"status":2,"share_id":"share"}}`)
		case "/1/clouddrive/share/password":
			if lostLink {
				w.WriteHeader(503)
			} else if expiredLink {
				fmt.Fprint(w, `{"code":0,"data":{"pwd_id":"key","passcode":"A1b2","status":3}}`)
			} else {
				fmt.Fprint(w, `{"code":0,"data":{"pwd_id":"key","passcode":"A1b2"}}`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	cert := server.Certificate()
	if len(cert.DNSNames) > 0 {
		transport.TLSClientConfig.ServerName = cert.DNSNames[0]
	} else {
		transport.TLSClientConfig.ServerName = cert.IPAddresses[0].String()
	}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { m.Close() }()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-cookie", Name: "fixture", Folder: "0"}
	m.accounts = []Account{a}
	in := ShareInput{AccountID: "a", ParentID: "0", EntryID: "fid", Name: "fixture.txt", Action: "create", Password: "A1b2", Days: 7}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, e := m.ShareEntry(ctx, in)
	if e != nil || first.State != "unknown" || !first.CanCheck {
		t.Fatalf("%+v %v", first, e)
	}
	m.Close()
	m, e = NewManager(m.path, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(m.ShareHistory()) != 1 || m.ShareHistory()[0].Result.State != "unknown" {
		t.Fatal("unknown receipt not restored")
	}
	changedSettings := in
	changedSettings.Password = "New2"
	changedSettings.Days = 0
	guarded, guardErr := m.ShareBatchEntry(ctx, changedSettings)
	if guardErr != nil || guarded.ID != first.ID || guarded.State != "unknown" || guarded.URL != "" || guarded.Password != "" || creates != 1 {
		t.Fatalf("batch changed settings replayed unknown target: %+v %v creates=%d", guarded, guardErr, creates)
	}
	second, e := m.ShareEntry(ctx, in)
	if e != nil || second.ID != first.ID || creates != 1 {
		t.Fatal("duplicate create was submitted")
	}
	lostLink = false
	resolved, e := m.CheckShare(ctx, first.ID)
	if e != nil || resolved.State != "ready" || creates != 1 {
		t.Fatalf("resolve %+v %v", resolved, e)
	}
	second, e = m.ShareEntry(ctx, in)
	if e != nil || second.ID != first.ID || creates != 1 {
		t.Fatal("completed receipt not reused")
	}
	expiredLink = true
	expired, e := m.ShareEntry(ctx, in)
	if e != nil || expired.State != "failed" || expired.URL != "" || creates != 1 {
		t.Fatal("cached share was not revalidated", expired, e)
	}
	expiredLink = false
	m.accounts[0].Secret = "changed-cookie"
	if _, e = m.CheckShare(ctx, first.ID); e == nil {
		t.Fatal("changed source reused receipt")
	}
	m.accounts[0] = a
	wrongSource = true
	in.EntryID = "other-fid"
	result, e := m.ShareEntry(ctx, in)
	if e != nil || result.State != "failed" || creates != 1 {
		t.Fatal("stale source shared", result, e)
	}
	if len(m.renaming) != 0 {
		t.Fatal("account reservation leaked")
	}
	wrongSource = false
	blockCreates = true
	in.EntryID = "fid"
	in.Password = "New2"
	done := make(chan ShareResult, 1)
	go func() { r, _ := m.ShareEntry(context.Background(), in); done <- r }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("share did not start")
	}
	m.Close()
	select {
	case r := <-done:
		if r.State != "unknown" {
			t.Fatal("cancel claimed definitive outcome", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel share")
	}
	if len(m.renaming) != 0 {
		t.Fatal("shutdown leaked reservation")
	}
	m, e = NewManager(m.path, nil)
	if e != nil {
		t.Fatal(e)
	}
	before := creates
	r, e := m.ShareEntry(context.Background(), in)
	if e != nil || r.State != "unknown" || r.CanCheck || creates != before {
		t.Fatal("restart replayed a create whose response was lost", r, e)
	}
}
