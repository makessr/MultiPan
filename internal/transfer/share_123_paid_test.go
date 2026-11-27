package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func Test123PaidShareSubmitsOnceAndReadsOnlyOriginal(t *testing.T) {
	account := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	in := ShareInput{AccountID: account.ID, ParentID: "0", EntryID: "22", Name: "资料", Folder: true,
		Action: "create", PaidPriceFen123: 235, PaidDescription123: "测试说明"}
	if err := validateShare(account, in); err != nil {
		t.Fatal(err)
	}
	for _, cents := range []int{-1, 99, 999901} {
		invalid := in
		invalid.PaidPriceFen123 = cents
		if err := validateShare(account, invalid); err == nil {
			t.Fatal("accepted invalid amount", cents)
		}
	}
	for _, mode := range []string{"approved", "wrong-price", "missing-id", "auth-required", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			var fixture sync.Mutex
			createCalls, readCalls := 0, 0
			audit := 0
			const shareID = "9007199254740993123"
			const link = "https://www.123pan.com/s/original"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture.Lock()
				defer fixture.Unlock()
				switch r.URL.Path {
				case "/b/api" + pan123PaidShareCreate:
					createCalls++
					var body map[string]any
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil ||
						body["fileIdList"] != "22" || body["fileNum"] != float64(1) || body["shareName"] != in.Name ||
						body["isPayShare"] != true || body["payAmount"] != float64(235) ||
						body["resourceDesc"] != in.PaidDescription123 || body["sharePwd"] != "" {
						t.Error("paid submission did not preserve the intended target and amount")
					}
					reply := `{"code":0,"data":{"ShareId":9007199254740993123,"ShareKey":"original"}}`
					switch mode {
					case "missing-id":
						reply = `{"code":0,"data":{}}`
					case "auth-required":
						reply = `{"code":7013,"data":{}}`
					case "lost-response":
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					_, _ = w.Write([]byte(reply))
				case "/b/api" + pan123PaidShareList:
					readCalls++
					price := 235
					if mode == "wrong-price" {
						price = 236
					}
					if r.Method != http.MethodGet {
						t.Error("original-result check must be read-only")
					}
					row := map[string]any{"ShareId": json.Number(shareID), "ShareKey": "original", "ShareName": in.Name,
						"Expiration": share123Expiry(0, time.Now()).Format(time.RFC3339), "SharePwd": "", "isPayShare": 1,
						"payAmount": price, "auditStatus": audit, "shareLinkList": map[string][]string{"list": {link}}}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{row}, "Next": -1}})
				default:
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := &http.Client{Transport: pan123ExpiryFixtureTransport{server}}
			op := &shareOperation{in: in, created: time.Now()}
			got := share123Paid(context.Background(), client, account, op, false)
			want := "unknown"
			if mode == "auth-required" {
				want = "failed"
			}
			if got.State != want || got.URL != "" {
				t.Fatal("unreviewed share exposed to the user", got)
			}
			pending := share123Paid(context.Background(), client, account, op, true)
			if pending.State != "unknown" || pending.URL != "" {
				t.Fatal("review stage published a link", pending)
			}
			fixture.Lock()
			audit = 1
			fixture.Unlock()
			published := share123Paid(context.Background(), client, account, op, true)
			fixture.Lock()
			defer fixture.Unlock()
			if mode == "approved" {
				if published.State != "ready" || published.URL != link || op.share != shareID || readCalls != 2 {
					t.Fatal("original audited share was not recovered", published, readCalls)
				}
			} else if published.State != "unknown" || published.URL != "" {
				t.Fatal("unsupported evidence published a link", published)
			}
			if createCalls != 1 {
				t.Fatal("read-only recovery reissued creation", createCalls)
			}
		})
	}
}

func Test123PaidSharePendingReceiptBlocksResubmission(t *testing.T) {
	a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	in := ShareInput{AccountID: a.ID, ParentID: "0", EntryID: "22", Name: "资料", Folder: true,
		Action: "create", PaidPriceFen123: 235}
	op := &shareOperation{in: in, scope: downloadScope(a), result: ShareResult{ID: "pending", Kind: a.Kind, State: "unknown"}}
	m := &Manager{accounts: []Account{a}, shares: map[string]*shareOperation{"pending": op}}
	in.PaidPriceFen123 = 325
	got, err := m.ShareEntry(context.Background(), in)
	if err != nil || got.ID != "pending" || got.State != "unknown" {
		t.Fatal("new price replaced active receipt", got, err)
	}
	in.PaidPriceFen123 = 0
	in.ParentID = "33"
	got, err = m.ShareEntry(context.Background(), in)
	if err != nil || got.ID != "pending" {
		t.Fatal("free creation was accepted while original paid receipt is pending", got, err)
	}
	history := m.ShareHistory()
	if len(history) != 1 || history[0].Action != "create" || history[0].PaidPriceFen123 != 235 || history[0].Result.URL != "" {
		t.Fatalf("pending receipt lost the original submitted price or exposed a link: %+v", history)
	}
}
