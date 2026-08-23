package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/pflag"
)

func TestCLIVersion(t *testing.T) {
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"version"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	want := fmt.Sprintf("skills-manager %s\n", updater.Version)
	if got := buf.String(); got != want {
		t.Fatalf("version output = %q; want %q", got, want)
	}
}

func TestCLIInitAndLs(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	// 1. Run init
	RootCmd.SetArgs([]string{"init", "--config", configFile})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}

	// 2. Run ls --json
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"ls", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("ls --json failed: %v", err)
	}
}

func TestCLIProjectMode(t *testing.T) {
	resetRootCmdFlags()
	// resetRootCmdFlags marks --global as Changed, which makes ResolveScope
	// force Global Scope. Clear it so -p on individual commands survives.
	flagGlobal = false
	_ = RootCmd.PersistentFlags().Set("global", "false")

	tmpProjectDir := t.TempDir()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(tmpProjectDir)
	defer func() { _ = os.Chdir(oldWd) }()

	// Test skills init -p in fresh directory
	RootCmd.SetArgs([]string{"init", "-p"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("init -p failed: %v", err)
	}

	expectedConfig := filepath.Join(tmpProjectDir, ".agents", "skills.json")
	if _, err := os.Stat(expectedConfig); err != nil {
		t.Fatalf("expected .agents/skills.json to be created, but got error: %v", err)
	}

	// Test skills add -p local skill
	localSkillDir := filepath.Join(tmpProjectDir, "my-proj-skill")
	_ = os.MkdirAll(localSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte("# Proj Skill"), 0644)

	RootCmd.SetArgs([]string{"add", "-p", "--symlink", localSkillDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("add -p --symlink failed: %v", err)
	}

	// Verify master skill exists in .agents/skills
	if _, err := os.Stat(filepath.Join(tmpProjectDir, ".agents", "skills", "my-proj-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected skill in .agents/skills: %v", err)
	}

	// Verify project-level agent symlink exists in .claude/skills
	claudeSkillLink := filepath.Join(tmpProjectDir, ".claude", "skills", "my-proj-skill")
	if _, err := os.Lstat(claudeSkillLink); err != nil {
		t.Fatalf("expected project symlink .claude/skills/my-proj-skill to exist: %v", err)
	}

	// Test skills ls -p
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"ls", "-p", "--json"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("ls -p --json failed: %v", err)
	}
	if !strings.Contains(buf.String(), `"scope": "project"`) {
		t.Fatalf("expected scope to be project in ls -p --json, got: %s", buf.String())
	}

	// Test skills rm -p
	RootCmd.SetArgs([]string{"rm", "-p", "my-proj-skill"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("rm -p failed: %v", err)
	}

	// Verify master skill removed
	if _, err := os.Stat(filepath.Join(tmpProjectDir, ".agents", "skills", "my-proj-skill")); err == nil {
		t.Fatalf("expected master skill in .agents/skills to be removed")
	}

	// Verify project agent symlink removed
	if _, err := os.Lstat(claudeSkillLink); err == nil {
		t.Fatalf("expected project symlink %s to be removed, but it still exists", claudeSkillLink)
	}
}

func TestCLILocalSymlinkAddAndRemove(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	localSkillDir := filepath.Join(tmpDir, "my-local-skill")
	_ = os.MkdirAll(localSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte("# My Skill"), 0644)

	// Add local skill
	RootCmd.SetArgs([]string{"add", "--symlink", localSkillDir, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("add --symlink failed: %v", err)
	}

	// Check master link exists
	if _, err := os.Stat(filepath.Join(skillsDir, "my-local-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected symlinked skill to exist: %v", err)
	}

	// Remove skill
	RootCmd.SetArgs([]string{"rm", "my-local-skill", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("rm failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "my-local-skill")); err == nil {
		t.Fatalf("expected removed skill to no longer exist in skillsDir")
	}
}

// rm previously wrote its progress/summary text with raw fmt.Printf, bypassing
// whatever the command's writer was set to. Assert it's now capturable.
func TestCLIRmPrintsRemovalSummaryThroughCapturedOutput(t *testing.T) {
	resetRootCmdFlags()
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	localSkillDir := filepath.Join(tmpDir, "my-local-skill")
	_ = os.MkdirAll(localSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte("# My Skill"), 0644)

	if _, err := runCLI(t, "add", "--symlink", localSkillDir, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("add --symlink failed: %v", err)
	}

	out, err := runCLI(t, "rm", "my-local-skill", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("rm failed: %v", err)
	}
	if !strings.Contains(out, "Removing skill: my-local-skill") || !strings.Contains(out, "Skill removal complete") {
		t.Fatalf("rm output not captured through cmd.OutOrStdout():\n%s", out)
	}
}

// outdated previously wrote with raw fmt.Printf/Println. Assert its no-remote
// early exit is now capturable without requiring network access.
func TestCLIOutdatedNoRemoteReposPrintsThroughCapturedOutput(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")

	if _, err := runCLI(t, "init", "--config", configFile); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	out, err := runCLI(t, "outdated", "--config", configFile)
	if err != nil {
		t.Fatalf("outdated failed: %v", err)
	}
	if !strings.Contains(out, "No remote repositories configured") {
		t.Fatalf("outdated output not captured through cmd.OutOrStdout():\n%s", out)
	}
}

func TestCLISyncDoesNotPruneOrphans(t *testing.T) {
	resetRootCmdFlags()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	// 1. Initialize
	RootCmd.SetArgs([]string{"init", "--config", configFile})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 2. Create an orphaned skill on disk
	orphanDir := filepath.Join(skillsDir, "orphaned-skill")
	_ = os.MkdirAll(orphanDir, 0755)
	_ = os.WriteFile(filepath.Join(orphanDir, "SKILL.md"), []byte("# Orphan"), 0644)

	// 3. Sync only restores declared skills; it must not delete unrelated files.
	RootCmd.SetArgs([]string{"sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if _, err := os.Stat(orphanDir); err != nil {
		t.Fatalf("sync must leave orphaned skill alone: %v", err)
	}
}

func TestCLISyncPrintsCommandFailed(t *testing.T) {
	resetRootCmdFlags()
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalCommandEntry(cfg, "sample", "exit 1", "", "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Failed to run installer for sample") {
		t.Fatalf("missing command failure output:\n%s", out)
	}
}

func TestCLISyncReconcilesAvailabilityAndDryRunDoesNotMutate(t *testing.T) {
	resetRootCmdFlags()
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "github", "")
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := engine.EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}
	claudeLink := filepath.Join(project, ".claude", "skills", "sample")
	continueLink := filepath.Join(project, ".continue", "skills", "sample")

	if _, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("excluded Claude link still exists: %v", err)
	}
	if !engine.IsManagedSkillLink(continueLink, "sample", skillsDir) {
		t.Fatal("Continue link should remain")
	}

	if _, err := engine.EnsureAgentSymlink("sample", "claude", skillsDir); err != nil {
		t.Fatal(err)
	}
	dryRunOut, err := runCLI(t, "sync", "--dry-run", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dryRunOut, "Would unlink sample from claude-code") {
		t.Fatalf("dry-run did not preview availability drift:\n%s", dryRunOut)
	}
	if !engine.IsManagedSkillLink(claudeLink, "sample", skillsDir) {
		t.Fatal("dry-run removed an excluded link")
	}
	doctorOut, err := runCLI(t, "doctor", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil || !strings.Contains(doctorOut, "unexpected links: claude-code") {
		t.Fatalf("doctor did not report availability drift: err=%v\n%s", err, doctorOut)
	}
	if _, err := runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("doctor --fix: %v", err)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("doctor --fix left excluded link: %v", err)
	}
}

func TestCLIDeprecatedPruneAliasPrintsOneMigrationWarning(t *testing.T) {
	resetRootCmdFlags()
	configFile := filepath.Join(t.TempDir(), "skills.json")
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "--prune-only", "--dry-run", "--config", configFile, "--skills-dir", t.TempDir())
	if err != nil {
		t.Fatalf("deprecated prune alias failed: %v\n%s", err, out)
	}
	if got := strings.Count(out, "use `skills prune` instead"); got != 1 {
		t.Fatalf("migration warning count = %d; want 1\n%s", got, out)
	}
}

func TestCLIPruneRemovesOnlyManagedItems(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "configured", filepath.Join(skillsDir, "configured"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"configured", "orphan"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillsDir, name, "SKILL.md"), []byte("# "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	for _, agent := range []string{"claude", "augment"} {
		if _, err := engine.EnsureAgentSymlink("configured", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.EnsureAgentSymlink("orphan", "augment", skillsDir); err != nil {
		t.Fatal(err)
	}
	independent := filepath.Join(home, ".continue", "skills", "configured")
	if err := os.MkdirAll(independent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(independent, "SKILL.md"), []byte("# independent"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--yes", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --yes failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pruned 1 untracked master skill") || !strings.Contains(out, "2 unconfigured agent links") {
		t.Fatalf("expected prune summary, got:\n%s", out)
	}
	if !strings.Contains(out, "Removed master skill: orphan") || !strings.Contains(out, "Removed managed link:") {
		t.Fatalf("expected per-path prune results, got:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(skillsDir, "orphan")); !os.IsNotExist(err) {
		t.Fatal("untracked master skill should be removed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".augment", "skills", "orphan")); !os.IsNotExist(err) {
		t.Fatal("managed link for removed master skill should be removed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".augment", "skills", "configured")); !os.IsNotExist(err) {
		t.Fatal("unconfigured managed link should be removed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "configured")); err != nil {
		t.Fatal("configured managed link should remain")
	}
	if _, err := os.Stat(independent); err != nil {
		t.Fatal("independent agent skill must remain")
	}
}

func TestCLIPruneSkillsOnlyKeepsConfiguredSkillLinks(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "configured", filepath.Join(skillsDir, "configured"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"configured", "orphan"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.EnsureAgentSymlink("configured", "augment", skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.EnsureAgentSymlink("orphan", "augment", skillsDir); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "prune", "--skills-only", "--yes", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".augment", "skills", "configured")); err != nil {
		t.Fatal("--skills-only must keep links for configured skills")
	}
	if _, err := os.Lstat(filepath.Join(home, ".augment", "skills", "orphan")); !os.IsNotExist(err) {
		t.Fatal("--skills-only must remove links for a removed master skill")
	}
}

func TestCLIPruneRequiresYesWithoutTerminal(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	orphan := filepath.Join(skillsDir, "orphan")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatal("prune without --yes must fail without a terminal")
	}
	if !strings.Contains(out, "Prune plan:") {
		t.Fatalf("expected plan before refusal, got:\n%s", out)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("refused prune must not delete files")
	}
}

func TestSelectedPrunePlanExpandsMasterSkillsAndKeepsIndividualLinks(t *testing.T) {
	plan := engine.PrunePlan{
		UntrackedSkills: []string{"orphan"},
		Unconfigured: []engine.PruneLink{
			{Agent: "augment", Path: "/agents/augment/orphan"},
			{Agent: "continue", Path: "/agents/continue/configured"},
		},
	}

	selected := selectedPrunePlan(plan, []string{pruneMasterKey("orphan"), pruneLinkKey("/agents/continue/configured")})
	if got, want := selected.UntrackedSkills, []string{"orphan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected master skills = %v; want %v", got, want)
	}
	if got, want := selected.Unconfigured, []engine.PruneLink{
		{Agent: "augment", Path: "/agents/augment/orphan"},
		{Agent: "continue", Path: "/agents/continue/configured"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected links = %v; want %v", got, want)
	}
}

func TestCLILsJSONAgentsAreDeclaredAvailability(t *testing.T) {
	resetRootCmdFlags()
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	source := filepath.Join(project, "sample-source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "sample")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(skillsDir, "sample"), filepath.Join(project, ".continue", "skills", "sample")); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalSymlinkEntry(cfg, "sample", source, "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "ls", "--json", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if len(listed) != 1 {
		t.Fatalf("ls --json items = %d\n%s", len(listed), out)
	}
	agents, _ := listed[0]["agents"].([]any)
	if len(agents) != 1 || agents[0] != "claude-code" {
		t.Fatalf("ls --json agents = %#v; want [claude-code] not disk Lstat", listed[0]["agents"])
	}

	agentOut, err := runCLI(t, "ls", "--agent", "continue", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --agent continue: %v\n%s", err, agentOut)
	}
	if strings.Contains(agentOut, "sample") {
		t.Fatalf("ls --agent continue should not list a skill only linked on disk:\n%s", agentOut)
	}

	claudeOut, err := runCLI(t, "ls", "--agent", "claude", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --agent claude: %v\n%s", err, claudeOut)
	}
	if !strings.Contains(claudeOut, "sample") {
		t.Fatalf("ls --agent claude missing sample:\n%s", claudeOut)
	}

	autoOut, err := runCLI(t, "ls", "--agent", "gemini", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --agent gemini: %v\n%s", err, autoOut)
	}
	if !strings.Contains(autoOut, "sample") {
		t.Fatalf("ls --agent gemini should list installed Skills:\n%s", autoOut)
	}
}

func TestCLILsFormatting(t *testing.T) {
	resetRootCmdFlags()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	RootCmd.SetArgs([]string{"init", "--config", configFile})
	_ = RootCmd.Execute()

	// Add local skill
	localDir := filepath.Join(tmpDir, "sample-skill")
	_ = os.MkdirAll(localDir, 0755)
	_ = os.WriteFile(filepath.Join(localDir, "SKILL.md"), []byte("# Sample"), 0644)

	RootCmd.SetArgs([]string{"add", "--symlink", localDir, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	_ = RootCmd.Execute()

	// Run standard table ls
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"ls", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("ls table view failed: %v", err)
	}
}

// A custom --skills-dir with neither --project nor --global set must not by
// itself flip the reported Scope to project: only the flag says which Scope
// this is, not where its skills directory happens to point.
func TestCLILsJSONScopeLabelIgnoresCustomSkillsDirWithoutProjectFlag(t *testing.T) {
	resetRootCmdFlags()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	RootCmd.SetArgs([]string{"init", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"ls", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("ls --json failed: %v", err)
	}
	if strings.Contains(buf.String(), `"scope": "project"`) {
		t.Fatalf("custom --skills-dir without --project must not report project scope, got: %s", buf.String())
	}
}

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("VIBE_HOME", "")
	t.Setenv("HERMES_HOME", "")
	t.Setenv("AUTOHAND_HOME", "")
	t.Setenv("GROK_HOME", "")
	return home
}

// resetSubcommandFlags clears flag state on every subcommand. Commands are
// built once in init(), so their flag targets are closure variables that keep
// whatever a previous Execute set — a leaked --skill or --symlink silently
// changes what the next test is actually running.
func resetSubcommandFlags() {
	for _, c := range RootCmd.Commands() {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}
}

func resetRootCmdFlags() {
	resetSubcommandFlags()
	flagConfigFile = ""
	flagSkillsDir = ""
	flagCacheDir = ""
	flagProject = false
	flagGlobal = true
	_ = RootCmd.PersistentFlags().Set("config", "")
	_ = RootCmd.PersistentFlags().Set("skills-dir", "")
	_ = RootCmd.PersistentFlags().Set("cache-dir", "")
	_ = RootCmd.PersistentFlags().Set("global", "true")
	_ = RootCmd.PersistentFlags().Set("project", "false")
}

func TestCLIDoctorIgnoresUnusedEmptyAgentDirs(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(home, ".claude", "skills")
	crushDir := filepath.Join(home, ".config", "crush", "skills")
	jazzDir := filepath.Join(home, ".jazz", "skills")
	for _, dir := range []string{claudeDir, crushDir, jazzDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetErr(&buf)
	RootCmd.SetArgs([]string{"doctor", "--config", configFile, "--skills-dir", skillsDir})
	err := RootCmd.Execute()
	out := buf.String()

	if !strings.Contains(out, "[claude-code]") {
		t.Fatalf("expected configured claude-code in doctor output, got:\n%s", out)
	}
	if strings.Contains(out, "[crush]") {
		t.Fatalf("did not expect unused crush as a healthy harness, got:\n%s", out)
	}
	if strings.Contains(out, "[jazz]") {
		t.Fatalf("did not expect unused jazz as a healthy harness, got:\n%s", out)
	}
	if !strings.Contains(out, "leftover empty") {
		t.Fatalf("expected leftover empty agent dirs warning, got:\n%s", out)
	}
	if err == nil {
		t.Fatal("expected doctor to report leftover empty dirs as issues")
	}
}

func TestCLIDoctorFixRemovesLeftoverEmptyAgentDirs(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(home, ".claude", "skills")
	crushDir := filepath.Join(home, ".config", "crush", "skills")
	jazzDir := filepath.Join(home, ".jazz", "skills")
	for _, dir := range []string{claudeDir, crushDir, jazzDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetErr(&buf)
	RootCmd.SetArgs([]string{"doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir})
	_ = RootCmd.Execute()

	if _, err := os.Stat(filepath.Join(home, ".jazz")); !os.IsNotExist(err) {
		t.Fatal("expected leftover ~/.jazz to be removed")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "crush")); !os.IsNotExist(err) {
		t.Fatal("expected leftover ~/.config/crush to be removed")
	}
	if _, err := os.Stat(claudeDir); err != nil {
		t.Fatal("configured claude skills dir must remain")
	}
}

func TestCLIUpdateDryRunAndJSON(t *testing.T) {
	resetRootCmdFlags()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/test-repo", "skill-1", "skills/skill-1", "github", "")
	_ = config.SaveConfig(cfg, configFile)

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"update", "--dry-run", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("update --dry-run --json failed: %v", err)
	}

	if !strings.Contains(buf.String(), `"updated_repos"`) {
		t.Fatalf("expected updated_repos in JSON output, got: %s", buf.String())
	}
}

// projectScope prepares an isolated home plus a project directory that is the
// working directory, and returns the project root.
func projectScope(t *testing.T) string {
	t.Helper()
	resetRootCmdFlags()
	// resetRootCmdFlags marks --global as Changed, which makes ResolveScope
	// force Global Scope and ignore -p. Clear it so Project Scope survives.
	flagGlobal = false
	_ = RootCmd.PersistentFlags().Set("global", "false")
	home := isolateHome(t)
	project := filepath.Join(home, "workspace", "demo")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	// os.Getwd resolves symlinks (/var -> /private/var on macOS), and the CLI
	// derives project paths from it, so compare against the resolved form.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetErr(&buf)
	RootCmd.SetArgs(args)
	err := RootCmd.Execute()
	return buf.String(), err
}

func TestCLIConfigSetGetAndClear(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	if _, err := runCLI(t, "config", "set", "defaultAgents", "claude,continue", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("config set defaultAgents: %v", err)
	}
	out, err := runCLI(t, "config", "get", "defaultAgents", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("config get defaultAgents: %v", err)
	}
	if strings.TrimSpace(out) != `["claude-code","continue"]` {
		t.Fatalf("config get output = %q", out)
	}

	if _, err := runCLI(t, "config", "set", "excludeAgents", "continue", "--config", configFile, "--skills-dir", skillsDir); err == nil {
		t.Fatal("expected unknown config key excludeAgents")
	}
}

func TestCLIConfigAgentDefaultsReconcileInstalledSkills(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	source := filepath.Join(home, "sample-source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "add", "--symlink", source, "--skill", "sample", "--yes", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runCLI(t, "config", "set", "defaultAgents", "continue", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("config set defaultAgents: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".continue", "skills", "sample")); err != nil {
		t.Fatalf("continue link missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatalf("stale claude link remains; err = %v", err)
	}
}

func TestCLIAgentsMutationsPersistAndReconcile(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	source := filepath.Join(home, "sample-source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "add", "--symlink", source, "--skill", "sample", "--yes", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runCLI(t, "agents", "sample", "include", "continue", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("agents include: %v", err)
	}
	continueLink := filepath.Join(home, ".continue", "skills", "sample")
	if _, err := os.Lstat(continueLink); err != nil {
		t.Fatalf("continue link missing: %v", err)
	}
	if _, err := runCLI(t, "agents", "sample", "exclude", "claude", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("agents exclude: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatalf("claude link should be removed; err = %v", err)
	}
	if _, err := runCLI(t, "agents", "sample", "follow-defaults", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("agents follow-defaults: %v", err)
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatalf("follow-defaults left an override: %#v", cfg.Settings.Availability["sample"])
	}
}

func TestCLIAddAgentPersistsAndRmAgentIsRemoved(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	source := filepath.Join(home, "sample-source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "add", "--symlink", source, "--skill", "sample", "--agent", "continue", "--yes", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("add --agent: %v", err)
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Settings.Availability["sample"].Include; !reflect.DeepEqual(got, []string{"continue"}) {
		t.Fatalf("availability include = %v", got)
	}
	if _, err := runCLI(t, "rm", "sample", "--agent", "continue", "--config", configFile, "--skills-dir", skillsDir); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("rm --agent error = %v", err)
	}
}

// A source inside the project must be stored relative to the project root, so
// that a committed skills.json still resolves on a teammate's checkout.
func TestCLIProjectSymlinkSourceIsRelativeToProject(t *testing.T) {
	project := projectScope(t)

	inRepo := filepath.Join(project, "in-repo-skill")
	if err := os.MkdirAll(inRepo, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inRepo, "SKILL.md"), []byte("# In repo"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	if _, err := runCLI(t, "add", "-p", "--symlink", "./in-repo-skill", "--skill", "inrepo"); err != nil {
		t.Fatalf("add -p --symlink: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(project, ".agents", "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Local["inrepo"].Source
	if got != "in-repo-skill" {
		t.Fatalf("stored source = %q; want the project-relative %q", got, "in-repo-skill")
	}

	link := filepath.Join(project, ".agents", "skills", "inrepo")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Fatalf("symlink target %q is absolute; an in-project source must link relatively", target)
	}
	if _, err := os.Stat(filepath.Join(link, "SKILL.md")); err != nil {
		t.Fatalf("relative symlink must resolve: %v", err)
	}
}

// A source outside the project cannot be expressed relative to it, so it keeps
// its ~/ or absolute path rather than a project-relative path.
func TestCLIProjectSymlinkSourceOutsideProjectStaysAbsolute(t *testing.T) {
	project := projectScope(t)

	outside := filepath.Join(filepath.Dir(filepath.Dir(project)), "external-skill")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("# External"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	if _, err := runCLI(t, "add", "-p", "--symlink", outside, "--skill", "external"); err != nil {
		t.Fatalf("add -p --symlink: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(project, ".agents", "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Local["external"].Source
	if strings.HasPrefix(got, ".") {
		t.Fatalf("stored source = %q; a source outside the project must not be project-relative", got)
	}
	if got != "~/external-skill" && !filepath.IsAbs(got) {
		t.Fatalf("stored source = %q; want ~/external-skill or absolute path", got)
	}
}

// The config written by one project must resolve against whichever project it
// is synced into, not the one that produced it.
func TestCLIProjectSyncResolvesRelativeSourceInNewCheckout(t *testing.T) {
	project := projectScope(t)

	for _, dir := range []string{"in-repo-skill", ".agents"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "in-repo-skill", "SKILL.md"), []byte("# In repo"), 0644); err != nil {
		t.Fatal(err)
	}

	// A config as it would arrive from a teammate's commit.
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "inrepo", "in-repo-skill", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "-p")
	if err != nil {
		t.Fatalf("sync -p: %v\n%s", err, out)
	}

	resolved, err := filepath.EvalSymlinks(filepath.Join(project, ".agents", "skills", "inrepo"))
	if err != nil {
		t.Fatalf("synced skill must resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(project, "in-repo-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("synced skill resolves to %q; want this project's %q", resolved, want)
	}
}

// Whatever --fix repaired must stop counting as an outstanding issue, or
// `doctor --fix` reports failure and exits non-zero after a successful repair.
func TestCLIDoctorFixDoesNotReportRepairedIssues(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}

	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	claudeSkills := filepath.Join(project, ".claude", "skills")
	if _, err := engine.EnsureAgentSymlink("alpha", "claude", skillsDir); err != nil {
		t.Fatal(err)
	}
	// A healthy managed link and a dangling symlink.
	brokenTarget, err := filepath.Rel(claudeSkills, filepath.Join(skillsDir, "broken"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(brokenTarget, filepath.Join(claudeSkills, "broken")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--fix", "-p")
	if err != nil {
		t.Fatalf("doctor --fix repaired everything but still failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "Run with --fix") {
		t.Fatalf("doctor --fix told the user to run --fix:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(claudeSkills, "broken")); !os.IsNotExist(err) {
		t.Fatal("broken symlink should have been removed")
	}
	if !engine.IsManagedSkillLink(filepath.Join(claudeSkills, "alpha"), "alpha", skillsDir) {
		t.Fatal("healthy managed link should remain")
	}
}

// doctor --fix must still report what it could not repair.
func TestCLIDoctorFixStillReportsUnrepairableIssues(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".agents", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	// A physical directory with no master skill behind it cannot be converted.
	orphan := filepath.Join(project, ".claude", "skills", "no-master")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("# Orphan"), 0644); err != nil {
		t.Fatal(err)
	}
	unmanagedBroken := filepath.Join(project, ".claude", "skills", "custom-broken")
	if err := os.Symlink(filepath.Join(project, "gone"), unmanagedBroken); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--fix", "-p")
	if err == nil {
		t.Fatalf("doctor --fix should fail while an issue remains unrepaired:\n%s", out)
	}
	if !strings.Contains(out, "Cannot replace unmanaged directory") {
		t.Fatalf("expected an explanation of what could not be repaired:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(orphan, "SKILL.md")); err != nil {
		t.Fatalf("doctor --fix modified unmanaged data: %v", err)
	}
	if _, err := os.Lstat(unmanagedBroken); err != nil {
		t.Fatalf("doctor --fix removed unmanaged broken symlink: %v", err)
	}
}

// Cobra prints usage for any error out of RunE; a runtime failure is not misuse.
func TestCLIRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".agents", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(project, ".claude", "skills", "no-master")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil {
		t.Fatal("expected doctor to report the outstanding issue")
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "Global Flags:") {
		t.Fatalf("a runtime failure must not print the usage block:\n%s", out)
	}
}

// Genuine misuse keeps its usage block.
func TestCLIMisuseStillPrintsUsage(t *testing.T) {
	projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}

	out, err := runCLI(t, "rm", "-p")
	if err == nil {
		t.Fatal("expected rm with no skill name to fail")
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("missing required argument should still print usage:\n%s", out)
	}
}

// Stale links in universal agent directories were invisible to doctor, so once
// accumulated they could never be cleaned up.
func TestCLIDoctorDetectsAndFixesStaleUniversalAgentLinks(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)

	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(home, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	claude := filepath.Join(home, ".claude", "skills")
	codex := filepath.Join(home, ".codex", "skills")
	for _, dir := range []string{claude, codex} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "alpha"), filepath.Join(claude, "alpha")); err != nil {
		t.Fatal(err)
	}
	// Left over from a skill that no longer exists.
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "gone"), filepath.Join(codex, "gone")); err != nil {
		t.Fatal(err)
	}
	// The user's own content in the same directory must survive.
	ownDir := filepath.Join(codex, ".system")
	if err := os.MkdirAll(ownDir, 0755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatalf("doctor should report the stale link:\n%s", out)
	}
	if !strings.Contains(out, "Stale links") || !strings.Contains(out, "gone") {
		t.Fatalf("expected the stale link to be reported:\n%s", out)
	}

	out, err = runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("doctor --fix should succeed after repairing:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(codex, "gone")); !os.IsNotExist(err) {
		t.Fatal("stale link should have been removed")
	}
	if _, err := os.Stat(ownDir); err != nil {
		t.Fatalf("the agent's own directory must be left alone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(claude, "alpha")); err != nil {
		t.Fatalf("a healthy link must be left alone: %v", err)
	}
}

func TestCLILocalDirectoryScanMultipleSkills(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	if _, err := runCLI(t, "init", "--config", configFile); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Create a local repo with multiple skills
	localRepo := filepath.Join(home, "code", "agent-skills")
	skill1Dir := filepath.Join(localRepo, "skills", "skill-one")
	skill2Dir := filepath.Join(localRepo, "skills", "skill-two")
	_ = os.MkdirAll(skill1Dir, 0755)
	_ = os.MkdirAll(skill2Dir, 0755)
	_ = os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte("---\nname: skill-one\n---\n# Skill One"), 0644)
	_ = os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte("---\nname: skill-two\n---\n# Skill Two"), 0644)

	// Test add --all with --symlink
	out, err := runCLI(t, "add", "--symlink", localRepo, "--all", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("add --symlink --all failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Linked 2 local skill(s)") {
		t.Fatalf("expected 2 skills linked, got:\n%s", out)
	}

	// Check master links exist
	if _, err := os.Stat(filepath.Join(skillsDir, "skill-one", "SKILL.md")); err != nil {
		t.Fatalf("expected skill-one in skillsDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "skill-two", "SKILL.md")); err != nil {
		t.Fatalf("expected skill-two in skillsDir: %v", err)
	}

	// Check config has tilde paths
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if src := cfg.Local["skill-one"].Source; !strings.HasPrefix(src, "~/code/agent-skills/skills/skill-one") {
		t.Fatalf("expected tilde path in config, got: %s", src)
	}

	// Buffered output uses a text label and keeps the tilde path.
	lsOut, err := runCLI(t, "ls", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls failed: %v", err)
	}
	if !strings.Contains(lsOut, "[link] ~/code/agent-skills") {
		t.Fatalf("expected tilde path in ls output, got:\n%s", lsOut)
	}
}

func TestCLILocalPositionalPathAutoDetection(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	if _, err := runCLI(t, "init", "--config", configFile); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	localDir := filepath.Join(home, "my-standalone-skill")
	_ = os.MkdirAll(localDir, 0755)
	_ = os.WriteFile(filepath.Join(localDir, "SKILL.md"), []byte("# Standalone"), 0644)

	// Add via positional argument "~/my-standalone-skill" without --symlink flag
	out, err := runCLI(t, "add", "~/my-standalone-skill", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("add positional local path failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Linked 1 local skill(s)") {
		t.Fatalf("expected 1 skill linked, got:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "my-standalone-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected master symlink for my-standalone-skill: %v", err)
	}
}

func TestCLISyncReplacesLocalSymlinkWithRemotePhysicalSkill(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	if _, err := runCLI(t, "init", "--config", configFile); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Add local symlink skill
	localDir := filepath.Join(home, "override-skill")
	_ = os.MkdirAll(localDir, 0755)
	_ = os.WriteFile(filepath.Join(localDir, "SKILL.md"), []byte("# Local Version"), 0644)
	if _, err := runCLI(t, "add", "--symlink", localDir, "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("add --symlink failed: %v", err)
	}

	// 2. Add remote skill entry with same name
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "override-skill", "override-skill", "github", "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	// Verify cfg.Local no longer has override-skill
	if _, ok := cfg.Local["override-skill"]; ok {
		t.Fatal("expected local entry for override-skill to be removed when remote was added")
	}

	// Verify ls shows remote source
	lsOut, err := runCLI(t, "ls", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls failed: %v", err)
	}
	if !strings.Contains(lsOut, "[remote] owner/repo") {
		t.Fatalf("expected ls to display remote repo source, got:\n%s", lsOut)
	}
}

func TestCLILocalAddOverwriteRequiresConfirmationOrYes(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")

	if _, err := runCLI(t, "init", "--config", configFile); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. First install version 1
	v1Dir := filepath.Join(home, "v1", "my-skill")
	_ = os.MkdirAll(v1Dir, 0755)
	_ = os.WriteFile(filepath.Join(v1Dir, "SKILL.md"), []byte("# V1"), 0644)
	if _, err := runCLI(t, "add", "--symlink", v1Dir, "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	// 2. Prepare version 2 at a different path
	v2Dir := filepath.Join(home, "v2", "my-skill")
	_ = os.MkdirAll(v2Dir, 0755)
	_ = os.WriteFile(filepath.Join(v2Dir, "SKILL.md"), []byte("# V2"), 0644)

	// In non-terminal test environment without --yes, attempting to overwrite should return error
	out, err := runCLI(t, "add", "--symlink", v2Dir, "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatalf("expected error when overwriting without terminal and without --yes, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected refusing to overwrite error, got: %v", err)
	}

	// With --yes (-y), overwriting succeeds
	out, err = runCLI(t, "add", "--symlink", v2Dir, "-y", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("expected success with -y, got: %v\n%s", err, out)
	}
}

func TestCLIDoctorTildePathFormatting(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out)
	}

	// Should contain tilde paths
	if !strings.Contains(out, "Master skills directory: ~/.agents/skills") {
		t.Fatalf("expected tilde path for master skills directory in doctor output, got:\n%s", out)
	}
	if !strings.Contains(out, "Symlinks healthy (~/.claude/skills)") {
		t.Fatalf("expected tilde path for agent directory in doctor output, got:\n%s", out)
	}
	// Must not contain raw absolute home directory
	if strings.Contains(out, home) {
		t.Fatalf("expected doctor output not to contain raw absolute home path %q, got:\n%s", home, out)
	}
}

func TestCLILsJSONTildePath(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	skillDir := filepath.Join(skillsDir, "sample-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Sample"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "ls", "--json", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --json failed: %v\n%s", err, out)
	}

	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if len(items) == 0 {
		t.Fatal("expected at least 1 item in JSON output")
	}
	if items[0]["path"] != "~/.agents/skills/sample-skill" {
		t.Fatalf("expected path ~/.agents/skills/sample-skill, got: %v", items[0]["path"])
	}
}

func TestCLICommandAddOverwriteRequiresYes(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if _, err := runCLI(t, "add", "--command", "echo first", "--skill", "cmd-skill", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("first add: %v", err)
	}
	out, err := runCLI(t, "add", "--command", "echo second", "--skill", "cmd-skill", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatalf("expected overwrite refusal, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("got %v", err)
	}
	if _, err := runCLI(t, "add", "--command", "echo second", "--skill", "cmd-skill", "-y", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local["cmd-skill"].Command != "echo second" {
		t.Fatalf("command = %#v", cfg.Local["cmd-skill"])
	}
}

func TestCLICommandAddSavesWhenInstallerFails(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	out, err := runCLI(t, "add", "--command", "exit 1", "--skill", "cmd-skill", "-y", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Warning: Install command returned error") {
		t.Fatalf("expected installer warning, got:\n%s", out)
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local["cmd-skill"].Command != "exit 1" {
		t.Fatalf("command = %#v", cfg.Local)
	}
}
