package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func recycleFixtureClient(t *testing.T, kind string, rows any, total, next any, expectedPage int) *http.Client {
	t.Helper()
	return &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("recycle browsing wrote", r.Method, r.URL.Path)
		}
		var data any
		switch kind {
		case "quark":
			if r.URL.Host != "drive.quark.cn" || r.URL.Path != "/1/clouddrive/file/recycle/list" || r.URL.Query().Get("_page") != strconv.Itoa(expectedPage) || r.URL.Query().Get("_size") != "100" || r.Header.Get("Cookie") != "session=fixture-auth" {
				t.Fatal("wrong quark read", r.URL)
			}
			meta := map[string]any{}
			if total != nil {
				meta["_total"] = total
			}
			data = map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": rows}, "metadata": meta}
		case "pan123":
			if r.URL.Host != "api.123278.com" || r.URL.Path != "/b/api/file/list/new" || r.URL.Query().Get("trashed") != "true" || r.URL.Query().Get("Page") != strconv.Itoa(expectedPage) || r.URL.Query().Get("limit") != "100" || !strings.Contains(r.Header.Get("Authorization"), "fixture-auth") {
				t.Fatal("wrong 123 trash read", r.URL)
			}
			data = map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Total": total, "Next": next}}
		case "baidu":
			if r.URL.Host != "pan.baidu.com" || r.Header.Get("Cookie") != "session=fixture-auth" {
				t.Fatal("wrong baidu read")
			}
			if r.URL.Path == "/api/gettemplatevariable" {
				data = map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "fixture-token"}}
			} else {
				if r.URL.Path != "/api/recycle/list" || r.URL.Query().Get("start") != strconv.Itoa((expectedPage-1)*100) || r.URL.Query().Get("num") != "100" {
					t.Fatal("wrong baidu page", r.URL)
				}
				data = map[string]any{"errno": 0, "list": rows, "total": total}
			}
		}
		b, _ := json.Marshal(data)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
}
func recycleFixtureAccount(kind string) Account {
	return Account{ID: "a", Kind: kind, Endpoint: map[string]string{"quark": QuarkAPI, "pan123": Pan123API, "baidu": BaiduAPI}[kind], Secret: "session=fixture-auth"}
}
func recycleFixtureRow(kind, id string) map[string]any {
	switch kind {
	case "quark":
		return map[string]any{"record_id": id, "fid": "file-source", "file_name": "资料 &amp; 备份", "dir": false, "size": 0, "secret": "never-copy"}
	case "pan123":
		return map[string]any{"FileId": json.RawMessage(id), "FileName": "资料", "Type": 1, "Size": 25, "secret": "never-copy"}
	default:
		return map[string]any{"fs_id": json.RawMessage(id), "server_filename": "资料", "isdir": 0, "size": 25, "path": "/原目录/资料", "secret": "never-copy"}
	}
}
func TestRecycleReadOnlyMetadataAndPages(t *testing.T) {
	for _, kind := range []string{"quark", "pan123", "baidu"} {
		t.Run(kind, func(t *testing.T) {
			id := "9007199254740993123"
			row := recycleFixtureRow(kind, id)
			p, e := listAccountRecycle(context.Background(), recycleFixtureClient(t, kind, []any{row}, 101, nil, 2), recycleFixtureAccount(kind), 2)
			if e != nil || len(p.Items) != 1 || p.Items[0].ID != id || p.Page != 2 || p.HasMore {
				t.Fatal(p, e)
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "never-copy") || strings.Contains(string(raw), "fixture-auth") {
				t.Fatal("raw metadata leak")
			}
			if kind == "quark" && (p.Items[0].Name != "资料 & 备份" || p.Items[0].Size == nil || *p.Items[0].Size != 0) {
				t.Fatal("zero size or escaping lost")
			}
			if kind == "pan123" && p.Items[0].Kind != "folder" {
				t.Fatal("folder lost")
			}
			if kind == "baidu" && p.Items[0].OriginalPath != "/原目录/资料" {
				t.Fatal("original path lost")
			}
			for _, count := range []int{0, 99, 100, 101} {
				rows := []any{}
				for i := 0; i < count; i++ {
					rows = append(rows, recycleFixtureRow(kind, fmt.Sprint(1000+i)))
				}
				p, e = listAccountRecycle(context.Background(), recycleFixtureClient(t, kind, rows, nil, nil, 1), recycleFixtureAccount(kind), 1)
				if count > 100 {
					if e == nil {
						t.Fatal("oversized list accepted")
					}
				} else if e != nil || len(p.Items) != count || p.HasMore != (count == 100) {
					t.Fatal(count, p, e)
				}
			}
			for _, bad := range []any{nil, map[string]any{}, "bad", []any{row, row}, []any{map[string]any{"name": "bad"}}} {
				if _, e := listAccountRecycle(context.Background(), recycleFixtureClient(t, kind, bad, nil, nil, 1), recycleFixtureAccount(kind), 1); e == nil {
					t.Fatal("malformed list accepted", bad)
				}
			}
		})
	}
}
func TestRecycleRejectsUnsupportedAndInvalidPagesBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: quarkTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })}
	for _, a := range []Account{
		{Kind: "webdav", Endpoint: "https://dav.invalid"},
		{Kind: "guangya", Endpoint: "https://untrusted.invalid"},
		{Kind: "quark", Endpoint: "https://untrusted.invalid"},
	} {
		if _, e := listAccountRecycle(context.Background(), client, a, 1); e == nil {
			t.Fatal("unsupported source accepted")
		}
	}
	for _, p := range []int{0, -1, 1001} {
		if _, e := listAccountRecycle(context.Background(), client, recycleFixtureAccount("quark"), p); e == nil {
			t.Fatal("invalid page accepted")
		}
	}
}

