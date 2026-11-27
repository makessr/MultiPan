package installation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

func TestInspectMachineIntegration(t *testing.T) {
	s := scopedFixture(t)
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		t.Fatal(err)
	}
	inspect := func(registration, startMenu, desktop string) {
		t.Helper()
		r := inspectMachineIntegration(layout.Directory, filepath.Dir(layout.StartLink), filepath.Dir(layout.DesktopLink), registry.CURRENT_USER, layout.RegistryPath, 0, nil)
		if r.Registration.Status != registration || r.StartMenu.Status != startMenu || r.Desktop.Status != desktop {
			t.Fatalf("unexpected observations: %+v", r)
		}
	}
	inspect("missing", "missing", "missing")

	target := filepath.Join(layout.Directory, App)
	if err := Shortcut(layout.DesktopLink, target); err != nil {
		t.Fatal(err)
	}
	inspect("missing", "missing", "conflict")
	if err := os.Remove(layout.DesktopLink); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(ScopeMachine, fixture("0.99.0"), []byte("MZ-synthetic-uninstaller"), Options{}); err != nil {
		t.Fatal(err)
	}
	inspect("matches", "matches", "matches")
	blocked := inspectMachineIntegration(layout.Directory, filepath.Dir(layout.StartLink), filepath.Dir(layout.DesktopLink), registry.CURRENT_USER, layout.RegistryPath, 0, errors.New("fixture unsafe ACL"))
	if blocked.DirectorySecurity.Status != "blocked" || blocked.Registration.Status != "unverified" || blocked.StartMenu.Status != "unverified" || blocked.Desktop.Status != "unverified" {
		t.Fatalf("untrusted location was not reported independently: %+v", blocked)
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, layout.RegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if err = k.SetStringValue("UninstallString", uninstallCommand(layout.Directory)+" --silent"); err != nil {
		t.Fatal(err)
	}
	inspect("conflict", "matches", "matches")
	if err = s.Uninstall(ScopeMachine); err == nil {
		t.Fatal("sandbox uninstall accepted a modified registration")
	}
	requireFile(t, filepath.Join(layout.Directory, App), fixture("0.99.0").Files[App])
	if err = k.SetStringValue("UninstallString", uninstallCommand(layout.Directory)); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(layout.Directory, machineCheckpointName)
	checkpointBytes := []byte("synthetic unreadable checkpoint")
	if err = os.WriteFile(checkpoint, checkpointBytes, 0600); err != nil {
		t.Fatal(err)
	}
	inspect("unverified", "unverified", "unverified")
	requireFile(t, checkpoint, checkpointBytes)
	if err = os.Remove(checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := k.SetStringValue("InstallScope", string(ScopeUser)); err != nil {
		t.Fatal(err)
	}
	if err := withShortcut(layout.StartLink, func(link *ole.IDispatch) error {
		v, err := oleutil.PutProperty(link, "Arguments", "--unexpected-argument")
		if err != nil {
			return err
		}
		v.Clear()
		v, err = oleutil.CallMethod(link, "Save")
		if err == nil {
			v.Clear()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = k.SetStringValue("InstallScope", string(ScopeMachine)); err != nil {
		t.Fatal(err)
	}
	if err = s.Install(ScopeMachine, fixture("0.99.1"), []byte("MZ-upgrade"), Options{}); err == nil {
		t.Fatal("sandbox upgrade accepted a changed shortcut")
	}
	requireFile(t, filepath.Join(layout.Directory, App), fixture("0.99.0").Files[App])
	if err = k.SetStringValue("InstallScope", string(ScopeUser)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(layout.StartLink)
	if err != nil {
		t.Fatal(err)
	}
	inspect("conflict", "conflict", "matches")
	inspect("conflict", "conflict", "matches")
	requireFile(t, layout.StartLink, before)
	scope, _, err := k.GetStringValue("InstallScope")
	if err != nil || scope != string(ScopeUser) {
		t.Fatalf("inspection changed the registration: %q, %v", scope, err)
	}

	if err := k.SetExpandStringValue("InstallScope", string(ScopeMachine)); err != nil {
		t.Fatal(err)
	}
	inspect("conflict", "conflict", "matches")
	if err := os.Remove(layout.DesktopLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(layout.DesktopLink, 0700); err != nil {
		t.Fatal(err)
	}
	inspect("conflict", "conflict", "unreadable")
}
