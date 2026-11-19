package installation

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

const machineCheckpointName = ".multipan-machine-transaction.json"

var errMachineInterrupted = errors.New("发现未核对的机器安装事务，请保留安装目录与备份")

type machineCheckpoint struct {
	Format   int               `json:"format"`
	Stage    string            `json:"stage"`
	Backup   string            `json:"backup"`
	Previous Manifest          `json:"previous"`
	Staged   map[string]string `json:"staged"`
	Original []string          `json:"original"`
}

// The presence of even an unreadable or invalid checkpoint prevents further
// writes. Recovery must never treat its JSON as trusted path instructions.
func checkMachineInterrupted(dir string) error {
	_, err := os.Lstat(filepath.Join(dir, machineCheckpointName))
	if os.IsNotExist(err) {
		return nil
	}
	return errMachineInterrupted
}

func (p *protectedMachineInstall) beginCheckpoint(stage, backup string, old Manifest, originals []heldMachineFile) error {
	record := machineCheckpoint{
		Format: 1, Stage: filepath.Base(stage), Backup: filepath.Base(backup),
		Previous: old, Staged: p.hashes,
	}
	for _, f := range originals {
		record.Original = append(record.Original, f.name)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := filepath.Join(p.dir, machineCheckpointName)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.DELETE, 0,
		securityAttributes(p.fileSD), windows.CREATE_NEW,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_WRITE_THROUGH, 0)
	runtime.KeepAlive(p.fileSD)
	if err != nil {
		return err
	}
	p.checkpoint = os.NewFile(uintptr(handle), path)
	if _, err := p.policy.nodeHandle(handle, false, true, machineMutationMask); err != nil {
		return err
	}
	n, err := p.checkpoint.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return p.checkpoint.Sync()
}

func (p *protectedMachineInstall) finishCheckpoint() {
	if p.checkpoint == nil {
		return
	}
	if p.resolved {
		// Failure to delete is fail-closed on the next attempt.
		_ = disposeMachineFile(p.checkpoint)
	}
	_ = p.checkpoint.Close()
	p.checkpoint = nil
}
