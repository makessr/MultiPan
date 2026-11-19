package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPan123ExpiryWritesOnceAndVerifiesOriginal(t *testing.T) {
	for _, mode := range []string{"ready", "changed-before", "changed-after", "lost-ack", "checkpoint-failed",
		"rename-ready", "rename-unchanged", "rename-stale", "rename-traffic-changed", "rename-lost-ack",
		"guest-price-ready", "guest-price-changed-before", "guest-price-changed-after", "guest-price-added-before", "rename-guest-price",
		"limit-ready", "limit-remove", "limit-noop", "limit-changed-permissions", "limit-lost-ack",
		"permissions-enable", "permissions-both", "permissions-close", "permissions-insufficient", "permissions-no-balance", "permissions-lost-ack",
		"combined-enable", "combined-cap-only", "combined-lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Now().Truncate(time.Second)
			a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
			const providerID = "9007199254740993123"
			const originalTitle = "原分享"
			title, requestedDeadline := originalTitle, share123Expiry(7, at)
			rename := strings.HasPrefix(mode, "rename-")
			combined := strings.HasPrefix(mode, "combined-")
			changeLimit := strings.HasPrefix(mode, "limit-") || combined
			changePermissions := strings.HasPrefix(mode, "permissions-") || combined
			originalTraffic, requestedTraffic := 2, 2
			var originalLimit int64 = 1024
			var requestedLimit int64 = 1024
			switch mode {
			case "limit-ready", "limit-changed-permissions", "limit-lost-ack":
				requestedLimit = 2 << 30
			case "limit-remove":
				requestedLimit = 0
			case "permissions-enable":
				originalTraffic, requestedTraffic = 1, 2
				originalLimit, requestedLimit = 0, 0
			case "permissions-close":
				requestedTraffic, requestedLimit = 1, 0
			case "permissions-both", "permissions-insufficient", "permissions-no-balance":
				requestedTraffic = 4
			case "permissions-lost-ack":
				requestedTraffic = 3
			case "combined-enable":
				originalTraffic, requestedTraffic = 1, 4
				originalLimit, requestedLimit = 0, 2<<30
			case "combined-cap-only":
				requestedLimit = 2 << 30
			case "combined-lost-ack":
				requestedTraffic, requestedLimit = 3, 0
			}
			if rename {
				title, requestedDeadline = "新主题", at.Add(12*time.Hour+250*time.Millisecond)
			}
			if mode == "rename-unchanged" {
				title = originalTitle
			}
			originalDeadline := at.Add(12 * time.Hour)
			if rename {
				originalDeadline = requestedDeadline
			}
			if changeLimit || changePermissions {
				requestedDeadline = originalDeadline
			}
			var guestPrice int64
			if strings.Contains(mode, "guest-price") && mode != "guest-price-added-before" {
				guestPrice = 235
			}
			row := map[string]any{
				"ShareId": json.Number(providerID), "ShareKey": "fixture", "SharePwd": "Old1",
				"ShareName": originalTitle, "CreateAt": at.Add(-time.Hour).Format(time.RFC3339),
				"Expiration": originalDeadline.Format(time.RFC3339Nano), "Expired": false,
				"isPayShare": 0, "noLoginStdAmount": guestPrice,
				"trafficSwitch": originalTraffic, "trafficLimitSwitch": pan123TrafficLimitMode(originalLimit), "trafficLimit": originalLimit,
			}
			var mu sync.Mutex
			writes := 0
			balanceReads := 0
			checkpoint := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/b/api/share/list":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{row}, "Next": -1}})
				case "/b/api" + pan123ShareTrafficInfo:
					balanceReads++
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error("unexpected balance request", r.Method)
					}
					data := map[string]any{"shareTrafficBalanceTip": ""}
					if mode == "permissions-insufficient" {
						data["shareTrafficBalanceTip"] = "余额不足"
					} else if mode == "permissions-no-balance" {
						delete(data, "shareTrafficBalanceTip")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
				case "/b/api" + pan123ShareUpdate:
					writes++
					if r.Method != http.MethodPut || !checkpoint || r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error("write did not follow the expected method, scope or checkpoint")
					}
					var body struct {
						ShareID    string `json:"shareId"`
						Expiration string `json:"expiration"`
						Title      string `json:"shareName"`
						LimitMode  int    `json:"trafficLimitSwitch"`
						Limit      int64  `json:"trafficLimit"`
						Traffic    int    `json:"trafficSwitch"`
						GuestPrice *int64 `json:"noLoginStdAmount"`
					}
					var fields map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&fields) != nil {
						t.Error("invalid write payload")
						return
					}
					raw, _ := json.Marshal(fields)
					_ = json.Unmarshal(raw, &body)
					wantFieldCount := 6
					if guestPrice > 0 {
						wantFieldCount++
						if body.GuestPrice == nil || *body.GuestPrice != guestPrice {
							t.Error("write did not preserve the existing guest download price")
						}
					}
					if body.ShareID != providerID || body.Expiration != requestedDeadline.Format(time.RFC3339Nano) || body.Title != title ||
						body.Limit != requestedLimit || body.LimitMode != pan123TrafficLimitMode(requestedLimit) ||
						body.Traffic != requestedTraffic || len(fields) != wantFieldCount {
						t.Error("write did not preserve original settings", fields)
					}
					row["Expiration"] = body.Expiration
					row["ShareName"] = body.Title
					row["trafficLimitSwitch"], row["trafficLimit"] = body.LimitMode, body.Limit
					row["trafficSwitch"] = body.Traffic
					if mode == "changed-after" {
						row["SharePwd"] = "New2"
					}
					if mode == "guest-price-changed-after" {
						row["noLoginStdAmount"] = 499
					}
					if mode == "rename-traffic-changed" {
						row["trafficLimit"] = 2048
					}
					if mode == "limit-changed-permissions" {
						row["trafficSwitch"] = 3
					}
					if strings.HasSuffix(mode, "lost-ack") {
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					_, _ = w.Write([]byte(`{"code":0}`))
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := &http.Client{Transport: pan123ExpiryFixtureTransport{server}}
			list, err := pan123ManagedShares(context.Background(), client, a, "0", 1)
			if err != nil || len(list.Items) != 1 || !list.Items[0].CanEdit {
				t.Fatalf("expected editable share, got %#v, %v", list, err)
			}
			if mode == "changed-before" || mode == "rename-stale" {
				mu.Lock()
				row["Expiration"] = at.Add(24 * time.Hour).Format(time.RFC3339)
				mu.Unlock()
			}
			priceChangedBefore := mode == "guest-price-changed-before" || mode == "guest-price-added-before"
			if priceChangedBefore {
				mu.Lock()
				row["noLoginStdAmount"] = 499
				mu.Unlock()
			}
			original, err := decodeManagedShareHandle(list.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			err = validateManagedShareSettingsSources(context.Background(), client, a, []managedShareHandle{original}, 7, managedShareExpiryBatch)
			if (err != nil) != (mode == "changed-before" || mode == "rename-stale" || priceChangedBefore) {
				t.Fatalf("read-only expiry preflight did not detect original settings: %v", err)
			}
			op := &shareOperation{created: at, in: ShareInput{Handle: list.Items[0].ID, Days: 7},
				settingsCheckpoint: func(managedShareHandle) error {
					mu.Lock()
					defer mu.Unlock()
					if mode == "checkpoint-failed" {
						return context.Canceled
					}
					checkpoint = true
					return nil
				}}
			if rename {
				op.in.NewTitle123, op.in.Days = title, 0
			}
			if changeLimit {
				op.in.TrafficLimit123, op.in.Days = &requestedLimit, 0
			}
			balanceCount := 0
			if changePermissions {
				op.in.TrafficMode123, op.in.Days = &requestedTraffic, 0
				if requestedTraffic != 1 && requestedTraffic != originalTraffic {
					balanceCount = 1
				}
			}
			want, writeCount := "ready", 1
			switch mode {
			case "changed-before", "rename-stale", "permissions-insufficient", "permissions-no-balance", "guest-price-changed-before", "guest-price-added-before":
				want, writeCount = "failed", 0
			case "changed-after", "rename-traffic-changed", "limit-changed-permissions", "guest-price-changed-after":
				want = "unknown"
			case "rename-unchanged", "limit-noop":
				writeCount = 0
			case "checkpoint-failed":
				want, writeCount = "unknown", 0
			}
			got := sharePan123Settings(context.Background(), client, a, op, false)
			if got.State != want || want == "ready" && (got.Password != "Old1" || got.URL == "") {
				t.Fatalf("got %#v, want %s", got, want)
			}
			if (rename || changeLimit || changePermissions) && want == "ready" && got.Expiry != originalDeadline.Format(time.RFC3339Nano) {
				t.Fatal("settings result lost actual expiration", got.Expiry)
			}
			checked := sharePan123Settings(context.Background(), client, a, op, true)
			if mode == "guest-price-changed-after" && checked.State != "unknown" {
				t.Fatal("guest price mismatch was accepted on read-only recovery", checked)
			}
			if (rename || changeLimit || changePermissions) && want == "ready" && checked.State != "ready" {
				t.Fatal("read-only check could not verify the change", checked.State)
			}
			mu.Lock()
			defer mu.Unlock()
			if writes != writeCount || balanceReads != balanceCount {
				t.Fatalf("got %d writes and %d balance reads, want %d and %d", writes, balanceReads, writeCount, balanceCount)
			}
		})
	}
}

