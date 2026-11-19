package installation

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// SignUpdateManifest builds the signed update description from complete
// installer bytes. The caller supplies the installer and the release key.
func SignUpdateManifest(asset io.Reader, filename, version string, published time.Time, validity time.Duration, key ed25519.PrivateKey) ([]byte, []byte, error) {
	if len(key) != ed25519.PrivateKeySize || !validVersion(version) ||
		filename != updateInstallerName(version) || published.IsZero() ||
		validity <= 0 || validity > maxUpdateValidity {
		return nil, nil, errors.New("更新签名需要有效发布密钥、版本、安装包名和有效期")
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(asset, maxUpdateInstaller+1))
	if err != nil || size <= 0 || size > maxUpdateInstaller {
		return nil, nil, errors.New("安装包为空、读取失败或超过更新大小限制")
	}
	published = published.UTC()
	m := updateManifest{
		Format: 1, Version: version, Platform: "windows-x64",
		Published: published, Expires: published.Add(validity),
		Asset: filename, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)),
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, errors.New("无法生成更新清单")
	}
	raw = append(raw, '\n')
	return raw, ed25519.Sign(key, append([]byte(updateSignatureDomain), raw...)), nil
}
