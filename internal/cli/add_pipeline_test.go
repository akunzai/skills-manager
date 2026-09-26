package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/tui"
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

// fakeAddPrompter answers Add's questions from its fields and records which
// were asked, in order.
type fakeAddPrompter struct {
	interactive bool
	skills      []string
	skillsErr   error
	path        func(skill string, paths []string) (string, error)
	project     bool
	scopeErr    error
	customize   bool
	availErr    error
	agents      []string
	agentsErr   error
	overwrite   error
	asked       []string
}

func (f *fakeAddPrompter) Interactive() bool { return f.interactive }

func (f *fakeAddPrompter) SelectSkills(string, tui.GroupedItems, []tui.SelectOption) ([]string, error) {
	f.asked = append(f.asked, "skills")
	return f.skills, f.skillsErr
}

func (f *fakeAddPrompter) SelectSourcePath(skill string, paths []string) (string, error) {
	f.asked = append(f.asked, "path")
	return f.path(skill, paths)
}

func (f *fakeAddPrompter) SelectScope() (bool, error) {
	f.asked = append(f.asked, "scope")
	return f.project, f.scopeErr
}

func (f *fakeAddPrompter) SelectAvailability() (bool, error) {
	f.asked = append(f.asked, "availability")
	return f.customize, f.availErr
}

func (f *fakeAddPrompter) SelectAgents([]tui.SelectOption) ([]string, error) {
	f.asked = append(f.asked, "agents")
	return f.agents, f.agentsErr
}

func (f *fakeAddPrompter) ConfirmOverwrite([]engine.AddConflict) error {
	f.asked = append(f.asked, "overwrite")
	return f.overwrite
}

func useAddPrompter(t *testing.T, prompter *fakeAddPrompter) {
	t.Helper()
	old := newAddPrompter
	newAddPrompter = func(*cobra.Command) addPrompter { return prompter }
	t.Cleanup(func() { newAddPrompter = old })
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

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, true, nil, &fakeAddPrompter{}, false, t.TempDir())
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
			_, _, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, tc.skills, &fakeAddPrompter{}, false, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "requires a Source path") {
				t.Fatalf("error = %v; want unresolved duplicate error", err)
			}
		})
	}
}

func TestResolveSkillsToAddFlagsPromptForDivergentCandidates(t *testing.T) {
	prompter := &fakeAddPrompter{interactive: true, path: func(_ string, paths []string) (string, error) { return paths[1], nil }}

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
			got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, tc.all, tc.skills, prompter, true, t.TempDir())
			if err != nil || cancelled || got["duplicate"] != "skills/duplicate" {
				t.Fatalf("got=%v cancelled=%v err=%v; want selected Source path", got, cancelled, err)
			}
		})
	}
}

func TestResolveSkillsToAddSkillFlagExactAndCaseInsensitiveMatch(t *testing.T) {
	discovered := engine.DiscoveredSkills{"Api": {"skills/api"}, "lint": {"skills/lint"}}
	src := selectionIntake(t.TempDir())

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, []string{"lint", "api"}, &fakeAddPrompter{}, false, t.TempDir())
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

	got, cancelled, err := resolveSkillsToAdd(testCmd(), discovered, src, false, []string{"lint", "ghost"}, &fakeAddPrompter{}, false, t.TempDir())
	if err == nil || cancelled || got != nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("got=%v cancelled=%v err=%v; want atomic not-found error", got, cancelled, err)
	}
}

func TestAddRunCancellationPrecedesMutation(t *testing.T) {
	useAddPrompter(t, &fakeAddPrompter{interactive: true, path: func(string, []string) (string, error) { return "", errAddCancelled }})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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
		cliRunGit(t, origin, args...)
	}

	cmd := testCmd()
	cmd.SetErr(new(bytes.Buffer))
	intake, err := newRemoteIntake(cmd, filepath.Join(t.TempDir(), "skills.json"), "https://github.com/owner/repo/tree/main/skills", origin, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.DiscoveredSkills{"one": {"skills/one"}}
	if !reflect.DeepEqual(intake.discovered, want) {
		t.Fatalf("discovered = %v, want scoped tree result %v", intake.discovered, want)
	}
}

