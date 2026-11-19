package main

import (
	"strings"
	"testing"
)

func TestReadLoginResultSkipsReady(t *testing.T) {
	r, e := readLoginResult(strings.NewReader("{\"ready\":true}\n{\"loadingError\":\"network temporarily unavailable\"}\n{\"pageLoaded\":true}\n{\"cookie\":\"phpdisk_info=synthetic\",\"userAgent\":\"test\"}\n"))
	if e != nil || r.Cookie != "phpdisk_info=synthetic" {
		t.Fatalf("invalid result: %v", e)
	}
}
func TestReadLoginResultPreservesStartupError(t *testing.T) {
	r, e := readLoginResult(strings.NewReader("{\"error\":\"startup failed\"}\n"))
	if e != nil || r.Error != "startup failed" {
		t.Fatal("startup error lost")
	}
}