func TestPan123BatchTrafficPreflightsEveryShare(t *testing.T) {
	for _, scenario := range []string{"permission", "limit", "stale", "insufficient", "disabled", "paid-permission", "paid-limit", "paid-price-changed", "paid-combined"} {
		t.Run(scenario, func(t *testing.T) {
			a := Account{ID: "a", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
			now := time.Now().Truncate(time.Second)
			deadline := now.Add(time.Hour)
			paid := strings.HasPrefix(scenario, "paid-")
			endpoint := "/b/api/share/list"
			if paid {
				deadline = share123Expiry(0, now)
				endpoint = "/b/api" + pan123PaidShareList
			}
			records := []map[string]any{}
			for _, id := range []string{"101", "102"} {
				mode := 2
				if id == "102" && (scenario == "disabled" || scenario == "paid-combined") {
					mode = 1
				}
				record := map[string]any{
					"ShareId": json.Number(id), "ShareKey": "items" + id, "SharePwd": "Old1", "ShareName": "原分享" + id,
					"CreateAt": now.Add(-time.Hour).Format(time.RFC3339), "Expiration": deadline.Format(time.RFC3339),
					"Expired": false, "isPayShare": 0, "trafficSwitch": mode, "trafficLimitSwitch": 1, "trafficLimit": 0,
				}
				if paid {
					record["isPayShare"], record["auditStatus"], record["payAmount"] = 1, 1, 350
					record["shareLinkList"] = map[string]any{"list": []string{"https://www.123pan.com/s/items" + id}}
				}
				records = append(records, record)
			}
			var mu sync.Mutex
			pages, balances := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case endpoint:
					pages++
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": records, "Next": -1}})
				case "/b/api" + pan123ShareTrafficInfo:
					balances++
					tip := ""
					if scenario == "insufficient" {
						tip = "流量不足"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"shareTrafficBalanceTip": tip}})
				default:
					t.Error("batch preflight called a write endpoint", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
				if r.Method != http.MethodGet {
					t.Error("batch preflight wrote a request", r.Method)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			client := &http.Client{Transport: pan123ExpiryFixtureTransport{server}}
			list, err := pan123ManagedSharesByType(ctx, client, a, "0", 1, paid)
			if err != nil || len(list.Items) != 2 {
				t.Fatal("could not load fixture shares", err)
			}
			sources := make([]managedShareHandle, 0, 2)
			for _, item := range list.Items {
				source, err := decodeManagedShareHandle(item.ID)
				if err != nil {
					t.Fatal(err)
				}
				sources = append(sources, source)
			}
			if scenario == "stale" {
				mu.Lock()
				records[1]["Expiration"] = deadline.Add(time.Hour).Format(time.RFC3339)
				mu.Unlock()
			}
			if scenario == "paid-price-changed" {
				mu.Lock()
				records[1]["payAmount"] = 450
				mu.Unlock()
			}
			permission, limit := 4, int64(2<<30)
			change := ShareInput{TrafficMode123: &permission}
			if scenario == "limit" || scenario == "disabled" || scenario == "paid-limit" {
				change = ShareInput{TrafficLimit123: &limit}
			}
			if scenario == "paid-combined" {
				change.TrafficLimit123 = &limit
			}
			err = validatePan123TrafficBatchSources(ctx, client, a, sources, change)
			wantOK := scenario == "permission" || scenario == "limit" || scenario == "paid-permission" || scenario == "paid-limit" || scenario == "paid-combined"
			if (err == nil) != wantOK {
				t.Fatal("unexpected batch preflight result", err)
			}
			wantBalance := 0
			if scenario == "permission" || scenario == "insufficient" || scenario == "paid-permission" || scenario == "paid-combined" {
				wantBalance = 1
			}
			mu.Lock()
			defer mu.Unlock()
			if pages != 2 || balances != wantBalance {
				t.Fatalf("got %d reads for one source page and %d for balance, want 2 and %d", pages, balances, wantBalance)
			}
		})
	}
}

func TestPan123BatchExpiryTargetRejectsShortenedWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	deadline := now.Add(10 * time.Hour).Format(time.RFC3339)
	row := managed123Record{Created: now.AddDate(0, 0, -5).Format(time.RFC3339)}
	row.Expiration = deadline
	h := managedShareHandle{Deadline123: deadline}
	for _, days := range []int{1, 7, 0} {
		err := checkPan123ExpiryTarget(row, h, days, share123Expiry(days, now), now)
		if (err != nil) != (days == 1) {
			t.Fatalf("unexpected result for %d days: %v", days, err)
		}
	}
}

type pan123ExpiryFixtureTransport struct{ server *httptest.Server }

func (t pan123ExpiryFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	r.URL.Host = t.server.Listener.Addr().String()
	return t.server.Client().Transport.RoundTrip(r)
}
