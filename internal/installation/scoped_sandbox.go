package installation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const ScopedSandboxMarker = "MultiPan scoped installer sandbox v1\n"

// ScopedSandbox is an isolated development capability, not a machine installer.
// No method uses HKLM, common shell folders, elevation or production profiles.
// The root must already be marked; this constructor never creates directories.
type ScopedSandbox struct{ root string }

type SandboxLayout struct {
	Directory    string
	RegistryPath string
	StartLink    string
	DesktopLink  string
}

func OpenScopedSandbox(root string) (*ScopedSandbox, error) {
	s := &ScopedSandbox{root: root}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ScopedSandbox) validate() error {
	if s == nil || !filepath.IsAbs(s.root) || filepath.Clean(s.root) != s.root || !strings.HasPrefix(filepath.Base(s.root), "multipan-setup-check-") {
		return errors.New("安装范围验证只允许带指定前缀的规范临时目录")
	}
	temp, err := filepath.Abs(os.TempDir())
	if err != nil || sameLocation(temp, s.root) || !withinLocation(temp, s.root) {
		return errors.New("安装范围验证目录必须位于本机临时目录内")
	}
	if err = ValidateTarget(s.root, ""); err != nil {
		return err
	}
	marker := filepath.Join(s.root, ".multipan-setup-test")
	if err = noLink(marker); err != nil {
		return err
	}
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(ScopedSandboxMarker)) {
		return errors.New("缺少本轮安装范围隔离验证标识")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != ScopedSandboxMarker {
		return errors.New("安装范围隔离验证标识不匹配")
	}
	return nil
}

func (s *ScopedSandbox) Layout(scope Scope) (SandboxLayout, error) {
	if !scope.explicit() {
		return SandboxLayout{}, errors.New("验证范围必须明确指定 current-user 或 machine")
	}
	if err := s.validate(); err != nil {
		return SandboxLayout{}, err
	}
	namespace := Digest([]byte(strings.ToLower(s.root)))[:32]
	return SandboxLayout{
		Directory:    filepath.Join(s.root, string(scope), "MultiPan"),
		RegistryPath: `Software\MultiPan\InstallerTests\` + namespace + `\` + string(scope),
		StartLink:    filepath.Join(s.root, "shell", string(scope), "start", "MultiPan.lnk"),
		DesktopLink:  filepath.Join(s.root, "shell", string(scope), "desktop", "MultiPan.lnk"),
	}, nil
}

func (s *ScopedSandbox) integration(scope Scope) (SandboxLayout, Integration, error) {
	l, err := s.Layout(scope)
	if err != nil {
		return l, Integration{}, err
	}
	if err = ValidateTarget(l.Directory, filepath.Join(s.root, "profiles")); err != nil {
		return l, Integration{}, err
	}
	if scope == ScopeMachine {
		if err = checkMachineInterrupted(l.Directory); err != nil {
			return l, Integration{}, err
		}
	}
	// Check shortcut ancestors too, not just the payload root. Never follow a
	// test-root junction into real user/common shell folders.
	for _, path := range []string{l.StartLink, l.DesktopLink} {
		if err = safeDirectory(filepath.Dir(path)); err != nil {
			return l, Integration{}, err
		}
		if err = noLink(path); err != nil {
			return l, Integration{}, err
		}
	}
	i := Integration{RegistryPath: l.RegistryPath, StartLink: l.StartLink, DesktopLink: l.DesktopLink, expectedScope: scope, boundDirectory: l.Directory}
	registered, err := i.RegisteredLocation()
	if err != nil {
		return l, i, err
	}
	if registered != "" && !sameLocation(registered, l.Directory) {
		return l, i, errors.New("隔离安装登记指向固定验证目录之外，未改动")
	}
	return l, i, nil
}

func (s *ScopedSandbox) Install(scope Scope, b Bundle, self []byte, opts Options) error {
	l, i, err := s.integration(scope)
	if err != nil {
		return err
	}
	if err = i.Preflight(l.Directory); err != nil {
		return err
	}
	if err = WritableManaged(l.Directory); err != nil {
		return err
	}
	// Revalidate the sandbox, marker and bindings immediately before commit,
	// including after an injected test failure/stop hook has returned.
	hook := opts.BeforeCommit
	opts.BeforeCommit = func() error {
		if hook != nil {
			if err := hook(); err != nil {
				return err
			}
		}
		_, _, err := s.integration(scope)
		if err != nil {
			return err
		}
		return i.Preflight(l.Directory)
	}
	if err = installForScope(l.Directory, b, self, opts, scope); err != nil {
		return err
	}
	if warnings := i.Apply(l.Directory, b.Version, true); len(warnings) > 0 {
		return errors.New(strings.Join(warnings, "；"))
	}
	return nil
}

func (s *ScopedSandbox) Uninstall(scope Scope) error {
	l, i, err := s.integration(scope)
	if err != nil {
		return err
	}
	if err = i.Preflight(l.Directory); err != nil {
		return err
	}
	if err = WritableManaged(l.Directory); err != nil {
		return err
	}
	if err = uninstallForScope(l.Directory, scope); err != nil {
		return err
	}
	if warnings := i.Remove(l.Directory); len(warnings) > 0 {
		return errors.New(strings.Join(warnings, "；"))
	}
	return nil
}
