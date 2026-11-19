package installation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	updateSignatureDomain = "app.multipan.desktop/update-manifest/v1\x00"
	maxUpdateManifest     = 16 << 10
	maxUpdateInstaller    = 100 << 20
	maxUpdateValidity     = 30 * 24 * time.Hour
	UpdateManifestName    = "update.json"
	UpdateSignatureName   = "update.sig"
)

type updateManifest struct {
	Format    int       `json:"format"`
	Version   string    `json:"version"`
	Platform  string    `json:"platform"`
	Published time.Time `json:"published"`
	Expires   time.Time `json:"expires"`
	Asset     string    `json:"asset"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
}

// VerifiedUpdate describes a release authenticated by the publisher key.
type VerifiedUpdate struct {
	manifest updateManifest
	origin   *url.URL
}

var ErrSameUpdateVersion = errors.New("更新清单版本与当前版本一致")

func (u VerifiedUpdate) Version() string   { return u.manifest.Version }
func (u VerifiedUpdate) AssetName() string { return u.manifest.Asset }

func VerifyUpdateManifest(metadata, detachedSignature io.Reader, trustedPublisher ed25519.PublicKey, installedVersion string, now time.Time) (VerifiedUpdate, error) {
	if len(trustedPublisher) != ed25519.PublicKeySize || !validVersion(installedVersion) || now.IsZero() {
		return VerifiedUpdate{}, errors.New("更新核对需要可信发布公钥、当前版本和有效时间")
	}
	raw, err := io.ReadAll(io.LimitReader(metadata, maxUpdateManifest+1))
	if err != nil || len(raw) > maxUpdateManifest {
		return VerifiedUpdate{}, errors.New("更新清单无法读取或超出大小限制")
	}
	signature, err := io.ReadAll(io.LimitReader(detachedSignature, ed25519.SignatureSize+1))
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(trustedPublisher, append([]byte(updateSignatureDomain), raw...), signature) {
		return VerifiedUpdate{}, errors.New("更新清单签名无法验证")
	}
	var m updateManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF ||
		m.Format != 1 || m.Platform != "windows-x64" || !validVersion(m.Version) {
		return VerifiedUpdate{}, errors.New("更新清单格式、平台或版本无效")
	}
	if m.Published.IsZero() || m.Published.After(now) || !m.Expires.After(now) || m.Expires.Sub(m.Published) > maxUpdateValidity {
		return VerifiedUpdate{}, errors.New("更新清单已过期或发布时间无效")
	}
	expectedName := updateInstallerName(m.Version)
	sum, err := hex.DecodeString(m.SHA256)
	if m.Asset != expectedName || m.Size <= 0 || m.Size > maxUpdateInstaller ||
		err != nil || len(sum) != sha256.Size || m.SHA256 != strings.ToLower(m.SHA256) {
		return VerifiedUpdate{}, errors.New("更新安装包的名称、大小或摘要无效")
	}
	if m.Version == installedVersion {
		return VerifiedUpdate{}, ErrSameUpdateVersion
	}
	if !older(installedVersion, m.Version) {
		return VerifiedUpdate{}, errors.New("更新清单版本低于当前版本")
	}
	return VerifiedUpdate{manifest: m}, nil
}

func updateInstallerName(version string) string {
	return "MultiPan-" + version + "-windows-x64-setup.exe"
}

func (u VerifiedUpdate) VerifyInstaller(asset io.Reader) error {
	m := u.manifest
	if m.Size <= 0 || m.Size > maxUpdateInstaller {
		return errors.New("缺少已验证的更新清单")
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(asset, m.Size+1))
	if err != nil || size != m.Size || hex.EncodeToString(digest.Sum(nil)) != m.SHA256 {
		return errors.New("安装包大小或摘要与签名清单不一致")
	}
	return nil
}
