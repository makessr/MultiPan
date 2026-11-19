package installation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestCustomLocationValidationAndBinding(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile", "MultiPan")
	target := filepath.Join(root, "自选 安装", "MultiPan")
	if err := ValidateTarget(target, profile); err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(root)
	for _, dir := range []string{"", "relative", `C:relative`, volume + `\`, filepath.Join(root, "x") + `\..\MultiPan`, filepath.Join(root, "trailing."), filepath.Join(root, "trailing "), filepath.Join(root, "file:stream"), filepath.Join(root, "CON"), filepath.Join(root, "COM1.txt"), filepath.Join(root, "COM¹"), filepath.Join(root, "CONOUT$"), filepath.Join(root, "bad\x00"), `\\server\share\MultiPan`, `\\?\C:\MultiPan`, profile, filepath.Dir(profile), filepath.Join(profile, "app"), filepath.Join(root, strings.Repeat("a", 201))} {
		if ValidateTarget(dir, profile) == nil {
			t.Error("unsafe target accepted", dir)
		}
	}
	for _, protected := range []string{os.Getenv("SystemRoot"), os.Getenv("ProgramFiles"), os.Getenv("ProgramData")} {
		if protected != "" && ValidateTarget(filepath.Join(protected, "MultiPan"), profile) == nil {
			t.Fatal("protected target accepted")
		}
	}
	defaultDir := filepath.Join(root, "default")
	for _, tc := range []struct {
		requested, registered, self string
		uninstall, valid            bool
		want                        string
	}{
		{"", "", "setup.exe", false, true, defaultDir},
		{target, "", "setup.exe", false, true, target},
		{"", target, "setup.exe", false, true, target},
		{strings.ToUpper(target), target, "setup.exe", false, true, strings.ToUpper(target)},
		{defaultDir, target, "setup.exe", false, false, ""},
		{"", target, "setup.exe", true, true, target},
	} {
		dir, err := ResolveLocation(defaultDir, tc.requested, tc.registered, tc.self, tc.uninstall)
		if (err == nil) != tc.valid || tc.valid && !sameLocation(dir, tc.want) {
			t.Fatal(tc, dir, err)
		}
	}
	if err := Install(target, fixture("0.97.0"), []byte("MZ-setup"), Options{}); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(target, Uninstaller)
	if dir, err := ResolveLocation(defaultDir, "", "", self, true); err != nil || dir != target {
		t.Fatal("installed uninstaller lost own root", dir, err)
	}
	if _, err := ResolveLocation(defaultDir, defaultDir, "", self, true); err == nil {
		t.Fatal("uninstaller redirected")
	}
	if _, err := ResolveLocation(defaultDir, "", defaultDir, self, true); err == nil {
		t.Fatal("foreign registration accepted")
	}
	if _, err := ResolveLocation(defaultDir, "", "", filepath.Join(root, Uninstaller), true); err == nil {
		t.Fatal("unmarked uninstaller accepted")
	}
	if _, err := os.Stat(defaultDir); !os.IsNotExist(err) {
		t.Fatal("resolution mutated default root")
	}
}

func TestRegisteredCustomLocationLifecycle(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "apps with spaces", "MultiPan")
	i := Integration{RegistryPath: `Software\MultiPan\InstallerTests\` + Digest([]byte(root))[:24], StartLink: filepath.Join(root, "start", "MultiPan.lnk"), DesktopLink: filepath.Join(root, "desktop", "MultiPan.lnk")}
	defer registry.DeleteKey(registry.CURRENT_USER, i.RegistryPath)
	if location, err := i.RegisteredLocation(); err != nil || location != "" {
		t.Fatal(location, err)
	}
	if err := Install(target, fixture("0.96.0"), []byte("MZ-setup"), Options{}); err != nil {
		t.Fatal(err)
	}
	if w := i.Apply(target, "0.96.0", true); len(w) > 0 {
		t.Fatal(w)
	}
	location, err := i.RegisteredLocation()
	if err != nil || location != target {
		t.Fatal(location, err)
	}
	dir, err := ResolveLocation(filepath.Join(root, "default"), "", location, "setup.exe", false)
	if err != nil || dir != target {
		t.Fatal(dir, err)
	}
	if err = i.Preflight(dir); err != nil {
		t.Fatal(err)
	}
	if err = Install(dir, fixture("0.97.0"), []byte("MZ-new-setup"), Options{}); err != nil {
		t.Fatal(err)
	}
	if err = i.Preflight(filepath.Join(root, "other")); err == nil {
		t.Fatal("registration moved without uninstall")
	}
	if err = Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if w := i.Remove(dir); len(w) > 0 {
		t.Fatal(w)
	}
	if location, err = i.RegisteredLocation(); err != nil || location != "" {
		t.Fatal("registration left", location, err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, i.RegistryPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.SetStringValue("InstallLocation", filepath.Join(root, "unmarked")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	if _, err = i.RegisteredLocation(); err == nil {
		t.Fatal("stale unmarked registration accepted")
	}
	for _, bad := range []string{`\\untrusted-server\share\MultiPan`, `\\?\C:\MultiPan`, filepath.VolumeName(root) + `\`} {
		k, err := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		err = k.SetStringValue("InstallLocation", bad)
		k.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = i.RegisteredLocation(); err == nil {
			t.Fatal("unsafe registration accepted", bad)
		}
	}
}

func TestCustomTargetRejectsJunction(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	link := filepath.Join(root, "junction")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, actual)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture unavailable: %v %s", err, out)
	}
	defer os.Remove(link) // Remove the junction itself, never traverse its target.
	if ValidateTarget(filepath.Join(link, "MultiPan"), filepath.Join(root, "profile")) == nil {
		t.Fatal("junction target accepted")
	}
	if err := os.WriteFile(filepath.Join(actual, "sentinel"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(filepath.Join(link, "MultiPan"), fixture("0.97.0"), []byte("MZ"), Options{}); err == nil {
		t.Fatal("install traversed junction")
	}
	if data, err := os.ReadFile(filepath.Join(actual, "sentinel")); err != nil || string(data) != "keep" {
		t.Fatal("junction target changed", err)
	}
}
