package transfer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type diskAccount struct {
	Account Account `json:"account"`
	Secret  string  `json:"secret"`
}
type diskState struct {
	Relays          []diskRelay           `json:"relays,omitempty"`
	RecyclePurges   []diskRecyclePurge    `json:"recyclePurges,omitempty"`
	Trashes         []diskTrash           `json:"trashes,omitempty"`
	Copies          []diskCopy            `json:"copies,omitempty"`
	RecycleRestores []diskRecycleRestore  `json:"recycleRestores,omitempty"`
	Schema          int                   `json:"schema,omitempty"`
	Settings        TransferSettings      `json:"settings"`
	Downloads       []DownloadJob         `json:"downloads,omitempty"`
	Fingerprints    []UploadFingerprint   `json:"fingerprints,omitempty"`
	Accounts        []diskAccount         `json:"accounts"`
	Tasks           []Task                `json:"tasks"`
	Shares          []diskShare           `json:"shares,omitempty"`
	ShareRevokes    []diskShareRevocation `json:"shareRevokes,omitempty"`
}

func writeState(path string, s diskState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	encrypted, err := protect(raw)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "state-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(encrypted); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func readState(path string) (diskState, error) {
	var s diskState
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	raw, err = unprotect(raw)
	if err != nil {
		return s, fmt.Errorf("无法解密本机任务数据：%w", err)
	}
	err = json.Unmarshal(raw, &s)
	return s, err
}
