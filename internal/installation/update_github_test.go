package installation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type updateTestTransport func(*http.Request) (*http.Response, error)

func (f updateTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f(req)
	if resp != nil {
		resp.Request = req
	}
	return resp, err
}

func updateTestResponse(status int, location string, content []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Location": []string{location}},
		Body:          io.NopCloser(bytes.NewReader(content)),
		ContentLength: int64(len(content)),
	}
}

func TestGitHubUpdatePinsReleaseAndAuthenticatesAssets(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			testGitHubUpdateRelease(t, arch)
		})
	}
}

func testGitHubUpdateRelease(t *testing.T, arch string) {
	t.Helper()
	latest, err := url.Parse("https://github.com/makessr/MultiPan/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const version = "0.99.1"
	installer := []byte("synthetic installer asset")
	filename := updateInstallerName(version, arch)
	manifest, signature, err := SignUpdateManifest(bytes.NewReader(installer), filename, version, arch, time.Now(), time.Hour, private)
	if err != nil {
		t.Fatal(err)
	}
	manifestName, signatureName := UpdateAssetNames(arch)
	assets := map[string][]byte{manifestName: manifest, signatureName: signature, filename: installer}

	for _, tc := range []struct {
		name          string
		latestTarget  string
		tag           string
		assetRedirect string
		current       string
		wantError     string
	}{
		{name: "valid", tag: "v0.99.1"},
		{name: "current", tag: "v0.99.1", current: version, wantError: ErrSameUpdateVersion.Error()},
		{name: "other repository", latestTarget: "/another/MultiPan/releases/tag/v0.99.1", wantError: "无法确认"},
		{name: "no release", latestTarget: "/makessr/MultiPan/releases", wantError: "尚无可用版本"},
		{name: "tag mismatch", tag: "v0.99.2", wantError: "标签与签名清单版本不一致"},
		{name: "same manifest mismatch", tag: "v0.99.2", current: version, wantError: "标签与签名清单版本不一致"},
		{name: "wrong host", tag: "v0.99.1", assetRedirect: "https://untrusted.example/file", wantError: "无法读取"},
		{name: "non HTTPS", tag: "v0.99.1", assetRedirect: "http://release-assets.githubusercontent.com/file", wantError: "无法读取"},
		{name: "second redirect", tag: "v0.99.1", assetRedirect: "https://release-assets.githubusercontent.com/redirect", wantError: "无法读取"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := updateOriginClient(time.Second)
			prefix := "/makessr/MultiPan/releases/download/" + tc.tag + "/"
			latestReads := 0
			client.Transport = updateTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() == latest.String() {
					latestReads++
					target := tc.latestTarget
					if target == "" {
						target = "/makessr/MultiPan/releases/tag/" + tc.tag
					}
					return updateTestResponse(http.StatusFound, target, nil), nil
				}
				if name, ok := strings.CutPrefix(req.URL.Path, prefix); req.URL.Host == "github.com" && ok && assets[name] != nil {
					target := tc.assetRedirect
					if target == "" {
						target = "https://release-assets.githubusercontent.com/" + name + "?signed=fixture"
					}
					return updateTestResponse(http.StatusFound, target, nil), nil
				}
				if req.URL.Host == "release-assets.githubusercontent.com" && req.URL.Path == "/redirect" {
					return updateTestResponse(http.StatusFound, "https://objects.githubusercontent.com/redirect", nil), nil
				}
				if asset := assets[strings.TrimPrefix(req.URL.Path, "/")]; req.URL.Host == "release-assets.githubusercontent.com" && asset != nil {
					return updateTestResponse(http.StatusOK, "", asset), nil
				}
				if req.URL.Host == "gh-proxy.com" || req.URL.Host == "ghproxy.net" {
					return updateTestResponse(http.StatusBadGateway, "", nil), nil
				}
				t.Fatalf("unexpected update request to %s", req.URL)
				return nil, errors.New("unexpected request")
			})

			current := tc.current
			if current == "" {
				current = "0.99.0"
			}
			update, err := checkUpdateOrigin(ctx, client, latest, public, current, arch)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || update.Version() != version || update.githubRoute != "github.com" {
				t.Fatalf("expected a verified GitHub update, got %+v %v", update, err)
			}
			resp, err := openUpdateOriginFile(ctx, updateAssetClient(client, true), update.origin.JoinPath(update.AssetName()), int64(len(installer)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if err := update.VerifyInstaller(resp.Body); err != nil || latestReads != 1 {
				t.Fatalf("expected a valid installer from the pinned tag, latest reads %d: %v", latestReads, err)
			}
		})
	}
}

