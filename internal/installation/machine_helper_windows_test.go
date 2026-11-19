package installation

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMachineHelperTrustAndLease(t *testing.T) {
	floor, base, dir, policy, acl := machineSecurityFixture(t)
	path := filepath.Join(dir, machineHelperName)
	data := []byte("MZ synthetic machine helper")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	setFixtureDACL(t, path, acl)
	digest := sha256.Sum256(data)
	simulatedSignature := func(string, windows.Handle) error { return nil }
	for _, mode := range []string{"empty digest", "wrong digest", "untrusted ACL", "hard link", "unsigned"} {
		t.Run(mode, func(t *testing.T) {
			expected, signature := digest, simulatedSignature
			switch mode {
			case "empty digest":
				expected = [sha256.Size]byte{}
			case "wrong digest":
				expected[0] ^= 1
				signature = func(string, windows.Handle) error {
					t.Error("signature invoked for mismatched release digest")
					return nil
				}
			case "untrusted ACL":
				setFixtureDACL(t, path, acl+"(A;;GW;;;BU)")
				defer setFixtureDACL(t, path, acl)
			case "hard link":
				alias := filepath.Join(dir, "alias.exe")
				if err := os.Link(path, alias); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(alias)
			case "unsigned":
				signature = verifyHelperSignature
			}
			if lease, err := pinMachineHelper(base, dir, floor, policy, expected, signature); err == nil {
				lease.close()
				t.Fatal("untrusted helper was accepted")
			}
		})
	}
	lease, err := pinMachineHelper(base, dir, floor, policy, digest, simulatedSignature)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	if err = os.WriteFile(path, []byte("replacement"), 0600); err == nil {
		t.Fatal("pinned helper was writable")
	}
	for _, guarded := range []string{path, dir, base} {
		if err = moveWithoutReplace(guarded, guarded+".replacement"); err == nil {
			t.Fatal("pinned helper or ancestor was movable")
		}
	}
	if err = lease.verifyProcessImage(windows.CurrentProcess()); err == nil {
		t.Fatal("a different executable was accepted")
	}
	lease.close()
	if err = os.Rename(path, path+".released"); err != nil {
		t.Fatal("helper still locked after close", err)
	}
}

func TestMachineHelperAuthenticodeReference(t *testing.T) {
	path := os.Getenv("MULTIPAN_TEST_SIGNED_PE")
	if path == "" {
		t.Skip("set MULTIPAN_TEST_SIGNED_PE to an existing embedded-signed verification fixture")
	}
	floor, base, dir, policy, acl := machineSecurityFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, machineHelperName)
	if err = os.WriteFile(helper, data, 0600); err != nil {
		t.Fatal(err)
	}
	setFixtureDACL(t, helper, acl)
	lease, err := pinMachineHelper(base, dir, floor, policy, sha256.Sum256(data), verifyHelperSignature)
	if err != nil {
		t.Fatal(err)
	}
	lease.close()
	// A signed fixture still cannot satisfy another release's pinned digest.
	wrongDigest := sha256.Sum256(append(append([]byte(nil), data...), 0))
	if lease, err = pinMachineHelper(base, dir, floor, policy, wrongDigest, verifyHelperSignature); err == nil {
		lease.close()
		t.Fatal("signature alone accepted wrong release")
	}
}

func TestMachineHelperImageChild(t *testing.T) {
	if os.Getenv("MULTIPAN_TEST_HELPER_IMAGE") == "1" {
		// The parent only queries this process and owns its cancellation.
		time.Sleep(5 * time.Second)
	}
}

func TestMachineHelperMatchesLiveProcess(t *testing.T) {
	floor, base, dir, policy, acl := machineSecurityFixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, machineHelperName)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	setFixtureDACL(t, path, acl)
	lease, err := pinMachineHelper(base, dir, floor, policy, sha256.Sum256(data), func(string, windows.Handle) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-test.run=^TestMachineHelperImageChild$")
	cmd.Env = append(os.Environ(), "MULTIPAN_TEST_HELPER_IMAGE=1")
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err = lease.verifyProcessImage(h); err != nil {
		t.Fatal(err)
	}
	peer, err := identifyHandoffPeer(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lease.authorizeProcess(h, peer.session^1); err == nil {
		t.Fatal("wrong Windows session was accepted")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		if _, err = lease.authorizeProcess(h, peer.session); err == nil {
			t.Fatal("ordinary child was accepted as elevated")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err = lease.offer(ctx, 0, h, machineHandoff{}); err == nil || errors.Is(err, context.Canceled) {
			t.Fatal("offer advanced to the pipe before checking the process token", err)
		}
	}
	lease.close()
	if err = lease.verifyProcessImage(h); err == nil || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("closed helper lease was reused", err)
	}
}
