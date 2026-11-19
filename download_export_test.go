package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportDownloadReportDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 report.txt")
	choose := func() (string, error) { return path, nil }
	saved, e := saveDownloadReport("fixture report\r\n", choose)
	if e != nil || saved != path {
		t.Fatal(saved, e)
	}
	if _, e = saveDownloadReport("replace", choose); e == nil {
		t.Fatal("existing report overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != "fixture report\r\n" {
		t.Fatal("report changed")
	}
}

func TestExportDownloadReportCancellationAndInvalidNames(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("cancelled by user")} {
		if saved, e := saveDownloadReport("fixture", func() (string, error) { return "", cause }); e != nil || saved != "" {
			t.Fatal("cancel not handled", e)
		}
	}
	for _, p := range []string{"relative.txt", filepath.Join(t.TempDir(), "report.exe")} {
		if _, e := saveDownloadReport("fixture", func() (string, error) { return p, nil }); e == nil {
			t.Fatal("invalid export path")
		}
	}
	called := false
	if _, e := saveDownloadReport("", func() (string, error) { called = true; return "", nil }); e == nil || called {
		t.Fatal("empty report opened chooser")
	}
}
