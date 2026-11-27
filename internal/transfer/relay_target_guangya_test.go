package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestGuangyaCopySizeLimit(t *testing.T) {
	in := MoveFileInput{EntryID: "source-id", Name: "large.bin"}
	size := int64(2 << 30)
	page := FilePage{Entries: []CloudEntry{{ID: in.EntryID, Name: in.Name, Size: &size}}}
	if _, err := selectGuangyaCopySource(page, in, nil); err != nil {
		t.Fatal("file at 2 GiB should be accepted", err)
	}
	size++
	if _, err := selectGuangyaCopySource(page, in, nil); err == nil {
		t.Fatal("file larger than 2 GiB was accepted")
	}
}

func TestRelaySnapshotRowsPreserveDirectoryStamp(t *testing.T) {
	const content = "original bytes"
	a := Account{Kind: "quark", ID: "source", Endpoint: QuarkAPI}
	c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			if r.URL.Query().Get("pdir_fid") != "directory-id" {
				t.Fatal("unexpected directory read", r.URL)
			}
			return quarkResponse(fmt.Sprintf(`{"code":0,"data":{"list":[{"fid":"file-id","pdir_fid":"directory-id","file_name":"a.txt","file_type":1,"size":%d,"updated_at":123}]},"metadata":{"_total":1}}`, len(content)), nil), nil
		case "/1/clouddrive/file/download":
			return quarkResponse(`{"code":0,"data":[{"fid":"file-id","download_url":"https://download.quark.cn/content"}]}`, nil), nil
		case "/content":
			resp := quarkResponse(content, nil)
			resp.ContentLength = int64(len(content))
			return resp, nil
		default:
			return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
		}
	})}
	rootStamp := relayDirectoryStamp(a.Kind, CloudEntry{ID: "directory-id", Folder: true})
	if rootStamp == "" {
		t.Fatal("expected a directory stamp")
	}
	rows := []diskRelay{
		{Folder: true, ETag: rootStamp},
		{ETag: "123", Input: RelayInput{ParentID: "directory-id", EntryID: "file-id", Name: "a.txt", Size: int64(len(content))}},
	}
	var updates []RelayPreflight
	if err := readRelaySnapshotRows(context.Background(), c, a, rows, func(progress RelayPreflight) {
		updates = append(updates, progress)
	}); err != nil {
		t.Fatal(err)
	}
	if len(updates) < 3 || updates[0].CurrentFile != "a.txt" || updates[0].ReadBytes != 0 ||
		updates[0].Files != 1 || updates[0].TotalBytes != int64(len(content)) ||
		updates[len(updates)-1].ReadBytes != int64(len(content)) || updates[len(updates)-1].Completed != 1 {
		t.Fatal("unexpected snapshot progress", updates)
	}
	if expected := fmt.Sprintf("123:%x", sha256.Sum256([]byte(content))); rows[0].ETag != rootStamp || rows[1].ETag != expected {
		t.Fatal("expected original directory stamp and file digest")
	}
}

func TestRelayGuangyaChildResolvesAcknowledgedParent(t *testing.T) {
	parent := diskRelay{
		Folder: true, CreatedAck: true, TargetFolderID: "new-folder-id", TargetKind: "guangya",
		Input: RelayInput{DestinationID: guangyaRoot, Name: "Tree"},
		Job:   RelayJob{ID: "fixture-root", State: "verified"},
	}
	m := &Manager{relays: []diskRelay{parent}}
	child := diskRelay{TargetKind: "guangya", ParentJobID: parent.Job.ID,
		Input: RelayInput{DestinationID: relayPlannedChildTarget(parent), Name: "a.txt"},
	}
	resolved, err := m.resolveRelayTarget(child)
	if err != nil || relayTargetParent(resolved) != parent.TargetFolderID {
		t.Fatal("verified Guangya directory receipt did not resolve", err)
	}
	m.relays[0].TargetKind = "pan123"
	if _, err := m.resolveRelayTarget(child); err == nil {
		t.Fatal("receipt from another provider was accepted")
	}
}

