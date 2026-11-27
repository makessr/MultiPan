package installation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The all-users MSI has no marker file and registers through Windows
// Installer, so the legacy checks must not report it as missing.
func TestInspectMachineIntegrationRecognisesMSI(t *testing.T) {
	// Windows runners without the Chinese code page cannot save the literal
	// shortcut name through WScript.Shell, so exercise the same logic under an
	// ASCII name. The shipped name is covered by the MSI definition.
	previous := msiShortcutName
	msiShortcutName = "MultiPan-all-users.lnk"
	t.Cleanup(func() { msiShortcutName = previous })

	s := scopedFixture(t)
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		t.Fatal(err)
	}
	msiKey := layout.RegistryPath + `-msi`
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, msiKey) })
	if err = s.Install(ScopeMachine, fixture("0.99.0"), []byte("MZ-synthetic-uninstaller"), Options{}); err != nil {
		t.Fatal(err)
	}
	inspect := func(auditErr error) MachineIntegrationReport {
		t.Helper()
		return inspectMachineIntegration(layout.Directory, filepath.Dir(layout.StartLink), filepath.Dir(layout.DesktopLink), registry.CURRENT_USER, layout.RegistryPath, msiKey, 0, auditErr)
	}
	if r := inspect(nil); r.Installer != "legacy" || r.Registration.Status != "matches" {
		t.Fatalf("unmarked directory was not reported as the legacy layout: %+v", r)
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, msiKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if err = k.SetDWordValue("StartMenu", 0); err != nil {
		t.Fatal(err)
	}
	if r := inspect(nil); r.Installer != "legacy" {
		t.Fatalf("zero marker was treated as an installation: %+v", r)
	}
	if err = k.SetDWordValue("StartMenu", 1); err != nil {
		t.Fatal(err)
	}
	r := inspect(nil)
	if r.Installer != "msi" || r.Registration.Status != "managed-by-installer" || r.StartMenu.Status != "missing" || r.Desktop.Status != "missing" {
		t.Fatalf("marked directory was not reported as an MSI layout: %+v", r)
	}

	target := filepath.Join(layout.Directory, App)
	for _, path := range []string{filepath.Join(filepath.Dir(layout.StartLink), msiShortcutName), filepath.Join(filepath.Dir(layout.DesktopLink), msiShortcutName)} {
		if err = Shortcut(path, target); err != nil {
			t.Fatal(err)
		}
	}
	if r = inspect(nil); r.StartMenu.Status != "matches" || r.Desktop.Status != "matches" {
		t.Fatalf("MSI shortcuts were not verified: %+v", r)
	}

	blocked := inspect(errors.New("fixture unsafe ACL"))
	if blocked.DirectorySecurity.Status != "blocked" || blocked.StartMenu.Status != "unverified" || blocked.Registration.Status != "managed-by-installer" {
		t.Fatalf("untrusted MSI location was not reported independently: %+v", blocked)
	}

	// A leftover marker must not describe a directory that no longer holds the
	// shipped program, so the report falls back to the legacy layout checks.
	if err = os.Remove(filepath.Join(layout.Directory, App)); err != nil {
		t.Fatal(err)
	}
	if r = inspect(nil); r.Installer != "legacy" {
		t.Fatalf("marked key without the shipped program was treated as an installation: %+v", r)
	}
}