func TestGitHubUpdateUsesVerifiedMirrorAndRetriesDownload(t *testing.T) {
	ctx := context.Background()
	latest, err := url.Parse("https://github.com/makessr/MultiPan/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const version = "0.99.1"
	asset := []byte("valid installer")
	name := updateInstallerName(version, "amd64")
	manifest, signature, err := SignUpdateManifest(bytes.NewReader(asset), name, version, "amd64", time.Now(), time.Hour, private)
	if err != nil {
		t.Fatal(err)
	}
	alias := "/https://github.com/makessr/MultiPan/releases/latest/download/"
	tagged := "/https://github.com/makessr/MultiPan/releases/download/v" + version + "/"
	client := updateOriginClient(time.Second)
	var attempts []string
	client.Transport = updateTestTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "github.com" + latest.Path:
			return nil, errors.New("network unavailable")
		case "gh-proxy.com" + alias + UpdateManifestName:
			return updateTestResponse(http.StatusOK, "", manifest), nil
		case "gh-proxy.com" + alias + UpdateSignatureName:
			return updateTestResponse(http.StatusOK, "", make([]byte, ed25519.SignatureSize)), nil
		case "ghproxy.net" + alias + UpdateManifestName:
			return updateTestResponse(http.StatusOK, "", manifest), nil
		case "ghproxy.net" + alias + UpdateSignatureName:
			return updateTestResponse(http.StatusOK, "", signature), nil
		case "ghproxy.net" + tagged + name:
			attempts = append(attempts, req.URL.Host)
			return updateTestResponse(http.StatusOK, "", bytes.Repeat([]byte("x"), len(asset))), nil
		case "github.com/makessr/MultiPan/releases/download/v" + version + "/" + name:
			attempts = append(attempts, req.URL.Host)
			return updateTestResponse(http.StatusOK, "", asset), nil
		case "gh-proxy.com" + tagged + name:
			attempts = append(attempts, req.URL.Host)
			return updateTestResponse(http.StatusServiceUnavailable, "", nil), nil
		default:
			t.Fatalf("unexpected update request to %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	update, err := checkUpdateOrigin(ctx, client, latest, public, "0.99.0", "amd64")
	if err != nil || update.githubRoute != "ghproxy.net" {
		t.Fatalf("expected a signed version from the second mirror, got %q: %v", update.githubRoute, err)
	}
	tmp, err := os.Create(filepath.Join(t.TempDir(), "installer.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	tagAsset := update.origin.JoinPath(name)
	if err := update.downloadGitHubUpdate(ctx, client, tagAsset, tmp); err != nil {
		t.Fatal(err)
	}
	if result, err := os.ReadFile(tmp.Name()); err != nil || !bytes.Equal(result, asset) {
		t.Fatalf("wrong downloaded data %q: %v", result, err)
	}
	if strings.Join(attempts, ",") != "ghproxy.net,gh-proxy.com,github.com" {
		t.Fatalf("unexpected route order %v", attempts)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := update.downloadGitHubUpdate(cancelled, client, tagAsset, tmp); !errors.Is(err, context.Canceled) || len(attempts) != 3 {
		t.Fatalf("cancelled download started another route: %v %v", attempts, err)
	}
}

func TestDirectUpdateOriginStillRejectsRedirects(t *testing.T) {
	origin, err := url.Parse("https://updates.example.test/multipan")
	if err != nil {
		t.Fatal(err)
	}
	client := updateOriginClient(time.Second)
	client.Transport = updateTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != origin.Host {
			t.Fatalf("direct update origin followed redirect to %s", req.URL)
		}
		return updateTestResponse(http.StatusFound, "https://release-assets.githubusercontent.com/update.json", nil), nil
	})
	_, err = checkUpdateOrigin(context.Background(), client, origin, make(ed25519.PublicKey, ed25519.PublicKeySize), "0.99.0", "amd64")
	if err == nil {
		t.Fatal("direct update origin accepted a redirect")
	}
}
