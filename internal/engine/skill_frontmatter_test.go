package engine

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestReadSkillFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    skillFrontmatter
	}{
		{"name and description", "---\nname: my-awesome-skill\ndescription: Does awesome stuff\n---\n\n# My Awesome Skill\n", skillFrontmatter{Name: "my-awesome-skill", Description: "Does awesome stuff"}},
		{"quoted values", "---\nname: \"quoted\"\ndescription: 'Says: hi'\n---\n", skillFrontmatter{Name: "quoted", Description: "Says: hi"}},
		{"name keeps its leading valid characters", "---\nname: foo (beta)\n---\n", skillFrontmatter{Name: "foo"}},
		{"unquoted colon in description", "---\nname: a\ndescription: Use when the user says: do it\n---\n", skillFrontmatter{Name: "a", Description: "Use when the user says: do it"}},
		{"hash is not a comment", "---\nname: a\ndescription: Covers foo #bar\n---\n", skillFrontmatter{Name: "a", Description: "Covers foo #bar"}},
		{"dashes inside a value", "---\nname: a\ndescription: before --- after\n---\nbody\n", skillFrontmatter{Name: "a", Description: "before --- after"}},
		{"folded description", "---\nname: a\ndescription: >-\n  Long text\n  more\n---\n", skillFrontmatter{Name: "a", Description: "Long text more"}},
		{"literal description", "---\ndescription: |\n  first\n\n  second\nname: a\n---\n", skillFrontmatter{Name: "a", Description: "first second"}},
		{"plain description continued", "---\nname: a\ndescription: starts here\n  and continues\n---\n", skillFrontmatter{Name: "a", Description: "starts here and continues"}},
		{"CRLF", "---\r\nname: new\r\ndescription: hi\r\nmetadata:\r\n  replaces: old\r\n---\r\n", skillFrontmatter{Name: "new", Description: "hi", Replaces: []string{"old"}}},
		{"byte order mark", "\xef\xbb\xbf---\nname: a\ndescription: hi\n---\n", skillFrontmatter{Name: "a", Description: "hi"}},
		{"first declaration wins", "---\nname: first\nname: second\n---\n", skillFrontmatter{Name: "first"}},
		{"metadata block", "---\nname: new\nmetadata:\n  replaces: old\n---\n# New\n", skillFrontmatter{Name: "new", Replaces: []string{"old"}}},
		{"several names", "---\nname: new\nmetadata:\n  author: someone\n  replaces: \"old, older\"\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old", "older"}}},
		{"metadata flow map", "---\nname: new\nmetadata: {author: someone, replaces: \"old, older\"}\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old", "older"}}},
		{"replaces as a list is not a string", "---\nname: new\nmetadata:\n  replaces:\n    - old\n---\n", skillFrontmatter{Name: "new"}},
		{"replaces as a flow list", "---\nname: new\nmetadata:\n  replaces: [old, older]\n---\n", skillFrontmatter{Name: "new"}},
		{"replaces as a flow list in a flow map", "---\nname: new\nmetadata: {replaces: [old, older], author: someone}\n---\n", skillFrontmatter{Name: "new"}},
		{"flow map over several lines", "---\nname: new\nmetadata: {author: someone,\n  replaces: old}\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old"}}},
		{"nested map inside metadata", "---\nname: new\nmetadata:\n  replaces: old\n  nested:\n    replaces: other\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old"}}},
		{"first metadata key wins", "---\nname: new\nmetadata:\n  replaces: old\n  replaces: other\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old"}}},
		{"quoted metadata key", "---\nname: new\nmetadata:\n  \"replaces\": old\n---\n", skillFrontmatter{Name: "new", Replaces: []string{"old"}}},
		{"replaces outside metadata", "---\nname: new\nreplaces: old\n---\n", skillFrontmatter{Name: "new"}},
		{"metadata ends before replaces", "---\nmetadata:\n  author: someone\nreplaces: old\n---\n", skillFrontmatter{}},
		{"replaces in the body", "---\nname: new\n---\nmetadata:\n  replaces: old\n", skillFrontmatter{Name: "new"}},
		{"unclosed frontmatter", "---\nname: a\n", skillFrontmatter{}},
		{"no frontmatter", "# New\nname: a\n", skillFrontmatter{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readSkillFrontmatter(path); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("frontmatter = %#v; want %#v", got, tt.want)
			}
		})
	}
	if got := readSkillFrontmatter(filepath.Join(t.TempDir(), "missing", "SKILL.md")); !reflect.DeepEqual(got, skillFrontmatter{}) {
		t.Fatalf("missing file frontmatter = %#v", got)
	}
}

