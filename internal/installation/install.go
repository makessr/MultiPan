package installation

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"multipan/internal/appguard"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const Owner = "app.multipan.desktop"
const Marker = ".multipan-install.json"
const App = "MultiPan.exe"
const Uninstaller = "MultiPan-Uninstall.exe"

var PayloadNames = []string{App, "TRIAL-GUIDE.txt", "THIRD_PARTY_NOTICES.md"}

type Manifest struct {
	Owner   string            `json:"owner"`
	Version string            `json:"version"`
	Hashes  map[string]string `json:"hashes"`
	Scope   Scope             `json:"scope,omitempty"`
}
type Bundle struct {
	Version string
	Files   map[string][]byte
}
type Options struct {
	BeforeCommit        func() error
	Rename              func(string, string) error
	protected           *protectedMachineInstall
	beforeProtectedMove func(from, to string) error
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func version(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, errors.New("版本格式无效")
	}
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil || n < 0 || strconv.Itoa(n) != p {
			return out, errors.New("版本格式无效")
		}
		out[i] = n
	}
	return out, nil
}
func validVersion(v string) bool { _, e := version(v); return e == nil }
func older(a, b string) bool {
	av, _ := version(a)
	bv, _ := version(b)
	for i := range av {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}
func managed(name string) bool {
	for _, n := range append(append([]string{}, PayloadNames...), Uninstaller) {
		if n == name {
			return true
		}
	}
	return false
}
func Decode(data []byte) (Bundle, error) {
	out := Bundle{Files: map[string][]byte{}}
	if len(data) > 100<<20 {
		return out, errors.New("安装载荷过大")
	}
	r, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return out, errors.New("安装载荷损坏")
	}
	var manifest Manifest
	seen := map[string]bool{}
	for _, f := range r.File {
		if seen[f.Name] || f.Name != "manifest.json" && !managed(f.Name) || f.Name == Uninstaller || f.FileInfo().IsDir() || f.UncompressedSize64 > 80<<20 {
			return out, errors.New("安装载荷含重复或非预期文件")
		}
		seen[f.Name] = true
		stream, e := f.Open()
		if e != nil {
			return out, e
		}
		b, e := io.ReadAll(io.LimitReader(stream, 80<<20+1))
		stream.Close()
		if e != nil || len(b) > 80<<20 {
			return out, errors.New("安装载荷读取失败")
		}
		if f.Name == "manifest.json" {
			if json.Unmarshal(b, &manifest) != nil {
				return out, errors.New("安装载荷清单损坏")
			}
		} else {
			out.Files[f.Name] = b
		}
	}
	if manifest.Owner != Owner || manifest.Scope != "" || !validVersion(manifest.Version) || len(out.Files) != len(PayloadNames) || len(manifest.Hashes) != len(PayloadNames) {
		return out, errors.New("安装载荷清单不完整")
	}
	for _, n := range PayloadNames {
		b, ok := out.Files[n]
		if !ok || len(b) == 0 || manifest.Hashes[n] != Digest(b) {
			return out, errors.New("安装载荷摘要不符")
		}
	}
	if !bytes.HasPrefix(out.Files[App], []byte("MZ")) {
		return out, errors.New("应用程序格式无效")
	}
	out.Version = manifest.Version
	return out, nil
}
func readMarker(dir string) (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(filepath.Join(dir, Marker))
	if e != nil {
		return m, e
	}
	if json.Unmarshal(b, &m) != nil || !m.Scope.valid() || m.Owner != Owner || !validVersion(m.Version) || len(m.Hashes) != len(PayloadNames)+1 {
		return m, errors.New("安装目录标识无效，未覆盖已有文件")
	}
	for n, h := range m.Hashes {
		raw, e := hex.DecodeString(h)
		if !managed(n) || e != nil || len(raw) != 32 {
			return m, errors.New("安装清单无效，未覆盖已有文件")
		}
	}
	return m, nil
}
func noLink(path string) error {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("安装路径不能包含链接")
	}
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	attrs, e := windows.GetFileAttributes(name)
	if e != nil {
		return e
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("安装路径不能包含重解析点或目录联接")
	}
	return nil
}
func safeDirectory(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Dir(dir) == dir {
		return errors.New("安装目录无效")
	}
	for p := dir; ; p = filepath.Dir(p) {
		if e := noLink(p); e != nil {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func verifyOwned(dir string, m Manifest) error {
	for n, hash := range m.Hashes {
		p := filepath.Join(dir, n)
		if e := noLink(p); e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if Digest(b) != hash {
			return fmt.Errorf("安装目录中的 %s 已被手动修改，请先保留并核对；未覆盖或删除", n)
		}
	}
	return nil
}
func cleanup(dir string, names []string) {
	for _, n := range names {
		_ = os.Remove(filepath.Join(dir, n))
	}
	_ = os.Remove(dir)
}
func writeFile(path string, data []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}

// Install stages a verified payload, backs up only files owned by the previous
// installation and rolls back on commit failure. It never traverses user data.
func Install(dir string, b Bundle, self []byte, opts Options) error {
	return installForScope(dir, b, self, opts, ScopeUser)
}

// Machine writes are deliberately not exported. The only machine caller in
// this phase is ScopedSandbox, which binds every operation to a marked temp
// root. Production elevation, ACLs and cross-session coordination are separate.
func installForScope(dir string, b Bundle, self []byte, opts Options, scope Scope) error {
	if !scope.explicit() {
		return errors.New("安装范围无效")
	}
	if e := safeDirectory(dir); e != nil {
		return e
	}
	lease, err := appguard.AcquireInstallation(dir)
	if err != nil {
		return err
	}
	defer lease.Close()
	if scope == ScopeMachine {
		if err = checkMachineInterrupted(dir); err != nil {
			return err
		}
	}
	if !validVersion(b.Version) || !bytes.HasPrefix(self, []byte("MZ")) || len(b.Files) != len(PayloadNames) {
		return errors.New("安装程序或载荷无效")
	}
	for _, n := range PayloadNames {
		if len(b.Files[n]) == 0 {
			return errors.New("安装载荷缺文件")
		}
	}
	if opts.protected != nil {
		if scope != ScopeMachine {
			return errors.New("受保护的安装流程仅用于机器范围")
		}
		defer opts.protected.close()
		if err = opts.protected.prepare(dir); err != nil {
			return err
		}
	}
	old, e := readMarker(dir)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if exists {
		if old.Scope.effective() != scope {
			return errors.New("安装标识范围不一致，不允许覆盖或迁移另一范围的安装")
		}
		if older(b.Version, old.Version) {
			return errors.New("不支持用旧安装包降级，请保留现有版本")
		}
		if e = verifyOwned(dir, old); e != nil {
			return e
		}
	} else if rows, err := os.ReadDir(dir); err == nil && len(rows) > 0 {
		return errors.New("目标目录已有其他文件，缺少本程序安装标识；未覆盖")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if e = WritableManaged(dir); e != nil {
		return e
	}
	makeTemp := func(prefix string) (string, error) { return os.MkdirTemp(dir, prefix) }
	writeStage := writeFile
	cleanTemp := cleanup
	if opts.protected != nil {
		makeTemp = opts.protected.makeTemp
		writeStage = opts.protected.writeStage
		cleanTemp = opts.protected.cleanup
	} else if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	stage, e := makeTemp(".multipan-stage-")
	if e != nil {
		return e
	}
	backup, e := makeTemp(".multipan-backup-")
	if e != nil {
		cleanTemp(stage, nil)
		return e
	}
	names := append(append([]string{}, PayloadNames...), Uninstaller)
	names = append(names, Marker)
	keepBackup := false
	defer func() {
		if !keepBackup || opts.protected == nil {
			cleanTemp(stage, names)
		}
	}()
	defer func() {
		if !keepBackup {
			cleanTemp(backup, names)
		}
	}()
	files := map[string][]byte{}
	for n, data := range b.Files {
		files[n] = data
	}
	files[Uninstaller] = self
	m := Manifest{Owner: Owner, Version: b.Version, Hashes: map[string]string{}, Scope: scope}
	for n, data := range files {
		m.Hashes[n] = Digest(data)
	}
	marker, _ := json.MarshalIndent(m, "", "  ")
	files[Marker] = marker
	for _, n := range names {
		if e = writeStage(filepath.Join(stage, n), files[n]); e != nil {
			return e
		}
	}
	if opts.BeforeCommit != nil {
		if e = opts.BeforeCommit(); e != nil {
			return e
		}
	}
	// Staging/confirmation can take time. Do not replace files if ownership,
	// scope or path changed while preparing the payload.
	if e = lease.Check(dir); e != nil {
		return e
	}
	if e = safeDirectory(dir); e != nil {
		return e
	}
	latest, markerErr := readMarker(dir)
	if exists {
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(latest)
		if markerErr != nil || !bytes.Equal(a, b) {
			return errors.New("准备期间安装标识发生变化，未替换程序文件")
		}
		if e = verifyOwned(dir, latest); e != nil {
			return e
		}
	} else {
		if !os.IsNotExist(markerErr) {
			return errors.New("准备期间出现安装标识，未替换程序文件")
		}
		for _, n := range names {
			if _, err := os.Lstat(filepath.Join(dir, n)); !os.IsNotExist(err) {
				return errors.New("准备期间出现同名程序文件，未替换")
			}
		}
	}
	if e = WritableManaged(dir); e != nil {
		return e
	}
	if opts.protected != nil {
		if e = opts.protected.check(stage, backup, names); e != nil {
			return e
		}
		var commitErr error
		keepBackup, commitErr = opts.protected.commit(stage, backup, names, old, opts.beforeProtectedMove)
		return commitErr
	}
	rename := opts.Rename
	if rename == nil {
		rename = os.Rename
	}
	backed, installed := []string{}, []string{}
	rollback := func(cause error) error {
		failed := false
		for _, n := range installed {
			path := filepath.Join(dir, n)
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				failed = true
			}
		}
		for _, n := range backed {
			if e := os.Rename(filepath.Join(backup, n), filepath.Join(dir, n)); e != nil {
				failed = true
			}
		}
		if failed {
			keepBackup = true
			return fmt.Errorf("安装失败且回滚未完整完成，请保留目录和备份：%w", cause)
		}
		return fmt.Errorf("安装未完成，已恢复原文件：%w", cause)
	}
	for _, n := range names {
		source := filepath.Join(dir, n)
		if _, e = os.Stat(source); os.IsNotExist(e) {
			continue
		}
		if e = rename(source, filepath.Join(backup, n)); e != nil {
			return rollback(e)
		}
		backed = append(backed, n)
	}
	for _, n := range names {
		if e = rename(filepath.Join(stage, n), filepath.Join(dir, n)); e != nil {
			return rollback(e)
		}
		installed = append(installed, n)
	}
	return nil
}
func Uninstall(dir string) error {
	return uninstallForScope(dir, ScopeUser)
}

func uninstallForScope(dir string, scope Scope) error {
	if !scope.explicit() {
		return errors.New("卸载范围无效")
	}
	if e := safeDirectory(dir); e != nil {
		return e
	}
	lease, err := appguard.AcquireInstallation(dir)
	if err != nil {
		return err
	}
	defer lease.Close()
	if scope == ScopeMachine {
		if err = checkMachineInterrupted(dir); err != nil {
			return err
		}
	}
	m, e := readMarkerForScope(dir, scope)
	if e != nil {
		return errors.New("没有找到有效安装标识，未删除文件")
	}
	if e = verifyOwned(dir, m); e != nil {
		return e
	}
	if e = lease.Check(dir); e != nil {
		return e
	}
	if e = WritableManaged(dir); e != nil {
		return e
	}
	names := []string{}
	for n := range m.Hashes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if e = os.Remove(filepath.Join(dir, n)); e != nil && !os.IsNotExist(e) {
			return fmt.Errorf("卸载未完成，已保留安装标识以便重试：%w", e)
		}
	}
	rows, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.Name() != Marker {
			return nil
		}
	} // Keep ownership marker alongside preserved user files for a later reinstall.
	if e = os.Remove(filepath.Join(dir, Marker)); e != nil {
		return e
	}
	_ = os.Remove(dir)
	return nil
}
