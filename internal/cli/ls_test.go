package cli

import (
	"reflect"
	"testing"
)

func TestStringRuneLen(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"ascii", "skills", 6},
		{"empty", "", 0},
		{"multibyte", "日本語", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stringRuneLen(c.s); got != c.want {
				t.Errorf("stringRuneLen(%q) = %d, want %d", c.s, got, c.want)
			}
		})
	}
}

func TestTruncateWithEllipsis(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"shorter than max", "short", 10, "short"},
		{"exact fit", "exact", 5, "exact"},
		{"truncated with ellipsis", "this is long", 8, "this ..."},
		{"maxLen at or below ellipsis width", "anything", 3, "any"},
		{"multibyte truncation", "日本語のスキル", 5, "日本..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncateWithEllipsis(c.s, c.maxLen); got != c.want {
				t.Errorf("truncateWithEllipsis(%q, %d) = %q, want %q", c.s, c.maxLen, got, c.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{"pads to width", "abc", 6, "abc   "},
		{"already at width", "abcdef", 6, "abcdef"},
		{"already past width", "abcdefgh", 6, "abcdefgh"},
		{"multibyte pads by rune count", "日本語", 5, "日本語  "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := padRight(c.s, c.width); got != c.want {
				t.Errorf("padRight(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
			}
		})
	}
}

func TestAgentDisplayLabels(t *testing.T) {
	cases := []struct {
		name   string
		agents []string
		want   []string
	}{
		{"nil agents", nil, nil},
		{"claude-code folds to claude", []string{"claude-code"}, []string{"claude"}},
		{"strips -code suffix", []string{"github-copilot-code"}, []string{"github-copilot"}},
		{"excludes internal agents marker", []string{"claude", "agents"}, []string{"claude"}},
		{"claude listed first regardless of input order", []string{"codex", "claude-code", "cursor"}, []string{"claude", "codex", "cursor"}},
		{"deduplicates repeated agents", []string{"codex", "codex", "claude"}, []string{"claude", "codex"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agentDisplayLabels(c.agents)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("agentDisplayLabels(%#v) = %#v, want %#v", c.agents, got, c.want)
			}
		})
	}
}
