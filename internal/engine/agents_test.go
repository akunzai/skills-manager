package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestAvailabilityConfiguredAgentDirsSelectsDefaultsOnly(t *testing.T) {
	proj := t.TempDir()
	skillsDir := filepath.Join(proj, ".agents", "skills")

	cfg := config.DefaultConfig()
	got := NewAvailability(cfg, skillsDir).ConfiguredAgentDirs()

	if len(got) != 1 {
		t.Fatalf("expected 1 configured agent, got %d: %v", len(got), got)
	}
	want := filepath.Join(proj, ".claude", "skills")
	if got["claude-code"] != want {
		t.Errorf("claude-code dir = %q; want %q", got["claude-code"], want)
	}
	if _, ok := got["continue"]; ok {
		t.Errorf("continue should not be configured when defaultAgents is [claude]")
	}
}

func TestAvailabilityConfiguredAgentDirsHonorsInclude(t *testing.T) {
	proj := t.TempDir()
	skillsDir := filepath.Join(proj, ".agents", "skills")

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Include: []string{"continue"}}

	got := NewAvailability(cfg, skillsDir).ConfiguredAgentDirs()
	if _, ok := got["continue"]; !ok {
		t.Fatal("included continue should be configured")
	}
	if _, ok := got["claude-code"]; !ok {
		t.Fatal("expected claude-code to remain configured")
	}
}

func TestLeftoverEmptyAgentDirsIgnoresConfiguredAndNonEmpty(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	crush := filepath.Join(root, "crush")
	jazz := filepath.Join(root, "jazz")
	_ = os.MkdirAll(claude, 0755)
	_ = os.MkdirAll(crush, 0755)
	_ = os.MkdirAll(jazz, 0755)
	_ = os.WriteFile(filepath.Join(jazz, "keep"), []byte("x"), 0644)

	known := map[string]string{
		"claude-code": claude,
		"crush":       crush,
		"jazz":        jazz,
		"missing":     filepath.Join(root, "nope"),
	}
	configured := map[string]string{"claude-code": claude}

	got := leftoverEmptyAgentDirs(known, configured)
	if len(got) != 1 {
		t.Fatalf("got %#v; want only crush", got)
	}
	if got[0].Name != "crush" || got[0].Dir != crush {
		t.Fatalf("got %#v; want crush at %s", got[0], crush)
	}
}

func TestRemoveEmptyAgentDirPrunesEmptyParentsAndStopsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	jazzSkills := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(jazzSkills, 0755); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(jazzSkills, ""); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".jazz")); !os.IsNotExist(err) {
		t.Fatal("expected ~/.jazz to be removed")
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("home directory must remain")
	}
}

func TestRemoveEmptyAgentDirStopsAtXDGConfig(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	crushSkills := filepath.Join(xdg, "crush", "skills")
	if err := os.MkdirAll(crushSkills, 0755); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(crushSkills, ""); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "crush")); !os.IsNotExist(err) {
		t.Fatal("expected ~/.config/crush to be removed")
	}
	if _, err := os.Stat(xdg); err != nil {
		t.Fatal("XDG config directory must remain")
	}
}

func TestRemoveEmptyAgentDirLeavesNonEmptyParents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	grokSkills := filepath.Join(home, ".grok", "skills")
	if err := os.MkdirAll(grokSkills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(grokSkills, ""); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(grokSkills); !os.IsNotExist(err) {
		t.Fatal("expected empty skills dir to be removed")
	}
	if _, err := os.Stat(filepath.Join(home, ".grok", "config.json")); err != nil {
		t.Fatal("sibling files in parent must remain")
	}
}

func TestRemoveEmptyAgentDirDoesNotDeleteNonEmptySkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	skills := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(skills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(skills, ""); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skills, "keep")); err != nil {
		t.Fatal("non-empty skills dir must not be removed")
	}
}

func TestRemoveEmptyAgentDirStopsAtProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	projectRoot := filepath.Join(home, "code", "demo")
	cursorSkills := filepath.Join(projectRoot, ".cursor", "skills")
	if err := os.MkdirAll(cursorSkills, 0755); err != nil {
		t.Fatal(err)
	}
	// The project root holds nothing but dot entries; it must still survive.
	if err := os.MkdirAll(filepath.Join(projectRoot, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(cursorSkills, projectRoot); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("expected leftover .cursor to be removed")
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("project root must remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".git")); err != nil {
		t.Fatalf(".git must remain: %v", err)
	}
}

func TestRemoveEmptyAgentDirKeepsParentsHoldingHiddenEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	projectRoot := filepath.Join(home, "code", "demo")
	cursorSkills := filepath.Join(projectRoot, ".cursor", "skills")
	if err := os.MkdirAll(cursorSkills, 0755); err != nil {
		t.Fatal(err)
	}
	// A hidden sibling inside .cursor keeps that parent alive.
	if err := os.WriteFile(filepath.Join(projectRoot, ".cursor", ".keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(cursorSkills, projectRoot); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(cursorSkills); !os.IsNotExist(err) {
		t.Fatal("expected empty skills dir to be removed")
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".cursor", ".keep")); err != nil {
		t.Fatalf("hidden sibling must keep its parent alive: %v", err)
	}
}

