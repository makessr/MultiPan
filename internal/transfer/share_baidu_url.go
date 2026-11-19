package transfer

import (
	"errors"
	"net/url"
	"strings"
)

// Only authenticated share responses reach this parser. A bare short identifier
// is never expanded by guessing: it must match an independently returned URL.
func resolveBaiduShareURL(values ...string) (string, error) {
	anchor := ""
	tokens := []string{}
	fail := func(reason string) (string, error) { return "", errors.New("format=" + reason) }
	for _, value := range values {
		raw := strings.TrimSpace(value)
		if raw == "" {
			continue
		}
		if strings.ContainsAny(raw, "\r\n\t") {
			return fail("control")
		}
		if shareToken.MatchString(raw) {
			tokens = append(tokens, raw)
			continue
		}
		if strings.HasPrefix(raw, "//") {
			raw = "https:" + raw
		} else if strings.HasPrefix(raw, "/s/") || strings.HasPrefix(raw, "/share/init?") {
			raw = "https://pan.baidu.com" + raw
		} else if strings.HasPrefix(raw, "pan.baidu.com/") {
			raw = "https://" + raw
		}
		u, e := url.Parse(raw)
		if e != nil {
			return fail("parse")
		}
		if (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "pan.baidu.com") || u.User != nil || u.Fragment != "" || u.RawPath != "" {
			return fail("origin-or-path")
		}
		q, e := url.ParseQuery(u.RawQuery)
		if e != nil {
			return fail("query")
		}
		for k, v := range q {
			if len(v) != 1 {
				return fail("query-duplicate")
			}
			if k != "pwd" && !(k == "surl" && u.Path == "/share/init") {
				return fail("query-key")
			}
			if k == "pwd" && (len(v[0]) != 4 || !shareCode.MatchString(v[0])) {
				return fail("query-password")
			}
		}
		token := ""
		if u.Path == "/share/init" {
			surl := q.Get("surl")
			if !shareToken.MatchString(surl) || len(surl) > 127 {
				return fail("surl")
			}
			token = "1" + surl
		} else if strings.HasPrefix(u.Path, "/s/") {
			token = strings.TrimPrefix(u.Path, "/s/")
			if !shareToken.MatchString(token) {
				return fail("token")
			}
		} else {
			return fail("route")
		}
		canonical, e := shareURL("baidu", "https://pan.baidu.com/s/"+token)
		if e != nil {
			return fail("canonical")
		}
		if anchor != "" && anchor != canonical {
			return fail("conflicting-links")
		}
		anchor = canonical
	}
	if anchor == "" {
		if len(tokens) > 0 {
			return fail("unanchored-token")
		}
		return fail("missing-links")
	}
	tail := strings.TrimPrefix(anchor, "https://pan.baidu.com/s/")
	for _, token := range tokens {
		if token != tail && !(strings.HasPrefix(tail, "1") && token == tail[1:]) {
			return fail("token-conflict")
		}
	}
	return anchor, nil
}