// The Project declares the Source on dev and Global does not declare it at
// all. Choosing the Project must read dev: reading the Global Config's
// default branch would Materialize from a Cache the Project's Config does not
// name.
func TestAddReadsARemoteSourceOnTheChosenScopesBranch(t *testing.T) {
	scope := newAddRunScope(t)
	origin := filepath.Join(t.TempDir(), "origin")
	writeCLIGitSkill(t, origin, "sample")
	cliRunGit(t, origin, "switch", "-c", "dev")
	writeCLIGitSkill(t, origin, "dev-only")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "dev-only", "dev-only", "git", origin)
	repo := cfg.Remote["owner/repo"]
	repo.Branch = "dev"
	cfg.Remote["owner/repo"] = repo
	if err := config.SaveConfig(cfg, scope.projectConfig); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, origin, "switch", "-")

	prompter := &fakeAddPrompter{interactive: true, skills: []string{"sample"}, project: true}
	useAddPrompter(t, prompter)
	cmd := testCmd()
	cmd.SetErr(new(bytes.Buffer))
	target, err := resolveAddScope(cmd, false)
	if err != nil {
		t.Fatal(err)
	}
	intake, err := newRemoteIntake(cmd, target.ConfigPath, "owner/repo", origin, "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := intake.discovered["dev-only"]; !ok {
		t.Fatalf("discovered = %v; want the dev branch the Project declares", intake.discovered)
	}
	if err := intake.run(cmd, addRequest{scope: target}); err != nil {
		t.Fatalf("run: %v\n%s", err, cmd.OutOrStdout())
	}
	got, err := config.LoadConfig(scope.projectConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repo := got.Remote["owner/repo"]; repo.Branch != "dev" || repo.Skills["sample"] != "sample" {
		t.Fatalf("Project declares %#v; want sample added on dev", repo)
	}
}

// addRunScope is an isolated home and a Project with a local Source of two
// Skills, alpha and beta. It returns the Source, and the Global and Project
// Config and skills directory paths.
type addRunScope struct {
	source                      string
	globalConfig, projectConfig string
	globalSkills, projectSkills string
}

