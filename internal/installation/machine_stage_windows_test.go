package installation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProtectedMachineInstallLifecycle(t *testing.T) {
	s := scopedFixture(t)
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		t.Fatal(err)
	}
	_, sid := fixtureSecurityPolicy(t)
	if err = os.Mkdir(filepath.Dir(layout.Directory), 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{s.root, filepath.Dir(layout.Directory)} {
		setFixtureDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	}
	old, next := fixture("0.98.0"), fixture("0.99.0")
	self := []byte("MZ-synthetic")
	app := filepath.Join(layout.Directory, App)
	if err = s.installProtectedMachine(old, self, Options{}); err != nil {
		t.Fatal("first install", err)
	}
	check := func(b Bundle) {
		t.Helper()
		requireFile(t, app, b.Files[App])
		marker, err := readMarkerForScope(layout.Directory, ScopeMachine)
		if err != nil || marker.Version != b.Version {
			t.Fatal(marker, err)
		}
		p, _ := fixtureSecurityPolicy(t)
		if _, err = p.node(app, false, true, false, machineMutationMask); err != nil {
			t.Fatal("installed file security", err)
		}
	}
	check(old)
	unknown := filepath.Join(layout.Directory, "keep.txt")
	if err = os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("pinned-root-and-tampered-stage", func(t *testing.T) {
		err := s.installProtectedMachine(next, self, Options{BeforeCommit: func() error {
			if moveWithoutReplace(layout.Directory, layout.Directory+".saved") == nil {
				t.Fatal("pinned root was moved")
			}
			stages, err := filepath.Glob(filepath.Join(layout.Directory, ".multipan-stage-*"))
			if err != nil || len(stages) != 1 {
				t.Fatal(stages, err)
			}
			return os.WriteFile(filepath.Join(stages[0], App), []byte("MZ-altered"), 0600)
		}})
		if err == nil {
			t.Fatal("tampered stage committed")
		}
		check(old)
	})
	t.Run("mid-commit-rollback", func(t *testing.T) {
		injected := errors.New("synthetic commit failure")
		err := s.installProtectedMachine(next, self, Options{beforeProtectedMove: func(from, to string) error {
			if to == filepath.Join(layout.Directory, PayloadNames[1]) {
				return injected
			}
			return nil
		}})
		if !errors.Is(err, injected) {
			t.Fatal(err)
		}
		check(old)
	})
	t.Run("held-source-and-occupied-destination", func(t *testing.T) {
		var conflict string
		err := s.installProtectedMachine(next, self, Options{beforeProtectedMove: func(from, to string) error {
			if filepath.Dir(from) != layout.Directory || filepath.Base(from) != PayloadNames[1] {
				return nil
			}
			if err := moveWithoutReplace(from, from+".outside"); err == nil {
				t.Fatal("held original was renamed by a separate handle")
			}
			if writer, err := os.OpenFile(from, os.O_WRONLY|os.O_APPEND, 0); err == nil {
				_ = writer.Close()
				t.Fatal("held original allowed a writer")
			}
			conflict = to
			return os.WriteFile(to, []byte("foreign"), 0600)
		}})
		if err == nil || conflict == "" {
			t.Fatal("occupied destination was accepted", err)
		}
		check(old)
		requireFile(t, conflict, []byte("foreign"))
	})
	t.Run("protected-upgrade-and-repair", func(t *testing.T) {
		if err := s.installProtectedMachine(next, self, Options{}); err != nil {
			t.Fatal(err)
		}
		check(next)
		missing := filepath.Join(layout.Directory, PayloadNames[1])
		if err := os.Remove(missing); err != nil {
			t.Fatal(err)
		}
		if err := s.installProtectedMachine(next, self, Options{}); err != nil {
			t.Fatal(err)
		}
		requireFile(t, missing, next.Files[PayloadNames[1]])
		requireFile(t, unknown, []byte("keep"))
	})
	if err = s.Uninstall(ScopeMachine); err != nil {
		t.Fatal(err)
	}
	requireFile(t, unknown, []byte("keep"))
}

func TestProtectedMoveDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "new"), filepath.Join(dir, "existing")
	for path, data := range map[string]string{from: "new", to: "existing"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := moveWithoutReplace(from, to); !errors.Is(err, windows.ERROR_ALREADY_EXISTS) && !errors.Is(err, windows.ERROR_FILE_EXISTS) {
		t.Fatal("expected destination conflict", err)
	}
	requireFile(t, from, []byte("new"))
	requireFile(t, to, []byte("existing"))
}
