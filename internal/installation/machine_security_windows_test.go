package installation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMachineSecurityDescriptorPolicy(t *testing.T) {
	p := machineSecurityPolicy{trusted: map[string]bool{"S-1-5-18": true, "S-1-5-32-544": true}}
	for _, tc := range []struct {
		name, sddl string
		valid      bool
	}{
		{"administrators-only", "O:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)", true},
		{"users-read-execute", "O:BAD:P(A;;FA;;;BA)(A;;GRGX;;;BU)", true},
		{"everyone-read", "O:SYD:P(A;;FA;;;SY)(A;;GR;;;WD)", true},
		{"empty-not-null", "O:BAD:P", true},
		{"untrusted-owner", "O:BUD:P(A;;FA;;;BA)", false},
		{"missing-owner", "D:P(A;;FA;;;BA)", false},
		{"missing-dacl", "O:BA", false},
		{"null-dacl", "O:BAD:NO_ACCESS_CONTROL", false},
		{"unprotected-root", "O:BAD:(A;;FA;;;BA)", false},
		{"users-full", "O:BAD:P(A;;FA;;;BA)(A;;FA;;;BU)", false},
		{"deny-does-not-offset-allow", "O:BAD:P(D;;GW;;;WD)(A;;GW;;;WD)(A;;FA;;;BA)", false},
		{"inherit-only-write", "O:BAD:P(A;;FA;;;BA)(A;OICIIO;GW;;;WD)", false},
		{"object-ace", "O:BAD:P(OA;;GA;00000000-0000-0000-0000-000000000001;;WD)(A;;FA;;;BA)", false},
		{"unknown-mask", "O:BAD:P(A;;0x02000000;;;BA)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal("invalid test descriptor", err)
			}
			_, err = p.descriptor(sd, true, machineMutationMask)
			if (err == nil) != tc.valid {
				t.Fatal("ACL policy mismatch", tc.valid, err)
			}
		})
	}
	// Exercise every individual mutating right, including specific rights
	// which GENERIC_WRITE alone would miss (DELETE_CHILD/WRITE_DAC/OWNER).
	for _, mask := range []uint32{windows.GENERIC_ALL, windows.GENERIC_WRITE, windows.DELETE, windows.WRITE_DAC, windows.WRITE_OWNER, windows.FILE_WRITE_DATA, windows.FILE_APPEND_DATA, windows.FILE_WRITE_EA, windows.FILE_WRITE_ATTRIBUTES, directoryDeleteChild} {
		sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:BAD:P(A;;FA;;;BA)(A;;0x%08x;;;BU)", mask))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.descriptor(sd, true, machineMutationMask); err == nil {
			t.Fatalf("ordinary mutation accepted: %x", mask)
		}
	}
}

func fixtureSecurityPolicy(t *testing.T) (machineSecurityPolicy, string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	// This extra trust is PRIVATE TO THE TEST. No owner transfer, elevation,
	// SYSTEM token, real Program Files, or another Windows account is used.
	return machineSecurityPolicy{trusted: map[string]bool{sid: true, "S-1-5-18": true, "S-1-5-32-544": true}}, sid
}

func setFixtureDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func machineSecurityFixture(t *testing.T) (string, string, string, machineSecurityPolicy, string) {
	t.Helper()
	floor := t.TempDir()
	p, sid := fixtureSecurityPolicy(t)
	sddl := "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)"
	setFixtureDACL(t, floor, sddl)
	base := filepath.Join(floor, "Synthetic Program Files")
	dir := filepath.Join(base, "MultiPan")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	setFixtureDACL(t, dir, sddl)
	for _, n := range append(append(append([]string{}, PayloadNames...), Uninstaller), Marker) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("synthetic managed bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return floor, base, dir, p, sddl
}

func TestMachineSecuritySnapshotRejectsChanges(t *testing.T) {
	for _, mode := range []string{"unchanged", "root-acl", "file-acl", "safe-acl-change", "file-size", "file-replaced", "file-missing", "hard-link", "file-type", "new-previously-missing", "parent-acl", "root-replaced"} {
		t.Run(mode, func(t *testing.T) {
			floor, base, dir, p, safe := machineSecurityFixture(t)
			file := filepath.Join(dir, App)
			if mode == "new-previously-missing" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := captureMachineSecurityAtFloor(base, dir, p, floor)
			if err != nil {
				t.Fatal("safe fixture refused", err)
			}
			switch mode {
			case "safe-acl-change":
				setFixtureDACL(t, file, safe+"(A;;GR;;;WD)")
			case "file-size":
				if err = os.WriteFile(file, []byte("changed length"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root-acl", "file-acl", "parent-acl":
				changed := dir
				if mode == "file-acl" {
					changed = file
				}
				if mode == "parent-acl" {
					changed = base
				}
				setFixtureDACL(t, changed, safe+"(A;;GW;;;BU)")
			case "file-replaced":
				if err = os.Rename(file, file+".saved"); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, []byte("synthetic managed bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "file-missing":
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "hard-link":
				if err = os.Link(file, filepath.Join(dir, "linked.exe")); err != nil {
					t.Fatal(err)
				}
			case "file-type":
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "new-previously-missing":
				if err = os.WriteFile(file, []byte("new file"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root-replaced":
				if err = os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				setFixtureDACL(t, dir, safe)
			}
			err = snapshot.Check()
			if (err == nil) != (mode == "unchanged") {
				t.Fatal("snapshot change detection", mode, err)
			}
		})
	}
}

func TestMachineSecurityMissingRootAndJunction(t *testing.T) {
	floor, base, dir, p, _ := machineSecurityFixture(t)
	// Remove ONLY these explicitly enumerated synthetic files and empty dir.
	for _, n := range append(append(append([]string{}, PayloadNames...), Uninstaller), Marker) {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureMachineSecurityAtFloor(base, dir, p, floor)
	if err != nil || snapshot.Check() != nil {
		t.Fatal("absent candidate refused", err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, outside).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	defer os.Remove(dir)
	if snapshot.Check() == nil {
		t.Fatal("new junction accepted")
	}
	if _, err := captureMachineSecurityAtFloor(base, dir, p, floor); err == nil {
		t.Fatal("initial junction accepted")
	}
	requireFile(t, sentinel, []byte("keep"))
}

func TestMachineSecurityBoundariesAndOwner(t *testing.T) {
	floor, base, dir, p, _ := machineSecurityFixture(t)
	for _, invalid := range []string{filepath.Join(base, "OtherApp"), dir + `\..\MultiPan`, strings.ReplaceAll(dir, `\`, "/"), `\\server\share\MultiPan`} {
		if _, err := captureMachineSecurityAtFloor(base, invalid, p, floor); err == nil {
			t.Fatal("nonfixed candidate accepted")
		}
	}
	if _, err := captureMachineSecurityAtFloor(base, dir, p, filepath.Join(base, "not-an-ancestor")); err == nil {
		t.Fatal("invalid audit floor accepted")
	}
	strict := machineSecurityPolicy{trusted: map[string]bool{"S-1-5-18": true, "S-1-5-32-544": true}}
	if _, err := strict.node(dir, true, true, false, machineMutationMask); err == nil {
		t.Fatal("ordinary fixture owner accepted as production trust")
	}
	known, candidate, err := knownMachineDirectory() // Known Folder lookup only; no audit/write there
	if err != nil || !sameLocation(candidate, filepath.Join(known, "MultiPan")) {
		t.Fatal("known folder resolution", err)
	}
}
