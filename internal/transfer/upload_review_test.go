package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUploadPlanOnlyReadsRemoteAndDoesNotEnqueue(t *testing.T) {
	f := source(t)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "PROPFIND" {
			t.Errorf("review attempted mutation %s", r.Method)
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(207)
		fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>20</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, url.PathEscape(f.Name))
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a, e := m.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Folder: "/", Secret: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	review, e := m.CheckUploadPlan(context.Background(), []File{f}, []Target{{a.ID, "/"}})
	if e != nil || len(review.Items) != 1 || review.Items[0].Status != "conflict" || requests != 1 {
		t.Fatalf("review %+v requests%d %v", review, requests, e)
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("read-only review enqueued work")
	}
}

func TestUploadReviewNestedConflictsReuseMissingAndUncertainty(t *testing.T) {
	a := Account{ID: "fixture", Kind: "webdav", Folder: "/", Secret: "fixture"}
	files := []File{
		{Name: "root", RelativePath: "root", Directory: true},
		{Name: "same.txt", RelativePath: "root/same.txt"},
		{Name: "new", RelativePath: "root/new", Directory: true},
		{Name: "ok.txt", RelativePath: "root/new/ok.txt"},
		{Name: "blocked", RelativePath: "blocked", Directory: true},
		{Name: "child.txt", RelativePath: "blocked/child.txt"},
		{Name: "dup", RelativePath: "dup", Directory: true},
		{Name: "lost.txt", RelativePath: "unreadable/lost.txt"},
		{Name: "Case", RelativePath: "Case", Directory: true},
	}
	calls := map[string]int{}
	listing := func(_ context.Context, _ Account, parent string) (FilePage, error) {
		calls[parent]++
		switch parent {
		case "/":
			return FilePage{Entries: []CloudEntry{{ID: "r", Name: "root", Folder: true}, {ID: "b", Name: "blocked"}, {ID: "d1", Name: "dup", Folder: true}, {ID: "d2", Name: "dup", Folder: true}, {ID: "u", Name: "unreadable", Folder: true}, {ID: "c", Name: "case", Folder: true}}}, nil
		case "r":
			return FilePage{Entries: []CloudEntry{{ID: "s", Name: "SAME.txt"}}}, nil
		case "u":
			return FilePage{}, errors.New("fixture listing error")
		default:
			t.Errorf("unexpected listing %s", parent)
			return FilePage{}, nil
		}
	}
	report := reviewUploadPlan(context.Background(), files, []Target{{a.ID, "/"}}, []Account{a}, listing)
	want := []string{"reuse", "conflict", "ready", "ready", "conflict", "conflict", "conflict", "unknown", "conflict"}
	for i, row := range report.Items {
		if row.Status != want[i] || row.FileIndex != i || row.TargetIndex != 0 {
			t.Fatalf("row %d: %+v want%s", i, row, want[i])
		}
	}
	if calls["/"] != 1 || calls["r"] != 1 || calls["u"] != 1 || len(calls) != 3 {
		t.Fatal("lists not cached or missing directory was queried", calls)
	}
	if report.Stamp == "" || report.Stamp != uploadPlanStamp(files, []Target{{a.ID, "/"}}, []Account{a}) {
		t.Fatal("unstable plan stamp")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	report = reviewUploadPlan(canceled, files, []Target{{a.ID, "/"}}, []Account{a}, func(context.Context, Account, string) (FilePage, error) {
		t.Fatal("canceled review made request")
		return FilePage{}, nil
	})
	for _, row := range report.Items {
		if row.Status != "unknown" {
			t.Fatal("cancel interpreted as no conflicts")
		}
	}
}

func TestReviewedUploadSkipIsPerTargetAndBindsSource(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a := Account{ID: "a", Name: "a", Kind: "webdav", Endpoint: "https://fixture.invalid", Secret: "fixture", Folder: "/"}
	b := a
	b.ID = "b"
	b.Name = "b"
	m.accounts = []Account{a, b}
	m.tasks = []Task{{ID: "hold-a", AccountID: a.ID, Status: "running"}, {ID: "hold-b", AccountID: b.ID, Status: "running"}}
	m.active["hold-a"] = func() {}
	m.active["hold-b"] = func() {}
	f := source(t)
	files := []File{f}
	targets := []Target{{a.ID, "/"}, {b.ID, "/"}}
	stamp := uploadPlanStamp(files, targets, m.accounts)
	if _, e = m.EnqueueReviewedUploads(files, targets, nil, ""); e == nil {
		t.Fatal("missing review accepted")
	}
	if _, e = m.EnqueueReviewedUploads(files, targets, []UploadSkip{{FileIndex: 2}}, stamp); e == nil {
		t.Fatal("invalid skip accepted")
	}
	changed := append([]Account(nil), m.accounts...)
	changed[0].Secret = "changed-fixture"
	m.accounts = changed
	if _, e = m.EnqueueReviewedUploads(files, targets, nil, stamp); e == nil {
		t.Fatal("account change accepted stale review")
	}
	m.accounts = []Account{a, b}
	if n, e := m.EnqueueReviewedUploads(files, targets, []UploadSkip{{FileIndex: 0, TargetIndex: 0}}, stamp); e != nil || n != 1 {
		t.Fatalf("enqueue %d %v", n, e)
	}
	jobs := m.Snapshot().Tasks
	if len(jobs) != 3 || jobs[2].AccountID != b.ID || jobs[2].BatchID == "" {
		t.Fatal("skipped wrong target or lost batch", jobs)
	}
	if _, e = m.EnqueueReviewedUploads(files, targets, []UploadSkip{{0, 0}, {0, 1}}, stamp); e == nil {
		t.Fatal("empty reviewed plan accepted")
	}
	if e = os.WriteFile(f.Path, []byte("changed-source"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.EnqueueReviewedUploads(files, targets, nil, stamp); e == nil {
		t.Fatal("modified local source accepted")
	}
}

func TestRetryFailedUploadBatchPersistsBeforeDispatchAndLeavesOtherStates(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(profile, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.accounts = []Account{{ID: "a", Name: "a", Kind: "webdav", Endpoint: "https://fixture.invalid", Folder: "/", Secret: "fixture"}}
	m.tasks = []Task{{ID: "hold", AccountID: "a", Status: "running"}, {ID: "failed", AccountID: "a", Status: "failed", Sent: 4, File: File{Name: "file.txt", RelativePath: "root/file.txt"}}, {ID: "paused", AccountID: "a", Status: "paused"}, {ID: "success", AccountID: "a", Status: "success"}, {ID: "gone", AccountID: "missing", Status: "failed"}}
	m.active["hold"] = func() {}
	results, e := m.RetryFailedUploads([]string{"failed", "failed", "paused", "success", "gone", "missing"})
	if e != nil || len(results) != 5 || !results[0].Applied || results[0].Name != "root/file.txt" {
		t.Fatalf("%+v %v", results, e)
	}
	for _, r := range results[1:] {
		if r.Applied {
			t.Fatal("non-failed or unavailable task submitted")
		}
	}
	if m.tasks[1].Status != "queued" || m.tasks[1].Sent != 0 || m.tasks[2].Status != "paused" || m.tasks[3].Status != "success" {
		t.Fatal("wrong state transition")
	}
	disk, e := readState(profile)
	if e != nil || disk.Tasks[1].Status != "queued" {
		t.Fatal("retry not persisted", e)
	}
	m.tasks[1].Status = "failed"
	m.tasks[1].Sent = 4
	blocked := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocked, []byte("fixture"), 0600)
	m.path = filepath.Join(blocked, "state.enc")
	if _, e = m.RetryFailedUploads([]string{"failed"}); e == nil || m.tasks[1].Status != "failed" || m.tasks[1].Sent != 4 || len(m.active) != 1 {
		t.Fatal("failed save did not roll back")
	}
	if _, e = m.RetryFailedUploads(make([]string, 1001)); e == nil {
		t.Fatal("unbounded batch accepted")
	}
}
