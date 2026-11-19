package main

import (
	"strings"
	"testing"
)

func TestDownloadReportCopy(t *testing.T) {
	calls := 0
	put := func(s string) bool { calls++; return s == "报告\nfixture" }
	if e := copyDownloadReport("报告\nfixture", put); e != nil || calls != 1 {
		t.Fatal("copy failed", e)
	}
	for _, s := range []string{" ", strings.Repeat("x", (2<<20)+1), "a\x00b"} {
		if e := copyDownloadReport(s, put); e == nil {
			t.Fatal("invalid report accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid report touched clipboard")
	}
	if e := copyDownloadReport("fixture", func(string) bool { return false }); e == nil {
		t.Fatal("clipboard failure hidden")
	}
}
