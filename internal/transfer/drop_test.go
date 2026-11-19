package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDroppedFilesMetadataAndIgnoredInputs(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "中文 空格 & 文件.txt")
	second := filepath.Join(root, "空文件.zip")
	if e := os.WriteFile(first, []byte("original local bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(second, nil, 0600); e != nil {
		t.Fatal(e)
	}
	result := InspectDroppedFiles([]string{first, second, first, strings.ToUpper(first), root, filepath.Join(root, "missing.txt"), "relative.txt"})
	if len(result.Files) != 2 || result.Duplicates != 2 || result.Directories != 1 || result.Unreadable != 2 {
		t.Fatalf("unexpected selection: %+v", result)
	}
	if result.Files[0].Name != "中文 空格 & 文件.txt" || result.Files[0].Size != int64(len("original local bytes")) || !filepath.IsAbs(result.Files[0].Path) || result.Files[1].Size != 0 {
		t.Fatal("incorrect source metadata")
	}
	data, e := os.ReadFile(first)
	if e != nil || string(data) != "original local bytes" {
		t.Fatal("source file changed")
	}
	if empty := InspectDroppedFiles(nil); empty.Files == nil || len(empty.Files) != 0 {
		t.Fatal("empty drop must remain a list")
	}
}
