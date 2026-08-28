package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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
	discovered := engine.DiscoveredSkills{"one": {"skills/one"}, "two": {"skills/two"}}
	src := selectionIntake(t.TempDir(), false)

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, true, "", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills; want 2 (%v)", len(got), got)
	}
}

func TestResolveSkillsToAddFlagsRejectUnresolvedDuplicatesWithoutTerminal(t *testing.T) {
	discovered := engine.DiscoveredSkills{
		"duplicate": {"plugins/duplicate", "skills/duplicate"},
		"unique":    {"skills/unique"},
	}
	src := selectionIntake(t.TempDir(), false)

	for _, tc := range []struct {
		name   string
		all    bool
		skills []string
	}{
		{name: "all", all: true},
		{name: "skill", skills: []string{"duplicate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, "", tc.skills, true)
			if err == nil || !strings.Contains(err.Error(), "requires a Source path") {
				t.Fatalf("error = %v; want unresolved duplicate error", err)
			}
		})
	}
}

func TestResolveSkillsToAddFlagsPromptForDivergentCandidates(t *testing.T) {
	oldTerminal, oldPrompt := addSelectionIsTerminal, addPromptSourcePath
	addSelectionIsTerminal = func() bool { return true }
	addPromptSourcePath = func(_ string, paths []string) (string, error) { return paths[1], nil }
	t.Cleanup(func() { addSelectionIsTerminal, addPromptSourcePath = oldTerminal, oldPrompt })

	discovered := engine.DiscoveredSkills{
		"duplicate": {"plugins/duplicate", "skills/duplicate"},
		"unique":    {"skills/unique"},
	}
	src := selectionIntake(t.TempDir(), false)
	for _, tc := range []struct {
		name   string
		all    bool
		skills []string
	}{
		{name: "all", all: true},
		{name: "skill", skills: []string{"duplicate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, "", tc.skills, false)
			if err != nil || cancelled || got["duplicate"] != "skills/duplicate" {
				t.Fatalf("got=%v cancelled=%v err=%v; want selected Source path", got, cancelled, err)
			}
		})
	}
}

func TestResolveSkillsToAddPathMatchesDiscoveredSubpath(t *testing.T) {
	discovered := engine.DiscoveredSkills{"one": {"skills/one"}, "two": {"skills/two"}}
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

	got, cancelled, err := resolveSkillsToAdd(testCmd(), engine.DiscoveredSkills{}, src, false, "nested/tool", nil, true)
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

	_, _, err := resolveSkillsToAdd(testCmd(), engine.DiscoveredSkills{}, src, false, "missing/path", nil, true)
	if err == nil || !strings.Contains(err.Error(), "does not contain SKILL.md") {
		t.Fatalf("err = %v; want SKILL.md missing error", err)
	}
}

func TestResolveSkillsToAddSkillFlagExactAndCaseInsensitiveMatch(t *testing.T) {
	discovered := engine.DiscoveredSkills{"Api": {"skills/api"}, "lint": {"skills/lint"}}
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
	discovered := engine.DiscoveredSkills{"lint": {"skills/lint"}, "api": {"skills/api"}}
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
	discovered := engine.DiscoveredSkills{"original-name": {"."}}
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
	discovered := engine.DiscoveredSkills{"original-name": {"."}}
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

func TestResolveCandidatePathsFailsActionablyWithoutTerminal(t *testing.T) {
	discovered := engine.DiscoveredSkills{
		"duplicate": {"plugins/duplicate", "skills/duplicate"},
	}

	_, _, err := resolveCandidatePathsWith(map[string]string{"duplicate": ""}, discovered, false, nil)
	if err == nil || !strings.Contains(err.Error(), "plugins/duplicate") || !strings.Contains(err.Error(), "--path") {
		t.Fatalf("error = %v; want candidate paths and scoped-discovery guidance", err)
	}
}

func TestResolveCandidatePathsPromptsOnlyForSelectedAmbiguity(t *testing.T) {
	discovered := engine.DiscoveredSkills{
		"duplicate": {"plugins/duplicate", "skills/duplicate"},
		"unique":    {"skills/unique"},
	}
	called := false
	choose := func(name string, paths []string) (string, error) {
		called = true
		return paths[1], nil
	}

	got, cancelled, err := resolveCandidatePathsWith(map[string]string{"unique": "skills/unique"}, discovered, true, choose)
	if err != nil || cancelled || called || got["unique"] != "skills/unique" {
		t.Fatalf("unselected ambiguity: got=%v cancelled=%v called=%v err=%v", got, cancelled, called, err)
	}

	got, cancelled, err = resolveCandidatePathsWith(map[string]string{"duplicate": ""}, discovered, true, choose)
	if err != nil || cancelled || !called || got["duplicate"] != "skills/duplicate" {
		t.Fatalf("selected ambiguity: got=%v cancelled=%v called=%v err=%v", got, cancelled, called, err)
	}
}

func TestResolveCandidatePathsCancellationReturnsNoSelection(t *testing.T) {
	discovered := engine.DiscoveredSkills{"duplicate": {"plugins/duplicate", "skills/duplicate"}}
	choose := func(string, []string) (string, error) { return "", nil }

	got, cancelled, err := resolveCandidatePathsWith(map[string]string{"duplicate": ""}, discovered, true, choose)
	if err != nil || !cancelled || got != nil {
		t.Fatalf("got=%v cancelled=%v err=%v; want atomic cancellation", got, cancelled, err)
	}
}

func TestAddRunCancellationPrecedesMutation(t *testing.T) {
	oldTerminal, oldPrompt := addSelectionIsTerminal, addPromptSourcePath
	addSelectionIsTerminal = func() bool { return true }
	addPromptSourcePath = func(string, []string) (string, error) { return "", nil }
	t.Cleanup(func() { addSelectionIsTerminal, addPromptSourcePath = oldTerminal, oldPrompt })

	home := t.TempDir()
	t.Setenv("HOME", home)
	progressCalled := false
	intake := &addIntake{
		source:     engine.NewSymlinkAddSource(t.TempDir(), "", false),
		rootDir:    t.TempDir(),
		discovered: engine.DiscoveredSkills{"duplicate": {"plugins/duplicate", "skills/duplicate"}},
		labels:     sourceLabels{displayName: "test", resourceNoun: "Test source"},
		progressLine: func(string, string) string {
			progressCalled = true
			return ""
		},
	}

	if err := intake.run(testCmd(), addRequest{all: true}); err != nil {
		t.Fatal(err)
	}
	if progressCalled {
		t.Fatal("cancelled Add reached Materialization")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("cancelled Add mutated Config or Skills: %v", err)
	}
}

func TestNewRemoteIntakeAppliesTreeURLScopeBeforeDiscovery(t *testing.T) {
	origin := t.TempDir()
	for _, path := range []string{"skills/one/SKILL.md", "plugins/two/SKILL.md"} {
		fullPath := filepath.Join(origin, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("# Skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		if _, stderr, err := engine.RunGit(origin, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, stderr)
		}
	}

	cmd := testCmd()
	cmd.SetErr(new(bytes.Buffer))
	intake, err := newRemoteIntake(cmd, "https://github.com/owner/repo/tree/main/skills", origin, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.DiscoveredSkills{"one": {"skills/one"}}
	if !reflect.DeepEqual(intake.discovered, want) {
		t.Fatalf("discovered = %v, want scoped tree result %v", intake.discovered, want)
	}
}
