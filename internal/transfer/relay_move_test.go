package transfer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRelayDAVMoveReceipts(t *testing.T) {
	for _, mode := range []string{"success", "target-changed", "source-changed", "source-etag", "source-type-changed", "source-config-changed", "missing-cache", "tamper-cache", "initial-save-failed", "checkpoint-failed", "ack-lost", "ack-save-failed", "silent-ack", "precondition-failed", "async", "replacement", "post-target-changed", "post-list-failed", "source-get-still-present", "final-save-failed", "stop-before", "stop-after"} {
		t.Run(mode, func(t *testing.T) {
			f, m, in := newRelayFixture(t, "success")
			statePath := m.path
			if _, err := m.QueueRelays(context.Background(), []RelayInput{in}); err != nil {
				t.Fatal(err)
			}
			if j := awaitRelay(t, m)[0]; j.State != "verified" || !j.CanMove {
				t.Fatal("copy not ready for explicit move", j)
			}
			cachePath := filepath.Join(m.relayRoot(), m.RelayJobs()[0].ID, "data.bin")

			var mu sync.Mutex
			deletes := 0
			gone := false
			injected := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				isSource := strings.HasPrefix(r.URL.Path, "/src/")
				if mode == "source-type-changed" && injected && isSource && r.Method == "PROPFIND" {
					w.WriteHeader(207)
					fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/src/one.txt/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype><d:getetag>"version|1"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, r.URL.Path)
					return
				}
				if isSource && r.Method == "DELETE" {
					if mode == "success" {
						if err := os.WriteFile(cachePath, []byte("must not modify"), 0600); err == nil {
							t.Error("local original not locked during deletion")
						}
					}
					deletes++
					u, p, _ := r.BasicAuth()
					if u != "source-user" || p != "source-password" || r.URL.Path != "/src/one.txt" || r.Header.Get("If-Match") != `"version|1"` {
						t.Error("wrong conditional source delete", r.Method, r.URL, r.Header.Get("If-Match"))
					}
					m.mu.Lock()
					d := m.relayByIDLocked(m.relays[0].Job.ID)
					checkpoint := d.Move.Submitted && d.Job.State == "move_unknown"
					m.mu.Unlock()
					if !checkpoint {
						t.Error("delete without checkpoint")
					}
					gone = mode != "silent-ack" && mode != "precondition-failed" && mode != "async"
					if mode == "ack-save-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "post-target-changed" {
						f.mu.Lock()
						f.target["one.txt"] = []byte("changed")
						f.mu.Unlock()
					}
					if mode == "stop-after" {
						_ = m.PauseRelays()
					}
					if mode == "precondition-failed" {
						w.WriteHeader(412)
						return
					}
					if mode == "async" {
						w.WriteHeader(202)
						return
					}
					if mode == "ack-lost" {
						w.WriteHeader(503)
					} else {
						w.WriteHeader(204)
					}
					return
				}
				if mode == "stop-before" && !isSource && r.Method == "GET" && !injected {
					injected = true
					_ = m.PauseRelays()
				}
				if isSource && gone {
					if r.Method == "PROPFIND" {
						if mode == "post-list-failed" {
							w.WriteHeader(503)
							return
						}
						if mode == "final-save-failed" && !injected {
							injected = true
							m.mu.Lock()
							m.path = t.TempDir()
							m.mu.Unlock()
						}
						w.WriteHeader(207)
						fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, r.URL.Path)
						if mode == "replacement" {
							fmt.Fprint(w, `<d:response><d:href>/src/one.txt</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>1</d:getcontentlength><d:getetag>"replacement"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
						}
						fmt.Fprint(w, `</d:multistatus>`)
						return
					}
					if r.Method == "GET" {
						if mode == "source-get-still-present" {
							w.Write(f.data)
							return
						}
						w.WriteHeader(404)
						return
					}
				}
				if isSource && r.Method == "GET" {
					if mode == "source-type-changed" {
						injected = true
					}
					if mode == "source-changed" {
						w.Header().Set("ETag", `"version|1"`)
						w.Write([]byte(strings.Repeat("x", len(f.data))))
						return
					}
					if mode == "checkpoint-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if mode == "source-config-changed" {
						m.mu.Lock()
						m.accounts[0].Secret = "changed"
						m.mu.Unlock()
					}
				}
				f.serve(w, r)
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			old := http.DefaultTransport
			http.DefaultTransport = tr
			defer func() { http.DefaultTransport = old; tr.CloseIdleConnections() }()
			j := m.RelayJobs()[0]
			if err := m.StartRelayMove(j.ID, "删除"); err == nil || deletes != 0 {
				t.Fatal("confirmation bypass")
			}
			cache := filepath.Join(m.relayRoot(), j.ID, "data.bin")
			switch mode {
			case "target-changed":
				f.target["one.txt"] = []byte("changed")
			case "source-etag":
				f.mode = "source-changed"
			case "missing-cache":
				if err := os.Remove(cache); err != nil {
					t.Fatal(err)
				}
			case "tamper-cache":
				if err := os.WriteFile(cache, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "initial-save-failed":
				m.path = t.TempDir()
			}
			err := m.StartRelayMove(j.ID, "删除源文件")
			if mode == "initial-save-failed" {
				if err == nil || deletes != 0 {
					t.Fatal("initial save failed but started", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = m.StartRelayMove(j.ID, "删除源文件") // no duplicate worker or deletion
			j = awaitRelay(t, m)[0]

			m.mu.Lock()
			m.path = statePath
			m.accounts[0].Secret = "source-password"
			m.mu.Unlock()
			beforeWrite := mode == "target-changed" || mode == "source-changed" || mode == "source-etag" || mode == "source-type-changed" || mode == "source-config-changed" || mode == "missing-cache" || mode == "tamper-cache" || mode == "checkpoint-failed" || mode == "stop-before"
			if beforeWrite {
				if deletes != 0 || j.State != "verified" {
					t.Fatal("failed preflight deleted source", mode, deletes, j)
				}
				return
			}
			if deletes != 1 {
				t.Fatal("wrong delete count", deletes)
			}
			want := "move_unknown"
			if mode == "success" {
				want = "moved"
			}
			if j.State != want || j.CanMove {
				t.Fatal(mode, j.State, j.Message)
			}
			if _, err = os.Stat(cache); err != nil {
				t.Fatal("original cache lost", err)
			}
			if mode != "success" {
				if err = m.ClearRelay(j.ID); err == nil {
					t.Fatal("unknown move cleared")
				}
				p := m.PendingReview()
				found := false
				for _, r := range p.Rows {
					if r.ID == j.ID {
						found = true
						if r.Kind != "relay" || r.State != "unknown" || r.CanCheck {
							t.Fatal("unsafe pending move route", r)
						}
					}
				}
				if !found {
					t.Fatal("move omitted from pending review")
				}
			}
			m.Close()
			restored, err := NewManager(statePath, nil)
			if err != nil {
				t.Fatal("restore move", err)
			}
			defer restored.Close()
			m = restored
			if got := m.RelayJobs()[0]; got.State != want {
				t.Fatal("lost move receipt", got)
			}
			if err = m.StartRelayMove(j.ID, "删除源文件"); err == nil {
				t.Fatal("submitted delete replay allowed")
			}
			if want == "move_unknown" {
				if mode == "silent-ack" {
					mu.Lock()
					gone = true
					mu.Unlock()
				}
				if err = m.CheckRelayMove(j.ID); err != nil {
					t.Fatal(err)
				}
				got := awaitRelay(t, m)[0]

				if deletes != 1 {
					t.Fatal("read-only move check deleted source again")
				}
				if mode == "silent-ack" || mode == "final-save-failed" {
					if got.State != "moved" {
						t.Fatal("saved acknowledgement not recoverable", got)
					}
				} else if got.State != "move_unknown" {
					t.Fatal("unproven delete confirmed", mode, got)
				}
			}
		})
	}
}

func TestRelayMoveRestoreAndEligibility(t *testing.T) {
	_, m, in := newRelayFixture(t, "success")
	if _, err := m.QueueRelays(context.Background(), []RelayInput{in}); err != nil {
		t.Fatal(err)
	}
	awaitRelay(t, m)
	base := m.relays[0]
	native := base
	native.SourceKind = "pan123"
	native.Input.ParentID, native.Input.EntryID = "10", "42"
	native.ETag = "d41d8cd98f00b204e9800998ecf8427e"
	if !relayMoveEligible(native) {
		t.Fatal("verified 123 file with a provider MD5 must be eligible")
	}
	for _, mode := range []string{"unverified", "folder", "child", "native", "weak", "no-copy-checkpoint"} {
		d := base
		switch mode {
		case "unverified":
			d.Job.State = "paused"
		case "folder":
			d.Folder = true
		case "child":
			d.ParentJobID = "parent"
		case "native":
			d.SourceKind = "pan123"
		case "weak":
			d.ETag = `W/"old"`
		case "no-copy-checkpoint":
			d.Submitted = false
		}
		if relayMoveEligible(d) {
			t.Fatal("ineligible move allowed", mode)
		}
	}
	for _, mode := range []string{"preflight", "unknown", "confirmed"} {
		d := base
		d.Move = diskRelayMove{State: "working", Created: time.Now()}
		d.Job.State = "moving"
		want := "verified"
		if mode == "unknown" {
			d.Move.State = "unknown"
			d.Move.Submitted = true
			d.Job.State = "move_unknown"
			want = "move_unknown"
		}
		if mode == "confirmed" {
			d.Move.State = "confirmed"
			d.Move.Submitted = true
			d.Move.Acknowledged = true
			d.Job.State = "moved"
			want = "moved"
		}
		clone := &Manager{}
		if err := clone.restoreRelays([]diskRelay{d}); err != nil || clone.relays[0].Job.State != want {
			t.Fatal("restore move", mode, err)
		}
		if mode == "preflight" && clone.relays[0].Move.State != "not_sent" {
			t.Fatal("unsubmitted move auto resumed")
		}
		if mode == "confirmed" {
			for _, bad := range []string{"no-ack", "no-submit", "wrong-state", "native", "folder", "child", "no-date"} {
				x := d
				switch bad {
				case "no-ack":
					x.Move.Acknowledged = false
				case "no-submit":
					x.Move.Submitted = false
				case "wrong-state":
					x.Job.State = "verified"
				case "native":
					x.SourceKind = "pan123"
				case "folder":
					x.Folder = true
				case "child":
					x.ParentJobID = "parent"
				case "no-date":
					x.Move.Created = time.Time{}
				}
				if validRelayMoveRecord(x) {
					t.Fatal("corrupt move proof accepted", bad)
				}
			}
		}
	}
}

func TestRelayQuarkMoveReceipts(t *testing.T) {
	for _, mode := range []string{"success", "changed-source", "partial-source", "lost-ack", "wrong-recycle-id"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
			if err != nil {
				t.Fatal(err)
			}
			source := Account{ID: "source", Kind: "quark", Endpoint: QuarkAPI, Secret: "source-fixture"}
			target := Account{ID: "target", Kind: "webdav", Endpoint: "https://dav.example.com", Username: "target-user", Secret: "target-fixture"}
			m.accounts = []Account{source, target}
			const name, content = "item.txt", "verified content"
			sha := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
			in := RelayInput{SourceAccountID: source.ID, TargetAccountID: target.ID,
				ParentID: "parent-fid", EntryID: "source-fid", DestinationID: "/dst", Name: name, Size: int64(len(content))}
			d := diskRelay{
				Input: in, SourceKind: source.Kind, TargetKind: target.Kind, ETag: "123:" + sha, Submitted: true,
				SourceScope: downloadScope(source), TargetScope: downloadScope(target),
				Job: RelayJob{ID: strings.Repeat("a", 32), State: "verified", SHA256: sha},
			}
			if !relayMoveEligible(d) {
				t.Fatal("verified Quark relay should be eligible")
			}
			badPin := d
			badPin.ETag = "123:" + strings.Repeat("b", 64)
			if relayMoveEligible(badPin) {
				t.Fatal("different pinned SHA256 authorized source removal")
			}
			d.Job.State = "moving"
			d.Move = diskRelayMove{State: "working", Created: time.Now()}
			m.relays = []diskRelay{d}
			dir := filepath.Join(m.relayRoot(), d.Job.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for file, data := range map[string]string{".owner": "multipan-relay-v1:" + d.Job.ID, "data.bin": content} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writes := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Host + r.URL.Path {
				case "dav.example.com/dst/item.txt":
					return quarkResponse(content, nil), nil
				case "download.quark.cn/item":
					resp := quarkResponse(content, nil)
					if mode == "partial-source" {
						resp.StatusCode = http.StatusPartialContent
					}
					if mode == "changed-source" {
						resp = quarkResponse(strings.Repeat("x", len(content)), nil)
					}
					return resp, nil
				case "drive.quark.cn/1/clouddrive/file/download":
					return quarkResponse(quarkData([]any{map[string]string{
						"fid": in.EntryID, "download_url": "https://download.quark.cn/item",
					}}), nil), nil
				case "drive.quark.cn/1/clouddrive/file/sort":
					if writes > 0 {
						return quarkResponse(`{"code":0,"data":{"list":[]},"metadata":{"_total":0}}`, nil), nil
					}
					return quarkResponse(quarkData(map[string]any{"list": []any{map[string]any{
						"fid": in.EntryID, "pdir_fid": in.ParentID, "file_name": in.Name,
						"file_type": 1, "dir": false, "size": in.Size, "updated_at": 123,
					}}}), nil), nil
				case "drive.quark.cn/1/clouddrive/file/delete":
					writes++
					if r.Method != http.MethodPost {
						t.Error("unexpected recycle method", r.Method)
					}
					var payload struct {
						ActionType int      `json:"action_type"`
						Files      []string `json:"filelist"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ActionType != 2 ||
						len(payload.Files) != 1 || payload.Files[0] != in.EntryID {
						t.Error("unexpected source recycle payload", payload, err)
					}
					persisted, err := readState(m.path)
					if err != nil || len(persisted.Relays) != 1 || !persisted.Relays[0].Move.Submitted {
						t.Error("source recycle request without persisted checkpoint", err)
					}
					if mode == "lost-ack" {
						return nil, fmt.Errorf("lost response")
					}
					return quarkResponse(quarkData(map[string]string{"task_id": "trash-task"}), nil), nil
				case "drive.quark.cn/1/clouddrive/task":
					return quarkResponse(quarkData(map[string]int{"status": 2}), nil), nil
				case "drive.quark.cn/1/clouddrive/file/recycle/list":
					fid := in.EntryID
					if mode == "wrong-recycle-id" {
						fid = "another-fid"
					}
					return quarkResponse(quarkData(map[string]any{"total": 1, "list": []any{map[string]any{
						"record_id": "recycle-record", "fid": fid, "file_name": name, "pdir_fid": in.ParentID,
						"dir": false, "size": in.Size,
					}}}), nil), nil
				default:
					return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
			})}
			err = m.performRelayMove(ctx, client, source, target, d, m.relayRoot(), false)
			prewriteFailure := mode == "changed-source" || mode == "partial-source"
			if (mode == "success") != (err == nil) || prewriteFailure != (writes == 0) {
				t.Fatalf("unexpected move result, writes=%d, error=%v", writes, err)
			}
			if !prewriteFailure {
				saved := m.relays[0]
				err = m.performRelayMove(ctx, client, source, target, saved, m.relayRoot(), true)
				if (mode == "success") != (err == nil) || writes != 1 {
					t.Fatalf("unexpected read-only result, writes=%d, error=%v", writes, err)
				}
			}
		})
	}
}

