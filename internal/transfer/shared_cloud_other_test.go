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

func TestBaiduSharePageHundredWithoutTruncation(t *testing.T) {
	a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture-auth"}
	for _, count := range []int{0, 50, 51, 99, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rows := []any{}
			for i := 0; i < count; i++ {
				rows = append(rows, map[string]any{"shareId": json.RawMessage(fmt.Sprintf("9007199254740993%03d", i)), "typicalPath": fmt.Sprintf("/file-%d", i), "shortlink": fmt.Sprintf("https://pan.baidu.com/s/item%d", i)})
			}
			page, e := baiduManagedShares(context.Background(), otherManagedClient(t, "baidu", rows, nil, nil), a, 1)
			if count > 100 {
				if e == nil {
					t.Fatal("oversized page accepted")
				}
				return
			}
			if e != nil || len(page.Items) != count || page.HasMore != (count == 100) {
				t.Fatal("page truncated or wrong continuation", count, len(page.Items), page.HasMore, e)
			}
		})
	}
	for _, bad := range []any{nil, map[string]any{"unexpected": 1}, "not-a-list"} {
		if _, e := baiduManagedShares(context.Background(), otherManagedClient(t, "baidu", bad, nil, nil), a, 1); e == nil {
			t.Fatal("invalid list became empty")
		}
	}
}

func otherManagedClient(t *testing.T, kind string, rows, next, detail any) *http.Client {
	t.Helper()
	return &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("read-only provider issued a write", r.URL.Path)
		}
		var data any
		if kind == "pan123" {
			if r.URL.Host != "api.123278.com" || r.URL.Path != "/b/api/share/list" || r.URL.Query().Get("limit") != "50" {
				t.Fatal("unexpected 123 request")
			}
			if !strings.Contains(r.Header.Get("Authorization"), "fixture-auth") {
				t.Fatal("missing 123 authentication")
			}
			data = map[string]any{"code": 0, "data": map[string]any{"InfoList": rows, "Next": next}}
		} else {
			if r.URL.Host != "pan.baidu.com" || r.Header.Get("Cookie") != "BDUSS=fixture-auth" {
				t.Fatal("wrong Baidu destination")
			}
			switch r.URL.Path {
			case "/api/gettemplatevariable":
				data = map[string]any{"errno": 0, "result": map[string]any{"bdstoken": "fixture-token"}}
			case "/share/record":
				if r.URL.Query().Get("num") != "100" {
					t.Fatal("unbounded Baidu page")
				}
				data = map[string]any{"errno": 0, "list": rows}
			case "/share/surlinfoinrecord":
				if r.URL.Query().Get("shareid") != "9007199254740993123" || r.URL.Query().Get("sign") == "" {
					t.Fatal("wrong exact share detail")
				}
				data = detail
			default:
				t.Fatal("unexpected Baidu operation", r.URL.Path)
			}
		}
		raw, _ := json.Marshal(data)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
}

