package installation

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func scopedFixture(t *testing.T) *ScopedSandbox {
	t.Helper()
	root, err := os.MkdirTemp("", "multipan-setup-check-scopes-")
	if err != nil {
		t.Fatal(err)
	}
	// No recursive cleanup: preserve the test evidence and never traverse a
	// deliberately injected junction. Every test uses its own random root/key.
	if err = os.WriteFile(filepath.Join(root, ".multipan-setup-test"), []byte(ScopedSandboxMarker), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScopedSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		l, err := s.Layout(scope)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, l.RegistryPath) })
	}
	t.Log("isolated scope evidence:", root)
	return s
}

func requireFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("file changed: %s, err=%v", path, err)
	}
}

func putMarkerScope(t *testing.T, dir string, scope Scope) {
	t.Helper()
	m, err := readMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Scope = scope
	b, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(dir, Marker), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestScopedOwnershipMatrix(t *testing.T) {
	for _, original := range []Scope{"", ScopeUser, ScopeMachine} {
		for _, action := range []Scope{ScopeUser, ScopeMachine} {
			t.Run(string(original)+"-to-"+string(action), func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "program")
				if err := installForScope(dir, fixture("0.98.0"), []byte("MZ-old"), Options{}, original.effective()); err != nil {
					t.Fatal(err)
				}
				putMarkerScope(t, dir, original)
				before, _ := os.ReadFile(filepath.Join(dir, Marker))
				allowed := original.effective() == action
				err := installForScope(dir, fixture("0.99.0"), []byte("MZ-new"), Options{}, action)
				if (err == nil) != allowed {
					t.Fatal("scope upgrade matrix", original, action, err)
				}
				if !allowed {
					requireFile(t, filepath.Join(dir, Marker), before)
					requireFile(t, filepath.Join(dir, App), fixture("0.98.0").Files[App])
				}
				err = uninstallForScope(dir, action)
				if (err == nil) != allowed {
					t.Fatal("scope uninstall matrix", original, action, err)
				}
				if !allowed {
					requireFile(t, filepath.Join(dir, Marker), before)
					requireFile(t, filepath.Join(dir, App), fixture("0.98.0").Files[App])
				}
			})
		}
	}
}