func TestRemoveEmptyAgentDirPrunesOutsideHomeWithBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Project living outside $HOME: pruning is governed by the boundary alone.
	projectRoot := t.TempDir()
	windsurfSkills := filepath.Join(projectRoot, ".codeium", "windsurf", "skills")
	if err := os.MkdirAll(windsurfSkills, 0755); err != nil {
		t.Fatal(err)
	}

	if err := removeEmptyAgentDir(windsurfSkills, projectRoot); err != nil {
		t.Fatalf("removeEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".codeium")); !os.IsNotExist(err) {
		t.Fatal("expected .codeium to be pruned")
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("project root must remain: %v", err)
	}
}

// globalSkillsHome sets up an isolated home whose global skills directory holds
// one skill, and returns (home, skillsDir).
func globalSkillsHome(t *testing.T, skillName string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, skillName), 0755); err != nil {
		t.Fatal(err)
	}
	return home, skillsDir
}

// Universal agents read the skills directory directly, but older versions and
// setup scripts materialized their directories; links there must not dangle
// after the skill is removed.
func TestApplyLeftoverClearsAutomaticallyAvailableManagedPaths(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")

	codex := filepath.Join(home, ".codex", "skills")
	cursor := filepath.Join(home, ".cursor", "skills")
	for _, dir := range []string{codex, cursor} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "alpha"), filepath.Join(dir, "alpha")); err != nil {
			t.Fatal(err)
		}
	}

	availability := NewAvailability(config.DefaultConfig(), skillsDir)
	result := availability.ApplyLeftover(availability.ObserveLeftover().ForSkills([]string{"alpha"}).WithoutEmpty())

	for _, dir := range []string{codex, cursor} {
		if _, err := os.Lstat(filepath.Join(dir, "alpha")); !os.IsNotExist(err) {
			t.Fatalf("stale link left behind in %s", dir)
		}
	}
	var sawCodex, sawCursor bool
	for _, path := range result.RemovedPaths {
		switch path.Agent {
		case "codex":
			sawCodex = true
		case "cursor":
			sawCursor = true
		}
	}
	if !sawCodex || !sawCursor {
		t.Fatalf("removed = %#v; want codex and cursor reported", result.RemovedPaths)
	}
}

// The agent paths are conventions, so removal must be restricted to links this
// tool would have created. Everything else in those directories is the user's.
func TestRemoveAgentSymlinksLeavesUnmanagedEntriesAlone(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")

	codex := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(filepath.Join(codex, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	// A real directory the user owns, sharing the skill's name.
	keep := filepath.Join(codex, "alpha", "SKILL.md")
	if err := os.WriteFile(keep, []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}

	// A symlink of the same name pointing somewhere unrelated.
	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0755); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(codex, "beta")
	if err := os.Symlink(elsewhere, unrelated); err != nil {
		t.Fatal(err)
	}

	availability := NewAvailability(config.DefaultConfig(), skillsDir)
	availability.ApplyLeftover(availability.ObserveLeftover().ForSkills([]string{"alpha", "beta"}).WithoutEmpty())

	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a real directory must never be removed: %v", err)
	}
	if _, err := os.Lstat(unrelated); err != nil {
		t.Fatalf("a link pointing outside the skills dir must be left alone: %v", err)
	}
}

func TestDiagnoseAgentDirHealthClassifiesEntries(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "healthy")
	if err := os.MkdirAll(filepath.Join(skillsDir, "healthy"), 0755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	// A healthy managed link: its target still exists.
	if err := os.Symlink(filepath.Join(skillsDir, "healthy"), filepath.Join(agentDir, "healthy")); err != nil {
		t.Fatal(err)
	}
	// A managed link whose target has been removed: broken.
	if err := os.Symlink(filepath.Join(skillsDir, "removed"), filepath.Join(agentDir, "removed")); err != nil {
		t.Fatal(err)
	}
	// A dangling link this tool never created: unmanagedBroken.
	if err := os.Symlink(filepath.Join(home, "elsewhere-gone"), filepath.Join(agentDir, "foreign")); err != nil {
		t.Fatal(err)
	}
	// A real directory where a managed link is expected: physical.
	if err := os.MkdirAll(filepath.Join(agentDir, "manual"), 0755); err != nil {
		t.Fatal(err)
	}

	health := diagnoseAgentDirHealth(agentDir, skillsDir)

	if want := (AgentDirHealth{
		Broken:          []string{"removed"},
		UnmanagedBroken: []string{"foreign"},
		Physical:        []string{"manual"},
	}); !reflect.DeepEqual(health, want) {
		t.Fatalf("health = %#v; want %#v", health, want)
	}
}

