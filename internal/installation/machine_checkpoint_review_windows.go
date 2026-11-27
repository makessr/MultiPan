package installation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

type MachineCheckpointReview struct {
	Present         bool                    `json:"present"`
	PreviousVersion string                  `json:"previousVersion,omitempty"`
	Files           []MachineCheckpointFile `json:"files,omitempty"`
}

type MachineCheckpointFile struct {
	Name            string `json:"name"`
	OriginalPresent bool   `json:"originalPresent"`
	Installed       string `json:"installed"`
	Staging         string `json:"staging"`
	Backup          string `json:"backup"`
}

type openedMachineCheckpoint struct {
	audit     *machineSecuritySnapshot
	pins      *protectedMachineInstall
	file      *os.File
	record    machineCheckpoint
	locations []string
	names     []string
}

func (c *openedMachineCheckpoint) close() {
	if c.file != nil {
		_ = c.file.Close()
	}
	c.pins.close()
}

func (s *ScopedSandbox) openMachineCheckpoint(recovering bool) (_ *openedMachineCheckpoint, err error) {
	layout, err := s.Layout(ScopeMachine)
	if err != nil {
		return nil, err
	}
	dir := layout.Directory
	if err := ValidateTarget(dir, filepath.Join(s.root, "profiles")); err != nil {
		return nil, err
	}
	if checkMachineInterrupted(dir) == nil {
		return nil, nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	policy := sandboxMachinePolicy(user.User.Sid.String())
	audit, err := captureMachineSecurityAtFloor(filepath.Dir(dir), dir, policy, s.root)
	if err != nil {
		return nil, err
	}
	pins := protectedMachineInstall{pinned: map[string]windows.Handle{}}
	defer func() {
		if err != nil {
			pins.close()
		}
	}()
	for _, node := range audit.entries {
		if node.directory {
			if err := pins.pin(node.path); err != nil {
				return nil, err
			}
		}
	}
	if err := audit.Check(); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, machineCheckpointName)
	open := openMachineReviewFile
	if recovering {
		open = openMachineFile
	}
	checkpoint, err := open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = checkpoint.Close()
		}
	}()
	if _, err := policy.nodeHandle(windows.Handle(checkpoint.Fd()), false, true, machineMutationMask); err != nil {
		return nil, err
	}
	names := append(append([]string{}, PayloadNames...), Uninstaller, Marker)
	record, err := decodeMachineCheckpoint(checkpoint, names)
	if err != nil {
		return nil, err
	}

	locations := []string{dir, filepath.Join(dir, record.Stage), filepath.Join(dir, record.Backup)}
	for _, subdir := range locations[1:] {
		node := machineAuditEntry{path: subdir, directory: true, protected: true, optional: true, mask: machineMutationMask}
		node.before, err = policy.node(subdir, true, true, true, node.mask)
		if err != nil {
			return nil, err
		}
		if !node.before.Missing {
			if err := pins.pin(subdir); err != nil {
				return nil, err
			}
			observed, err := policy.nodeHandle(pins.pinned[subdir], true, true, node.mask)
			if err != nil {
				return nil, err
			}
			if observed != node.before {
				return nil, errors.New("核对期间机器安装目录已变化")
			}
		}
		audit.entries = append(audit.entries, node)
	}

	return &openedMachineCheckpoint{audit, &pins, checkpoint, record, locations, names}, nil
}

func (s *ScopedSandbox) ReviewMachineCheckpoint() (MachineCheckpointReview, error) {
	c, err := s.openMachineCheckpoint(false)
	if err != nil || c == nil {
		return MachineCheckpointReview{}, err
	}
	defer c.close()
	review := MachineCheckpointReview{Present: true, PreviousVersion: c.record.Previous.Version}
	for _, name := range c.names {
		var states [3]string
		for i, location := range c.locations {
			state, err := reviewMachineCheckpointFile(c.audit, filepath.Join(location, name), name, c.record)
			if err != nil {
				return MachineCheckpointReview{}, err
			}
			states[i] = state
		}
		review.Files = append(review.Files, MachineCheckpointFile{
			Name: name, OriginalPresent: slices.Contains(c.record.Original, name),
			Installed: states[0], Staging: states[1], Backup: states[2],
		})
	}
	if err := c.audit.Check(); err != nil {
		return MachineCheckpointReview{}, err
	}
	return review, nil
}

