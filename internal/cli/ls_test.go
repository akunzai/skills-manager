package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
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
		{"claude-code and claude alias both present fold to one claude", []string{"claude-code", "claude"}, []string{"claude"}},
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

// ls words each entry the way Doctor classifies it: a text stub, an Untracked
// link to nothing and a local Source inside the skills directory are not
// "Invalid" or "Installed", and --json carries the same status.
func TestCLILsStatusFollowsInventory(t *testing.T) {
	project := projectScope(t)
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "nested", "SKILL.md"), []byte("# Nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "stub"), []byte("../src/stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "gone"), filepath.Join(skillsDir, "leftover")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "stub", filepath.Join(project, "src", "stub"), "")
	config.AddLocalSymlinkEntry(cfg, "nested", ".agents/skills/nested", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "ls", "-p")
	if err != nil {
		t.Fatalf("ls -p: %v\n%s", err, out)
	}
	for name, want := range map[string]string{"stub": "Stub (text file)", "leftover": "Broken link", "nested": "Source inside skills dir"} {
		line := ""
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(l, name+" ") {
				line = l
			}
		}
		if !strings.Contains(line, want) {
			t.Fatalf("ls line for %s = %q; want %q\n%s", name, line, want, out)
		}
	}

	resetSubcommandFlags()
	out, err = runCLI(t, "ls", "-p", "--json")
	if err != nil {
		t.Fatalf("ls -p --json: %v\n%s", err, out)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	got := make(map[string]any)
	for _, item := range items {
		got[item["name"].(string)] = item["status"]
	}
	want := map[string]any{"stub": "stub", "leftover": "untracked-link", "nested": "illegal-local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %#v; want %#v", got, want)
	}
}
