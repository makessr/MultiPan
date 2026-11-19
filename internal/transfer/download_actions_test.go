package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBatchDownloadsPausesBeforeDispatchAndRemovesOnlyInactiveRecords(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	canceled := false
	m.downloadActive["running"] = func() { canceled = true }
	m.downloads = []DownloadJob{{ID: "running", Status: "running"}, {ID: "queued", Status: "queued"}, {ID: "failed", Status: "failed"}, {ID: "saved", Status: "saved"}}
	results, e := m.BatchDownloadAction([]string{"running", "queued", "queued", "saved", "missing"}, "pause")
	if e != nil || len(results) != 4 {
		t.Fatalf("%+v %v", results, e)
	}
	if !canceled || m.downloads[1].Status != "paused" || results[2].Applied || results[3].Applied {
		t.Fatal("pause state/count mismatch")
	}
	file := filepath.Join(t.TempDir(), "saved.txt")
	os.WriteFile(file, []byte("keep"), 0600)
	m.downloads[3].Result.Path = file
	results, e = m.BatchDownloadAction([]string{"running", "failed", "saved"}, "remove")
	if e != nil || results[0].Applied || !results[1].Applied || !results[2].Applied {
		t.Fatalf("remove %+v %v", results, e)
	}
	if len(m.downloads) != 2 {
		t.Fatal("removed active or retained failed records")
	}
	if b, e := os.ReadFile(file); e != nil || string(b) != "keep" {
		t.Fatal("history clear touched file")
	}
}

func TestBatchDownloadPersistenceFailureDoesNotCancelOrChangeState(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocked, []byte("fixture"), 0600)
	m.path = filepath.Join(blocked, "state.enc")
	canceled := false
	m.downloadActive["running"] = func() { canceled = true }
	m.downloads = []DownloadJob{{ID: "running", Status: "running"}, {ID: "queued", Status: "queued"}, {ID: "failed", Status: "failed"}}
	if _, e = m.BatchDownloadAction([]string{"running", "queued"}, "pause"); e == nil {
		t.Fatal("expected disk failure")
	}
	if canceled || m.downloads[1].Status != "queued" {
		t.Fatal("pause applied before durable save")
	}
	if _, e = m.BatchDownloadAction([]string{"failed"}, "remove"); e == nil || len(m.downloads) != 3 {
		t.Fatal("remove not rolled back")
	}
	if _, e = m.BatchDownloadAction([]string{"failed"}, "resume"); e == nil || m.downloads[2].Status != "failed" {
		t.Fatal("resume not rolled back")
	}
}

func TestBatchDownloadResumeScopeAndCapacity(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.downloadActive["hold"] = func() {}
	m.downloads = []DownloadJob{{ID: "paused", Status: "paused"}, {ID: "failed", Status: "failed"}, {ID: "saved", Status: "saved"}}
	result, e := m.BatchDownloadAction([]string{"paused", "failed", "saved"}, "resume")
	if e != nil || !result[0].Applied || !result[1].Applied || result[2].Applied {
		t.Fatalf("%+v %v", result, e)
	}
	if m.downloads[0].Status != "queued" || m.downloads[1].Status != "queued" || m.downloads[2].Status != "saved" {
		t.Fatal("resume modified wrong states")
	}
	if _, e = m.BatchDownloadAction(make([]string, 201), "pause"); e == nil {
		t.Fatal("unbounded selection")
	}
	if _, e = m.BatchDownloadAction([]string{"saved"}, "unknown"); e == nil {
		t.Fatal("invalid action")
	}

}
