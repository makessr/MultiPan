//go:build linux || darwin

package transfer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

const vaultService = "app.multipan.desktop"
const vaultAccount = "encrypted-state-v1"
const vaultFormat = "MultiPan-Unix-v1\x00"

var unixVault struct {
	sync.Mutex
	aead cipher.AEAD
}

func systemVault(create bool) (cipher.AEAD, error) {
	unixVault.Lock()
	defer unixVault.Unlock()
	if unixVault.aead != nil {
		return unixVault.aead, nil
	}
	encoded, err := keyring.Get(vaultService, vaultAccount)
	var key []byte
	if create && errors.Is(err, keyring.ErrNotFound) {
		key = make([]byte, 32)
		defer clear(key)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		err = keyring.Set(vaultService, vaultAccount, base64.StdEncoding.EncodeToString(key))
	} else if err == nil {
		key, err = base64.StdEncoding.DecodeString(encoded)
		defer clear(key)
	}
	if err != nil {
		return nil, errors.New("无法访问系统密钥服务，请确认当前用户的钥匙串或登录密钥环已解锁")
	}
	if len(key) != 32 {
		return nil, errors.New("系统密钥服务中的 MultiPan 密钥格式无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	unixVault.aead, err = cipher.NewGCMWithRandomNonce(block)
	return unixVault.aead, err
}

func protect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("加密内容为空")
	}
	vault, err := systemVault(true)
	if err != nil {
		return nil, err
	}
	return vault.Seal([]byte(vaultFormat), nil, data, []byte(vaultFormat)), nil
}

func unprotect(data []byte) ([]byte, error) {
	body, ok := bytes.CutPrefix(data, []byte(vaultFormat))
	if !ok {
		return nil, errors.New("本机任务数据格式不兼容")
	}
	vault, err := systemVault(false)
	if err != nil {
		return nil, err
	}
	return vault.Open(nil, nil, body, []byte(vaultFormat))
}
