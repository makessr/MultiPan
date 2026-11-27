package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/windows"

	"multipan/internal/appguard"
)

type MachineCheckpointRecovery struct {
	Recovered        bool   `json:"recovered"`
	PreviousVersion  string `json:"previousVersion,omitempty"`
	ArchiveDirectory string `json:"archiveDirectory,omitempty"`
}

type recoveryCopy struct {
	file  *os.File
	state string
}

// RecoverMachineCheckpoint resolves an interrupted transaction in a marked
// test sandbox and retains the evidence.
func (s *ScopedSandbox) RecoverMachineCheckpoint() (MachineCheckpointRecovery, error) {
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		return MachineCheckpointRecovery{}, err
	}
	if checkMachineInterrupted(layout.Directory) == nil {
		return MachineCheckpointRecovery{}, nil
	}
	lease, err := appguard.AcquireInstallation(layout.Directory)
	if err != nil {
		return MachineCheckpointRecovery{}, err
	}
	defer lease.Close()
	c, err := s.openMachineCheckpoint(true)
	if err != nil || c == nil {
		return MachineCheckpointRecovery{}, err
	}
	defer c.close()
	dir, stage, backup := c.locations[0], c.locations[1], c.locations[2]
	if c.pins.pinned[stage] == 0 || c.pins.pinned[backup] == 0 {
		return MachineCheckpointRecovery{}, errors.New("暂存或备份目录缺失，无法安全恢复")
	}
	if c.record.Previous.Version == "" {
		if err := lease.Check(dir); err != nil {
			return MachineCheckpointRecovery{}, err
		}
		return c.archiveFirstInstall(layout.RegistryPath)
	}
	archived := filepath.Join(backup, machineCheckpointName)
	if err := requireRecoveryAbsent(archived); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	if err := c.audit.Check(); err != nil {
		return MachineCheckpointRecovery{}, err
	}

	type recoveryEntry struct {
		name       string
		evacuate   *os.File
		restore    *os.File
		wantAtRoot *os.File
	}
	var plan []recoveryEntry
	var held []*os.File
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for _, name := range c.names {
		var copies [3]recoveryCopy
		for i, location := range c.locations {
			copy, err := c.holdRecoveryCopy(filepath.Join(location, name), name)
			if err != nil {
				return MachineCheckpointRecovery{}, fmt.Errorf("核对 %s：%w", name, err)
			}
			copies[i] = copy
			if copy.file != nil {
				held = append(held, copy.file)
			}
		}
		root, staged, backedUp := copies[0], copies[1], copies[2]
		original := slices.Contains(c.record.Original, name)
		previous := func(copy recoveryCopy) bool { return copy.state == "previous" || copy.state == "both" }
		next := func(copy recoveryCopy) bool { return copy.state == "staged" || copy.state == "both" }
		if (staged.file != nil && !next(staged)) || (backedUp.file != nil && (!original || !previous(backedUp))) {
			return MachineCheckpointRecovery{}, fmt.Errorf("%s 的暂存或备份与检查点不匹配", name)
		}
		entry := recoveryEntry{name: name}
		switch {
		case root.file == nil:
		case original && previous(root) && (root.state != "both" || backedUp.file == nil || staged.file != nil):
			entry.wantAtRoot = root.file
		case next(root) && staged.file == nil:
			entry.evacuate = root.file
		default:
			return MachineCheckpointRecovery{}, fmt.Errorf("%s 的安装位置有冲突，无法安全恢复", name)
		}
		if original && entry.wantAtRoot == nil {
			if backedUp.file == nil {
				return MachineCheckpointRecovery{}, fmt.Errorf("%s 的原文件缺失，无法安全恢复", name)
			}
			entry.restore = backedUp.file
			entry.wantAtRoot = backedUp.file
		}
		plan = append(plan, entry)
	}
	if err := lease.Check(dir); err != nil {
		return MachineCheckpointRecovery{}, err
	}
	for _, entry := range plan {
		if entry.evacuate != nil {
			if err := renameMachineFile(entry.evacuate, c.pins.pinned[stage], entry.name); err != nil {
				return MachineCheckpointRecovery{}, fmt.Errorf("无法退回暂存文件 %s，检查点仍可用于恢复：%w", entry.name, err)
			}
		}
		if entry.restore != nil {
			if err := renameMachineFile(entry.restore, c.pins.pinned[dir], entry.name); err != nil {
				return MachineCheckpointRecovery{}, fmt.Errorf("无法恢复原文件 %s，检查点仍可用于恢复：%w", entry.name, err)
			}
		}
	}

	// Verify the directory entries still refer to the inspected file identities.
	// GetFileInformationByHandle works on the same exclusive file handles.
	for _, entry := range plan {
		path := filepath.Join(dir, entry.name)
		if entry.wantAtRoot == nil {
			if err := requireRecoveryAbsent(path); err != nil {
				return MachineCheckpointRecovery{}, err
			}
			continue
		}
		if err := verifyRecoveryPath(entry.wantAtRoot, path); err != nil {
			return MachineCheckpointRecovery{}, err
		}
	}
	if err := renameFileInDirectory(c.file, c.pins.pinned[backup], machineCheckpointName); err != nil {
		return MachineCheckpointRecovery{}, fmt.Errorf("原文件已就位，无法归档检查点：%w", err)
	}
	return MachineCheckpointRecovery{Recovered: true, PreviousVersion: c.record.Previous.Version}, nil
}

func (c *openedMachineCheckpoint) holdRecoveryCopy(path, name string) (recoveryCopy, error) {
	file, err := openMachineFile(path)
	if os.IsNotExist(err) {
		return recoveryCopy{}, nil
	}
	if err != nil {
		return recoveryCopy{}, err
	}
	if _, err = c.audit.policy.nodeHandle(windows.Handle(file.Fd()), false, false, machineMutationMask); err == nil {
		var state string
		state, err = machineCheckpointFileState(file, name, c.record)
		if err == nil {
			return recoveryCopy{file: file, state: state}, nil
		}
	}
	_ = file.Close()
	return recoveryCopy{}, err
}

func requireRecoveryAbsent(path string) error {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("%s 未能确认空缺，检查点仍保留", filepath.Base(path))
	}
	return nil
}

func verifyRecoveryPath(file *os.File, path string) error {
	handle := windows.Handle(file.Fd())
	var held windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &held); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Metadata-only access does not conflict with the exclusive data handle.
	observed, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(observed)
	var found windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(observed, &found); err != nil {
		return err
	}
	if held.VolumeSerialNumber != found.VolumeSerialNumber || held.FileIndexHigh != found.FileIndexHigh ||
		held.FileIndexLow != found.FileIndexLow || found.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s 恢复后的文件身份不匹配，检查点仍保留", filepath.Base(path))
	}
	return nil
}
