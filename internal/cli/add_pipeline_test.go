package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

func selectionIntake(dir string, allowRename bool) *addIntake {
	return &addIntake{
		source:  engine.NewSymlinkAddSource(dir, "", allowRename),
		rootDir: dir,
		labels: sourceLabels{
			displayName: "test", resourceNoun: "Test source",
		},
	}
}

func testCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "add"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	return cmd
}

func TestResolveSkillsToAddAllFlag(t *testing.T) {
	discovered := map[string]string{"one": "skills/one", "two": "skills/two"}
	src := selectionIntake(t.TempDir(), false)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, true, "", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills; want 2 (%v)", len(got), got)
	}
}

func TestResolveSkillsToAddPathMatchesDiscoveredSubpath(t *testing.T) {
	discovered := map[string]string{"one": "skills/one", "two": "skills/two"}
	src := selectionIntake(t.TempDir(), false)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, "skills/two", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["two"] != "skills/two" {
		t.Fatalf("got %v; want only 'two'", got)
	}
}

func TestResolveSkillsToAddPathFallsBackToSkillMD(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "nested", "tool")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte("# Tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := selectionIntake(root, false)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), map[string]string{}, src, false, "nested/tool", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["tool"] != "nested/tool" {
		t.Fatalf("got %v; want fallback skill 'tool'", got)
	}
}

func TestResolveSkillsToAddPathWithoutSkillMDErrors(t *testing.T) {
	root := t.TempDir()
	src := selectionIntake(root, false)

	_, _, err := resolveSkillsToAdd(testCmd(), map[string]string{}, src, false, "missing/path", nil, true)
	if err == nil || !strings.Contains(err.Error(), "does not contain SKILL.md") {
		t.Fatalf("err = %v; want SKILL.md missing error", err)
	}
}

func TestResolveSkillsToAddSkillFlagExactAndCaseInsensitiveMatch(t *testing.T) {
	discovered := map[string]string{"Api": "skills/api", "lint": "skills/lint"}
	src := selectionIntake(t.TempDir(), false)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, "", []string{"lint", "api"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 || got["lint"] != "skills/lint" || got["Api"] != "skills/api" {
		t.Fatalf("got %v; want lint (exact) and Api (case-insensitive)", got)
	}
}

func TestResolveSkillsToAddSkillFlagUnmatchedWarnsAndSkips(t *testing.T) {
	discovered := map[string]string{"lint": "skills/lint", "api": "skills/api"}
	src := selectionIntake(t.TempDir(), false)
	cmd := testCmd()

	got, cancelled, err := resolveSkillsToAdd(cmd, discovered, src, false, "", []string{"ghost"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v; want no matches for an unknown skill", got)
	}
	out := cmd.OutOrStdout().(*bytes.Buffer).String()
	if !strings.Contains(out, "Skill 'ghost' not found") {
		t.Fatalf("output = %q; want a not-found warning", out)
	}
}

func TestResolveSkillsToAddSingleSkillFlagRenamesSoleDiscoveredWhenAllowed(t *testing.T) {
	discovered := map[string]string{"original-name": "."}
	src := selectionIntake(t.TempDir(), true)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, "", []string{"renamed"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["renamed"] != "." {
		t.Fatalf("got %v; want the sole discovered skill renamed to 'renamed'", got)
	}
}

// Remote Adds never had the local-only sole-discovered-rename shortcut:
// a --skill name that doesn't match must fall through to exact/case-insensitive
// matching and simply not match, not silently rename the one discovered skill.
func TestResolveSkillsToAddSingleSkillFlagDoesNotRenameWhenDisallowed(t *testing.T) {
	discovered := map[string]string{"original-name": "."}
	src := selectionIntake(t.TempDir(), false)
	cmd := testCmd()

	got, cancelled, err := resolveSkillsToAdd(cmd, discovered, src, false, "", []string{"renamed"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v; want no match, not a silent rename", got)
	}
	out := cmd.OutOrStdout().(*bytes.Buffer).String()
	if !strings.Contains(out, "Skill 'renamed' not found") {
		t.Fatalf("output = %q; want a not-found warning", out)
	}
}