func TestRelayGuangyaQueueSameAccountCopy(t *testing.T) {
	const source, target, sourceID, copyID, content = "source-dir", "target-dir", "original-file", "copied-file", "original bytes"
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if r.Method == http.MethodPost && json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid request payload")
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/userres/v1/file/get_file_list":
			parent := payload["parentId"]
			rows := []map[string]any{}
			if parent == source || parent == target && writes.Load() > 0 {
				id := sourceID
				if parent == target {
					id = copyID
				}
				rows = append(rows, map[string]any{"fileId": id, "parentId": parent, "fileName": "item.txt", "fileSize": len(content), "resType": 1})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": rows, "total": len(rows)}})
		case "/userres/v1/get_res_download_url":
			fmt.Fprint(w, `{"code":0,"data":{"signedURL":"https://download.guangyapan.com/content"}}`)
		case "/content":
			fmt.Fprint(w, content)
		case "/userres/v2/get_res_center_token":
			if payload["parentId"] != target || payload["name"] != "item.txt" {
				t.Error("copy used the wrong target", payload)
			}
			writes.Add(1)
			fmt.Fprint(w, `{"code":156,"data":{"taskId":"upload-task"}}`)
		case "/userres/v1/file/get_info_by_task_id":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"fileId": copyID}})
		default:
			t.Error("unexpected same-account copy request", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	original := http.DefaultTransport
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	dialer := net.Dialer{}
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()

	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a := Account{ID: "guangya", Kind: "guangya", Endpoint: GuangyaAPI, Secret: "fixture"}
	m.accounts = []Account{a}
	in := RelayInput{SourceAccountID: a.ID, TargetAccountID: a.ID, ParentID: source, EntryID: sourceID,
		DestinationID: target, Name: "item.txt", Size: int64(len(content))}
	ctx := context.Background()
	sameFolder := in
	sameFolder.DestinationID = source
	if n, err := m.QueueRelaySnapshots(ctx, []RelayInput{sameFolder}); err == nil || n != 0 {
		t.Fatal("accepted the source directory as a target", n, err)
	}
	if n, err := m.QueueRelaySnapshots(ctx, []RelayInput{in}); err != nil || n != 1 {
		t.Fatal("same-account copy did not queue", n, err)
	}
	jobs := awaitRelay(t, m)
	if len(jobs) != 1 || jobs[0].State != "verified" || jobs[0].CanMove || writes.Load() != 1 {
		t.Fatal("same-account copy did not verify or exposed a move", jobs, writes.Load())
	}
	if n, err := m.QueueRelaySnapshots(ctx, []RelayInput{in}); err == nil || n != 0 || writes.Load() != 1 {
		t.Fatal("existing target was not rejected before uploading", n, err, writes.Load())
	}
}

func TestRelayGuangyaSameAccountFileReceipt(t *testing.T) {
	const account, source, target, file = "guangya-account", "source-folder", "target-folder", "source-file"
	d := diskRelay{
		Input:      RelayInput{SourceAccountID: account, TargetAccountID: account, ParentID: source, DestinationID: target, EntryID: file},
		SourceKind: "guangya", TargetKind: "guangya", SourceScope: "scope", TargetScope: "scope",
	}
	if !validRelayAccounts(d) || relayMoveEligible(d) {
		t.Fatal("same-account standalone copy must retain its source")
	}
	for _, change := range []func(*diskRelay){
		func(row *diskRelay) { row.Input.DestinationID = source },
		func(row *diskRelay) { row.Input.DestinationID = file },
		func(row *diskRelay) { row.TargetFileID = file },
		func(row *diskRelay) { row.TargetScope = "different" },
		func(row *diskRelay) { row.TargetKind = "quark" },
		func(row *diskRelay) { row.Folder = true },
		func(row *diskRelay) { row.ParentJobID = "tree-job" },
	} {
		changed := d
		change(&changed)
		if validRelayAccounts(changed) {
			t.Fatal("unsafe same-account copy record was accepted", changed.Input)
		}
	}
}

func TestRelayGuangyaStandaloneReceipts(t *testing.T) {
	const content = "standalone relay"
	for _, mode := range []string{"success", "same-name", "old-id", "source-id"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			a := Account{ID: "target", Kind: "guangya", Endpoint: GuangyaAPI}
			m.accounts = []Account{a}
			d := diskRelay{TargetKind: a.Kind, TargetScope: downloadScope(a), Submitted: true,
				Input: RelayInput{SourceAccountID: a.ID, TargetAccountID: a.ID, EntryID: "source-id", DestinationID: guangyaRoot, Name: "item.txt", Size: int64(len(content))},
				Job:   RelayJob{ID: "fixture-relay", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))},
			}
			m.relays = []diskRelay{d}
			cache := filepath.Join(t.TempDir(), "cache")
			if err := os.WriteFile(cache, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			spool, err := os.Open(cache)
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			writes := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					if writes != 0 {
						id := "uploaded-id"
						if mode == "old-id" {
							id = "existing-id"
						} else if mode == "source-id" {
							id = "source-id"
						}
						return quarkResponse(fmt.Sprintf(`{"code":0,"data":{"list":[{"fileId":%q,"parentId":"","fileName":"item.txt","fileSize":%d,"resType":1}],"total":1}}`, id, len(content)), nil), nil
					}
					name := "unrelated.txt"
					if mode == "same-name" {
						name = "ITEM.TXT"
					}
					return quarkResponse(fmt.Sprintf(`{"code":0,"data":{"list":[{"fileId":"existing-id","parentId":"","fileName":%q,"fileSize":1,"resType":1}],"total":1}}`, name), nil), nil
				case "/userres/v2/get_res_center_token":
					writes++
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["parentId"] != "" || payload["name"] != "item.txt" {
						t.Fatal("wrong upload destination", payload, err)
					}
					return quarkResponse(`{"code":156,"data":{"taskId":"upload-task"}}`, nil), nil
				case "/userres/v1/file/get_info_by_task_id":
					id := "uploaded-id"
					if mode == "old-id" {
						id = "existing-id"
					} else if mode == "source-id" {
						id = "source-id"
					}
					return quarkResponse(fmt.Sprintf(`{"code":0,"data":{"fileId":%q}}`, id), nil), nil
				case "/userres/v1/get_res_download_url":
					return quarkResponse(`{"code":0,"data":{"signedURL":"https://download.guangyapan.com/content"}}`, nil), nil
				case "/content":
					resp := quarkResponse(content, nil)
					resp.ContentLength = int64(len(content))
					return resp, nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
				}
			})}
			err = m.relayNativeTargetUpload(ctx, client, a, &d, spool, func(int64) {})
			if (mode == "success") != (err == nil) || (mode == "same-name") != (writes == 0) {
				t.Fatalf("unexpected upload result: writes=%d, error=%v", writes, err)
			}
			if mode != "success" {
				if d.TargetFileID != "" {
					t.Fatal("unconfirmed target was saved")
				}
				return
			}
			saved, err := readState(m.path)
			if err != nil || len(saved.Relays) != 1 || saved.Relays[0].TargetFileID != "uploaded-id" {
				t.Fatal("missing saved upload result ID", err)
			}
			if err := relayMoveTarget(ctx, client, a, d); err != nil {
				t.Fatal("cannot verify standalone root target", err)
			}
		})
	}
}

