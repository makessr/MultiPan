//go:build linux || darwin

package appguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixProfileLockRejectsConcurrentUseAndLinks(t *testing.T) {
	dir := t.TempDir()
	release, err := LockProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if releaseSecond, err := LockProfile(dir); !errors.Is(err, ErrBusy) {
		if releaseSecond != nil {
			releaseSecond()
		}
		release()
		t.Fatalf("second profile lock: %v", err)
	}
	release()
	lock := filepath.Join(dir, "MultiPan", ".multipan-profile.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "unexpected"), lock); err != nil {
		t.Fatal(err)
	}
	if releaseLinked, err := LockProfile(dir); err == nil {
		releaseLinked()
		t.Fatal("followed a profile lock symlink")
	}
}
