package desktop

import (
	"errors"
	"multipan/internal/transfer"
	"testing"
)

func TestFolderNativeCancelAndMutex(t *testing.T) {
	for _, dialogError := range []error{nil, errors.New("cancelled by user")} {
		s := &DesktopService{}
		result, e := s.folderWithSelection(transfer.DownloadInput{}, func() (string, error) { return "", dialogError })
		if e != nil || !result.Cancelled || s.downloadCancel != nil {
			t.Fatal("folder cancellation mishandled", e)
		}
	}
	s := &DesktopService{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		r, e := s.folderWithSelection(transfer.DownloadInput{}, func() (string, error) { close(entered); <-release; return "C:\\unused", nil })
		if !r.Cancelled && e == nil {
			e = errors.New("started cancelled folder")
		}
		done <- e
	}()
	<-entered
	if _, e := s.downloadWithSelection(transfer.DownloadInput{}, func() (string, error) { t.Error("parallel dialog"); return "", nil }); e == nil {
		t.Fatal("single overlap")
	}
	if _, e := s.batchWithSelection([]transfer.DownloadInput{{AccountID: "a", EntryID: "1", Name: "file"}}, func() (string, error) { t.Error("batch overlap"); return "", nil }); e == nil {
		t.Fatal("batch overlap")
	}
	if _, e := s.RetryCloudFolder(transfer.DownloadInput{}, "stale"); e == nil {
		t.Fatal("folder retry overlap")
	}
	s.CancelDownload()
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestFolderRetryInvalidSessionReleasesDownloadLock(t *testing.T) {
	s := &DesktopService{manager: &transfer.Manager{}}
	if _, e := s.RetryCloudFolder(transfer.DownloadInput{}, "stale"); e == nil {
		t.Fatal("missing retry accepted")
	}
	if s.downloadCancel != nil {
		t.Fatal("download lock leaked")
	}
	r, e := s.folderWithSelection(transfer.DownloadInput{}, func() (string, error) { return "", nil })
	if e != nil || !r.Cancelled {
		t.Fatal("download locked after failed retry", e)
	}
}
