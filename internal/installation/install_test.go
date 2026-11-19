package installation

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(v string) Bundle {
	return Bundle{Version: v, Files: map[string][]byte{App: []byte("MZ-app-" + v), "TRIAL-GUIDE.txt": []byte("guide " + v), "THIRD_PARTY_NOTICES.md": []byte("notices")}}
}
func bundleBytes(b Bundle, mutate func(map[string][]byte)) []byte {
	files := map[string][]byte{}
	m := Manifest{Owner: Owner, Version: b.Version, Hashes: map[string]string{}}
	for n, d := range b.Files {
		files[n] = d
		m.Hashes[n] = Digest(d)
	}
	files["manifest.json"], _ = json.Marshal(m)
	if mutate != nil {
		mutate(files)
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for n, d := range files {
		w, _ := z.Create(n)
		w.Write(d)
	}
	z.Close()
	return out.Bytes()
}
func TestInstallUpgradeUninstallPreservesUnknownAndProfile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "program")
	profile := filepath.Join(root, "profile")
	os.MkdirAll(profile, 0700)
	state := filepath.Join(profile, "state.enc")
	os.WriteFile(state, []byte("fixture encrypted state"), 0600)
	b, e := Decode(bundleBytes(fixture("0.78.0"), nil))
	if e != nil {
		t.Fatal(e)
	}
	if e = Install(dir, b, []byte("MZ-setup"), Options{}); e != nil {
		t.Fatal(e)
	}
	extra := filepath.Join(dir, "user-notes.txt")
	os.WriteFile(extra, []byte("keep user work"), 0600)
	if e = Install(dir, fixture("0.79.0"), []byte("MZ-new-setup"), Options{}); e != nil {
		t.Fatal(e)
	}
	m, e := readMarker(dir)
	if e != nil || m.Version != "0.79.0" {
		t.Fatal(m, e)
	}
	if e = Install(dir, fixture("0.78.0"), []byte("MZ-setup"), Options{}); e == nil {
		t.Fatal("downgrade allowed")
	}
	if e = Uninstall(dir); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{extra, state} {
		if _, e = os.Stat(p); e != nil {
			t.Fatal("user data lost", p, e)
		}
	}
	if _, e = os.Stat(filepath.Join(dir, App)); !os.IsNotExist(e) {
		t.Fatal("app left", e)
	}
}
func TestInstallRejectsForeignModifiedAndRollsBack(t *testing.T) {
	for _, mode := range []string{"foreign", "modified", "commit-failure", "backup-failure", "precommit"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "program")
			if mode == "foreign" {
				os.MkdirAll(dir, 0700)
				os.WriteFile(filepath.Join(dir, App), []byte("foreign"), 0600)
				if e := Install(dir, fixture("0.78.0"), []byte("MZ"), Options{}); e == nil {
					t.Fatal("foreign overwritten")
				}
				return
			}
			if e := Install(dir, fixture("0.78.0"), []byte("MZ"), Options{}); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(filepath.Join(dir, App))
			opts := Options{}
			if mode == "modified" {
				os.WriteFile(filepath.Join(dir, App), []byte("MZ-user-modified"), 0600)
				if e := Uninstall(dir); e == nil {
					t.Fatal("modified deleted")
				}
			}
			if mode == "precommit" {
				opts.BeforeCommit = func() error { return errors.New("locked fixture") }
			}
			if mode == "commit-failure" || mode == "backup-failure" {
				count := 0
				opts.Rename = func(a, b string) error {
					count++
					if mode == "backup-failure" && count == 2 || mode == "commit-failure" && strings.Contains(a, ".multipan-stage-") && filepath.Base(a) == "TRIAL-GUIDE.txt" {
						return errors.New("injected commit failure")
					}
					return os.Rename(a, b)
				}
			}
			if e := Install(dir, fixture("0.79.0"), []byte("MZ-new"), opts); e == nil {
				t.Fatal("expected rejection")
			}
			after, _ := os.ReadFile(filepath.Join(dir, App))
			if mode != "modified" && !bytes.Equal(before, after) {
				t.Fatal("old app not restored")
			}
			m, e := readMarker(dir)
			if e != nil || m.Version != "0.78.0" {
				t.Fatal(m, e)
			}
		})
	}
}
func TestBundleValidationAndDirectoryGuards(t *testing.T) {
	for _, mutate := range []func(map[string][]byte){func(m map[string][]byte) { m[App] = []byte("corrupt") }, func(m map[string][]byte) { m["../state.enc"] = []byte("bad") }, func(m map[string][]byte) { delete(m, "TRIAL-GUIDE.txt") }, func(m map[string][]byte) { m["manifest.json"] = []byte(`{}`) }} {
		if _, e := Decode(bundleBytes(fixture("0.78.0"), mutate)); e == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	for _, dir := range []string{"relative", filepath.VolumeName(t.TempDir()) + string(os.PathSeparator)} {
		if e := Install(dir, fixture("0.78.0"), []byte("MZ"), Options{}); e == nil {
			t.Fatal("unsafe path")
		}
	}
	target := filepath.Join(t.TempDir(), "target")
	os.MkdirAll(target, 0700)
	link := filepath.Join(t.TempDir(), "link")
	if os.Symlink(target, link) == nil {
		if e := Install(link, fixture("0.78.0"), []byte("MZ"), Options{}); e == nil {
			t.Fatal("link accepted")
		}
	}
}
func TestProfileLockAndSandboxIntegration(t *testing.T) {
	root := t.TempDir()
	unlock, e := LockProfile(root)
	if e != nil {
		t.Fatal(e)
	}
	if release, e := LockProfile(root); e == nil {
		release()
		t.Fatal("running profile allowed")
	}
	unlock()
	unlock()
	release, e := LockProfile(root)
	if e != nil {
		t.Fatal(e)
	}
	release()
	dir := filepath.Join(root, "program")
	if err := Install(dir, fixture("0.78.0"), []byte("MZ-setup"), Options{}); err != nil {
		t.Fatal(err)
	}
	name := Digest([]byte(root))[:24]
	i := Integration{RegistryPath: `Software\MultiPan\InstallerTests\` + name, StartLink: filepath.Join(root, "start", "MultiPan.lnk"), DesktopLink: filepath.Join(root, "desktop", "MultiPan.lnk")}
	defer registry.DeleteKey(registry.CURRENT_USER, i.RegistryPath)
	if e = i.Preflight(dir); e != nil {
		t.Fatal(e)
	}
	if warnings := i.Apply(dir, "0.78.0", true); len(warnings) > 0 {
		t.Fatal(warnings)
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE)
	if e != nil {
		t.Fatal(e)
	}
	v, _, e := k.GetStringValue("DisplayVersion")
	k.Close()
	if e != nil || v != "0.78.0" {
		t.Fatal(v, e)
	}
	other := filepath.Join(root, "other.exe")
	os.WriteFile(other, []byte("MZ"), 0600)
	if e = Shortcut(i.DesktopLink, other); e != nil {
		t.Fatal(e)
	}
	if e = i.Preflight(dir); e == nil {
		t.Fatal("foreign shortcut overwritten")
	}
	if warnings := i.Remove(dir); len(warnings) > 0 {
		t.Fatal(warnings)
	}
	if _, e = os.Stat(i.DesktopLink); e != nil {
		t.Fatal("foreign shortcut deleted")
	}
	if _, e = os.Stat(i.StartLink); !os.IsNotExist(e) {
		t.Fatal("owned shortcut left")
	}
	if k, e := registry.OpenKey(registry.CURRENT_USER, i.RegistryPath, registry.QUERY_VALUE); e == nil {
		k.Close()
		t.Fatal("owned registration left")
	}
}