func TestManaged123PaidListCapabilities(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-auth"}
	const id = "9007199254740993123"
	const link = "https://www.123pan.com/s/paid-fixture"
	const alternate = "https://fixture.share.123pan.cn/123pan/PaidAlias"
	row := map[string]any{"ShareId": json.RawMessage(id), "ShareName": "付费资料", "ShareKey": "paid-fixture",
		"SharePwd": "", "Expiration": "2099-12-12T08:00:00+08:00", "isPayShare": 1, "payAmount": 350,
		"amount": "0", "shareLinkList": map[string]any{"list": []string{link, alternate}}}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Host != "api.123278.com" || r.URL.Path != "/b/api"+pan123PaidShareList ||
			r.URL.Query().Get("limit") != "50" || r.Header.Get("Authorization") != "Bearer fixture-auth" {
			t.Fatal("paid view sent a mismatched request", r.Method, r.URL)
		}
		encoded, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{row}, "Next": "paid-next"}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})}
	page, err := pan123ManagedSharesByType(ctx, client, a, "0", 1, true)
	if err != nil || len(page.Items) != 1 || !page.HasMore {
		t.Fatal("paid list unavailable", err)
	}
	item := page.Items[0]
	if item.CanEdit || !item.CanRevoke || item.PriceFen == nil || *item.PriceFen != 350 || item.EarningsFen == nil || *item.EarningsFen != 0 {
		t.Fatal("paid list exposed incorrect capabilities or lost reported amounts")
	}
	sealed, _ := base64.RawURLEncoding.DecodeString(page.Next)
	plain, err := unprotect(sealed)
	var cursor managedShareCursor
	if err != nil || json.Unmarshal(plain, &cursor) != nil || !cursor.Paid123 || cursor.Scope != downloadScope(a) || cursor.Next != "paid-next" {
		t.Fatal("paid pagination lost account or share type")
	}
	h, err := decodeManagedShareHandle(item.ID)
	if err != nil || !h.Paid123 || h.ProviderID != id || !revocationAccountMatches(a, h) || validPan123ExpiryHandle(h) {
		t.Fatal("paid handle has the wrong capabilities", err)
	}
	m := &Manager{accounts: []Account{a}}
	matched, err := m.MatchManagedShareLinkKeys(ctx, a.ID, []string{item.ID}, []string{managedShareLinkKey(alternate)})
	if err != nil || !slices.Equal(matched.Keys, []string{item.SelectionKey}) {
		t.Fatal("paid alias did not locate the original record", matched, err)
	}
	detail, err := pan123ManagedShareLink(ctx, client, a, h, nil)
	if err != nil || detail.URL != link || detail.Name != "付费资料" {
		t.Fatal("original paid link was not verified", err)
	}
	keys := []string{managedShareLinkKey(alternate), managedShareLinkKey(link)}
	detail, err = pan123ManagedShareLink(ctx, client, a, h, keys)
	if err != nil || detail.URL != alternate {
		t.Fatal("requested alternate link was not read", detail, err)
	}
	row["shareLinkList"] = map[string]any{"list": []string{link}}
	if got, err := pan123ManagedShareLink(ctx, client, a, h, keys[:1]); err == nil || got.URL != "" {
		t.Fatal("removed link did not fail closed", got, err)
	}
	delete(row, "shareLinkList")
	row["amount"] = -1
	if _, err := pan123ManagedShareLink(ctx, client, a, h, nil); err == nil {
		t.Fatal("missing explicit paid link fell back to a synthesized URL")
	}
	if page, err := pan123ManagedSharesByType(ctx, client, a, "0", 1, true); err != nil || page.Items[0].EarningsFen != nil || page.Items[0].CanRevoke {
		t.Fatal("paid list accepted invalid earnings or an unverified link for revoke", err)
	}
	row["isPayShare"] = 0
	if _, err := pan123ManagedSharesByType(ctx, client, a, "0", 1, true); err == nil {
		t.Fatal("paid list accepted contradictory type")
	}
}

