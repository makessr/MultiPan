package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestGuangyaCombinedShareOneRequest(t *testing.T) {
	a := Account{ID: "a", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture-token"}
	in := ShareInput{AccountID: "a", ParentID: "root", EntryID: "file", Name: "fixture", Action: "create", Targets: []ShareTarget{{ID: "file", Name: "fixture"}, {ID: "folder", Name: "folder", Folder: true}}}
	if e := validateShare(a, in); e != nil {
		t.Fatal(e)
	}
	creates := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		record := map[string]any{"id": "internal", "shareId": "public", "shareUrl": "https://www.guangyapan.com/s/public", "code": "", "shareType": 0, "shareStatus": 1, "leftTime": -1}
		switch r.URL.Path {
		case "/userres/v1/share_file":
			creates++
			var body struct {
				FileIDs []string `json:"fileIds"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(body.FileIDs, []string{"file", "folder"}) {
				t.Fatal("selection split or lost", body.FileIDs)
			}
			return baiduResponse(map[string]any{"code": 0, "data": record}), nil
		case "/userres/v1/get_share_list":
			return baiduResponse(map[string]any{"code": 0, "data": map[string]any{"list": []any{record}}}), nil
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
			return nil, nil
		}
	})}
	op := &shareOperation{in: in}
	first := shareGuangya(context.Background(), client, a, op, false)
	if first.State != "ready" || creates != 1 {
		t.Fatal(first, creates)
	}
	checked := shareGuangya(context.Background(), client, a, op, true)
	if checked.State != "ready" || checked.URL != first.URL || creates != 1 {
		t.Fatal("check replayed share", checked, creates)
	}
	duplicate := in
	duplicate.Targets = append([]ShareTarget(nil), in.Targets...)
	duplicate.Targets[1] = duplicate.Targets[0]
	if validateShare(a, duplicate) == nil {
		t.Fatal("duplicate targets accepted")
	}
	changed := in
	changed.Targets = append([]ShareTarget(nil), in.Targets...)
	changed.Targets[1].Name = ""
	if validateShare(a, changed) == nil {
		t.Fatal("incomplete folder accepted")
	}
}
