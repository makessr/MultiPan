package installation

import (
	"errors"
	"multipan/internal/appguard"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerRunLeaseHandshake(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "program")
	if err := Install(dir, fixture("0.98.0"), []byte("MZ-old-setup"), Options{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, Marker))
	release, err := appguard.AcquireRun(filepath.Join(dir, App))
	if err != nil {
		t.Fatal(err)
	}
	if err = Install(dir, fixture("0.99.0"), []byte("MZ-new-setup"), Options{}); err == nil {
		t.Fatal("running program upgraded")
	}
	if err = Uninstall(dir); err == nil {
		t.Fatal("running program uninstalled")
	}
	requireFile(t, filepath.Join(dir, Marker), before)
	requireFile(t, filepath.Join(dir, App), fixture("0.98.0").Files[App])
	release()
	checked := false
	err = Install(dir, fixture("0.99.0"), []byte("MZ-new-setup"), Options{BeforeCommit: func() error {
		checked = true
		if run, err := appguard.AcquireRun(filepath.Join(dir, App)); !errors.Is(err, appguard.ErrBusy) {
			if run != nil {
				run()
			}
			return errors.New("startup during staging was allowed")
		}
		return nil
	}, Rename: func(a, b string) error {
		if err := os.Rename(a, b); err != nil {
			return err
		}
		if b == filepath.Join(dir, App) {
			if run, err := appguard.AcquireRun(b); !errors.Is(err, appguard.ErrBusy) {
				if run != nil {
					run()
				}
				return errors.New("new executable startup during commit was allowed")
			}
		}
		return nil
	}})
	if err != nil || !checked {
		t.Fatal("install handshake failed", checked, err)
	}
	run, err := appguard.AcquireRun(filepath.Join(dir, App))
	if err != nil {
		t.Fatal("successful install leaked maintenance marker", err)
	}
	run()
	if err = Uninstall(dir); err != nil {
		t.Fatal(err)
	}
}
