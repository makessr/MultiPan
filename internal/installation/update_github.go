package installation

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var githubRepositoryPath = regexp.MustCompile(`^/[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
var githubUpdateRoutes = [...]string{"gh-proxy.com", "ghproxy.net", "github.com"}
var errNoGitHubRelease = errors.New("GitHub 更新渠道尚无可用版本")

func githubRouteURL(route string, source *url.URL) *url.URL {
	if route == "github.com" {
		return source
	}
	return &url.URL{Scheme: "https", Host: route, Path: "/" + source.String()}
}

func githubDownloadRoutes(preferred string) [3]string {
	routes := githubUpdateRoutes
	if preferred == routes[1] {
		routes[0], routes[1] = routes[1], routes[0]
	}
	return routes
}

func githubUpdateRepository(origin *url.URL) string {
	if origin.Scheme != "https" || !strings.EqualFold(origin.Host, "github.com") {
		return ""
	}
	repo, ok := strings.CutSuffix(strings.TrimSuffix(origin.Path, "/"), "/releases/latest")
	if !ok || !githubRepositoryPath.MatchString(repo) {
		return ""
	}
	return repo
}

func resolveGitHubUpdateRelease(ctx context.Context, client *http.Client, origin *url.URL, repo string) (*url.URL, string, error) {
	unavailable := errors.New("无法确认 GitHub 最新发布版本，请稍后重试")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
	if err != nil {
		return nil, "", unavailable
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", unavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", errNoGitHubRelease
	}
	target, err := resp.Location()
	if resp.StatusCode != http.StatusFound || err != nil || target.Scheme != "https" ||
		!strings.EqualFold(target.Host, "github.com") || target.User != nil ||
		target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || target.RawPath != "" {
		return nil, "", unavailable
	}
	if strings.EqualFold(target.Path, repo+"/releases") {
		return nil, "", errNoGitHubRelease
	}
	targetRepo, tag, ok := strings.Cut(target.Path, "/releases/tag/")
	version, hasPrefix := strings.CutPrefix(tag, "v")
	if !ok || !strings.EqualFold(targetRepo, repo) || !hasPrefix || !validVersion(version) {
		return nil, "", unavailable
	}
	return &url.URL{Scheme: "https", Host: "github.com", Path: repo + "/releases/download/" + tag}, version, nil
}

func updateAssetClient(client *http.Client, githubRelease bool) *http.Client {
	if !githubRelease {
		return client
	}
	githubClient := *client
	githubClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) != 1 ||
			req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Fragment != "" {
			return http.ErrUseLastResponse
		}
		from := strings.ToLower(via[0].URL.Host)
		if from != githubUpdateRoutes[0] && from != githubUpdateRoutes[1] && from != githubUpdateRoutes[2] {
			return http.ErrUseLastResponse
		}
		switch strings.ToLower(req.URL.Host) {
		case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return http.ErrUseLastResponse
		}
	}
	return &githubClient
}

func checkGitHubUpdate(ctx context.Context, client *http.Client, latest *url.URL, repo string, publisher ed25519.PublicKey, installedVersion, arch string) (VerifiedUpdate, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	origin, tagVersion, resolveErr := resolveGitHubUpdateRelease(resolveCtx, client, latest, repo)
	cancel()
	if errors.Is(resolveErr, errNoGitHubRelease) {
		return VerifiedUpdate{}, resolveErr
	}
	routes := githubUpdateRoutes[:]
	if resolveErr != nil {
		origin = latest.JoinPath("download")
		routes = routes[:len(routes)-1]
	}
	assetClient := updateAssetClient(client, true)
	lastErr := resolveErr
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return VerifiedUpdate{}, err
		}
		update, err := checkGitHubUpdateRoute(ctx, assetClient, origin, tagVersion, route, publisher, installedVersion, arch)
		if err == nil || errors.Is(err, ErrSameUpdateVersion) {
			return update, err
		}
		lastErr = errors.Join(lastErr, err)
	}
	return VerifiedUpdate{}, lastErr
}

func checkGitHubUpdateRoute(ctx context.Context, client *http.Client, origin *url.URL, version, route string, publisher ed25519.PublicKey, installedVersion, arch string) (VerifiedUpdate, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manifest, signature := UpdateAssetNames(arch)
	update, err := checkUpdateAssets(ctx, client, githubRouteURL(route, origin.JoinPath(manifest)),
		githubRouteURL(route, origin.JoinPath(signature)), publisher, installedVersion, arch)
	if version != "" && (err == nil && update.Version() != version ||
		errors.Is(err, ErrSameUpdateVersion) && installedVersion != version) {
		return VerifiedUpdate{}, errors.New("GitHub Release 标签与签名清单版本不一致")
	}
	if err != nil {
		return VerifiedUpdate{}, err
	}
	if version == "" {
		// On a mirror-only check, only the authenticated version can pin the installer.
		repo, _ := strings.CutSuffix(origin.Path, "/releases/latest/download")
		origin = &url.URL{Scheme: "https", Host: "github.com", Path: repo + "/releases/download/v" + update.Version()}
	}
	update.origin = origin
	update.githubRoute = route
	return update, nil
}
