package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

type Account struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Endpoint      string `json:"endpoint"`
	Username      string `json:"username"`
	Folder        string `json:"folder"`
	FolderDisplay string `json:"folderDisplay"`
	UserAgent     string `json:"userAgent"`
	Secret        string `json:"-"`
}
type AccountInput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Endpoint      string `json:"endpoint"`
	Username      string `json:"username"`
	Folder        string `json:"folder"`
	FolderDisplay string `json:"folderDisplay"`
	UserAgent     string `json:"userAgent"`
	Secret        string `json:"secret"`
}
type File struct {
	RelativePath string `json:"relativePath,omitempty"`
	Directory    bool   `json:"directory,omitempty"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Modified     int64  `json:"modified"`
}

func Inspect(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() {
		return File{}, os.ErrInvalid
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return File{}, err
	}
	return File{Path: absolute, Name: info.Name(), Size: info.Size(), Modified: info.ModTime().UnixMilli()}, nil
}

type Target struct {
	AccountID string `json:"accountId"`
	Folder    string `json:"folder"`
}
type Task struct {
	SourceSHA256  string `json:"sourceSha256,omitempty"`
	LastAttempt   string `json:"lastAttempt,omitempty"`
	ID            string `json:"id"`
	BatchID       string `json:"batchId"`
	AccountID     string `json:"accountId"`
	AccountName   string `json:"accountName"`
	Kind          string `json:"kind"`
	File          File   `json:"file"`
	Folder        string `json:"folder"`
	FolderDisplay string `json:"folderDisplay"`
	Status        string `json:"status"`
	Sent          int64  `json:"sent"`
	Message       string `json:"message"`
	Created       string `json:"created"`
}
type Snapshot struct {
	Settings     TransferSettings `json:"settings"`
	Downloads    []DownloadJob    `json:"downloads"`
	Accounts     []Account        `json:"accounts"`
	Tasks        []Task           `json:"tasks"`
	StorageError string           `json:"storageError"`
}
type Uploader interface {
	Upload(context.Context, Account, File, string, func(int64)) error
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func now() string { return time.Now().Format(time.RFC3339) }
