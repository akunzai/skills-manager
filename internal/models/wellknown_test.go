package models

import "testing"

func TestParseWellKnownSource(t *testing.T) {
	tests := []struct {
		raw      string
		ok       bool
		key, url string
	}{
		{"https://redocly.com", true, "redocly.com", "https://redocly.com"},
		{"https://redocly.com/", true, "redocly.com", "https://redocly.com"},
		{"https://Developers.Cloudflare.com/.well-known/agent-skills/index.json", true, "developers.cloudflare.com", "https://developers.cloudflare.com"},
		{"https://example.com/docs/.well-known/agent-skills/index.json", true, "example.com/docs", "https://example.com/docs"},
		{"https://127.0.0.1:8443", true, "127.0.0.1:8443", "https://127.0.0.1:8443"},
		// http:// is classified, so Add can refuse it rather than clone it.
		{"http://example.com", true, "example.com", "http://example.com"},
		{"https://github.com/o/r", false, "", ""},
		{"https://example.com/docs", false, "", ""},
		{"https://example.com/.well-known/agent-skills/", false, "", ""},
		{"https://example.com?x=1", false, "", ""},
		{"github:o/r", false, "", ""},
		{"o/r", false, "", ""},
	}
	for _, tt := range tests {
		got, ok := ParseWellKnownSource(tt.raw)
		if ok != tt.ok || got.SourceKey != tt.key || got.URL != tt.url {
			t.Errorf("ParseWellKnownSource(%q) = %+v, %v; want key %q url %q, %v", tt.raw, got, ok, tt.key, tt.url, tt.ok)
		}
	}
}

func TestWellKnownIndexURL(t *testing.T) {
	if got := WellKnownIndexURL("https://example.com/docs"); got != "https://example.com/docs/.well-known/agent-skills/index.json" {
		t.Errorf("WellKnownIndexURL = %q", got)
	}
}
