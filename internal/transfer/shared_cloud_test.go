package transfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func managedFixtureClient(t *testing.T, path string, data any, metadata ...any) *http.Client {
	t.Helper()
	return &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "drive.quark.cn" || r.URL.Path != "/1/clouddrive"+path || r.Header.Get("Cookie") != "fixture-auth" {
			t.Fatal("unexpected request or credential destination")
		}
		if path == "/share/mypage/detail" {
			if r.Method != "GET" || r.URL.Query().Get("_size") != "50" || r.URL.Query().Get("_page") != "2" || r.URL.Query().Get("_order_type") != "desc" {
				t.Fatal("unbounded or wrong list query")
			}
		} else {
			var payload map[string]string
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload["share_id"] != "provider-private" {
				t.Fatal("unexpected detail operation")
			}
		}
		response := map[string]any{"code": 0, "data": data}
		if len(metadata) != 0 {
			response["metadata"] = metadata[0]
		}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
}

func TestMatchManagedShareLinkKeys(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	m := &Manager{accounts: []Account{a}}
	h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, ProviderID: "one", Name: "资料", URL: "https://pan.quark.cn/s/AbC"}
	makeToken := func(source managedShareHandle) string {
		t.Helper()
		token, err := sealManagedShare(source)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	known := makeToken(h)
	unknown := h
	unknown.ProviderID, unknown.URL = "two", ""
	keys := []string{"pan.quark.cn/s/AbC"}
	failed := &http.Client{Transport: quarkTransport(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("fixture read failed")
	})}
	result, err := m.matchManagedShareLinkKeys(ctx, failed, a.ID, []string{known, makeToken(unknown)}, keys)
	if err != nil || result.Unknown != 1 || !slices.Equal(result.Keys, []string{managedShareSelectionKey(h)}) {
		t.Fatal(result, err)
	}
	differentCase, err := m.MatchManagedShareLinkKeys(ctx, a.ID, []string{known}, []string{"pan.quark.cn/s/abc"})
	if err != nil || len(differentCase.Keys) != 0 {
		t.Fatal(differentCase, err)
	}
	other := h
	other.AccountID = "other"
	stale := h
	stale.Scope = "previous-source"
	for _, token := range []string{"invalid", makeToken(other), makeToken(stale)} {
		if _, err := m.MatchManagedShareLinkKeys(ctx, a.ID, []string{known, token}, keys); err == nil {
			t.Fatal("accepted invalid or stale handle")
		}
	}
}

