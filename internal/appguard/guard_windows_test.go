package appguard

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A separate process really owns the kernel handles. The file-only mode
// deliberately skips Local mutexes; it does not pretend to be another user.
func TestGuardHelperProcess(t *testing.T) {
	mode := os.Getenv("MULTIPAN_GUARD_TEST_MODE")
	if mode == "" {
		return
	}
	path := os.Getenv("MULTIPAN_GUARD_TEST_PATH")
	var release func()
	var err error
	switch mode {
	case "profile-file":
		release, err = lockProfileFile(path)
	case "run":
		release, err = AcquireRun(path)
	case "install":
		var lease *InstallationLease
		lease, err = AcquireInstallation(path)
		if err == nil {
			release = lease.Close
		}
	default:
		t.Fatal("invalid helper mode")
	}
	if err != nil {
		fmt.Println("ERROR:", err)
		t.Fatal(err)
	}
	defer release()
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func childGuard(t *testing.T, mode, path string) func(bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestGuardHelperProcess$")
	cmd.Env = append(os.Environ(), "MULTIPAN_GUARD_TEST_MODE="+mode, "MULTIPAN_GUARD_TEST_PATH="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	stop := func(crash bool) {
		once.Do(func() {
			if crash {
				_ = cmd.Process.Kill() // only this test's own bounded helper
			}
			_ = input.Close()
			err := cmd.Wait()
			cancel()
			if err != nil && !crash {
				t.Errorf("guard helper failed: %v", err)
			}
		})
	}
	t.Cleanup(func() { stop(false) })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		t.Fatalf("guard helper not ready: %q, %v", line, err)
	}
	return stop
}

func appFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "MultiPan.exe")
	if err := os.WriteFile(path, []byte("MZ-synthetic-executable"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileFileLockAcrossProcessesAndCrash(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprint("crash-", crash), func(t *testing.T) {
			root := t.TempDir()
			stop := childGuard(t, "profile-file", root)
			if release, err := LockProfile(root); !errors.Is(err, ErrBusy) {
				if release != nil {
					release()
				}
				t.Fatal("file-only owner was bypassed", err)
			}
			other, err := LockProfile(t.TempDir())
			if err != nil {
				t.Fatal("separate profile blocked", err)
			}
			other()
			stop(crash)
			release, err := LockProfile(root)
			if err != nil {
				t.Fatal("stale lock after process exit", err)
			}
			release()
			release()
			info, err := os.Stat(filepath.Join(root, "MultiPan", ".multipan-profile.lock"))
			if err != nil || info.Size() != 0 {
				t.Fatal("profile lock changed contents", err)
			}
		})
	}
}

func TestProfileLegacyLockCompatibilityAndCaseAliases(t *testing.T) {
	root := t.TempDir()
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\MultiPan-%x", sha256.Sum256([]byte(root))))
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		t.Fatal(err)
	}
	_, blocked := LockProfile(root)
	windows.CloseHandle(h)
	if !errors.Is(blocked, ErrBusy) {
		t.Fatal("legacy application lock ignored", blocked)
	}
	release, err := LockProfile(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if other, err := LockProfile(strings.ToUpper(root)); !errors.Is(err, ErrBusy) {
		if other != nil {
			other()
		}
		t.Fatal("case alias bypassed file lock", err)
	}
}

func TestProfileRefusesForeignFileHardLinksAndJunctions(t *testing.T) {
	for _, mode := range []string{"nonempty", "hard-link", "junction"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "MultiPan")
			if mode == "junction" {
				outside := t.TempDir()
				if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, outside).CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
				defer os.Remove(dir)
				if _, err := LockProfile(root); err == nil {
					t.Fatal("profile junction accepted")
				}
				if rows, err := os.ReadDir(outside); err != nil || len(rows) != 0 {
					t.Fatal("junction target touched", err)
				}
				return
			}
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, ".multipan-profile.lock")
			want := []byte("keep existing file")
			if mode == "hard-link" {
				want = nil
			}
			if err := os.WriteFile(file, want, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "hard-link" {
				if err := os.Link(file, filepath.Join(root, "linked-lock")); err != nil {
					t.Fatal(err)
				}
			}
			if release, err := LockProfile(root); err == nil {
				release()
				t.Fatal("invalid lock accepted", mode)
			}
			if got, err := os.ReadFile(file); err != nil || string(got) != string(want) {
				t.Fatal("invalid lock overwritten", err)
			}
		})
	}
}

