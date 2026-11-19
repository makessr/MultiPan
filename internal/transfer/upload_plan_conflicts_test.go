package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnqueueRejectsCollidingRelativePathsBeforeCreatingTasks(t *testing.T) {
	u := &controlledUploader{calls: map[string]int{}, gate: make(chan struct{}), started: make(chan string, 10)}
	m, e := NewManager(filepath.Join(t.TempDir(), "state.enc"), u)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	a, e := m.SaveAccount(AccountInput{Name: "fixture", Kind: "webdav", Endpoint: "https://fixture.invalid", Folder: "/", Secret: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	first, second := source(t), source(t)
	if _, e = m.Enqueue([]File{first, second}, []Target{{a.ID, "/"}}); e == nil || !strings.Contains(e.Error(), "相同上传路径") {
		t.Fatalf("collision not diagnosed: %v", e)
	}
	dirs := []File{}
	for i := 0; i < 2; i++ {
		dir := filepath.Join(t.TempDir(), "same")
		os.Mkdir(dir, 0700)
		tree, e := ScanUploadTree(dir)
		if e != nil {
			t.Fatal(e)
		}
		dirs = append(dirs, tree...)
	}
	if _, e = m.Enqueue(dirs, []Target{{a.ID, "/"}}); e == nil || !strings.Contains(e.Error(), "相同上传路径") {
		t.Fatalf("roots merged silently: %v", e)
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("part of invalid plan enqueued")
	}
}
