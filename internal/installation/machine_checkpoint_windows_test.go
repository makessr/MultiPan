package installation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMachineCheckpointCrashChild(t *testing.T) {
	root := os.Getenv("MULTIPAN_TEST_MACHINE_CRASH_ROOT")
	if root == "" {
		return
	}
	s, err := OpenScopedSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.installProtectedMachine(fixture("0.99.0"), []byte("MZ-new"), Options{
		beforeProtectedMove: func(from, to string) error {
			atSecondFile := filepath.Base(from) == PayloadNames[1]
			backupPhase := filepath.Dir(from) == layout.Directory
			if atSecondFile && (backupPhase == (os.Getenv("MULTIPAN_TEST_MACHINE_CRASH_PHASE") == "backup")) {
				os.Exit(23) // The kernel closes handles, no Go cleanup runs.
			}
			return nil
		},
	})
	t.Fatal("did not reach the requested interruption")
}

func TestMachineCheckpointInterruptedProcessProtectsBothVersions(t *testing.T) {
	for _, phase := range []string{"backup", "commit"} {
		t.Run(phase, func(t *testing.T) {
			s := scopedFixture(t)
			l, err := s.Layout(ScopeMachine)
			if err != nil {
				t.Fatal(err)
			}
			_, sid := fixtureSecurityPolicy(t)
			if err = os.Mkdir(filepath.Dir(l.Directory), 0700); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{s.root, filepath.Dir(l.Directory)} {
				setFixtureDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
			}
			old, next := fixture("0.98.0"), fixture("0.99.0")
			if err = s.installProtectedMachine(old, []byte("MZ-old"), Options{}); err != nil {
				t.Fatal(err)
			}
			checkpoint := filepath.Join(l.Directory, machineCheckpointName)
			if _, err = os.Lstat(checkpoint); !os.IsNotExist(err) {
				t.Fatal("completed install left a checkpoint", err)
			}
			unknown := filepath.Join(l.Directory, "leave-this.txt")
			if err = os.WriteFile(unknown, []byte("unmanaged"), 0600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMachineCheckpointCrashChild$")
			cmd.Env = append(os.Environ(), "MULTIPAN_TEST_MACHINE_CRASH_ROOT="+s.root, "MULTIPAN_TEST_MACHINE_CRASH_PHASE="+phase)
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 23 {
				t.Fatalf("crash witness: %v %s", err, output)
			}
			raw, err := os.ReadFile(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			var record machineCheckpoint
			if err := json.Unmarshal(raw, &record); err != nil || record.Format != 1 ||
				record.Previous.Version != old.Version || record.Staged[App] != Digest(next.Files[App]) ||
				filepath.Base(record.Backup) != record.Backup || filepath.Base(record.Stage) != record.Stage ||
				len(record.Original) != len(PayloadNames)+2 {
				t.Fatalf("invalid crash checkpoint: %+v %v", record, err)
			}
			review, err := s.ReviewMachineCheckpoint()
			if err != nil || !review.Present || review.PreviousVersion != old.Version || len(review.Files) != len(PayloadNames)+2 {
				t.Fatalf("interrupted transaction review unavailable: %+v %v", review, err)
			}
			wantApp := MachineCheckpointFile{Name: App, OriginalPresent: true, Installed: "missing", Staging: "staged", Backup: "previous"}
			if phase == "commit" {
				wantApp.Installed, wantApp.Staging = "staged", "missing"
			}
			if review.Files[0] != wantApp {
				t.Fatalf("wrong app placement: %+v", review.Files[0])
			}
			backup := filepath.Join(l.Directory, record.Backup, App)
			if err = s.installProtectedMachine(next, []byte("MZ-new"), Options{}); !errors.Is(err, errMachineInterrupted) {
				t.Fatal("retry bypassed checkpoint", err)
			}
			if err = s.Uninstall(ScopeMachine); !errors.Is(err, errMachineInterrupted) {
				t.Fatal("uninstall bypassed checkpoint", err)
			}
			requireFile(t, checkpoint, raw)
			requireFile(t, backup, old.Files[App])
			requireFile(t, unknown, []byte("unmanaged"))
			if phase == "backup" {
				if _, err = os.Lstat(filepath.Join(l.Directory, App)); !os.IsNotExist(err) {
					t.Fatal("retry filled interrupted location", err)
				}
				requireFile(t, filepath.Join(l.Directory, record.Stage, App), next.Files[App])
			} else {
				requireFile(t, filepath.Join(l.Directory, App), next.Files[App])
				requireFile(t, filepath.Join(l.Directory, record.Stage, PayloadNames[1]), next.Files[PayloadNames[1]])
			}
		})
	}
}

func TestMachineCheckpointMalformedStillBlocks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, machineCheckpointName)
	partiallyWritten := []byte("{")
	if err := os.WriteFile(path, partiallyWritten, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkMachineInterrupted(root); !errors.Is(err, errMachineInterrupted) {
		t.Fatal(err)
	}
	requireFile(t, path, partiallyWritten)
}
