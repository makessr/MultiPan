package installation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"multipan/internal/appguard"
)

// UninstallProtectedMachine exercises handle-based uninstall in an explicitly
// marked sandbox. Registration and shortcuts are the sandbox's test entries.
func (s *ScopedSandbox) UninstallProtectedMachine() error {
	layout, integration, err := s.integration(ScopeMachine)
	if err != nil {
		return err
	}
	dir := layout.Directory
	lease, err := appguard.AcquireInstallation(dir)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := checkMachineInterrupted(dir); err != nil {
		return err
	}
	if err := integration.Preflight(dir); err != nil {
		return err
	}

	protected, err := s.protectedMachine(layout)
	if err != nil {
		return err
	}
	defer protected.close()
	if err := protected.prepare(dir, false); err != nil {
		return err
	}
	marker, err := readMarkerForScope(dir, ScopeMachine)
	if err != nil {
		return errors.New("缺少可核对的系统级安装标识")
	}
	if err := protected.snapshot.Check(); err != nil {
		return err
	}
	if err := lease.Check(dir); err != nil {
		return err
	}
	if err := protected.removePayload(marker); err != nil {
		return err
	}
	if warnings := integration.Remove(dir); len(warnings) > 0 {
		return errors.New(strings.Join(warnings, "；"))
	}

	// Retain the ownership marker if untracked content remains, allowing a
	// later reinstall without taking ownership of that content.
	root, err := os.Open(dir)
	if err != nil {
		return err
	}
	rows, err := root.ReadDir(2)
	_ = root.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(rows) == 1 && rows[0].Name() == Marker {
		f, err := protected.holdMarker(marker)
		if err != nil {
			return err
		}
		err = disposeMachineFile(f)
		closeErr := f.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return fmt.Errorf("卸载标识清理未完成：%w", err)
		}
	}
	return nil
}

func (p *protectedMachineInstall) holdMarker(marker Manifest) (*os.File, error) {
	path := filepath.Join(p.dir, Marker)
	for _, entry := range p.snapshot.entries {
		if entry.path == path && !entry.before.Missing {
			return p.hold(path, entry.before, "", &marker)
		}
	}
	return nil, errors.New("系统级安装标识已变化")
}

func (p *protectedMachineInstall) removePayload(marker Manifest) error {
	proof, err := p.holdMarker(marker)
	if err != nil {
		return err
	}
	defer proof.Close()

	var held []heldMachineFile
	defer func() {
		for _, item := range held {
			if item.file != nil {
				_ = item.file.Close()
			}
		}
	}()
	for _, entry := range p.snapshot.entries {
		name := filepath.Base(entry.path)
		if entry.directory || entry.before.Missing || !managed(name) {
			continue
		}
		f, err := p.hold(entry.path, entry.before, marker.Hashes[name], nil)
		if err != nil {
			return fmt.Errorf("卸载前无法独占核对 %s：%w", name, err)
		}
		held = append(held, heldMachineFile{file: f, name: name})
	}

	for i := range held {
		item := &held[i]
		// Use the verified, non-following handle for deletion. On a partial
		// delete the marker remains, so retry can verify all remaining files.
		if err := disposeMachineFile(item.file); err != nil {
			return fmt.Errorf("未能删除 %s，已保留安装标识，可重试：%w", item.name, err)
		}
		err := item.file.Close()
		item.file = nil
		if err != nil {
			return fmt.Errorf("未能确认 %s 已关闭，已保留安装标识，可重试：%w", item.name, err)
		}
	}
	return nil
}
