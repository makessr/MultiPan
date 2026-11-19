package transfer

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

func safePublicDownloadURL(raw string, base *url.URL) (*url.URL, error) {
	invalid := errors.New("网盘返回的下载地址无效或不支持安全连接")
	if raw == "" || len(raw) > 65536 {
		return nil, invalid
	}
	u, e := url.Parse(raw)
	if e != nil {
		return nil, invalid
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	// Provider-generated HTTP links may use the same CDN over TLS. Never fall
	// back to cleartext or transmit an API credential to make them work.
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, invalid
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, invalid
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return nil, invalid
		}
	} else if !validUploadDNS(host) {
		return nil, invalid
	}
	return u, nil
}
