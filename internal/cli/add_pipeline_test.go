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

func selectionIntake(dir string) *addIntake {
	return &addIntake{
		source: engine.NewSymlinkAddSource(dir, ""),
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
	src := selectionIntake(t.TempDir())

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, true, nil, true)
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
	src := selectionIntake(t.TempDir())

	for _, tc := range []struct {
		name   string
		all    bool
		skills []string
	}{
		{name: "all", all: true},
		{name: "skill", skills: []string{"duplicate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, tc.skills, true)
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
	src := selectionIntake(t.TempDir())
	for _, tc := range []struct {
		name   string
		all    bool
		skills []string
	}{
		{name: "all", all: true},
		{name: "skill", skills: []string{"duplicate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, tc.skills, false)
			if err != nil || cancelled || got["duplicate"] != "skills/duplicate" {
				t.Fatalf("got=%v cancelled=%v err=%v; want selected Source path", got, cancelled, err)
			}
		})
	}
}

func TestResolveSkillsToAddSkillFlagExactAndCaseInsensitiveMatch(t *testing.T) {
	discovered := engine.DiscoveredSkills{"Api": {"skills/api"}, "lint": {"skills/lint"}}
	src := selectionIntake(t.TempDir())

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, []string{"lint", "api"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToAdd() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 || got["lint"] != "skills/lint" || got["Api"] != "skills/api" {
		t.Fatalf("got %v; want lint (exact) and Api (case-insensitive)", got)
	}
}

func TestResolveSkillsToAddSkillFlagUnmatchedFailsAtomically(t *testing.T) {
	discovered := engine.DiscoveredSkills{"lint": {"skills/lint"}, "api": {"skills/api"}}
	src := selectionIntake(t.TempDir())

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, []string{"lint", "ghost"}, true)
	if err == nil || cancelled || got != nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("got=%v cancelled=%v err=%v; want atomic not-found error", got, cancelled, err)
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
		source:     engine.NewSymlinkAddSource(t.TempDir(), ""),
		discovered: engine.DiscoveredSkills{"duplicate": {"plugins/duplicate", "skills/duplicate"}},
		labels:     sourceLabels{displayName: "test", resourceNoun: "Test source"},
		progressLine: func(string, string) string {
			progressCalled = true
			return ""
		},
	}

	cmd := testCmd()
	if err := intake.run(cmd, addRequest{all: true}); err != nil {
		t.Fatal(err)
	}
	if progressCalled {
		t.Fatal("cancelled Add reached Materialization")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("cancelled Add mutated Config or Skills: %v", err)
	}
	if out := cmd.OutOrStdout().(*bytes.Buffer).String(); strings.Count(out, "Operation cancelled.") != 1 {
		t.Fatalf("cancellation output = %q; want exactly one message", out)
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
