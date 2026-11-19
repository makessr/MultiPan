package main

import (
	"errors"
	"multipan/internal/transfer"
	"testing"
)

func TestBatchNativeFolderCancelAndLock(t *testing.T) {
	inputs := []transfer.DownloadInput{{AccountID: "a", ParentID: "/", EntryID: "1", Name: "file"}}
	for _, dialogError := range []error{nil, errors.New("cancelled by user")} {
		s := &DesktopService{}
		r, e := s.batchWithSelection(inputs, func() (string, error) { return "", dialogError })
		if e != nil || !r.Cancelled || s.downloadCancel != nil {
			t.Fatal("folder cancellation mishandled", e)
		}
	}
	s := &DesktopService{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		r, e := s.batchWithSelection(inputs, func() (string, error) { close(entered); <-release; return "C:\\unused", nil })
		if !r.Cancelled && e == nil {
			e = errors.New("cancelled folder selection started download")
		}
		done <- e
	}()
	<-entered
	if _, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { t.Error("parallel save dialog"); return "", nil }); e == nil {
		t.Fatal("parallel single accepted")
	}
	if _, e := s.batchWithSelection(inputs, func() (string, error) { t.Error("parallel folder dialog"); return "", nil }); e == nil {
		t.Fatal("parallel batch accepted")
	}
	s.CancelDownload()
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if s.downloadCancel != nil {
		t.Fatal("download lock leaked")
	}
}