func newAddRunScope(t *testing.T) addRunScope {
	t.Helper()
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	project := filepath.Join(home, "project")
	source := filepath.Join(home, "source")
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(source, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, name, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	global, local := resolveScopeFor(false), resolveScopeFor(true)
	return addRunScope{
		source:       source,
		globalConfig: global.ConfigPath, projectConfig: local.ConfigPath,
		globalSkills: global.SkillsDir, projectSkills: local.SkillsDir,
	}
}

func (s addRunScope) run(t *testing.T, prompter *fakeAddPrompter, req addRequest) (string, error) {
	t.Helper()
	useAddPrompter(t, prompter)
	cmd := testCmd()
	scope, err := resolveAddScope(cmd, req.yes)
	if err != nil {
		err = endAdd(cmd.OutOrStdout(), err)
		return cmd.OutOrStdout().(*bytes.Buffer).String(), err
	}
	intake, err := newLocalIntake(cmd, s.source, "", "")
	if err != nil {
		t.Fatal(err)
	}
	req.scope = scope
	err = intake.run(cmd, req)
	return cmd.OutOrStdout().(*bytes.Buffer).String(), err
}

// plantDuplicate adds a second alpha under plugins/, so choosing alpha needs a
// Source path.
func (s addRunScope) plantDuplicate(t *testing.T) {
	t.Helper()
	path := filepath.Join(s.source, "plugins", "alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# alpha, plugin edition\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// plantConflict puts an untracked alpha on the Global skills directory, so
// adding alpha there must confirm the overwrite.
func (s addRunScope) plantConflict(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(s.globalSkills, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Add asks the Scope before it offers Skills: the Scope's Config decides
// which branch a Source is read from, and which Skills are already there.
func TestAddRunAsksScopeAndAvailabilityThenAdds(t *testing.T) {
	t.Run("Project scope", func(t *testing.T) {
		scope := newAddRunScope(t)
		prompter := &fakeAddPrompter{interactive: true, skills: []string{"alpha"}, project: true}

		if out, err := scope.run(t, prompter, addRequest{}); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if got, want := prompter.asked, []string{"scope", "skills", "availability"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("asked %v; want %v", got, want)
		}
		cfg, err := config.LoadConfig(scope.projectConfig)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := cfg.Local["alpha"]; !ok {
			t.Fatalf("Project Config does not declare alpha: %#v", cfg.Local)
		}
		if _, err := os.Stat(scope.globalConfig); !os.IsNotExist(err) {
			t.Fatalf("Global Config was written: %v", err)
		}
	})

	t.Run("customized Agents", func(t *testing.T) {
		scope := newAddRunScope(t)
		prompter := &fakeAddPrompter{interactive: true, skills: []string{"alpha"}, customize: true, agents: []string{"claude-code"}}

		if out, err := scope.run(t, prompter, addRequest{}); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		cfg, err := config.LoadConfig(scope.globalConfig)
		if err != nil {
			t.Fatal(err)
		}
		if got := engine.NewAvailability(cfg, scope.globalSkills).ManagedAgents("alpha"); !reflect.DeepEqual(got, []string{"claude-code"}) {
			t.Fatalf("alpha's Agents = %v; want [claude-code]", got)
		}
	})

	t.Run("confirmed overwrite", func(t *testing.T) {
		scope := newAddRunScope(t)
		scope.plantConflict(t)
		prompter := &fakeAddPrompter{interactive: true, skills: []string{"alpha"}}

		if out, err := scope.run(t, prompter, addRequest{}); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if !slices.Contains(prompter.asked, "overwrite") {
			t.Fatalf("asked %v; want the overwrite confirmed", prompter.asked)
		}
		if info, err := os.Lstat(filepath.Join(scope.globalSkills, "alpha")); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("alpha was not replaced by the linked Skill: %v", err)
		}
	})
}

// Without a terminal, or with --yes, Add asks nothing: it fails where only an
// answer could decide.
func TestAddRunWithoutPromptsFailsWhereOnlyAnAnswerDecides(t *testing.T) {
	tests := []struct {
		name      string
		conflict  bool
		duplicate bool
		req       addRequest
		want      string
	}{
		{name: "which Skills", req: addRequest{}, want: "multiple skills found without selection"},
		{name: "which Source path", duplicate: true, req: addRequest{skills: []string{"alpha"}}, want: `duplicate Skill "alpha" requires a Source path`},
		{name: "overwrite", conflict: true, req: addRequest{skills: []string{"alpha"}}, want: "refusing to overwrite 1 existing skill(s) without a terminal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := newAddRunScope(t)
			if tt.conflict {
				scope.plantConflict(t)
			}
			if tt.duplicate {
				scope.plantDuplicate(t)
			}
			prompter := &fakeAddPrompter{}

			out, err := scope.run(t, prompter, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want %q\n%s", err, tt.want, out)
			}
			if len(prompter.asked) != 0 {
				t.Fatalf("asked %v without a terminal", prompter.asked)
			}
		})
	}
}

// Backing out of any question is the user's choice, not a failure: Add says
// so, writes nothing, and succeeds (ADR-0002).
func TestAddRunCancellation(t *testing.T) {
	cancel := func(string, []string) (string, error) { return "", errAddCancelled }
	tests := []struct {
		name      string
		duplicate bool
		conflict  bool
		prompter  fakeAddPrompter
		wantOut   string
	}{
		{name: "Skills", prompter: fakeAddPrompter{skillsErr: errAddCancelled}, wantOut: "Operation cancelled."},
		{name: "no Skills chosen", prompter: fakeAddPrompter{skills: []string{}}, wantOut: "No skills selected. Aborted."},
		{name: "Source path", duplicate: true, prompter: fakeAddPrompter{skills: []string{"alpha"}, path: cancel}, wantOut: "Operation cancelled."},
		{name: "Scope", prompter: fakeAddPrompter{skills: []string{"alpha"}, scopeErr: errAddCancelled}, wantOut: "Operation cancelled."},
		{name: "Availability", prompter: fakeAddPrompter{skills: []string{"alpha"}, availErr: errAddCancelled}, wantOut: "Operation cancelled."},
		{name: "Agents", prompter: fakeAddPrompter{skills: []string{"alpha"}, customize: true, agentsErr: errAddCancelled}, wantOut: "Operation cancelled."},
		{name: "overwrite", conflict: true, prompter: fakeAddPrompter{skills: []string{"alpha"}, overwrite: errAddCancelled}, wantOut: "Operation cancelled."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := newAddRunScope(t)
			if tt.duplicate {
				scope.plantDuplicate(t)
			}
			if tt.conflict {
				scope.plantConflict(t)
			}
			prompter := tt.prompter
			prompter.interactive = true

			out, err := scope.run(t, &prompter, addRequest{})
			if err != nil {
				t.Fatalf("error = %v; a cancelled Add succeeds\n%s", err, out)
			}
			if strings.Count(out, tt.wantOut) != 1 {
				t.Fatalf("output does not say %q exactly once:\n%s", tt.wantOut, out)
			}
			for _, path := range []string{scope.globalConfig, scope.projectConfig} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("cancelled Add wrote %s: %v", path, err)
				}
			}
		})
	}
}

// The exit code a cancelled Add ends with, through the command itself.
func TestCLIAddCancelledExitsZero(t *testing.T) {
	scope := newAddRunScope(t)
	// resetRootCmdFlags marks --global as set, so Scope is not asked here.
	prompter := &fakeAddPrompter{interactive: true, skills: []string{"alpha"}, availErr: errAddCancelled}
	useAddPrompter(t, prompter)

	out, err := runCLI(t, "add", "--symlink", scope.source)
	if err != nil {
		t.Fatalf("add error = %v (exit %d); want exit 0\n%s", err, ExitCode(err), out)
	}
	if !strings.Contains(out, "Operation cancelled.") {
		t.Fatalf("output does not say it was cancelled (asked %v):\n%q", prompter.asked, out)
	}
}