func TestManaged123AlternateLinksSearch(t *testing.T) {
	ctx := context.Background()
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-auth"}
	const primary = "https://www.123pan.com/s/Main"
	const alternate = "https://www.123pan.cn/s/AltAbC"
	row := map[string]any{"ShareId": 41, "ShareName": "分享", "ShareKey": "fixture",
		"shareLinkList": map[string]any{"list": []string{primary, alternate, alternate, "https://example.invalid/s/AltAbC"}}}
	page, err := pan123ManagedShares(ctx, otherManagedClient(t, "pan123", []any{row}, "-1", nil), a, "0", 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	item := page.Items[0]
	h, err := decodeManagedShareHandle(item.ID)
	if err != nil || h.URL != primary || !slices.Equal(h.AlternateLinks123, []string{alternate}) || !h.AlternateLinksIncomplete {
		t.Fatal("alternate links were not normalized and deduplicated", h, err)
	}
	m := &Manager{accounts: []Account{a}}
	matched, err := m.MatchManagedShareLinkKeys(ctx, a.ID, []string{item.ID}, []string{managedShareLinkKey(primary), managedShareLinkKey(alternate)})
	if err != nil || !slices.Equal(matched.Keys, []string{item.SelectionKey}) || matched.Unknown != 0 {
		t.Fatal("the same share should match once", matched, err)
	}
	for _, key := range []string{"www.123pan.cn/s/altabc", "example.invalid/s/AltAbC", "www.123865.com/s/AltAbC"} {
		matched, err = m.MatchManagedShareLinkKeys(ctx, a.ID, []string{item.ID}, []string{key})
		if err != nil || len(matched.Keys) != 0 || matched.Unknown != 1 {
			t.Fatal("unconfirmed alias was matched or not reported", key, matched, err)
		}
	}
}

func TestManaged123ExactCursorAndReadOnlyOriginal(t *testing.T) {
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-auth"}
	row := map[string]any{"ShareId": json.RawMessage(`9007199254740993123`), "ShareName": "fixture.zip", "ShareKey": "public-key", "SharePwd": "A1b2", "Expiration": "2099-12-12T08:00:00+08:00"}
	client := otherManagedClient(t, "pan123", []any{row}, json.RawMessage(`9007199254740993122`), nil)
	page, e := pan123ManagedShares(context.Background(), client, a, "0", 1)
	if e != nil || len(page.Items) != 1 || !page.Items[0].CanRevoke || !page.HasMore || page.Next == "" {
		t.Fatal(page, e)
	}
	h, e := decodeManagedShareHandle(page.Items[0].ID)
	if e != nil || h.Kind != "pan123" || h.ProviderID != "9007199254740993123" || h.Cursor != "0" {
		t.Fatal("handle lost original provider identity", e)
	}
	sealed, _ := base64.RawURLEncoding.DecodeString(page.Next)
	plain, e := unprotect(sealed)
	var cursor managedShareCursor
	if e != nil || json.Unmarshal(plain, &cursor) != nil || cursor.Next != "9007199254740993122" || cursor.Page != 2 {
		t.Fatal("cursor precision lost")
	}
	result, e := pan123ManagedShareLink(context.Background(), client, a, h, nil)
	if e != nil || result.Password != "A1b2" || result.URL != "https://www.123pan.com/s/public-key" {
		t.Fatal(result, e)
	}
	m := &Manager{accounts: []Account{a}}
	m.accounts[0].Secret = "changed"
	if _, e = m.ManagedSharesNext(context.Background(), a.ID, page.Next); e == nil {
		t.Fatal("changed source reused cursor")
	}
	if _, e = m.ManagedSharesNext(context.Background(), "another", page.Next); e == nil {
		t.Fatal("cursor crossed account")
	}
	for _, mode := range []string{"missing-code", "invalid-code", "expired", "explicit-expired", "restricted", "renamed", "different-key"} {
		t.Run(mode, func(t *testing.T) {
			bad := map[string]any{}
			for k, v := range row {
				bad[k] = v
			}
			switch mode {
			case "missing-code":
				delete(bad, "SharePwd")
			case "invalid-code":
				bad["SharePwd"] = "bad code"
			case "expired":
				bad["Expiration"] = "2020-01-01T00:00:00Z"
			case "explicit-expired":
				bad["Expired"] = true
			case "restricted":
				bad["ViolateFileIdList"] = []int{1}
			case "renamed":
				bad["ShareName"] = "other.zip"
			case "different-key":
				bad["ShareKey"] = "other"
			}
			if got, e := pan123ManagedShareLink(context.Background(), otherManagedClient(t, "pan123", []any{bad}, -1, nil), a, h, nil); e == nil || got.URL != "" {
				t.Fatal("unsafe original link exposed", got, e)
			}
		})
	}
	public := map[string]any{}
	for k, v := range row {
		public[k] = v
	}
	public["SharePwd"] = ""
	public["ShareKey"] = h.URL
	if got, e := pan123ManagedShareLink(context.Background(), otherManagedClient(t, "pan123", []any{public}, -1, nil), a, h, nil); e != nil || got.URL != h.URL || got.Password != "" {
		t.Fatal("public full-URL key failed", got, e)
	}
	for _, rows := range []any{nil, []any{row, row}, []any{map[string]any{"ShareId": "a/b"}}} {
		if _, e := pan123ManagedShares(context.Background(), otherManagedClient(t, "pan123", rows, -1, nil), a, "0", 1); e == nil {
			t.Fatal("malformed 123 page accepted")
		}
	}
	if _, e = pan123ManagedShares(context.Background(), otherManagedClient(t, "pan123", []any{row}, "0", nil), a, "0", 1); e != nil {
		t.Fatal("explicit end cursor rejected", e)
	}
	if _, e = pan123ManagedShares(context.Background(), otherManagedClient(t, "pan123", []any{row}, "same", nil), a, "same", 2); e == nil {
		t.Fatal("non-advancing cursor accepted")
	}
	empty, e := pan123ManagedShares(context.Background(), otherManagedClient(t, "pan123", []any{}, -1, nil), a, "0", 1)
	if e != nil || empty.HasMore || empty.Items == nil {
		t.Fatal("empty end page rejected", e)
	}
}

func TestManagedBaiduListAndDetailNeverCreate(t *testing.T) {
	a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture-auth"}
	row := map[string]any{"shareId": json.RawMessage(`9007199254740993123`), "typicalPath": "/folder/fixture.zip", "shortlink": "https://pan.baidu.com/s/public", "expiredType": 7, "expiredTime": 6000, "public": 0, "channel": 4}
	detail := map[string]any{"errno": 0, "pwd": "A1b2", "shorturl": "https://pan.baidu.com/s/public"}
	client := otherManagedClient(t, "baidu", []any{row}, nil, detail)
	page, e := baiduManagedShares(context.Background(), client, a, 1)
	if e != nil || len(page.Items) != 1 || !page.Items[0].CanRevoke || page.HasMore || page.Items[0].Name != "fixture.zip" {
		t.Fatal(page, e)
	}
	h, e := decodeManagedShareHandle(page.Items[0].ID)
	if e != nil || h.Kind != "baidu" || h.ProviderID != "9007199254740993123" {
		t.Fatal(e)
	}
	r, e := baiduManagedShareLink(context.Background(), client, a, h)
	if e != nil || r.Password != "A1b2" || r.URL != row["shortlink"] {
		t.Fatal(r, e)
	}
	for _, mode := range []string{"expired", "missing-expiry", "renamed", "wrong-link", "missing-code", "mode-conflict"} {
		t.Run(mode, func(t *testing.T) {
			bad := map[string]any{}
			for k, v := range row {
				bad[k] = v
			}
			badDetail := map[string]any{}
			for k, v := range detail {
				badDetail[k] = v
			}
			switch mode {
			case "expired":
				bad["expiredTime"] = -1
			case "missing-expiry":
				delete(bad, "expiredType")
			case "renamed":
				bad["typicalPath"] = "/folder/other.zip"
			case "wrong-link":
				badDetail["shorturl"] = "https://evil.example/s/public"
			case "missing-code":
				delete(badDetail, "pwd")
			case "mode-conflict":
				bad["public"] = 1
			}
			if got, e := baiduManagedShareLink(context.Background(), otherManagedClient(t, "baidu", []any{bad}, nil, badDetail), a, h); e == nil || got.URL != "" {
				t.Fatal("unsafe Baidu detail exposed", got, e)
			}
		})
	}
	for _, rows := range []any{nil, []any{row, row}} {
		if _, e := baiduManagedShares(context.Background(), otherManagedClient(t, "baidu", rows, nil, nil), a, 1); e == nil {
			t.Fatal("malformed Baidu page accepted")
		}
	}
	for _, rows := range []any{[]any{}, map[string]any{}} {
		empty, e := baiduManagedShares(context.Background(), otherManagedClient(t, "baidu", rows, nil, nil), a, 1)
		if e != nil || len(empty.Items) != 0 || empty.HasMore {
			t.Fatal("empty list convention rejected", e)
		}
	}
}
