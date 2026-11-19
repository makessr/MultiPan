package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBatchDownloadsIsolationAndCancellation(t *testing.T) {
	for _, mode := range []string{"mixed", "cancel", "already-cancelled", "names"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			inputs := []DownloadInput{{AccountID: "a", ParentID: "/", EntryID: "1", Name: "one.txt"}, {AccountID: "a", ParentID: "/", EntryID: "2", Name: "two.txt"}, {AccountID: "a", ParentID: "/", EntryID: "3", Name: "three.txt"}}
			if mode == "names" {
				inputs[0].Name = "x?.txt"
				inputs[1].Name = "x*.txt"
				inputs[2].Name = "X_.TXT"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "already-cancelled" {
				cancel()
			}
			var calls []string
			if mode == "mixed" {
				os.WriteFile(filepath.Join(dir, "one.txt"), []byte("existing"), 0600)
			}
			result, e := RunDownloadBatch(ctx, inputs, dir, func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
				calls = append(calls, in.EntryID)
				if mode == "cancel" && in.EntryID == "2" {
					cancel()
					return DownloadResult{}, ctx.Err()
				}
				if mode == "mixed" && in.EntryID == "2" {
					return DownloadResult{}, errors.New("simulated failure")
				}
				file, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return DownloadResult{}, err
				}
				file.Write([]byte("abc"))
				file.Close()
				p(DownloadProgress{Received: 3, Total: 3})
				return DownloadResult{Path: dest, Bytes: 3, Verification: "verified"}, nil
			}, func(event BatchDownloadEvent) {
				if event.Index < 1 || event.Index > 3 || event.Count != 3 {
					t.Error("invalid progress position")
				}
			})
			if e != nil || len(result.Items) != 3 {
				t.Fatal("missing results", e)
			}
			var statuses []string
			for _, item := range result.Items {
				statuses = append(statuses, item.Status)
			}
			want := map[string][]string{"mixed": {"failed", "failed", "saved"}, "cancel": {"saved", "cancelled", "skipped"}, "already-cancelled": {"skipped", "skipped", "skipped"}, "names": {"saved", "skipped", "skipped"}}[mode]
			if !reflect.DeepEqual(statuses, want) {
				t.Fatal(statuses, want)
			}
			if (mode == "cancel" || mode == "already-cancelled") && !result.Cancelled {
				t.Fatal("cancel not reported")
			}
			if mode == "cancel" && len(calls) != 2 {
				t.Fatal("started after cancellation")
			}
			if mode == "already-cancelled" && len(calls) != 0 {
				t.Fatal("started cancelled batch")
			}
			if mode == "names" && len(calls) != 1 {
				t.Fatal("conflicting local names requested")
			}
			if mode == "mixed" {
				b, _ := os.ReadFile(filepath.Join(dir, "one.txt"))
				if string(b) != "existing" {
					t.Fatal("existing file overwritten")
				}
			}
		})
	}
}
func TestBatchInputValidation(t *testing.T) {
	base := DownloadInput{AccountID: "a", ParentID: "/", EntryID: "1", Name: "a"}
	for _, mode := range []string{"empty", "limit", "duplicate", "account", "parent", "no-name"} {
		t.Run(mode, func(t *testing.T) {
			inputs := []DownloadInput{base}
			switch mode {
			case "empty":
				inputs = nil
			case "limit":
				inputs = make([]DownloadInput, 201)
			case "duplicate":
				inputs = append(inputs, base)
			case "account":
				next := base
				next.EntryID = "2"
				next.AccountID = "b"
				inputs = append(inputs, next)
			case "parent":
				next := base
				next.EntryID = "2"
				next.ParentID = "/other"
				inputs = append(inputs, next)
			case "no-name":
				inputs[0].Name = ""
			}
			if ValidateDownloadBatch(inputs) == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
	if _, e := RunDownloadBatch(context.Background(), []DownloadInput{base}, filepath.Join(t.TempDir(), "missing"), nil, nil); e == nil {
		t.Fatal("invalid directory accepted")
	}
}
