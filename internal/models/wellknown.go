package models

import (
	"net/url"
	"strings"
)

// WellKnownIndexPath is where a publisher serves its Agent Skills Discovery
// index, relative to the base URL
// (https://github.com/cloudflare/agent-skills-discovery-rfc, v0.2.0).
const WellKnownIndexPath = "/.well-known/agent-skills/index.json"

// WellKnownSource is a Source served over HTTPS through a well-known index.
type WellKnownSource struct {
	// SourceKey is the base URL without its scheme: the host, lowercased,
	// and any path the index sits under.
	SourceKey string
	// URL is the base URL the index path is appended to.
	URL string
}

// ParseWellKnownSource classifies raw as a well-known Source: an http(s) URL
// with no path, or one whose path ends in the index path. Every other URL is
// a git Source, decided without asking the network. http:// is classified
// too, so Add can refuse it instead of reading it as git.
func ParseWellKnownSource(raw string) (WellKnownSource, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return WellKnownSource{}, false
	}
	prefix, ok := strings.CutSuffix(u.Path, WellKnownIndexPath)
	if !ok {
		if strings.Trim(u.Path, "/") != "" {
			return WellKnownSource{}, false
		}
		prefix = ""
	}
	prefix = strings.TrimRight(prefix, "/")
	host := strings.ToLower(u.Host)
	return WellKnownSource{SourceKey: host + prefix, URL: u.Scheme + "://" + host + prefix}, true
}

// WellKnownIndexURL is the index URL of the Source at base.
func WellKnownIndexURL(base string) string {
	return strings.TrimRight(base, "/") + WellKnownIndexPath
}
