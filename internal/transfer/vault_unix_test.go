//go:build linux || darwin

package transfer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestUnixVaultAndExclusiveDownloadCommit(t *testing.T) {
	keyring.MockInit()
	resetVault := func() {
		unixVault.Lock()
		unixVault.aead = nil
		unixVault.Unlock()
	}
	defer resetVault()
	plain := []byte("synthetic account state")
	first, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := protect(plain)
	if err != nil || bytes.Equal(first, second) || bytes.Contains(first, plain) {
		t.Fatalf("encryption failed: %v", err)
	}
	resetVault()
	decoded, err := unprotect(first)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("could not decrypt after emulated restart: %v", err)
	}
	first[len(first)-1] ^= 1
	if _, err = unprotect(first); err == nil {
		t.Fatal("accepted altered state")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "download.tmp"), filepath.Join(dir, "download")
	if err := os.WriteFile(src, plain, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commitNewDownload(src, dst); err == nil {
		t.Fatal("overwrote the existing download")
	}
	if existing, err := os.ReadFile(dst); err != nil || string(existing) != "keep" {
		t.Fatalf("existing download changed: %v", err)
	}
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	if err := commitNewDownload(src, dst); err != nil {
		t.Fatal(err)
	}
}