func TestRunLeaseAllowsReadersButDeniesWriteDeleteAndInstallStartup(t *testing.T) {
	path := appFixture(t)
	stop := childGuard(t, "run", path)
	release, err := AcquireRun(path)
	if err != nil {
		t.Fatal("second reader blocked", err)
	}
	if err = os.Rename(path, path+".old"); err == nil {
		t.Fatal("leased executable renamed")
	}
	if err = os.Remove(path); err == nil {
		t.Fatal("leased executable deleted")
	}
	native, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(native, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		windows.CloseHandle(h)
		t.Fatal("leased executable writable")
	}
	lease, err := AcquireInstallation(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if run, err := AcquireRun(path); !errors.Is(err, ErrBusy) {
		if run != nil {
			run()
		}
		t.Fatal("new application bypassed maintenance", err)
	}
	lease.Close()
	release()
	release()
	stop(false)
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal("read lease leaked", err)
	}
}

func TestMaintenanceAcrossProcessesAliasesAndCrash(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprint("crash-", crash), func(t *testing.T) {
			path := appFixture(t)
			root := filepath.Dir(path)
			stop := childGuard(t, "install", root)
			if lease, err := AcquireInstallation(strings.ToUpper(root)); !errors.Is(err, ErrBusy) {
				if lease != nil {
					lease.Close()
				}
				t.Fatal("second installer bypassed marker", err)
			}
			if release, err := AcquireRun(path); !errors.Is(err, ErrBusy) {
				if release != nil {
					release()
				}
				t.Fatal("run bypassed installer", err)
			}
			stop(crash)
			lease, err := AcquireInstallation(root)
			if err != nil {
				t.Fatal("stale installer marker", err)
			}
			lease.Close()
			release, err := AcquireRun(path)
			if err != nil {
				t.Fatal("run blocked after installer exit", err)
			}
			release()
		})
	}
}

func TestMaintenanceCoversMissingDirectoryAndReplacementExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-program")
	lease, err := AcquireInstallation(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(root); err != nil {
		t.Fatal("new directory changed maintenance identity", err)
	}
	path := filepath.Join(root, "MultiPan.exe")
	for i := 0; i < 2; i++ {
		if err = os.WriteFile(path, []byte(fmt.Sprint("MZ-generation-", i)), 0600); err != nil {
			t.Fatal(err)
		}
		if release, err := AcquireRun(path); !errors.Is(err, ErrBusy) {
			if release != nil {
				release()
			}
			t.Fatal("replacement executable bypassed maintenance", err)
		}
		if err = os.Rename(path, path+fmt.Sprint(i)); err != nil {
			t.Fatal("failed startup leaked executable handle", err)
		}
	}
	if err = lease.Check(t.TempDir()); err == nil {
		t.Fatal("lease redirected")
	}
	lease.Close()
	lease.Close()
	if err = lease.Check(root); err == nil {
		t.Fatal("closed lease remained valid")
	}
}

func TestMaintenanceOtherObjectCollisionFailsClosed(t *testing.T) {
	path := appFixture(t)
	root, err := canonicalRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateEvent(nil, 0, 0, maintenanceName(root))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if lease, err := AcquireInstallation(filepath.Dir(path)); !errors.Is(err, ErrBusy) {
		if lease != nil {
			lease.Close()
		}
		t.Fatal("non-mutex collision bypassed installer guard", err)
	}
	if release, err := AcquireRun(path); !errors.Is(err, ErrBusy) {
		if release != nil {
			release()
		}
		t.Fatal("non-mutex collision bypassed startup guard", err)
	}
}
