package installation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// CheckUpdateOrigin reads signed release metadata from a source configured by
// the caller. The origin and the publisher key are both trust inputs.
func CheckUpdateOrigin(ctx context.Context, releaseURL string, trustedPublisher ed25519.PublicKey, installedVersion, arch string) (VerifiedUpdate, error) {
	if len(trustedPublisher) != ed25519.PublicKeySize || !validVersion(installedVersion) || !SupportedUpdateArchitecture(arch) {
		return VerifiedUpdate{}, errors.New("在线检查需要可信发布公钥和有效的当前版本")
	}
	origin, err := url.Parse(releaseURL)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" || origin.RawPath != "" ||
		strings.ContainsAny(origin.Path, "\\%") {
		return VerifiedUpdate{}, errors.New("更新源必须是明确配置的 HTTPS 目录地址")
	}
	if dir := strings.TrimSuffix(origin.Path, "/"); dir != "" && path.Clean(dir) != dir {
		return VerifiedUpdate{}, errors.New("更新源目录路径无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return checkUpdateOrigin(ctx, updateOriginClient(10*time.Second), origin, trustedPublisher, installedVersion, arch)
}

func checkUpdateOrigin(ctx context.Context, client *http.Client, origin *url.URL, trustedPublisher ed25519.PublicKey, installedVersion, arch string) (VerifiedUpdate, error) {
	if repo := githubUpdateRepository(origin); repo != "" {
		return checkGitHubUpdate(ctx, client, origin, repo, trustedPublisher, installedVersion, arch)
	}
	manifest, signature := UpdateAssetNames(arch)
	update, err := checkUpdateAssets(ctx, client, origin.JoinPath(manifest), origin.JoinPath(signature), trustedPublisher, installedVersion, arch)
	if err == nil {
		update.origin = origin
	}
	return update, err
}

func checkUpdateAssets(ctx context.Context, client *http.Client, manifestURL, signatureURL *url.URL, trustedPublisher ed25519.PublicKey, installedVersion, arch string) (VerifiedUpdate, error) {
	manifest, err := readUpdateOriginFile(ctx, client, manifestURL, maxUpdateManifest)
	if err != nil {
		return VerifiedUpdate{}, err
	}
	signature, err := readUpdateOriginFile(ctx, client, signatureURL, ed25519.SignatureSize)
	if err != nil {
		return VerifiedUpdate{}, err
	}
	return VerifyUpdateManifest(bytes.NewReader(manifest), bytes.NewReader(signature), trustedPublisher, installedVersion, arch, time.Now())
}

func updateOriginClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func openUpdateOriginFile(ctx context.Context, client *http.Client, fileURL *url.URL, limit int64) (*http.Response, error) {
	unavailable := errors.New("无法读取完整的 HTTPS 更新资料")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL.String(), nil)
	if err != nil {
		return nil, unavailable
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return nil, unavailable
	}
	encoding := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != http.StatusOK || resp.Uncompressed || (encoding != "" && !strings.EqualFold(encoding, "identity")) ||
		resp.Header.Get("Content-Range") != "" || resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, unavailable
	}
	return resp, nil
}

func readUpdateOriginFile(ctx context.Context, client *http.Client, fileURL *url.URL, limit int64) ([]byte, error) {
	resp, err := openUpdateOriginFile(ctx, client, fileURL, limit)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit ||
		(resp.ContentLength >= 0 && int64(len(raw)) != resp.ContentLength) {
		return nil, errors.New("无法读取完整的 HTTPS 更新资料")
	}
	return raw, nil
}
