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
