package installation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// A first install has no previous version. Preserve its entire transaction in
// a sibling directory and free the fixed install path with one rename.
func (c *openedMachineCheckpoint) archiveFirstInstall(registryPath string) (MachineCheckpointRecovery, error) {
	if registered, err := (Integration{RegistryPath: registryPath}).RegisteredLocation(); err != nil || registered != "" {
		return MachineCheckpointRecovery{}, errors.New("首次安装存在登记或无法核对登记，未归档")
	}
	dir, stage, backup := c.locations[0], c.locations[1], c.locations[2]
	parent := filepath.Dir(dir)
	archive := ".multipan-recovered-" + strings.TrimPrefix(c.record.Stage, ".multipan-stage-")
	target := filepath.Join(parent, archive)
	if err := requireRecoveryAbsent(target); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	if err := c.verifyFirstInstallInventory(); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	if err := c.audit.Check(); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	policy := c.audit.policy
	before, err := policy.nodeHandle(c.pins.pinned[dir], true, true, machineMutationMask)
	if err != nil {
		return MachineCheckpointRecovery{}, err
	}

	// Windows can refuse a directory rename while files or descendant
	// directories are open. Keep the protected parent pinned and retain the
	// installation lease while exchanging the root pin for a rename handle.
	if err := c.file.Close(); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	c.file = nil
	for _, path := range []string{stage, backup, dir} {
		_ = windows.CloseHandle(c.pins.pinned[path])
		delete(c.pins.pinned, path)
	}
	root, err := openPinnedDirectoryWithAccess(dir, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.DELETE)
	if err != nil {
		return MachineCheckpointRecovery{}, err
	}
	file := os.NewFile(uintptr(root), dir)
	defer file.Close()
	after, err := policy.nodeHandle(root, true, true, machineMutationMask)
	if err != nil || before != after {
		return MachineCheckpointRecovery{}, errors.New("首次安装目录身份或权限已变化，未归档")
	}
	if err := renameFileInDirectory(file, c.pins.pinned[parent], archive); err != nil {
		return MachineCheckpointRecovery{}, fmt.Errorf("无法归档首次安装目录，检查点仍保留：%w", err)
	}
	if err := verifyRecoveryPath(file, target); err != nil {
		return MachineCheckpointRecovery{}, fmt.Errorf("无法确认首次安装归档身份：%w", err)
	}
	return MachineCheckpointRecovery{Recovered: true, ArchiveDirectory: archive}, nil
}

func (c *openedMachineCheckpoint) verifyFirstInstallInventory() error {
	seen := make(map[string]bool, len(c.names))
	for i, location := range c.locations {
		dir, err := os.Open(location)
		if err != nil {
			return err
		}
		limit := len(c.names) + 3
		entries, err := dir.ReadDir(limit + 1)
		_ = dir.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(entries) > limit {
			return errors.New("首次安装目录含额外内容，未归档")
		}
		for _, entry := range entries {
			name := entry.Name()
			if i == 0 && (name == machineCheckpointName || name == c.record.Stage || name == c.record.Backup) {
				// The caller already validated and pinned these objects.
				continue
			}
			if i == 2 || !slices.Contains(c.names, name) || seen[name] {
				return errors.New("首次安装目录含无法归属或重复的项目，未归档")
			}
			copy, err := c.holdRecoveryCopy(filepath.Join(location, name), name)
			if err != nil {
				return fmt.Errorf("核对首次安装文件 %s：%w", name, err)
			}
			if copy.file != nil {
				_ = copy.file.Close()
			}
			if copy.state != "staged" {
				return fmt.Errorf("首次安装文件 %s 与检查点不符，未归档", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != len(c.names) {
		return errors.New("首次安装缺少暂存文件，未归档")
	}
	return nil
}
