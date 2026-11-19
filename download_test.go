package main

import (
	"errors"
	"fmt"
	"multipan/internal/transfer"
	"testing"
)

func TestDownloadNativeSaveCancellation(t *testing.T) {
	// Wails 3.1.0's internal CFD maps HRESULT 0x800704c7 to this
	// unexported sentinel; application.SaveFile returns it to the caller.
	for _, err := range []error{errors.New("cancelled by user"), fmt.Errorf("dialog: %w", errors.New("cancelled by user")), nil} {
		s := &DesktopService{}
		result, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { return "", err })
		if e != nil || !result.Cancelled || s.downloadCancel != nil {
			t.Fatalf("native cancel misreported: %+v %v", result, e)
		}
	}
	s := &DesktopService{}
	if _, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { return "", errors.New("dialog access denied") }); e == nil {
		t.Fatal("real dialog failure hidden")
	}
}

func TestDownloadSelectionLockAndCancelBeforeTransfer(t *testing.T) {
	s := &DesktopService{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { close(entered); <-release; return "C:\\fixture\\unused.txt", nil })
		if e == nil && !result.Cancelled {
			e = errors.New("transfer started after cancellation")
		}
		done <- e
	}()
	<-entered
	if _, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { t.Error("second save chooser opened"); return "", nil }); e == nil {
		t.Fatal("parallel download accepted")
	}
	s.CancelDownload()
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if s.downloadCancel != nil {
		t.Fatal("download lock not released")
	}
}