func TestMatchManagedShareLinkKeysFillsOneQuarkBatch(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	m := &Manager{accounts: []Account{a}}
	handles := make([]string, 0, 11)
	keys := []string{"pan.quark.cn/s/Match"}
	var expected string
	for i := 1; i <= 11; i++ {
		row := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, Name: "资料", ProviderID: fmt.Sprint(i)}
		token, err := sealManagedShare(row)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, token)
		if i == 2 {
			expected = managedShareSelectionKey(row)
		}
	}
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var data map[string][]string
		if r.Method != http.MethodPost || r.URL.Path != "/1/clouddrive/share/password/batch" ||
			json.NewDecoder(r.Body).Decode(&data) != nil || len(data["share_ids"]) != 10 {
			t.Fatal("unexpected fallback request")
		}
		body := `{"code":0,"data":{"shares":{
			"1":{"share_id":"1","share_url":"https://pan.quark.cn/s/Other","url_type":1},
			"2":{"share_id":"2","share_url":"https://pan.quark.cn/s/Match","url_type":2,"passcode":"A1b2"},
			"3":{"share_id":"4","share_url":"https://pan.quark.cn/s/Match","url_type":1}
		}}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := m.matchManagedShareLinkKeys(ctx, client, a.ID, handles, keys)
	if err != nil || calls != 1 || result.Unknown != 9 || !slices.Equal(result.Keys, []string{expected}) {
		t.Fatal(result, calls, err)
	}
	other, err := m.matchManagedShareLinkKeys(ctx, client, a.ID, handles, []string{"pan.baidu.com/s/1Link"})
	if err != nil || calls != 1 || len(other.Keys) != 0 || other.Unknown != 11 {
		t.Fatal(other, calls, err)
	}
}

func TestQuarkManagedSharesReconcilesReportedTotal(t *testing.T) {
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	rows := make([]map[string]any, 50)
	for i := range rows {
		rows[i] = map[string]any{"share_id": fmt.Sprint(i + 1)}
	}
	rows[0]["status"], rows[0]["audit_status"], rows[0]["url_type"] = 3, 2, 2
	rows[1]["audit_status"], rows[1]["expired_at"], rows[1]["url_type"] = 2, 1, 1
	rows[2]["expired_at"], rows[2]["url_type"] = 1, "unknown"
	rows[3]["expired_at"], rows[3]["expired_type"] = 1, 1
	for _, tt := range []struct {
		name    string
		total   any
		count   int
		more    bool
		invalid bool
	}{
		{name: "exact full last page", total: 100, count: 50},
		{name: "full page with more", total: 101, count: 50, more: true},
		{name: "short last page", total: 53, count: 3},
		{name: "page beyond end", total: 40, count: 0},
		{name: "short middle page", total: 101, count: 3, invalid: true},
		{name: "missing last record", total: 53, count: 2, invalid: true},
		{name: "unexpected records beyond end", total: 49, count: 1, invalid: true},
		{name: "invalid total", total: -1, count: 0, invalid: true},
		{name: "malformed total", total: "100", count: 50, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": rows[:tt.count]}, map[string]any{"_total": tt.total})
			result, err := quarkManagedShares(context.Background(), client, a, 2)
			if tt.invalid {
				if err == nil {
					t.Fatal("inconsistent response was accepted")
				}
				return
			}
			if err != nil || result.HasMore != tt.more || len(result.Items) != tt.count {
				t.Fatalf("page returned %d rows, hasMore=%v, err=%v", len(result.Items), result.HasMore, err)
			}
			if tt.count >= 3 {
				wantStatus := []string{"源文件已删除", "分享违规", "分享已过期"}
				wantMode := []string{"protected", "public", ""}
				for i, status := range wantStatus {
					if result.Items[i].Status != status || result.Items[i].QuarkAccess != wantMode[i] {
						t.Fatalf("unexpected status or access at row %d: %+v", i, result.Items[i])
					}
				}
			}
			if tt.count >= 4 && (result.Items[3].Expiry != "永久" || result.Items[3].Status != "有效性待核对") {
				t.Fatal("permanent share classified as expired")
			}
		})
	}
}

func TestQuarkManagedShareFiltersAndRevocationScope(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	var calls []string
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		filter := q.Get("_filter")
		calls = append(calls, filter)
		if r.Method != "GET" || r.URL.Host != "drive.quark.cn" || r.URL.Path != "/1/clouddrive/share/mypage/detail" ||
			q.Get("_size") != "50" || q.Get("_fetch_total") != "1" || q.Get("_page") != "1" ||
			filter != "" && q.Get("_share_type") != "0,1" || filter == "" && q.Has("_share_type") {
			t.Fatal("wrong filtered query", r.Method, r.URL)
		}
		id := filter
		if filter == "" {
			id = "private"
		}
		raw, _ := json.Marshal(map[string]any{
			"code": 0, "metadata": map[string]int{"_total": 1},
			"data": map[string]any{"list": []map[string]string{{"share_id": id, "title": id}}},
		})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	filters := []string{"all", "valid", "violated", "expired", "failed", "private"}
	var sources []managedShareHandle
	for _, filter := range filters {
		page, err := quarkManagedSharesFiltered(ctx, client, a, 1, filter)
		if err != nil || len(page.Items) != 1 || page.HasMore {
			t.Fatal("filtered list unavailable", filter, err)
		}
		h, err := decodeManagedShareHandle(page.Items[0].ID)
		if err != nil || h.QuarkFilter != filter || h.Scope != downloadScope(a) {
			t.Fatal("filter not bound to protected selection", filter, err)
		}
		sources = append(sources, h)
	}
	if _, err := quarkManagedSharesFiltered(ctx, client, a, 1, "bogus"); err == nil || !slices.Equal(calls, filters) {
		t.Fatal("invalid filter reached the provider", calls, err)
	}
	calls = nil
	selected := []managedShareHandle{sources[3], sources[5]}
	if err := validateShareRevocationPages(ctx, client, a, selected); err != nil || !slices.Equal(calls, []string{"expired", "private"}) {
		t.Fatal("preflight did not preserve each row's filter", calls, err)
	}
	calls = nil
	if present, err := observeShareRevocation(ctx, client, a, selected[1]); err != nil || !present || !slices.Equal(calls, []string{""}) {
		t.Fatal("revocation observed a filtered list", calls, err)
	}
}

func TestQuarkManagedShareRestrictionDetails(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	for _, mode := range []string{"partial", "changed-key", "stale-status", "duplicate-files", "missing-list"} {
		t.Run(mode, func(t *testing.T) {
			detailCalls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				var data any
				switch r.URL.Path {
				case "/1/clouddrive/share/mypage/detail":
					if r.Method != "GET" || r.URL.Query().Get("_filter") != "violated" || r.URL.Query().Get("_page") != "2" {
						t.Fatal("unexpected source page request")
					}
					row := map[string]any{"share_id": "original", "title": "原分享", "share_url": "https://pan.quark.cn/s/public",
						"pwd_id": "public", "partial_violation": true, "violation_cnt": 120, "stoken": "fixture-detail-token"}
					if mode == "changed-key" {
						row["pwd_id"] = "changed"
					}
					if mode == "stale-status" {
						row["partial_violation"] = false
					}
					data = map[string]any{"list": []any{row}}
				case "/1/clouddrive/share/invalid/file/list":
					detailCalls++
					var body struct {
						Key   string `json:"pwd_id"`
						Token string `json:"stoken"`
						Size  int    `json:"size"`
					}
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Key != "public" ||
						body.Token != "fixture-detail-token" || body.Size != 100 {
						t.Fatal("unexpected restriction details request")
					}
					id := "9007199254740993123"
					file := map[string]any{"fid": json.RawMessage(id), "file_name": "测试文件", "pdir_name": "示例"}
					rows := []any{file}
					if mode == "duplicate-files" {
						rows = append(rows, file)
					}
					result := map[string]any{"file_list": rows, "invalid_fid_reasons": map[string]any{id: map[string]string{"reason_message": "网盘返回的原因"}}}
					if mode == "missing-list" {
						delete(result, "file_list")
					}
					data = result
				default:
					t.Fatal("unexpected endpoint", r.URL.Path)
				}
				raw, _ := json.Marshal(map[string]any{"code": 0, "data": data})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			h := managedShareHandle{Kind: "quark", AccountID: a.ID, Scope: downloadScope(a), ProviderID: "original", Name: "原分享",
				URL: "https://pan.quark.cn/s/public", Page: 2, QuarkFilter: "violated", QuarkLinkKey: "public"}
			result, err := readQuarkShareRestrictions(ctx, newQuarkClient(client, a), h)
			switch mode {
			case "partial":
				if err != nil || detailCalls != 1 || result.Reported != 120 || len(result.Items) != 1 ||
					result.Items[0] != (QuarkShareRestriction{Name: "测试文件", Parent: "示例", Reason: "网盘返回的原因"}) {
					t.Fatal("unexpected details", result, err)
				}
				page, err := quarkManagedSharesFiltered(ctx, client, a, 2, "violated")
				public, _ := json.Marshal(page)
				if err != nil || len(page.Items) != 1 || page.Items[0].QuarkRestricted != 120 || strings.Contains(string(public), "fixture-detail-token") {
					t.Fatal("restriction count missing or private token leaked", err)
				}
			case "changed-key", "stale-status":
				if err == nil || detailCalls != 0 {
					t.Fatal("stale route reached the details endpoint", err, detailCalls)
				}
			default:
				if err == nil || detailCalls != 1 || len(result.Items) != 0 {
					t.Fatal("incomplete or duplicate details accepted", err, detailCalls)
				}
			}
		})
	}
}

func TestQuarkManagedShareBatchVerifiesOriginalDetails(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	ids := []string{"9007199254740993123", "public", "wrong-id", "wrong-link", "missing-mode", "expired", "missing", "malformed"}
	sources := make([]managedShareHandle, 0, len(ids))
	for _, id := range ids {
		sources = append(sources, managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), Kind: "quark", ProviderID: id, Page: 1, Name: id, URL: "https://pan.quark.cn/s/" + id})
	}
	calls := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string][]string
		if r.Method != "POST" || r.URL.Host != "drive.quark.cn" || r.URL.Path != "/1/clouddrive/share/password/batch" ||
			r.Header.Get("Cookie") != "fixture-auth" || json.NewDecoder(r.Body).Decode(&body) != nil ||
			len(body) != 1 || !slices.Equal(body["share_ids"], ids) {
			t.Fatal("unexpected batch route or payload")
		}
		rows := map[string]any{
			ids[0]:         map[string]any{"share_id": json.RawMessage(ids[0]), "share_url": sources[0].URL, "url_type": 2, "passcode": "A1b2"},
			"public":       map[string]any{"share_url": sources[1].URL, "url_type": 1},
			"wrong-id":     map[string]any{"share_id": "public", "share_url": sources[2].URL, "url_type": 1},
			"wrong-link":   map[string]any{"share_url": sources[1].URL, "url_type": 1},
			"missing-mode": map[string]any{"share_url": sources[4].URL},
			"expired":      map[string]any{"share_url": sources[5].URL, "url_type": 1, "expired_at": 1700000000000},
			"malformed":    "broken",
			"unexpected":   map[string]any{"share_url": "https://pan.quark.cn/s/unexpected", "url_type": 1},
		}
		raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"shares": rows}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	links, err := quarkManagedShareLinks(ctx, client, a, sources)
	if err != nil || calls != 1 || len(links) != 2 || links[ids[0]].Password != "A1b2" || links["public"].URL != sources[1].URL {
		t.Fatal("valid details unavailable or unverified details exposed", len(links), calls, err)
	}
	token, err := sealManagedShare(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	other := sources[1]
	other.AccountID = "another-account"
	otherToken, err := sealManagedShare(other)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{accounts: []Account{a}}
	for _, handles := range [][]string{{token, token}, {token, otherToken}, make([]string, 11)} {
		if _, err := m.ManagedQuarkShareLinks(ctx, handles); err == nil {
			t.Fatal("duplicate, mixed-account or oversized selection accepted")
		}
	}
}

func TestManagedSharesBoundedProtectedHandlesAndSourceIsolation(t *testing.T) {
	a := Account{ID: "account-private", Name: "fixture", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	row := map[string]any{"share_id": "provider-private", "title": "fixture.zip", "pwd_id": "fixture-public", "status": 1, "created_at": 1700000000000, "expired_type": 1}
	page, e := quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": []any{row}}), a, 2)
	if e != nil || len(page.Items) != 1 || !page.Items[0].CanRevoke || page.Page != 2 || page.HasMore || page.Items[0].Expiry != "永久" {
		t.Fatal(page, e)
	}
	raw, _ := json.Marshal(page)
	for _, private := range []string{a.ID, a.Secret, "provider-private", "fixture-public"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private metadata exposed")
		}
	}
	again, e := quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": []any{row}}), a, 2)
	if e != nil || len(again.Items) != 1 || page.Items[0].SelectionKey == "" || again.Items[0].SelectionKey != page.Items[0].SelectionKey {
		t.Fatal("repeated list read lost stable selection key", e)
	}
	otherAccount := a
	otherAccount.ID = "other-local-account"
	otherPage, e := quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": []any{row}}), otherAccount, 2)
	if e != nil || len(otherPage.Items) != 1 || otherPage.Items[0].SelectionKey == page.Items[0].SelectionKey {
		t.Fatal("selection key reused across accounts", e)
	}
	sealed, _ := base64.RawURLEncoding.DecodeString(page.Items[0].ID)
	plain, e := unprotect(sealed)
	var h managedShareHandle
	if e != nil || json.Unmarshal(plain, &h) != nil || h.ProviderID != "provider-private" || h.Scope != downloadScope(a) {
		t.Fatal("source handle lost")
	}
	m := &Manager{accounts: []Account{a}}
	m.accounts[0].Secret = "changed-auth"
	if _, e = m.ManagedShareLink(context.Background(), page.Items[0].ID); e == nil {
		t.Fatal("changed credentials reused handle")
	}
	if _, e = m.ManagedShareLink(context.Background(), "not-a-handle"); e == nil {
		t.Fatal("invalid handle accepted")
	}
	for _, p := range []int{0, -1, 1001} {
		if _, e = m.ManagedShares(context.Background(), a.ID, p); e == nil {
			t.Fatal("invalid page accepted")
		}
	}
	m.accounts[0].Kind = "baidu"
	if _, e = m.ManagedShares(context.Background(), a.ID, 1); e == nil {
		t.Fatal("unsupported provider queried")
	}

	for _, data := range []any{map[string]any{}, map[string]any{"list": nil}, map[string]any{"list": []any{row, row}}, map[string]any{"list": []any{map[string]any{"share_id": "a/b"}}}, map[string]any{"list": []any{map[string]any{"share_id": "id", "share_url": "https://evil.example/s/id"}}}} {
		if _, e = quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", data), a, 2); e == nil {
			t.Fatal("malformed list accepted")
		}
	}
	rows := []any{}
	for i := 0; i < 50; i++ {
		rows = append(rows, map[string]any{"share_id": fmt.Sprint(i + 1)})
	}
	full, e := quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": rows}), a, 2)
	if e != nil || !full.HasMore || len(full.Items) != 50 {
		t.Fatal("full-page boundary", e)
	}
	rows = append(rows, map[string]any{"share_id": "extra"})
	if _, e = quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": rows}), a, 2); e == nil {
		t.Fatal("oversized page accepted")
	}
	empty, e := quarkManagedShares(context.Background(), managedFixtureClient(t, "/share/mypage/detail", map[string]any{"list": []any{}}), a, 2)
	if e != nil || empty.HasMore || empty.Items == nil {
		t.Fatal("empty page boundary", e)
	}
}

func TestManagedShareDetailReadsOnlyExactOriginalAndValidatesSettings(t *testing.T) {
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth"}
	h := managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: "provider-private", Name: "fixture.zip", URL: "https://pan.quark.cn/s/public"}
	for _, mode := range []string{"public", "protected", "wrong-id", "foreign-url", "changed-link", "bad-mode", "missing-mode", "bad-code", "public-with-code", "expired", "blocked", "audit"} {
		t.Run(mode, func(t *testing.T) {
			row := map[string]any{"share_id": h.ProviderID, "share_url": h.URL, "url_type": 1, "expired_type": 1, "status": 1}
			switch mode {
			case "protected":
				row["url_type"] = 2
				row["passcode"] = "A1b2"
			case "wrong-id":
				row["share_id"] = "another"
			case "foreign-url":
				row["share_url"] = "https://evil.example/s/public"
			case "changed-link":
				row["share_url"] = "https://pan.quark.cn/s/other"
			case "bad-mode":
				row["url_type"] = 3
			case "missing-mode":
				delete(row, "url_type")
			case "bad-code":
				row["url_type"] = 2
				row["passcode"] = "bad code"
			case "public-with-code":
				row["passcode"] = "A1b2"
			case "expired":
				row["expired_at"] = 1700000000000
			case "blocked":
				row["status"] = 3
			case "audit":
				row["audit_status"] = 2
			}
			result, e := quarkManagedShareLink(context.Background(), managedFixtureClient(t, "/share/password", row), a, h)
			if mode == "public" || mode == "protected" {
				if e != nil || result.URL != h.URL || result.Name != h.Name || mode == "protected" && result.Password != "A1b2" {
					t.Fatal(result, e)
				}
			} else if e == nil || result.URL != "" {
				t.Fatal("invalid original share exposed", result, e)
			}
		})
	}
}
