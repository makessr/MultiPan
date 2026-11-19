package transfer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRecycleGuangyaEmptyRequiresTaskAndEmptyReadback(t *testing.T) {
	for _, scenario := range []string{"empty", "new-record", "missing-ack", "failed-task"} {
		t.Run(scenario, func(t *testing.T) {
			writes, statusReads, listReads := 0, 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.guangyapan.com" || r.Method != http.MethodPost {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				var payload string
				switch r.URL.Path {
				case "/userres/v1/file/clear_recycle_bin":
					writes++
					if r.Body != nil {
						t.Fatal("clear task requires no request body")
					}
					payload = `{"code":0,"data":{"taskId":"fixture-task"}}`
					if scenario == "missing-ack" {
						payload = `{"code":0,"data":{}}`
					}
				case "/userres/v1/get_task_status":
					statusReads++
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["taskId"] != "fixture-task" {
						t.Fatal("readback did not use original task", body)
					}
					payload = `{"code":0,"data":{"status":2}}`
					if scenario == "failed-task" {
						payload = `{"code":0,"data":{"status":2,"detail":{"code":7}}}`
					}
				case "/userres/v1/file/get_file_list":
					listReads++
					payload = `{"code":0,"data":{"total":0,"list":[]}}`
					if scenario == "new-record" {
						payload = `{"code":0,"data":{"total":1,"list":[{"fileId":"new-item","fileName":"new.txt","resType":1,"fileSize":0}]}}`
					}
				default:
					t.Fatal("unexpected endpoint", r.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}
			a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
			taskID, err := submitGuangyaEmpty(context.Background(), client, a)
			if (err != nil) != (scenario == "missing-ack") {
				t.Fatal(taskID, err)
			}
			d := diskRecyclePurge{TaskID: taskID, Submitted: true, Acknowledged: err == nil}
			for i := 0; i < 2; i++ {
				state, message := checkGuangyaEmpty(context.Background(), client, a, d)
				if (state == "confirmed") != (scenario == "empty") {
					t.Fatal(state, message)
				}
			}
			wantStatus, wantList := 2, 2
			if scenario == "failed-task" {
				wantList = 0
			}
			if scenario == "missing-ack" {
				wantStatus, wantList = 0, 0
			}
			if writes != 1 || statusReads != wantStatus || listReads != wantList {
				t.Fatal("unexpected requests", writes, statusReads, listReads)
			}
		})
	}
}