func TestRecycleGuangyaUsesOnlyTrashView(t *testing.T) {
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if r.Method != http.MethodPost || r.URL.Host != "api.guangyapan.com" || r.URL.Path != "/userres/v1/file/get_file_list" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body["page"] != float64(1) || body["dirType"] != float64(4) ||
			body["pageSize"] != float64(100) || body["parentId"] != "" {
			t.Fatalf("unexpected recycle view request: %s %s %v", r.Method, r.URL, body)
		}
		content := `{"code":0,"data":{"total":101,"list":[{"fileId":"item-1","parentId":"parent-1","fileName":"资料","resType":1,"fileSize":0}]}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(content))}, nil
	})}
	a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
	page, err := listAccountRecycle(context.Background(), client, a, 2)
	if err != nil || page.Incomplete || page.HasMore || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	item := page.Items[0]
	if item.Kind != "file" || item.ParentID != "parent-1" || item.Size == nil || *item.Size != 0 || item.RestoreHandle != "" {
		t.Fatalf("unexpected recycle metadata: %#v", item)
	}
}
func TestRecycle123EmptyListCompatibility(t *testing.T) {
	a := recycleFixtureAccount("pan123")
	tests := []struct {
		name string
		data string
		page int
		ok   bool
	}{
		{"null with terminal marker", `{"InfoList":null,"Total":0,"Next":-1}`, 1, true},
		{"null with no cursor", `{"InfoList":null,"Total":0}`, 1, true},
		{"omitted list with explicit count", `{"Total":0,"Next":"-1"}`, 1, true},
		{"terminal marker without total", `{"InfoList":null,"Next":-1}`, 1, true},
		{"missing list with terminal marker", `{"Next":"-1","Len":0}`, 1, true},
		{"unknown total and marker", `{"InfoList":null}`, 1, false},
		{"positive list length", `{"InfoList":null,"Total":0,"Next":-1,"Len":8}`, 1, false},
		{"invalid total", `{"InfoList":null,"Total":"unexpected-private-value","Next":-1}`, 1, false},
		{"unexpected data value", `"unexpected-private-value"`, 1, false},
		{"positive count", `{"InfoList":null,"Total":3,"Next":-1}`, 1, false},
		{"nonterminal cursor", `{"InfoList":null,"Total":0,"Next":5}`, 1, false},
		{"unexpected shape", `{"InfoList":{},"Total":0,"Next":-1}`, 1, false},
		{"later page", `{"InfoList":null,"Total":0,"Next":-1}`, 2, false},
		{"null envelope data", `null`, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: quarkTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Query().Get("trashed") != "true" {
					t.Fatal("unexpected request to recover empty view", req.Method, req.URL.Path)
				}
				content := `{"code":0,"data":` + tc.data + `}`
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(content))}, nil
			})}
			p, err := listAccountRecyclePage(context.Background(), client, a, tc.page, true)
			if tc.ok {
				if err != nil || len(p.Items) != 0 || p.HasMore || p.Incomplete || p.Total == nil || *p.Total != 0 {
					t.Fatalf("expected reliable empty browse result: %+v, %v", p, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "RECYCLE123 data=") || strings.Contains(err.Error(), "unexpected-private-value") {
				t.Fatalf("expected a redacted format error: %+v, %v", p, err)
			}
			if _, err := listAccountRecycle(context.Background(), client, a, tc.page); err == nil {
				t.Fatal("nonarray listing was accepted as evidence for restore or deletion")
			}
		})
	}
}

func TestRecycle123EmptyBusinessCodeOnlyForBrowse(t *testing.T) {
	client := &http.Client{Transport: quarkTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Query().Get("trashed") != "true" {
			t.Fatal("unexpected non-list request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":5007,"data":null}`))}, nil
	})}
	a := recycleFixtureAccount("pan123")
	p, err := listAccountRecyclePage(context.Background(), client, a, 1, true)
	if err != nil || p.Total == nil || *p.Total != 0 || p.HasMore || len(p.Items) != 0 {
		t.Fatalf("explicit empty browse response was rejected: %+v, %v", p, err)
	}
	if _, err := listAccountRecyclePage(context.Background(), client, a, 2, true); err == nil {
		t.Fatal("empty business code must not certify a later page")
	}
	if _, err := listAccountRecycle(context.Background(), client, a, 1); err == nil {
		t.Fatal("empty business code must not certify absence for writes")
	}
}