// A copy of the Skill named by its own entry is Availability, not a stray
// directory, and it is classified in the same pass that rules it out of
// Physical rather than by a second scan of the same markers.
func TestDiagnoseAgentDirHealthReportsManagedCopiesApartFromPhysicalDirs(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha\n"))
	agentDir := filepath.Join(home, ".codex", "skills")
	if err := replaceManagedCopy(filepath.Join(skillsDir, "alpha"), filepath.Join(agentDir, "alpha")); err != nil {
		t.Fatal(err)
	}

	health := diagnoseAgentDirHealth(agentDir, skillsDir)

	if !reflect.DeepEqual(health.Copies, []string{"alpha"}) {
		t.Fatalf("Copies = %#v; want [alpha]", health.Copies)
	}
	if health.Physical != nil {
		t.Fatalf("a managed copy must not also read as a stray directory: %#v", health.Physical)
	}
}

func TestDiagnoseAgentDirHealthOnMissingDirReportsNothing(t *testing.T) {
	_, skillsDir := globalSkillsHome(t, "alpha")
	health := diagnoseAgentDirHealth(filepath.Join(skillsDir, "..", "..", "nope"), skillsDir)
	if !reflect.DeepEqual(health, AgentDirHealth{}) {
		t.Fatalf("got %#v; want nothing for a missing agent dir", health)
	}
}

func TestObserveLeftoverReportsDanglingAutomaticallyAvailablePaths(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "healthy")
	if err := os.MkdirAll(filepath.Join(skillsDir, "healthy"), 0755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(home, ".gemini", "skills")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(skillsDir, "healthy"), filepath.Join(agentDir, "healthy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(skillsDir, "removed"), filepath.Join(agentDir, "removed")); err != nil {
		t.Fatal(err)
	}

	occupancy := NewAvailability(config.DefaultConfig(), skillsDir).ObserveLeftover()
	var dangling, live []string
	for _, path := range occupancy.Paths {
		if path.Agent != "gemini-cli" {
			continue
		}
		if path.Dangling {
			dangling = append(dangling, path.Skill)
		} else {
			live = append(live, path.Skill)
		}
	}
	if len(dangling) != 1 || dangling[0] != "removed" {
		t.Fatalf("dangling = %v; want [removed]", dangling)
	}
	if len(live) != 1 || live[0] != "healthy" {
		t.Fatalf("live = %v; want [healthy]", live)
	}
}

func TestIsManagedSkillLinkAcceptsDanglingAndAbsoluteForms(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	codex := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(codex, 0755); err != nil {
		t.Fatal(err)
	}

	rel := filepath.Join(codex, "rel")
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "alpha"), rel); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(codex, "abs")
	if err := os.Symlink(filepath.Join(skillsDir, "alpha"), abs); err != nil {
		t.Fatal(err)
	}
	// Dangling: the master skill is gone but the link still names it.
	dangling := filepath.Join(codex, "dangling")
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "removed"), dangling); err != nil {
		t.Fatal(err)
	}

	if !isManagedSkillLink(rel, "alpha", skillsDir) {
		t.Error("relative link to the master skill should be managed")
	}
	if !isManagedSkillLink(abs, "alpha", skillsDir) {
		t.Error("absolute link to the master skill should be managed")
	}
	if !isManagedSkillLink(dangling, "removed", skillsDir) {
		t.Error("dangling link to a removed master skill should be managed")
	}
	if isManagedSkillLink(rel, "beta", skillsDir) {
		t.Error("a link naming a different skill must not be managed")
	}
	if isManagedSkillLink(filepath.Join(skillsDir, "alpha"), "alpha", skillsDir) {
		t.Error("a real directory must never be reported as a managed link")
	}
}

func TestAvailabilityApplyCreatesAndClearsLinkableAgentPath(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	availability := NewAvailability(cfg, skillsDir)

	if _, err := availability.Apply("alpha"); err != nil {
		t.Fatal(err)
	}
	claudeLink := filepath.Join(home, ".claude", "skills", "alpha")
	if !isManagedSkillLink(claudeLink, "alpha", skillsDir) {
		t.Error("expected claudeLink to be a managed link")
	}
	cfg.Settings.DefaultAgents = []string{"continue"}
	availability = NewAvailability(cfg, skillsDir)
	if _, err := availability.Apply("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(claudeLink); err == nil {
		t.Error("expected Apply to remove the undeclared Claude path")
	}
}