func decodeMachineCheckpoint(f io.Reader, names []string) (machineCheckpoint, error) {
	const maxCheckpoint = 16 << 10
	invalid := errors.New("机器安装事务格式无效，请保留检查点和备份")
	data, err := io.ReadAll(io.LimitReader(f, maxCheckpoint+1))
	if err != nil || len(data) > maxCheckpoint {
		return machineCheckpoint{}, invalid
	}
	var record machineCheckpoint
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.Format != 1 ||
		!validMachineTransactionDirectory(record.Stage, ".multipan-stage-") ||
		!validMachineTransactionDirectory(record.Backup, ".multipan-backup-") || len(record.Staged) != len(names) {
		return machineCheckpoint{}, invalid
	}
	previous := record.Previous
	if previous.Version == "" {
		if previous.Owner != "" || previous.Scope != "" || previous.Hashes != nil || len(record.Original) != 0 {
			return machineCheckpoint{}, invalid
		}
	} else if previous.Owner != Owner || previous.Scope != ScopeMachine ||
		!validVersion(previous.Version) || len(previous.Hashes) != len(names)-1 {
		return machineCheckpoint{}, invalid
	}
	for _, name := range names {
		if !validMachineHex(record.Staged[name], sha256.Size) ||
			(previous.Version != "" && name != Marker && !validMachineHex(previous.Hashes[name], sha256.Size)) {
			return machineCheckpoint{}, invalid
		}
	}
	seen := map[string]bool{}
	for _, name := range record.Original {
		if seen[name] || record.Staged[name] == "" {
			return machineCheckpoint{}, invalid
		}
		seen[name] = true
	}
	if previous.Version != "" && !seen[Marker] {
		return machineCheckpoint{}, invalid
	}
	return record, nil
}

func validMachineTransactionDirectory(value, prefix string) bool {
	suffix, ok := strings.CutPrefix(value, prefix)
	return ok && validMachineHex(suffix, 16)
}

func validMachineHex(value string, size int) bool {
	if len(value) != size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func openMachineReviewFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func reviewMachineCheckpointFile(audit *machineSecuritySnapshot, path, name string, record machineCheckpoint) (string, error) {
	node := machineAuditEntry{path: path, optional: true, mask: machineMutationMask}
	file, err := openMachineReviewFile(path)
	if os.IsNotExist(err) {
		node.before.Missing = true
		audit.entries = append(audit.entries, node)
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	node.before, err = audit.policy.nodeHandle(windows.Handle(file.Fd()), false, false, node.mask)
	if err != nil {
		return "", err
	}
	audit.entries = append(audit.entries, node)
	return machineCheckpointFileState(file, name, record)
}

func machineCheckpointFileState(file *os.File, name string, record machineCheckpoint) (string, error) {
	digest := sha256.New()
	const maxManaged = 100 << 20
	var previous bool
	if name == Marker {
		const maxMarker = 16 << 10
		data, err := io.ReadAll(io.LimitReader(file, maxMarker+1))
		if err != nil || len(data) > maxMarker {
			return "", errors.New("机器安装标识无法读取或超出核对范围")
		}
		var manifest Manifest
		previous = record.Previous.Version != "" && json.Unmarshal(data, &manifest) == nil && reflect.DeepEqual(manifest, record.Previous)
		_, _ = digest.Write(data)
	} else {
		size, err := io.Copy(digest, io.LimitReader(file, maxManaged+1))
		if err != nil || size > maxManaged {
			return "", errors.New("机器安装受管文件无法读取或超出核对范围")
		}
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	if name != Marker {
		previous = record.Previous.Hashes[name] == sum
	}
	next := record.Staged[name] == sum
	switch {
	case previous && next:
		return "both", nil
	case previous:
		return "previous", nil
	case next:
		return "staged", nil
	default:
		return "other", nil
	}
}