func TestRecycle123ExplicitEndAndUnknownType(t *testing.T) {
	a := recycleFixtureAccount("pan123")
	row := recycleFixtureRow("pan123", "1")
	p, e := listAccountRecycle(context.Background(), recycleFixtureClient(t, "pan123", []any{row}, nil, -1, 1), a, 1)
	if e != nil || p.HasMore {
		t.Fatal(p, e)
	}
	row["Type"] = nil
	p, e = listAccountRecycle(context.Background(), recycleFixtureClient(t, "pan123", []any{row}, nil, 0, 1), a, 1)
	if e != nil || p.Items[0].Kind != "unknown" || !p.HasMore {
		t.Fatal(p, e)
	}
	row["Type"] = 9
	if _, e := listAccountRecycle(context.Background(), recycleFixtureClient(t, "pan123", []any{row}, nil, -1, 1), a, 1); e == nil {
		t.Fatal("invalid type accepted")
	}
}

func TestRecycle123TerminalArrayBrowseOnly(t *testing.T) {
	a := recycleFixtureAccount("pan123")
	for _, tt := range []struct {
		name  string
		count int
		total any
		next  int
		want  bool
	}{
		{"oversized terminal", 101, 101, -1, true},
		{"decimal count", 1, json.RawMessage("1.0"), -1, true},
		{"nonterminal overflow", 101, 101, 50, false},
		{"above browse bound", 1001, 1001, -1, false},
		{"unknown total type", 1, "unverified", -1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]any, tt.count)
			for i := range rows {
				rows[i] = recycleFixtureRow("pan123", strconv.Itoa(i+1))
			}
			client := recycleFixtureClient(t, "pan123", rows, tt.total, tt.next, 1)
			browse, err := listAccountRecyclePage(context.Background(), client, a, 1, true)
			if tt.want {
				if err != nil || !browse.BrowseOnly || browse.HasMore || len(browse.Items) != tt.count || browse.Items[0].RestoreHandle != "" {
					t.Fatalf("expected bounded read-only listing: %d rows, %v", len(browse.Items), err)
				}
			} else if err == nil {
				t.Fatal("accepted an unverified listing")
			}
			if _, err := listAccountRecycle(context.Background(), client, a, 1); err == nil {
				t.Fatal("unsafe list accepted for write preflight")
			}
		})
	}
}