// Discovery and Rename read the same SKILL.md files: both skip ignored
// directories and Skills nested deeper than MaxScanDepth.
func TestDiscoveryAndRenameWalkTheSameSkills(t *testing.T) {
	repo := t.TempDir()
	for _, subpath := range []string{"skills/kept", "node_modules/ignored", "a/b/c/d/e/f/g", "a/b/c/d/e/f/g/too-deep"} {
		content := "---\nname: " + filepath.Base(subpath) + "\nmetadata:\n  replaces: old\n---\n"
		writeTestFile(t, filepath.Join(repo, filepath.FromSlash(subpath), "SKILL.md"), []byte(content))
	}

	discovered, _, err := DiscoverSkillsInRepo(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	var discoveredPaths []string
	for _, paths := range discovered {
		discoveredPaths = append(discoveredPaths, paths...)
	}
	slices.Sort(discoveredPaths)
	if want := []string{"a/b/c/d/e/f/g", "skills/kept"}; !reflect.DeepEqual(discoveredPaths, want) {
		t.Fatalf("discovered = %v; want %v", discoveredPaths, want)
	}

	var replacing []string
	found := findReplacements(repo, []string{"old"}, func(subpath string) bool {
		replacing = append(replacing, subpath)
		return false
	})
	slices.Sort(replacing)
	if !reflect.DeepEqual(replacing, discoveredPaths) {
		t.Fatalf("rename read %v; discovery read %v", replacing, discoveredPaths)
	}
	if len(found) != 0 {
		t.Fatalf("rejected candidates were returned: %v", slices.Collect(maps.Keys(found)))
	}
}

// writeTestFile writes data to path, creating its parent directories.
func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWithSkillFrontmatter(t *testing.T) {
	herdr := "---\nname: herdr\ndescription: Control Herdr\n---\n# Herdr"
	herdrMultiline := "---\nname: herdr\ndescription: >-\n  Control Herdr\n  multiline\nmetadata:\n  replaces: old\n---\n# Herdr"
	herdrApp := "# herdr-app: drive the person's window\n\nHerdr shows spaces, tabs and panes. herdr-core (`hc`) runs their terminals\nand keeps them alive.\n\n## Usage\n"
	tests := []struct {
		name, content, description, want string
		fellBack                         bool
	}{
		{"printed frontmatter is kept when description is empty", herdr, "", herdr, false},
		{"printed frontmatter description is overridden", herdr, "Custom desc", "---\nname: herdr\ndescription: \"Custom desc\"\n---\n# Herdr", false},
		{"multiline frontmatter description is overridden", herdrMultiline, "Custom desc", "---\nname: herdr\ndescription: \"Custom desc\"\nmetadata:\n  replaces: old\n---\n# Herdr", false},
		{"multiline description with blank lines is replaced completely", "---\nname: herdr\ndescription: |\n  first line\n\n  second line\nmetadata:\n  foo: bar\n---\n# Herdr", "Custom desc", "---\nname: herdr\ndescription: \"Custom desc\"\nmetadata:\n  foo: bar\n---\n# Herdr", false},
		{"nested description under metadata is preserved and top-level appended", "---\nname: herdr\nmetadata:\n  description: nested\n---\n# Herdr", "Custom desc", "---\nname: herdr\nmetadata:\n  description: nested\ndescription: \"Custom desc\"\n---\n# Herdr", false},
		{"nested description under metadata is not matched when top-level exists", "---\nname: herdr\nmetadata:\n  description: nested\ndescription: old top\n---\n# Herdr", "Custom desc", "---\nname: herdr\nmetadata:\n  description: nested\ndescription: \"Custom desc\"\n---\n# Herdr", false},
		{"frontmatter without description has it appended", "---\nname: herdr\n---\n# Herdr", "Custom desc", "---\nname: herdr\ndescription: \"Custom desc\"\n---\n# Herdr", false},
		{"configured description wins on unfrontmattered content", herdrApp, "Drive the window", "---\nname: tool\ndescription: \"Drive the window\"\n---\n\n" + herdrApp, false},
		{"unconfigured falls back to the name", herdrApp, "", "---\nname: tool\ndescription: \"tool\"\n---\n\n" + herdrApp, true},
		{"no prose falls back to the name", "just text with no title", "", "---\nname: tool\ndescription: \"tool\"\n---\n\njust text with no title", true},
		{"empty summary falls back to the name", "```\ncode\n```", "", "---\nname: tool\ndescription: \"tool\"\n---\n\n```\ncode\n```", true},
		{"quotes and backslashes in description are escaped", "# A title", "A \"quoted\" \\ desc", "---\nname: tool\ndescription: \"A \\\"quoted\\\" \\\\ desc\"\n---\n\n# A title", false},
		{"newlines in description are normalized to spaces", "# A title", "Line 1\nLine 2\r\nLine 3", "---\nname: tool\ndescription: \"Line 1 Line 2 Line 3\"\n---\n\n# A title", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fellBack := withSkillFrontmatter(tt.content, "tool", tt.description)
			if got != tt.want {
				t.Fatalf("got\n%q\nwant\n%q", got, tt.want)
			}
			if fellBack != tt.fellBack {
				t.Fatalf("fellBack = %v, want %v", fellBack, tt.fellBack)
			}
		})
	}
}
