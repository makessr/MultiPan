package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var handoffFixtureSelf = []byte("MZ-synthetic-handoff")

func syntheticHandoff(target, mode string) machineHandoff {
	bundle := fixture("0.99.0")
	data, _ := json.Marshal(struct {
		Bundle Bundle
		Self   []byte
	}{bundle, handoffFixtureSelf})
	b := machineHandoff{operation: handoffInstall, target: target, version: bundle.Version, payload: sha256.Sum256(data)}
	switch mode {
	case "payload":
		b.payload = sha256.Sum256([]byte("other payload"))
	case "target":
		b.target = filepath.Join(filepath.Dir(target), "elsewhere", "MultiPan")
	case "operation":
		b.operation = handoffRemove
	case "version":
		b.version = "0.98.0"
	}
	return b
}

func TestMachineHandoffChild(t *testing.T) {
	name := os.Getenv("MULTIPAN_TEST_HANDOFF_PIPE")
	if name == "" {
		return
	}
	parentPID, err := strconv.ParseUint(os.Getenv("MULTIPAN_TEST_HANDOFF_PARENT"), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(parentPID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	expected, err := identifyHandoffPeer(parent)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.Getenv("MULTIPAN_TEST_HANDOFF_MODE")
	if mode == "wrong-initiator" {
		expected.created.LowDateTime ^= 1
	}
	h, err := openHandoffPipe(name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if mode == "oversized" {
		var n uint32
		if err := windows.WriteFile(h, make([]byte, handoffSize+1), &n, nil); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		return
	}
	if mode == "stale-confirmation" {
		frame, err := readHandoff(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		frame[0] = 'A'
		frame[5] ^= 1
		if err = writeHandoff(ctx, h, frame); err != nil {
			t.Fatal(err)
		}
		if _, err = readHandoff(ctx, h); err == nil {
			t.Fatal("unexpected commit authorization")
		}
		t.Fatal("stale handoff did not authorize installation")
	}
	b := syntheticHandoff(os.Getenv("MULTIPAN_TEST_HANDOFF_TARGET"), mode)
	if err := acceptMachineHandoff(ctx, h, expected, b); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScopedSandbox(os.Getenv("MULTIPAN_TEST_HANDOFF_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	layout, err := s.Layout(ScopeMachine)
	if err != nil || !sameLocation(layout.Directory, b.target) || b.operation != handoffInstall {
		t.Fatal("sandbox target or operation mismatch", err)
	}
	if err = s.installProtectedMachine(fixture(b.version), handoffFixtureSelf, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestMachineHandoffCrossProcess(t *testing.T) {
	for _, mode := range []string{"success", "payload", "target", "operation", "version", "wrong-initiator", "wrong-helper", "oversized", "stale-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			s := scopedFixture(t)
			layout, err := s.Layout(ScopeMachine)
			if err != nil {
				t.Fatal(err)
			}
			target := layout.Directory
			if err = os.Mkdir(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			_, sid := fixtureSecurityPolicy(t)
			for _, dir := range []string{s.root, filepath.Dir(target)} {
				setFixtureDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
			}
			name, pipe, err := newHandoffPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(pipe)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMachineHandoffChild$")
			cmd.Env = append(os.Environ(),
				"MULTIPAN_TEST_HANDOFF_PIPE="+name, "MULTIPAN_TEST_HANDOFF_TARGET="+target,
				"MULTIPAN_TEST_HANDOFF_ROOT="+s.root,
				"MULTIPAN_TEST_HANDOFF_MODE="+mode, "MULTIPAN_TEST_HANDOFF_PARENT="+strconv.Itoa(os.Getpid()))
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			child, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(child)
			expected, err := identifyHandoffPeer(child)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "wrong-helper" {
				expected.pid = uint32(os.Getpid())
			}
			offerErr := offerMachineHandoff(ctx, pipe, expected, syntheticHandoff(target, ""))
			if offerErr != nil {
				_ = windows.DisconnectNamedPipe(pipe)
				if mode == "oversized" {
					cancel()
				}
			}
			childErr := cmd.Wait()
			if mode == "success" {
				if offerErr != nil || childErr != nil {
					t.Fatalf("handoff failed: parent=%v child=%v %s", offerErr, childErr, &output)
				}
				requireFile(t, filepath.Join(target, App), fixture("0.99.0").Files[App])
				if err = s.Uninstall(ScopeMachine); err != nil {
					t.Fatal(err)
				}
			} else if offerErr == nil || childErr == nil {
				t.Fatalf("invalid handoff accepted: parent=%v child=%v %s", offerErr, childErr, &output)
			} else if _, err = os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("rejected handoff modified the target", err)
			}
		})
	}
}

func TestMachineHandoffCancelsWithoutPeer(t *testing.T) {
	_, pipe, err := newHandoffPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	err = offerMachineHandoff(ctx, pipe, handoffPeer{}, syntheticHandoff(filepath.Join(t.TempDir(), "MultiPan"), ""))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("idle handoff did not time out", err)
	}
}