func TestScopedSandboxLifecyclePreservesOtherScopeAndProfiles(t *testing.T) {
	s := scopedFixture(t)
	old, next := fixture("0.98.0"), fixture("0.99.0") // synthetic versions, not release artifacts
	profiles := []string{filepath.Join(s.root, "profiles", "user-a", "MultiPan", "state.enc"), filepath.Join(s.root, "profiles", "user-b", "MultiPan", "state.enc")}
	for _, p := range profiles {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("synthetic profile sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		if err := s.Install(scope, old, []byte("MZ-synthetic-uninstaller"), Options{}); err != nil {
			t.Fatal(scope, err)
		}
		l, i, err := s.integration(scope)
		if err != nil {
			t.Fatal(err)
		}
		m, err := readMarkerForScope(l.Directory, scope)
		if err != nil || m.Scope != scope {
			t.Fatal(m, err)
		}
		if got, err := i.RegisteredLocation(); err != nil || got != l.Directory {
			t.Fatal(got, err)
		}
		if err = os.WriteFile(filepath.Join(l.Directory, "user-note.txt"), []byte("keep unknown"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = s.Install(scope, next, []byte("MZ-upgrade"), Options{}); err != nil {
			t.Fatal(scope, "upgrade", err)
		}
		if err = s.Install(scope, old, []byte("MZ-old"), Options{}); err == nil {
			t.Fatal("downgrade accepted", scope)
		}
		// Missing owned file can be repaired without changing scope or data.
		if err = os.Remove(filepath.Join(l.Directory, "TRIAL-GUIDE.txt")); err != nil {
			t.Fatal(err)
		}
		if err = s.Install(scope, next, []byte("MZ-upgrade"), Options{}); err != nil {
			t.Fatal(scope, "repair", err)
		}
	}
	user, _ := s.Layout(ScopeUser)
	machine, _ := s.Layout(ScopeMachine)
	if err := Install(machine.Directory, next, []byte("MZ-user"), Options{}); err == nil {
		t.Fatal("public current-user Install changed machine root")
	}
	if err := Uninstall(machine.Directory); err == nil {
		t.Fatal("public current-user Uninstall removed machine root")
	}
	if _, err := ResolveLocation(user.Directory, "", "", filepath.Join(machine.Directory, Uninstaller), true); err == nil {
		t.Fatal("current-user uninstaller accepted machine ownership")
	}
	// Simulated running application holds a real OS read handle that denies
	// modification/deletion. Neither scope may ignore it for upgrade/uninstall.
	path, _ := windows.UTF16PtrFromString(filepath.Join(machine.Directory, App))
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	upgradeErr := s.Install(ScopeMachine, next, []byte("MZ-upgrade"), Options{})
	uninstallErr := s.Uninstall(ScopeMachine)
	windows.CloseHandle(h)
	if upgradeErr == nil || uninstallErr == nil {
		t.Fatal("locked machine application was ignored", upgradeErr, uninstallErr)
	}
	machineMarker, _ := os.ReadFile(filepath.Join(machine.Directory, Marker))
	if err = s.Uninstall(ScopeUser); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(machine.Directory, Marker), machineMarker)
	requireFile(t, filepath.Join(machine.Directory, App), next.Files[App])
	if _, _, err = s.integration(ScopeMachine); err != nil {
		t.Fatal("user uninstall affected machine integration", err)
	}
	if err = s.Install(ScopeUser, next, []byte("MZ-reinstall"), Options{}); err != nil {
		t.Fatal(err)
	}
	if err = s.Uninstall(ScopeMachine); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(user.Directory, App), next.Files[App])
	for _, l := range []SandboxLayout{user, machine} {
		requireFile(t, filepath.Join(l.Directory, "user-note.txt"), []byte("keep unknown"))
	}
	for _, p := range profiles {
		requireFile(t, p, []byte("synthetic profile sentinel"))
	}
	if err = s.Uninstall(ScopeUser); err != nil {
		t.Fatal(err)
	}
	for _, l := range []SandboxLayout{user, machine} {
		for _, p := range []string{filepath.Join(l.Directory, App), l.StartLink, l.DesktopLink} {
			if _, err = os.Stat(p); !os.IsNotExist(err) {
				t.Fatal("managed file left", p, err)
			}
		}
		if key, err := registry.OpenKey(registry.CURRENT_USER, l.RegistryPath, registry.QUERY_VALUE); err == nil {
			key.Close()
			t.Fatal("test registration left", l.RegistryPath)
		}
	}
}

func TestScopedSandboxRefusesLostCapabilityBeforeCommit(t *testing.T) {
	s := scopedFixture(t)
	if err := s.Install(ScopeMachine, fixture("0.98.0"), []byte("MZ-before"), Options{}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Layout(ScopeMachine)
	before, _ := os.ReadFile(filepath.Join(l.Directory, Marker))
	err := s.Install(ScopeMachine, fixture("0.99.0"), []byte("MZ-after"), Options{BeforeCommit: func() error {
		return os.WriteFile(filepath.Join(s.root, ".multipan-setup-test"), []byte("changed"), 0600)
	}})
	if err == nil {
		t.Fatal("lost sandbox capability was ignored")
	}
	requireFile(t, filepath.Join(l.Directory, Marker), before)
	requireFile(t, filepath.Join(l.Directory, App), fixture("0.98.0").Files[App])
	if err = s.Uninstall(ScopeMachine); err == nil {
		t.Fatal("uninstall accepted invalid sandbox marker")
	}
}

func TestScopedCommitRechecksOwnershipAndFreshFiles(t *testing.T) {
	for _, mode := range []string{"scope", "payload", "fresh-file", "fresh-marker"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "program")
			existing := mode == "scope" || mode == "payload"
			if existing {
				if err := Install(dir, fixture("0.98.0"), []byte("MZ-before"), Options{}); err != nil {
					t.Fatal(err)
				}
			}
			err := Install(dir, fixture("0.99.0"), []byte("MZ-after"), Options{BeforeCommit: func() error {
				switch mode {
				case "scope":
					putMarkerScope(t, dir, ScopeMachine)
				case "payload":
					return os.WriteFile(filepath.Join(dir, App), []byte("MZ-user-changed"), 0600)
				case "fresh-file":
					return os.WriteFile(filepath.Join(dir, App), []byte("MZ-user-changed"), 0600)
				case "fresh-marker":
					return os.WriteFile(filepath.Join(dir, Marker), []byte("foreign-marker"), 0600)
				}
				return nil
			}})
			if err == nil {
				t.Fatal("changed ownership/file overwritten", mode)
			}
			if mode == "payload" || mode == "fresh-file" {
				requireFile(t, filepath.Join(dir, App), []byte("MZ-user-changed"))
			}
			if mode == "scope" {
				if _, err = readMarkerForScope(dir, ScopeMachine); err != nil {
					t.Fatal("external scope overwritten", err)
				}
				requireFile(t, filepath.Join(dir, App), fixture("0.98.0").Files[App])
			}
			if mode == "fresh-marker" {
				requireFile(t, filepath.Join(dir, Marker), []byte("foreign-marker"))
			}
		})
	}
}

