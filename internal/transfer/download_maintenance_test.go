package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearDownloadPartialsKeepsFinalFileAndSkipsActive(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	id := strings.Repeat("a", 32)
	dest := filepath.Join(t.TempDir(), "final.txt")
	part := dest + ".multipan-" + id + ".part"
	for path, data := range map[string]string{dest: "formal", part: "partial", part + ".resume": "checkpoint"} {
		if e := os.WriteFile(path, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	m.downloads = []DownloadJob{{ID: id, Destination: dest, Status: "paused", Received: 7}}
	m.downloadActive[id] = func() {}
	result, e := m.ClearDownloadPartials([]string{id})
	if e != nil || result[0].Applied {
		t.Fatal("active cleanup accepted", e)
	}
	if _, e := os.Stat(part); e != nil {
		t.Fatal("active partial deleted")
	}
	delete(m.downloadActive, id)
	result, e = m.ClearDownloadPartials([]string{id, id})
	if e != nil || len(result) != 1 || !result[0].Applied {
		t.Fatalf("cleanup %+v %v", result, e)
	}
	for _, p := range []string{part, part + ".resume"} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("partial remains", p)
		}
	}
	if b, e := os.ReadFile(dest); e != nil || string(b) != "formal" {
		t.Fatal("formal file touched")
	}
	if m.downloads[0].Received != 0 {
		t.Fatal("progress not reset")
	}
	again, e := m.ClearDownloadPartials([]string{id})
	if e != nil || !again[0].Applied {
		t.Fatal("cleanup not idempotent", e)
	}
}

func TestClearDownloadPartialsRejectsReplacedPaths(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	id := strings.Repeat("b", 32)
	dest := filepath.Join(t.TempDir(), "file.txt")
	part := dest + ".multipan-" + id + ".part"
	os.WriteFile(part, []byte("keep"), 0600)
	os.Mkdir(part+".resume", 0700)
	m.downloads = []DownloadJob{{ID: id, Destination: dest, Status: "failed"}, {ID: "../invalid", Destination: dest, Status: "paused"}, {ID: strings.Repeat("c", 32), Destination: dest, Status: "saved"}}
	results, e := m.ClearDownloadPartials([]string{id, "../invalid", strings.Repeat("c", 32)})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range results {
		if r.Applied {
			t.Fatal("unsafe or completed task cleaned")
		}
	}
	if b, _ := os.ReadFile(part); string(b) != "keep" {
		t.Fatal("partial deleted before all paths checked")
	}
}

func TestAllDownloadControlsIncludeHiddenAndMoreThanSelectedLimit(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.downloadActive["hold"] = func() {}
	for i := 0; i < 250; i++ {
		m.downloads = append(m.downloads, DownloadJob{ID: fmt.Sprintf("%032x", i+1), Status: "paused"})
	}
	m.downloads = append(m.downloads, DownloadJob{ID: "saved", Status: "saved"})
	results, e := m.ControlAllDownloads("resume")
	if e != nil || len(results) != 250 {
		t.Fatalf("all action bounded by selection %d %v", len(results), e)
	}
	for _, j := range m.downloads[:250] {
		if j.Status != "queued" {
			t.Fatal("global resume omitted task")
		}
	}
	if m.downloads[250].Status != "saved" {
		t.Fatal("saved task changed")
	}
	results, e = m.ControlAllDownloads("pause")
	if e != nil || len(results) != 250 {
		t.Fatal("global pause omitted task", e)
	}
	if _, e = m.ControlAllDownloads("remove"); e == nil {
		t.Fatal("unsupported global destructive action")
	}
}

func TestDownloadReportSelectsWhitelistAndPreservesRequestedOrder(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.downloads = []DownloadJob{{ID: "one", Input: DownloadInput{Name: "one.txt", AccountID: "never-account-id"}, Destination: "C:/Downloads/one.txt", Scope: "never-scope", Kind: "quark", Status: "saved", Result: DownloadResult{Details: "content verified"}}, {ID: "two", Input: DownloadInput{Name: "two.txt"}, Kind: "baidu", Status: "failed", Message: "HTTP=403"}}
	report, e := m.DownloadReport([]string{"two", "two", "missing", "one"}, "0.48.0")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(report, "never-") || strings.Count(report, "文件：two.txt") != 1 || strings.Index(report, "文件：two.txt") > strings.Index(report, "文件：one.txt") {
		t.Fatal("wrong scope or duplicate report")
	}
	if !strings.Contains(report, "HTTP=403") || !strings.Contains(report, "夸克网盘") {
		t.Fatal("missing diagnostic")
	}
	if _, e = m.DownloadReport([]string{"missing"}, "0.48.0"); e == nil {
		t.Fatal("empty report accepted")
	}
}