func TestRelayMoveNativeTargetReadback(t *testing.T) {
	for _, kind := range []string{"pan123", "baidu"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte("verified backup bytes")
			md := md5.Sum(data)
			sha := sha256.Sum256(data)
			digest := hex.EncodeToString(md[:])
			reads := 0
			a := Account{ID: "target", Kind: kind, Endpoint: Pan123API, Secret: "target-private"}
			parent := "0"
			if kind == "baidu" {
				a.Endpoint = BaiduAPI
				a.Secret = "BDUSS=target-private"
				parent = "/"
			}
			d := diskRelay{TargetKind: kind, Input: RelayInput{Name: "one.txt", DestinationID: parent, Size: int64(len(data))}, Job: RelayJob{SHA256: hex.EncodeToString(sha[:])}}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "relay-read.example" || r.Host == "d.pcs.baidu.com" {
					reads++
					if r.Header.Get("Authorization") != "" {
						t.Error("API token leaked to CDN")
					}
					w.Write(data)
					return
				}
				if kind == "pan123" {
					switch r.URL.Path {
					case "/b/api/file/list/new":
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{map[string]any{"FileId": 3000, "ParentFileId": 0, "FileName": "one.txt", "Type": 0, "Size": len(data), "Etag": digest, "S3KeyFlag": "fixture"}}, "Total": 1, "Next": -1}})
					case "/b/api/file/download_info":
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": "https://relay-read.example/target"}})
					default:
						t.Error("unexpected native move write/route", r.Method, r.URL.Path)
						w.WriteHeader(405)
					}
					return
				}
				switch r.URL.Path {
				case "/api/list":
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "list": []any{map[string]any{"fs_id": 3000, "path": "/one.txt", "server_filename": "one.txt", "isdir": 0, "size": len(data), "md5": digest}}})
				case "/api/filemetas":
					json.NewEncoder(w).Encode(map[string]any{"errno": 0, "info": []any{map[string]any{"fs_id": 3000, "path": "/one.txt", "size": len(data), "dlink": "https://d.pcs.baidu.com/target"}}})
				default:
					t.Error("unexpected native move write/route", r.Method, r.URL.Path)
					w.WriteHeader(405)
				}
			}))
			defer server.Close()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			tr.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr}
			if err := relayMoveTarget(context.Background(), client, a, d); err != nil || reads != 1 {
				t.Fatal("native backup not fully read", err, reads)
			}
			d.Job.SHA256 = strings.Repeat("0", 64)
			if err := relayMoveTarget(context.Background(), client, a, d); err == nil {
				t.Fatal("corrupt backup accepted")
			}
		})
	}
}