func TestScopedMachineRollbackKeepsOriginalOwnership(t *testing.T) {
	s := scopedFixture(t)
	if err := s.Install(ScopeMachine, fixture("0.98.0"), []byte("MZ-before"), Options{}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Layout(ScopeMachine)
	before, _ := os.ReadFile(filepath.Join(l.Directory, Marker))
	err := s.Install(ScopeMachine, fixture("0.99.0"), []byte("MZ-after"), Options{Rename: func(a, b string) error {
		if filepath.Base(a) == "TRIAL-GUIDE.txt" && filepath.Dir(b) == l.Directory {
			return errors.New("synthetic commit interruption")
		}
		return os.Rename(a, b)
	}})
	if err == nil {
		t.Fatal("injected failure ignored")
	}
	requireFile(t, filepath.Join(l.Directory, Marker), before)
	requireFile(t, filepath.Join(l.Directory, App), fixture("0.98.0").Files[App])
	if err = s.Uninstall(ScopeMachine); err != nil {
		t.Fatal("rollback lost uninstall ownership", err)
	}
}

func TestScopedIntegrationRejectsOtherScopeBeforeWrites(t *testing.T) {
	s := scopedFixture(t)
	if err := s.Install(ScopeMachine, fixture("0.98.0"), []byte("MZ"), Options{}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Layout(ScopeMachine)
	i := Integration{RegistryPath: l.RegistryPath, StartLink: l.StartLink, DesktopLink: l.DesktopLink} // user API
	before, _ := os.ReadFile(l.StartLink)
	if _, err := i.RegisteredLocation(); err == nil {
		t.Fatal("user integration loaded machine registration")
	}
	if err := i.Preflight(l.Directory); err == nil {
		t.Fatal("user preflight accepted machine registration")
	}
	if warnings := i.Apply(l.Directory, "0.99.0", true); len(warnings) == 0 {
		t.Fatal("user integration overwrote machine registration")
	}
	if warnings := i.Remove(l.Directory); len(warnings) == 0 {
		t.Fatal("user integration removed machine registration")
	}
	requireFile(t, l.StartLink, before)
	if err := s.Uninstall(ScopeMachine); err != nil {
		t.Fatal(err)
	}
}

func TestScopedSandboxRejectsPathsAndInvalidScope(t *testing.T) {
	s := scopedFixture(t)
	for _, bad := range []Scope{"", "all-users", "MACHINE", "system", "future"} {
		if _, err := s.Layout(bad); err == nil {
			t.Fatal("implicit/unknown scope accepted", bad)
		}
		if err := s.Install(bad, fixture("0.98.0"), []byte("MZ"), Options{}); err == nil {
			t.Fatal("invalid scope dispatched install", bad)
		}
		if err := s.Uninstall(bad); err == nil {
			t.Fatal("invalid scope dispatched uninstall", bad)
		}
	}
	for _, bad := range []string{"", ".", filepath.Dir(s.root), s.root + `\..\escape`, filepath.Join(t.TempDir(), "multipan-setup-check-unmarked"), `\\server\share\multipan-setup-check-bad`} {
		if _, err := OpenScopedSandbox(bad); err == nil {
			t.Fatal("unsafe sandbox accepted", bad)
		}
	}
	for _, which := range []string{"machine-root", "shell"} {
		t.Run(which, func(t *testing.T) {
			s := scopedFixture(t)
			outside := t.TempDir()
			file := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(file, []byte("keep outside"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(s.root, string(ScopeMachine))
			if which == "shell" {
				link = filepath.Join(s.root, "shell")
			}
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
			defer os.Remove(link) // only the junction itself
			if err := s.Install(ScopeMachine, fixture("0.98.0"), []byte("MZ"), Options{}); err == nil {
				t.Fatal("sandbox followed junction", which)
			}
			requireFile(t, file, []byte("keep outside"))
			if _, err := os.Stat(filepath.Join(outside, "MultiPan")); !os.IsNotExist(err) {
				t.Fatal("sandbox wrote through junction", err)
			}
		})
	}
}

func TestScopedPayloadCannotChooseScopeAndUnknownMarkerRefuses(t *testing.T) {
	for _, scope := range []Scope{ScopeUser, ScopeMachine, "future"} {
		data := bundleBytes(fixture("0.98.0"), func(files map[string][]byte) {
			var m Manifest
			if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
				t.Fatal(err)
			}
			m.Scope = scope
			files["manifest.json"], _ = json.Marshal(m)
		})
		if _, err := Decode(data); err == nil {
			t.Fatal("payload chose install scope", scope)
		}
	}
	dir := filepath.Join(t.TempDir(), "program")
	if err := Install(dir, fixture("0.98.0"), []byte("MZ"), Options{}); err != nil {
		t.Fatal(err)
	}
	putMarkerScope(t, dir, "future")
	before, _ := os.ReadFile(filepath.Join(dir, Marker))
	if err := Install(dir, fixture("0.99.0"), []byte("MZ"), Options{}); err == nil {
		t.Fatal("future scope overwritten")
	}
	if err := Uninstall(dir); err == nil {
		t.Fatal("future scope removed")
	}
	requireFile(t, filepath.Join(dir, Marker), before)
	requireFile(t, filepath.Join(dir, App), fixture("0.98.0").Files[App])
}

func TestScopedRegistrationTamperAndLegacyCompatibility(t *testing.T) {
	for _, mode := range []string{"missing-machine-scope", "wrong-scope", "invalid-scope", "typed-scope", "foreign-root", "legacy-user"} {
		t.Run(mode, func(t *testing.T) {
			s := scopedFixture(t)
			scope := ScopeMachine
			if mode == "legacy-user" {
				scope = ScopeUser
			}
			if err := s.Install(scope, fixture("0.98.0"), []byte("MZ"), Options{}); err != nil {
				t.Fatal(err)
			}
			l, _ := s.Layout(scope)
			k, err := registry.OpenKey(registry.CURRENT_USER, l.RegistryPath, registry.SET_VALUE)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-machine-scope", "legacy-user":
				err = k.DeleteValue("InstallScope")
			case "wrong-scope":
				err = k.SetStringValue("InstallScope", string(ScopeUser))
			case "invalid-scope":
				err = k.SetStringValue("InstallScope", "future")
			case "typed-scope":
				err = k.SetDWordValue("InstallScope", 1)
			case "foreign-root":
				err = k.SetStringValue("InstallLocation", `\\invalid.example\share\MultiPan`)
			}
			k.Close()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "legacy-user" {
				putMarkerScope(t, l.Directory, "")
				if err = s.Install(scope, fixture("0.99.0"), []byte("MZ"), Options{}); err != nil {
					t.Fatal("legacy current-user upgrade refused", err)
				}
				if err = s.Uninstall(scope); err != nil {
					t.Fatal("legacy current-user uninstall refused", err)
				}
				return
			}
			marker, _ := os.ReadFile(filepath.Join(l.Directory, Marker))
			shortcut, _ := os.ReadFile(l.StartLink)
			if err = s.Install(scope, fixture("0.99.0"), []byte("MZ"), Options{}); err == nil {
				t.Fatal("tampered registration upgraded", mode)
			}
			if err = s.Uninstall(scope); err == nil {
				t.Fatal("tampered registration uninstalled", mode)
			}
			requireFile(t, filepath.Join(l.Directory, Marker), marker)
			requireFile(t, filepath.Join(l.Directory, App), fixture("0.98.0").Files[App])
			requireFile(t, l.StartLink, shortcut)
		})
	}
}
