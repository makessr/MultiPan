package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRelayQuarkSameAccountCopy(t *testing.T) {
	const source, target, fileID, copyID, content = "source-dir", "target-dir", "original-file", "copied-file", "original bytes"
	const tree, child, treeCopy, childCopy = "tree", "nested", "tree-copy", "nested-copy"
	const movedDestination = "moved-destination"
	var uploaded, targetReadable, destinationMoved atomic.Bool
	var writes atomic.Int32
	var directories atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			parent := r.URL.Query().Get("pdir_fid")
			rows := []map[string]any{}
			if parent == source || parent == target && uploaded.Load() {
				id := fileID
				if parent == target {
					id = copyID
				}
				rows = append(rows, map[string]any{"fid": id, "pdir_fid": parent, "file_name": "a.txt", "file_type": 1, "size": len(content), "updated_at": 7})
			}
			switch {
			case parent == source:
				rows = append(rows, map[string]any{"fid": tree, "pdir_fid": parent, "file_name": "Tree", "file_type": 0})
			case parent == tree:
				rows = append(rows, map[string]any{"fid": child, "pdir_fid": parent, "file_name": "Nested", "file_type": 0})
			case parent == child && destinationMoved.Load():
				rows = append(rows, map[string]any{"fid": movedDestination, "pdir_fid": parent, "file_name": "Moved", "file_type": 0})
			case parent == target && directories.Load() >= 1:
				rows = append(rows, map[string]any{"fid": treeCopy, "pdir_fid": parent, "file_name": "Tree", "file_type": 0})
			case parent == treeCopy && directories.Load() >= 2:
				rows = append(rows, map[string]any{"fid": childCopy, "pdir_fid": parent, "file_name": "Nested", "file_type": 0})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": len(rows)}})
		case "/1/clouddrive/file":
			var body map[string]any
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid directory request", r.Method, body)
			}
			id := ""
			switch directories.Add(1) {
			case 1:
				if body["pdir_fid"] == target && body["file_name"] == "Tree" {
					id = treeCopy
				}
			case 2:
				if body["pdir_fid"] == treeCopy && body["file_name"] == "Nested" {
					id = childCopy
				}
			}
			if id == "" {
				t.Error("unexpected directory write", body)
			}
			fmt.Fprint(w, quarkData(map[string]string{"fid": id}))
		case "/1/clouddrive/file/download":
			var input struct {
				FIDs []string `json:"fids"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.FIDs) != 1 ||
				(input.FIDs[0] != fileID && input.FIDs[0] != copyID) {
				t.Error("invalid content request", input)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, quarkData([]any{map[string]string{"fid": input.FIDs[0], "download_url": "https://download.quark.cn/content/" + input.FIDs[0]}}))
		case "/content/" + fileID:
			io.WriteString(w, content)
		case "/content/" + copyID:
			if !targetReadable.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			io.WriteString(w, content)
		case "/1/clouddrive/file/upload/pre":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["pdir_fid"] != target || body["file_name"] != "a.txt" {
				t.Error("upload did not target the requested directory", body)
			}
			fmt.Fprint(w, quarkData(map[string]string{"task_id": "upload-task"}))
		case "/1/clouddrive/file/update/hash":
			writes.Add(1)
			uploaded.Store(true)
			fmt.Fprint(w, quarkData(map[string]any{"finish": true, "fid": copyID}))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
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
	a := Account{ID: "quark-account", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}
	m.accounts = []Account{a}
	in := RelayInput{SourceAccountID: a.ID, TargetAccountID: a.ID, ParentID: source, EntryID: fileID,
		DestinationID: target, Name: "a.txt", Size: int64(len(content))}
	sameFolder := in
	sameFolder.DestinationID = source
	if n, err := m.QueueRelaySnapshots(context.Background(), []RelayInput{sameFolder}); err == nil || n != 0 {
		t.Fatal("accepted the source directory as the target", n, err)
	}
	if n, err := m.QueueRelaySnapshots(context.Background(), []RelayInput{in}); err != nil || n != 1 {
		t.Fatal("same-account file copy was not queued", n, err)
	}
	jobs := awaitRelay(t, m)
	if len(jobs) != 1 || jobs[0].State != "unknown" || !jobs[0].CanCheck || jobs[0].CanMove || writes.Load() != 1 {
		t.Fatal("unreadable target did not retain its upload receipt for checking", jobs, writes.Load())
	}
	targetReadable.Store(true)
	if err := m.CheckRelay(jobs[0].ID); err != nil {
		t.Fatal("same-account target could not be checked", err)
	}
	jobs = awaitRelay(t, m)
	if len(jobs) != 1 || jobs[0].State != "verified" || jobs[0].CanMove || writes.Load() != 1 {
		t.Fatal("same-account copy did not complete as a copy-only task", jobs, writes.Load())
	}
	if err := m.StartRelayMove(jobs[0].ID, "移入回收站"); err == nil {
		t.Fatal("same-account copy allowed a source deletion")
	}
	if n, err := m.QueueRelaySnapshots(context.Background(), []RelayInput{in}); err == nil || n != 0 {
		t.Fatal("accepted a duplicate target name", n, err)
	}
	folder := in
	folder.Size, folder.EntryID, folder.Name = 0, tree, "Tree"
	for _, overlap := range []string{source, tree, child} {
		folder.DestinationID = overlap
		if n, err := m.QueueRelaySnapshotDirectory(context.Background(), folder); err == nil || n != 0 || directories.Load() != 0 {
			t.Fatal("accepted an overlapping directory copy", overlap, n, err)
		}
	}
	folder.DestinationID = target
	if n, err := m.QueueRelaySnapshotDirectory(context.Background(), folder); err != nil || n != 2 {
		t.Fatal("nested directory copy was not queued", n, err)
	}
	jobs = awaitRelay(t, m)
	if len(jobs) != 3 || directories.Load() != 2 {
		t.Fatal("nested directory copy did not create the expected tree", jobs, directories.Load())
	}
	loaded, err := NewManager(m.path, nil)
	if err != nil {
		t.Fatal("same-account copy receipt did not survive reload", err)
	}
	for _, job := range loaded.RelayJobs() {
		if job.State != "verified" || job.CanMove {
			t.Fatal("same-account copy receipt changed after reload", job)
		}
	}
	loaded.Close()

	// Simulate a destination moved into the source tree while the root waits.
	m.mu.Lock()
	m.renaming[a.ID] = true
	m.mu.Unlock()
	folder.DestinationID = movedDestination
	if n, err := m.QueueRelaySnapshotDirectory(context.Background(), folder); err != nil || n != 2 {
		t.Fatal("copy could not be queued before the destination moved", n, err)
	}
	var rootJob RelayJob
	for _, job := range m.RelayJobs() {
		if job.Destination == movedDestination {
			rootJob = job
		}
	}
	if !rootJob.CanResume {
		t.Fatal("root copy did not wait for the account reservation", rootJob)
	}
	destinationMoved.Store(true)
	m.mu.Lock()
	delete(m.renaming, a.ID)
	m.mu.Unlock()
	if err := m.ResumeRelay(rootJob.ID); err != nil {
		t.Fatal("root copy could not resume", err)
	}
	for _, job := range awaitRelay(t, m) {
		if job.ID == rootJob.ID && (job.State != "failed" || !strings.Contains(job.Message, "目标目录已进入来源树")) {
			t.Fatal("destination overlap was not detected before the write", job)
		}
	}
	if directories.Load() != 2 {
		t.Fatal("copy submitted a directory after the target moved", directories.Load())
	}
}

func TestRelayQuarkStandaloneTargetReceipt(t *testing.T) {
	a := Account{Kind: "quark", Endpoint: QuarkAPI}
	for _, parent := range []string{"0", "selected-folder"} {
		d := diskRelay{TargetKind: a.Kind, TargetFileID: "uploaded-file", Submitted: true,
			Input: RelayInput{DestinationID: parent, Size: 10}, Job: RelayJob{State: "verified"},
		}
		if !validRelayPlannedTarget(a, d) || !validRelayTreeRecord(d) {
			t.Fatal("standalone Quark target should retain its receipt", parent)
		}
		d.TargetFileID = ""
		if validRelayTreeRecord(d) {
			t.Fatal("verified standalone Quark target lost its receipt")
		}
	}
	d := diskRelay{TargetKind: a.Kind, Input: RelayInput{DestinationID: "0", Size: 0}}
	if validRelayPlannedTarget(a, d) {
		t.Fatal("standalone empty files must be rejected")
	}
}

func TestRelayQuarkDirectoryTarget(t *testing.T) {
	const content = "verified content"
	for _, mode := range []string{"success", "missing-folder-id", "missing-file-id", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			a := Account{ID: "target", Kind: "quark", Endpoint: QuarkAPI}
			m.accounts = []Account{a}
			parent := diskRelay{
				Folder: true, TargetKind: a.Kind, TargetScope: downloadScope(a),
				Input: RelayInput{DestinationID: "0", Name: "Tree"},
				Job:   RelayJob{ID: "root-receipt", State: "verified"},
			}
			const folderID, fileID = "created-folder", "uploaded-file"
			folderWritten, uploaded := false, false
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/1/clouddrive/file":
					var body map[string]any
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil ||
						body["pdir_fid"] != "0" || body["file_name"] != "Tree" {
						t.Fatal("bad mkdir payload", body)
					}
					folderWritten = true
					if mode == "missing-folder-id" {
						return quarkResponse(quarkData(map[string]string{}), nil), nil
					}
					return quarkResponse(quarkData(map[string]string{"fid": folderID}), nil), nil
				case "/1/clouddrive/file/sort":
					rows := []map[string]any{}
					switch r.URL.Query().Get("pdir_fid") {
					case "0":
						if folderWritten {
							rows = append(rows, map[string]any{"fid": folderID, "pdir_fid": "0", "file_name": "Tree", "file_type": 0})
						}
					case folderID:
						if uploaded {
							rows = append(rows, map[string]any{"fid": fileID, "pdir_fid": folderID, "file_name": "a.txt", "file_type": 1, "size": len(content), "updated_at": 7})
						}
					default:
						t.Fatal("unexpected parent", r.URL)
					}
					raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows}, "metadata": map[string]any{"_total": len(rows)}})
					return quarkResponse(string(raw), nil), nil
				case "/1/clouddrive/file/upload/pre":
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["pdir_fid"] != folderID || body["file_name"] != "a.txt" {
						t.Fatal("upload did not use the acknowledged folder", body)
					}
					return quarkResponse(quarkData(map[string]string{"task_id": "upload-task"}), nil), nil
				case "/1/clouddrive/file/update/hash":
					uploaded = true
					id := fileID
					if mode == "missing-file-id" {
						id = ""
					}
					return quarkResponse(quarkData(map[string]any{"finish": true, "fid": id}), nil), nil
				case "/1/clouddrive/file/download":
					return quarkResponse(quarkData([]any{map[string]string{"fid": fileID, "download_url": "https://download.quark.cn/content"}}), nil), nil
				case "/content":
					data := content
					if mode == "mismatch" {
						data = "different bytes"
					}
					return quarkResponse(data, nil), nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL)
				}
			})}
			create, err := prepareRelayDirectoryCreate(ctx, client, a, parent)
			if err != nil {
				t.Fatal(err)
			}
			parent.TargetFolderID, err = create()
			if mode == "missing-folder-id" {
				if err == nil {
					t.Fatal("accepted folder without a receipt ID")
				}
				return
			}
			if err != nil || parent.TargetFolderID != folderID {
				t.Fatal("missing folder receipt", err)
			}
			parent.CreatedAck = true
			if err := relayDirectoryTarget(ctx, client, a, parent); err != nil {
				t.Fatal(err)
			}
			child := diskRelay{TargetKind: a.Kind, TargetScope: parent.TargetScope, ParentJobID: parent.Job.ID, Submitted: true,
				Input: RelayInput{Name: "a.txt", DestinationID: relayPlannedChildTarget(parent), Size: int64(len(content))},
				Job:   RelayJob{ID: "file-receipt", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))},
			}
			m.relays = []diskRelay{parent, child}
			child, err = m.resolveRelayTarget(child)
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(t.TempDir(), "cache")
			if err := os.WriteFile(cache, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			spool, err := os.Open(cache)
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			err = m.relayNativeTargetUpload(ctx, client, a, &child, spool, func(int64) {})
			if mode == "missing-file-id" {
				if err == nil || child.TargetFileID != "" {
					t.Fatal("accepted uploaded file without an ID")
				}
				return
			}
			if err != nil || child.TargetFileID != fileID {
				t.Fatal("missing upload receipt", err)
			}
			err = relayNativeTargetReadback(ctx, client, a, child)
			if (mode == "success") != (err == nil) {
				t.Fatal("unexpected full readback result", err)
			}
		})
	}
}