func TestRelayGuangyaDirectoryReceipts(t *testing.T) {
	const original = "relay bytes"
	a := Account{Kind: "guangya", Endpoint: GuangyaAPI}
	for _, mode := range []string{"success", "bad-file-id", "missing-total", "missing-file-receipt"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				var in map[string]any
				if r.Method == http.MethodPost {
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Fatal(err)
					}
				}
				switch r.URL.Path {
				case "/userres/v1/file/create_dir":
					if in["parentId"] != "" || in["dirName"] != "Tree" || in["failIfNameExist"] != true {
						t.Fatal("unexpected folder create payload", in)
					}
					return quarkResponse(`{"code":0,"data":{"fileId":"tree-id"}}`, nil), nil
				case "/userres/v1/file/get_file_list":
					if in["parentId"] == "" {
						return quarkResponse(`{"code":0,"data":{"total":1,"list":[{"fileId":"tree-id","parentId":"","fileName":"Tree","resType":2}]}}`, nil), nil
					}
					if in["parentId"] != "tree-id" {
						t.Fatal("unexpected target folder", in)
					}
					id := "copied-file"
					if mode == "bad-file-id" {
						id = "someone-else"
					}
					total := `"total":1,`
					if mode == "missing-total" {
						total = ""
					}
					return quarkResponse(fmt.Sprintf(`{"code":0,"data":{%s"list":[{"fileId":%q,"parentId":"tree-id","fileName":"a.txt","fileSize":%d,"resType":1}]}}`, total, id, len(original)), nil), nil
				case "/userres/v1/get_res_download_url":
					if in["fileId"] != "copied-file" {
						t.Fatal("unexpected file ID", in)
					}
					return quarkResponse(`{"code":0,"data":{"signedURL":"https://download.guangyapan.com/content"}}`, nil), nil
				case "/content":
					resp := quarkResponse(original, nil)
					resp.ContentLength = int64(len(original))
					return resp, nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
				}
			})}
			ctx := context.Background()
			root := diskRelay{Folder: true, TargetKind: a.Kind, Input: RelayInput{Name: "Tree", DestinationID: guangyaRoot}}
			create, err := prepareRelayDirectoryCreate(ctx, c, a, root)
			if err != nil {
				t.Fatal(err)
			}
			root.TargetFolderID, err = create()
			if err != nil {
				t.Fatal(err)
			}
			root.CreatedAck = true
			if err := relayDirectoryTarget(ctx, c, a, root); err != nil {
				t.Fatal(err)
			}
			child := diskRelay{
				TargetKind: a.Kind, ParentJobID: "parent", targetParent: root.TargetFolderID, TargetFileID: "copied-file",
				Input: RelayInput{DestinationID: relayPlannedChildTarget(root), Name: "a.txt", Size: int64(len(original))},
				Job:   RelayJob{SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(original)))},
			}
			if mode == "missing-file-receipt" {
				child.TargetFileID = ""
			}
			before := requests
			err = relayNativeTargetReadback(ctx, c, a, child)
			if (mode == "success") != (err == nil) {
				t.Fatal("unexpected readback", err)
			}
			if mode == "missing-file-receipt" && requests != before {
				t.Fatal("readback used a target without an accepted file ID")
			}
		})
	}
}
