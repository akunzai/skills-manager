package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
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

// An unreadable Scope state keeps rm from forgetting the Baseline, not from
// removing the Skill: it says why and exits 2.
// A local Skill has no Baseline to forget, so an unreadable Scope state is a
// warning for rm, not a failure (ADR-0002). A remote Skill's Baseline cannot
// be forgotten, which is; nor can a stale one of a Skill Config does not
// declare be ruled out.
func TestCLIRmOnUnreadableScopeStateFailsOnlyForARemoteSkill(t *testing.T) {
	for _, tc := range []struct {
		name       string
		remote     bool
		undeclared bool
		wantExit   int
		wantLine   string
	}{
		{name: "local", wantExit: 0, wantLine: scopeStateWarning},
		{name: "remote", remote: true, wantExit: 2, wantLine: "Failed to read the Scope baseline: "},
		{name: "undeclared", undeclared: true, wantExit: 2, wantLine: "Failed to read the Scope baseline: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetSubcommandFlags()
			t.Cleanup(resetSubcommandFlags)
			isolateHome(t)
			root := t.TempDir()
			configFile, skillsDir, cacheDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
			addArgs := []string{"add", "--symlink", writeCLILocalSkill(t, root, "sample")}
			if tc.remote {
				origin := filepath.Join(root, "origin")
				writeCLIGitSkill(t, origin, "sample")
				addArgs = []string{"add", "owner/repo", "--url", origin, "--skill", "sample", "-y"}
			}
			if tc.undeclared {
				// An Untracked Skill: on the skills directory, not in Config.
				writeCLILocalSkill(t, skillsDir, "sample")
			} else if out, err := runCLI(t, append(addArgs, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)...); err != nil {
				t.Fatalf("add: %v\n%s", err, out)
			}
			statePath, bad := makeScopeStateUnreadable(t, skillsDir)

			out, err := runCLI(t, "rm", "sample", "-y", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)

			if exit := exitCodeOf(err); exit != tc.wantExit {
				t.Fatalf("rm error = %v (exit %d); want exit %d\n%s", err, exit, tc.wantExit, out)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Fatalf("output does not contain %q:\n%s", tc.wantLine, out)
			}
			assertScopeStateWarning(t, out, tc.wantExit == 0)
			if _, err := os.Lstat(filepath.Join(skillsDir, "sample")); !os.IsNotExist(err) {
				t.Fatal("the Skill must still be removed")
			}
			if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
				t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
			}
		})
	}
}

// An Availability link rm cannot remove is not silently left for doctor to
// find: rm names it, reports the Skill not fully removed, and exits 2.
func TestCLIRmReportsAnAvailabilityLinkItCouldNotRemove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this user removing its entries")
	}
	resetRootCmdFlags()
	isolateHome(t)
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	writeCLILocalSkill(t, filepath.Dir(skillsDir), "sample")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalSymlinkEntry(cfg, "sample", filepath.Join(filepath.Dir(skillsDir), "local", "sample"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	agentDir := filepath.Join(project, ".claude", "skills")
	link := filepath.Join(agentDir, "sample")
	if !isSymlink(link) {
		t.Fatal("sync did not link the Skill for Claude Code")
	}
	if err := os.Chmod(agentDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentDir, 0o755) })

	out, err := runCLI(t, "rm", "sample", "-y", "--config", configFile, "--skills-dir", skillsDir)

	if exit := exitCodeOf(err); exit != 2 {
		t.Fatalf("rm error = %v (exit %d); want exit 2\n%s", err, exit, out)
	}
	if !strings.Contains(out, "Failed to unlink from claude-code: "+link) {
		t.Fatalf("output does not name the link rm could not remove:\n%s", out)
	}
	if strings.Contains(out, "Skill removal complete") || !strings.Contains(err.Error(), "sample") {
		t.Fatalf("rm must report sample not fully removed: err=%v\n%s", err, out)
	}
	if !isSymlink(link) {
		t.Fatal("the link was removed from a read-only directory")
	}
	if _, statErr := os.Lstat(filepath.Join(skillsDir, "sample")); !os.IsNotExist(statErr) {
		t.Fatal("a link rm could not remove must not stop the Scope copy going")
	}
}

// Sync needs a Baseline only for a remote Skill. A Scope declaring none still
// matches its Config with an unreadable Scope state, and says so with a
// warning; one declaring a remote Skill cannot record its Baseline.
func TestCLISyncOnUnreadableScopeStateFailsOnlyWithARemoteSkill(t *testing.T) {
	for _, tc := range []struct {
		name     string
		remote   bool
		command  []string
		wantExit int
		wantLine string
	}{
		{name: "local", command: []string{"sync"}, wantExit: 0, wantLine: scopeStateWarning},
		{name: "local dry run", command: []string{"sync", "--dry-run"}, wantExit: 0, wantLine: scopeStateWarning},
		{name: "local update", command: []string{"update"}, wantExit: 0, wantLine: scopeStateWarning},
		{name: "remote", remote: true, command: []string{"sync"}, wantExit: 2, wantLine: "Failed to read the Scope baseline: "},
		{name: "remote update", remote: true, command: []string{"update"}, wantExit: 2, wantLine: "Failed to read the Scope baseline: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetSubcommandFlags()
			t.Cleanup(resetSubcommandFlags)
			isolateHome(t)
			root := t.TempDir()
			configFile, skillsDir, cacheDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
			addArgs := []string{"add", "--symlink", writeCLILocalSkill(t, root, "sample")}
			if tc.remote {
				origin := filepath.Join(root, "origin")
				writeCLIGitSkill(t, origin, "sample")
				addArgs = []string{"add", "owner/repo", "--url", origin, "--skill", "sample", "-y"}
			}
			if out, err := runCLI(t, append(addArgs, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)...); err != nil {
				t.Fatalf("add: %v\n%s", err, out)
			}
			statePath, bad := makeScopeStateUnreadable(t, skillsDir)

			out, err := runCLI(t, append(tc.command, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)...)

			if exit := exitCodeOf(err); exit != tc.wantExit {
				t.Fatalf("%s error = %v (exit %d); want exit %d\n%s", strings.Join(tc.command, " "), err, exit, tc.wantExit, out)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Fatalf("output does not contain %q:\n%s", tc.wantLine, out)
			}
			assertScopeStateWarning(t, out, tc.wantExit == 0)
			if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
				t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
			}
		})
	}
}

// A local Add has no Baseline to record: it warns about an unreadable Scope
// state and succeeds. TestCLIAddReportsUnreadableScopeState covers remote.
func TestCLIAddOfALocalSkillWarnsOnUnreadableScopeState(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	makeScopeStateUnreadable(t, skillsDir)

	out, err := runCLI(t, "add", "--symlink", writeCLILocalSkill(t, root, "sample"), "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)

	if err != nil {
		t.Fatalf("add error = %v (exit %d); want exit 0\n%s", err, ExitCode(err), out)
	}
	assertScopeStateWarning(t, out, true)
}

// scopeStateWarning opens the warning a command prints for an unreadable
// Scope state it needed no Baseline from (ADR-0002).
const scopeStateWarning = "Scope state is unreadable: "

// assertScopeStateWarning checks that out warns about an unreadable Scope
// state, naming doctor --fix, exactly when want: a command that fails on it
// says so instead.
func assertScopeStateWarning(t *testing.T, out string, want bool) {
	t.Helper()
	got := strings.Contains(out, scopeStateWarning) && strings.Contains(out, "--fix' to reset it.")
	if got != want {
		t.Fatalf("warns about the unreadable Scope state = %v; want %v\n%s", got, want, out)
	}
}

// exitCodeOf is the process exit code for a command's returned error.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	return ExitCode(err)
}

// writeCLILocalSkill writes a local Skill directory named name under root and
// returns its path.
func writeCLILocalSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, "local", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
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
	if !strings.Contains(out, "No remote Sources configured") {
		t.Fatalf("outdated output not captured through cmd.OutOrStdout():\n%s", out)
	}
}

func TestCLISyncDoesNotPruneOrphans(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)

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
	isolateHome(t)
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
	if err == nil {
		t.Fatalf("failed command should make Sync non-zero:\n%s", out)
	}
	if !strings.Contains(out, "Failed to run installer for sample") {
		t.Fatalf("missing command failure output:\n%s", out)
	}
}

// A path Availability refuses fails Sync (ADR-0002), so the preview says so
// up front rather than promising work the Sync it points to cannot do.
func TestCLISyncDryRunReportsARefusedAgentPathAsFailed(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeCLIGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}

	out, err := runCLI(t, append(args, "--dry-run")...)
	if ExitCode(err) != 2 {
		t.Fatalf("dry-run = %v; want exit 2:\n%s", err, out)
	}
	if !strings.Contains(out, "Would fail sample: ") || !strings.Contains(out, "is not managed by skills") || !strings.Contains(out, "Next: run 'skills doctor") {
		t.Fatalf("dry-run did not preview the refused path:\n%s", out)
	}
	if strings.Contains(out, "to reconcile") {
		t.Fatalf("dry-run still counts the refused Skill as pending:\n%s", out)
	}
	if out, err := runCLI(t, args...); ExitCode(err) != 2 {
		t.Fatalf("sync = %v; want the exit 2 the preview promised:\n%s", err, out)
	}
}

func TestCLISyncReconcilesAvailabilityAndDryRunDoesNotMutate(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	project := t.TempDir()
	configFile := filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeCLIGitSkill(t, origin, "sample")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		plantManagedAgentLink(t, skillsDir, "sample", agent)
	}
	claudeLink := filepath.Join(project, ".claude", "skills", "sample")
	continueLink := filepath.Join(project, ".continue", "skills", "sample")

	if _, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("excluded Claude link still exists: %v", err)
	}
	if !isSymlink(continueLink) {
		t.Fatal("Continue link should remain")
	}

	plantManagedAgentLink(t, skillsDir, "sample", "claude")
	dryRunOut, _ := runCLI(t, "sync", "--dry-run", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if !strings.Contains(dryRunOut, "Would unlink sample from claude-code") {
		t.Fatalf("dry-run did not preview availability drift:\n%s", dryRunOut)
	}
	if !isSymlink(claudeLink) {
		t.Fatal("dry-run removed an excluded link")
	}
	doctorOut, err := runCLI(t, "doctor", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || !strings.Contains(doctorOut, "unexpected links: claude-code") {
		t.Fatalf("doctor did not report availability drift: err=%v\n%s", err, doctorOut)
	}
	if out, err := runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("doctor --fix left excluded link: %v", err)
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
		plantManagedAgentLink(t, skillsDir, "configured", agent)
	}
	plantManagedAgentLink(t, skillsDir, "orphan", "augment")
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
	if !strings.Contains(out, "2 unconfigured agent links") {
		t.Fatalf("expected prune summary, got:\n%s", out)
	}
	if !strings.Contains(out, "Skipped 1 untracked real directory") || !strings.Contains(out, "Skipped master skill: orphan") {
		t.Fatalf("expected real master to be skipped, got:\n%s", out)
	}
	if strings.Contains(out, "Removed master skill: orphan") {
		t.Fatalf("prune --yes must not RemoveAll a real master directory:\n%s", out)
	}
	if !strings.Contains(out, "Removed managed link:") {
		t.Fatalf("expected per-path prune results, got:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(skillsDir, "orphan")); err != nil {
		t.Fatal("untracked real master directory must survive prune --yes")
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
	plantManagedAgentLink(t, skillsDir, "configured", "augment")
	plantManagedAgentLink(t, skillsDir, "orphan", "augment")

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

// Doctor already counts and removes this as an issue; prune must too, by
// default and under --yes.
func TestCLIPruneYesRemovesLeftoverEmptyAgentDir(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	jazzDir := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(jazzDir, 0755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--yes", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --yes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 empty agent directory") {
		t.Fatalf("expected the empty agent directory counted in the summary, got:\n%s", out)
	}
	if !strings.Contains(out, "Removed empty agent directory:") {
		t.Fatalf("expected a per-item removal line, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".jazz")); !os.IsNotExist(err) {
		t.Fatal("expected leftover ~/.jazz to be removed")
	}
}

// --skills-only plans master skills and their links only; a leftover empty
// Agent directory is links territory and must survive.
func TestCLIPruneSkillsOnlyKeepsLeftoverEmptyAgentDir(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	jazzDir := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(jazzDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "elsewhere", "orphan")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "orphan")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--skills-only", "--yes", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --skills-only --yes: %v\n%s", err, out)
	}
	if strings.Contains(out, "empty agent directory") {
		t.Fatalf("--skills-only must not touch leftover empty Agent directories, got:\n%s", out)
	}
	if _, err := os.Stat(jazzDir); err != nil {
		t.Fatal("expected leftover ~/.jazz/skills to survive --skills-only")
	}
	if _, err := os.Lstat(filepath.Join(skillsDir, "orphan")); !os.IsNotExist(err) {
		t.Fatal("expected the untracked master symlink to be removed")
	}
}

func TestCLIPruneDryRunListsLeftoverEmptyAgentDir(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	jazzDir := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(jazzDir, 0755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--dry-run", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 leftover empty agent director") {
		t.Fatalf("expected the empty agent directory counted in the plan, got:\n%s", out)
	}
	if !strings.Contains(out, "empty agent directory: ") || !strings.Contains(out, "(jazz)") {
		t.Fatalf("expected a per-item plan line naming the agent, got:\n%s", out)
	}
	if _, err := os.Stat(jazzDir); err != nil {
		t.Fatal("dry-run must not remove anything")
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

// An untracked entry that is no longer a link was left alone by apply; the
// summary says so apart from the real directories --yes never offered.
func TestPrunePrintsSkippedSkillsThatAreNoLongerLinks(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	printPruneSummary(cmd, engine.PruneResult{SkippedSkills: []string{"mine"}})

	for _, want := range []string{"Skipped 1 untracked master skill that is no longer a link.", "  Skipped master skill (not a link): mine"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary does not contain %q:\n%s", want, out.String())
		}
	}
}

// The prompt's keys name items; pruneSelection only maps them back.
// engine.PrunePlan.Select owns what a selection implies.
func TestPruneSelectionMapsEachKeyToTheItemItNames(t *testing.T) {
	plan := engine.PrunePlan{
		UntrackedSkills: []string{"orphan"},
		UntrackedDirs:   []string{"mine"},
		Unconfigured:    []engine.ManagedAgentPath{{Agent: "continue", Skill: "configured", Path: "/agents/continue/configured"}},
		EmptyAgentDirs:  []engine.AgentDir{{Name: "jazz", Dir: "/agents/jazz/skills"}},
		StateSkills:     []string{"gone"},
	}

	got := pruneSelection(plan, []string{
		pruneMasterKey("orphan"), pruneMasterKey("mine"), pruneLinkKey("/agents/continue/configured"),
		pruneEmptyDirKey("/agents/jazz/skills"), pruneBaselineKey("gone"),
	})

	want := engine.PruneSelection{
		Masters:   []string{"orphan", "mine"},
		Links:     []string{"/agents/continue/configured"},
		EmptyDirs: []string{"/agents/jazz/skills"},
		Baselines: []string{"gone"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection = %#v; want %#v", got, want)
	}
}

// staleBaselineScope is an isolated Global Scope whose only prunable item is
// the Baseline of a Skill Config no longer declares.
func staleBaselineScope(t *testing.T) (configFile, skillsDir string) {
	t.Helper()
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile = filepath.Join(home, ".agents", "skills.json")
	skillsDir = filepath.Join(home, ".agents", "skills")
	scopeCopy := filepath.Join(skillsDir, "gone")
	if err := os.MkdirAll(scopeCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scopeCopy, "SKILL.md"), []byte("# Gone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := engine.SkillFreshness{Name: "gone", Source: "owner/repo", ScopePath: scopeCopy}
	if err := engine.OpenBaselines(skillsDir).Record(skill, "cache", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(scopeCopy); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	return configFile, skillsDir
}

// With nothing else to prune, a stale Baseline used to leave prune saying
// "Nothing to prune." while doctor counted it as an issue (#179).
func TestCLIPruneClearsStaleBaselinesOnTheirOwn(t *testing.T) {
	t.Run("dry run lists it", func(t *testing.T) {
		configFile, skillsDir := staleBaselineScope(t)
		out, err := runCLI(t, "prune", "--dry-run", "--config", configFile, "--skills-dir", skillsDir)
		if err != nil {
			t.Fatalf("prune --dry-run: %v\n%s", err, out)
		}
		if strings.Contains(out, "Nothing to prune.") || !strings.Contains(out, "  stale baseline: gone") {
			t.Fatalf("dry run does not list the stale Baseline:\n%s", out)
		}
	})
	t.Run("--yes clears it", func(t *testing.T) {
		configFile, skillsDir := staleBaselineScope(t)
		out, err := runCLI(t, "prune", "--yes", "--config", configFile, "--skills-dir", skillsDir)
		if err != nil {
			t.Fatalf("prune --yes: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Pruned 1 stale Baseline.") || !strings.Contains(out, "  Forgot stale Baseline: gone") {
			t.Fatalf("prune --yes does not report the cleared Baseline:\n%s", out)
		}
		if _, ok := engine.OpenBaselines(skillsDir).Applied("gone"); ok {
			t.Fatal("the stale Baseline is still recorded")
		}
	})
	// --links-only does not touch Baselines, so it does not read the Scope
	// state at all and an unreadable one is not its concern.
	t.Run("--links-only ignores an unreadable Scope state", func(t *testing.T) {
		configFile, skillsDir := staleBaselineScope(t)
		makeScopeStateUnreadable(t, skillsDir)
		out, err := runCLI(t, "prune", "--links-only", "--yes", "--config", configFile, "--skills-dir", skillsDir)
		if err != nil || strings.Contains(out, "Failed to read the Scope baseline") {
			t.Fatalf("prune --links-only = %v; want it to leave the Scope state alone\n%s", err, out)
		}
	})
	t.Run("--links-only leaves it", func(t *testing.T) {
		configFile, skillsDir := staleBaselineScope(t)
		out, err := runCLI(t, "prune", "--links-only", "--yes", "--config", configFile, "--skills-dir", skillsDir)
		if err != nil {
			t.Fatalf("prune --links-only: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Nothing to prune.") {
			t.Fatalf("--links-only must leave Baselines alone:\n%s", out)
		}
		if _, ok := engine.OpenBaselines(skillsDir).Applied("gone"); !ok {
			t.Fatal("--links-only cleared a Baseline")
		}
	})
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
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: sample\n---\n# Sample\n"), 0o644); err != nil {
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

func TestCLIConfigScopeLabelIgnoresCustomSkillsDirWithoutProjectFlag(t *testing.T) {
	resetRootCmdFlags()
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")
	if _, err := runCLI(t, "init", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "config", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Scope: Global") {
		t.Fatalf("custom --skills-dir without --project must report Global, got:\n%s", out)
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
	t.Setenv(updater.SkipSelfUpdateCheckEnv, "")
	t.Setenv("SKILLS_CACHE_DIR", "")
	// Git config isolation, core.autocrlf included, is TestMain's.
	return home
}

func plantManagedAgentLink(t *testing.T, skillsDir, skill, agent string) {
	t.Helper()
	agents := models.ForSkillsDir(skillsDir).KnownDirs()
	dir, ok := agents[models.NormalizeAgentName(agent)]
	if !ok {
		t.Fatalf("unknown agent %q", agent)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(dir, filepath.Join(skillsDir, skill))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(dir, skill)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
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
	isolateHome(t)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "skills.json")
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, ".cache")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/test-repo", "skill-1", "skills/skill-1", "github", "")
	repo := cfg.Remote["owner/test-repo"]
	repo.Branch = "main"
	cfg.Remote["owner/test-repo"] = repo
	_ = config.SaveConfig(cfg, configFile)

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"update", "--dry-run", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	// A Source still to refresh means the Scope does not match its Config.
	if err := RootCmd.Execute(); ExitCode(err) != 1 {
		t.Fatalf("update --dry-run --json = %v; want exit 1", err)
	}

	var doc struct {
		UpdatedRepos []engine.UpdatedRepoInfo `json:"updated_repos"`
		Sync         *updateSyncJSON          `json:"sync"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, buf.String())
	}
	if len(doc.UpdatedRepos) != 1 || doc.Sync == nil || doc.Sync.Converged || doc.Sync.Configured != 1 {
		t.Fatalf("JSON = %s; want the Source to refresh and an unconverged sync", buf.String())
	}
	if strings.Contains(buf.String(), `"updated_skills"`) {
		t.Fatalf("Update JSON still describes updated Skills: %s", buf.String())
	}
	if _, err := runCLI(t, "update", "typo", "--dry-run", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err == nil || !strings.Contains(err.Error(), "unknown update target") {
		t.Fatalf("unknown target error = %v", err)
	}
}

func TestCLIUpdateReportsEachRefreshedSourceOnceWithoutATerminal(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	configFile := filepath.Join(root, "skills.json")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	sha, _, err := engine.RunCmd("git rev-parse --short=7 HEAD", origin)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&stderr)
	skillsDir := filepath.Join(root, "skills")
	RootCmd.SetArgs([]string{"update", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", filepath.Join(root, "cache")})
	if err := RootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// The durable per-Source line replaces the progress region's "ok" line,
	// and the Sync that follows applies what was refreshed and names it.
	want := "      Updated owner/repo (" + sha + ").\n" +
		"Refreshed 1 Source Cache(s).\n" +
		"Restored 1 skill: sample.\n" +
		"Skills sync complete. 1 skills configured.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q\nwant     %q", got, want)
	}
	if got := stderr.String(); got != "ok  sample\n" {
		t.Fatalf("stderr = %q; want only Sync's plain line without a terminal", got)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("update did not sync the refreshed Skill: %v", err)
	}

	// A second run finds nothing to refresh and nothing to sync: one line.
	resetSubcommandFlags()
	stdout.Reset()
	stderr.Reset()
	if err := RootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String() + stderr.String(); got != "Everything is already up to date.\n" {
		t.Fatalf("converged update printed %q; want one line", got)
	}
}

// A commit outside every declared Skill moves the Cache but is not reported
// as an update, so a daily run stays one line.
func TestCLIUpdateDoesNotReportASourceWhoseSkillsDidNotChange(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	configFile := filepath.Join(root, "skills.json")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	args := []string{"update", "--config", configFile, "--skills-dir", filepath.Join(root, "skills"), "--cache-dir", filepath.Join(root, "cache")}
	if out, err := runCLI(t, args...); err != nil {
		t.Fatalf("first update = %v:\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# Origin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, origin, "add", ".")
	cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "readme")

	resetSubcommandFlags()
	var stdout, stderr bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs(args)
	if err := RootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "Everything is already up to date.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q\nwant     %q", got, want)
	}
	if got := stderr.String(); got != "ok  owner/repo\n" {
		t.Fatalf("stderr = %q; want the refresh's plain line", got)
	}

	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# Origin again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-am", "readme again")
	resetSubcommandFlags()
	stdout.Reset()
	RootCmd.SetArgs(append(args, "--json"))
	if err := RootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		UpdatedRepos []engine.UpdatedRepoInfo `json:"updated_repos"`
		SkippedRepos []engine.SkippedRepoInfo `json:"skipped_repos"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout.String())
	}
	if len(doc.UpdatedRepos) != 0 || len(doc.SkippedRepos) != 1 || doc.SkippedRepos[0].Reason != engine.SkippedSkillsUnchanged {
		t.Fatalf("JSON = %+v", doc)
	}
}

// A Skill whose content changed upstream is named once Sync has written it,
// in text and in JSON, apart from one the Scope was only missing.
func TestCLIUpdateNamesTheSkillsItWrote(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "changed")
	writeCLIGitSkill(t, origin, "deleted")
	writeCLIGitSkill(t, origin, "steady")
	configFile, skillsDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills")
	cfg := config.DefaultConfig()
	for _, name := range []string{"changed", "deleted", "steady"} {
		config.AddRemoteSkillEntry(cfg, "owner/repo", name, name, "git", origin)
	}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	args := []string{"update", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", filepath.Join(root, "cache")}
	if out, err := runCLI(t, args...); err != nil {
		t.Fatalf("first update = %v:\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(origin, "changed", "SKILL.md"), []byte("---\nname: changed\ndescription: new\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-am", "change")
	if err := os.RemoveAll(filepath.Join(skillsDir, "deleted")); err != nil {
		t.Fatal(err)
	}

	resetSubcommandFlags()
	out, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("update = %v:\n%s", err, out)
	}
	if !strings.Contains(out, "Updated 1 skill: changed.\nRestored 1 skill: deleted.\n") || strings.Contains(out, "steady.") {
		t.Fatalf("output = %q; want changed updated, deleted restored, steady unnamed", out)
	}

	if err := os.RemoveAll(filepath.Join(skillsDir, "deleted")); err != nil {
		t.Fatal(err)
	}
	resetSubcommandFlags()
	var stdout bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetArgs(append(args, "--json"))
	if err := RootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sync updateSyncJSON `json:"sync"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout.String())
	}
	if len(doc.Sync.Updated) != 0 || !reflect.DeepEqual(doc.Sync.Restored, []string{"deleted"}) {
		t.Fatalf("sync = %+v; want only deleted restored", doc.Sync)
	}
}

// Past ten names a terminal gets one line naming what fits and counting the
// rest; a log, with no width, keeps every name.
func TestMaterializedLineFitsOneTerminalLine(t *testing.T) {
	names := func(n int) []string {
		var skills []string
		for i := range n {
			skills = append(skills, fmt.Sprintf("skill-%02d", i+1))
		}
		return skills
	}
	all12 := "Updated 12 skills: " + strings.Join(names(12), ", ") + "."
	for _, tc := range []struct {
		name   string
		skills []string
		width  int
		want   string
	}{
		{"ten stay whole on a narrow terminal", names(10), 20, "Updated 10 skills: " + strings.Join(names(10), ", ") + "."},
		{"a log keeps every name", names(12), 0, all12},
		{"a wide terminal keeps every name", names(12), len(all12), all12},
		{"a narrow terminal names what fits", names(12), 60, "Updated 12 skills: skill-01, skill-02, skill-03, and 9 more."},
		{"too narrow for one name counts only", names(12), 30, "Updated 12 skills."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := materializedLine("Updated", tc.skills, tc.width); got != tc.want {
				t.Fatalf("line = %q\nwant   %q", got, tc.want)
			}
			if tc.width > 0 && len(tc.skills) > materializedLineFull && len(tc.want) > tc.width {
				t.Fatalf("line of %d columns overflows %d", len(tc.want), tc.width)
			}
		})
	}
}

// Update is the one daily command whatever the Config declares: without a
// remote Source it still syncs the Scope.
func TestCLIUpdateSyncsAScopeWithoutRemoteSources(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	source := filepath.Join(root, "src", "local")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: local\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configFile, skillsDir := filepath.Join(root, "scope", "skills.json"), filepath.Join(root, "scope", "skills")
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "local", source, "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "update", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", filepath.Join(root, "cache"))
	if err != nil || !strings.Contains(out, "Skills sync complete. 1 skills configured.") {
		t.Fatalf("update = %v; want a sync:\n%s", err, out)
	}
	if _, err := os.Readlink(filepath.Join(skillsDir, "local")); err != nil {
		t.Fatalf("update did not link the local Skill: %v", err)
	}
}

// One Source that cannot be fetched does not hold back the others: update
// still syncs the Scope from the Cache it has, then exits 2 (ADR-0002).
func TestCLIUpdateSyncsTheRestWhenASourceFailsToFetch(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	configFile, skillsDir := filepath.Join(root, "scope", "skills.json"), filepath.Join(root, "scope", "skills")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	config.AddRemoteSkillEntry(cfg, "owner/missing", "lost", "lost", "git", filepath.Join(root, "missing"))
	for _, source := range []string{"owner/repo", "owner/missing"} {
		repo := cfg.Remote[source]
		repo.Branch = cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
		cfg.Remote[source] = repo
	}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "update", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", filepath.Join(root, "cache"))
	if ExitCode(err) != 2 || !strings.Contains(out, "Error updating owner/missing") || !strings.Contains(out, "Update completed with errors.") {
		t.Fatalf("update = %v; want exit 2 naming the failed Source:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("the fetched Skill was not synced: %v\n%s", err, out)
	}
}

func TestCLIOutdatedJSONNestsScopeStatusAndReturnsNonZero(t *testing.T) {
	resetRootCmdFlags()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	configFile, skillsDir, cacheDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", filepath.Join(root, "missing-origin"))
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	RootCmd.SetOut(&output)
	RootCmd.SetErr(&output)
	RootCmd.SetArgs([]string{"outdated", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	oldSilenceErrors := RootCmd.SilenceErrors
	t.Cleanup(func() { RootCmd.SilenceErrors = oldSilenceErrors })
	err := Execute()
	out := output.String()
	if err == nil {
		t.Fatal("Outdated should fail when Cache/Scope is not current")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("outdated exit code = %d; want 1", got)
	}
	if strings.Contains(out, "Error:") {
		t.Fatalf("freshness difference printed as an error:\n%s", out)
	}
	if !strings.Contains(out, `"repositories"`) || !strings.Contains(out, `"skills"`) || !strings.Contains(out, `"status": "unverified"`) {
		t.Fatalf("unexpected nested JSON:\n%s", out)
	}
}

func TestExitCodeDefaultsFailuresToTwo(t *testing.T) {
	if got := ExitCode(errors.New("failed")); got != 2 {
		t.Fatalf("exit code = %d; want 2", got)
	}
}

func TestOutdatedStatusStyling(t *testing.T) {
	oldGreen, oldYellow, oldRed, oldBold, oldReset := colorGreen, colorYellow, colorRed, colorBold, colorReset
	colorGreen, colorYellow, colorRed, colorBold, colorReset = "<green>", "<yellow>", "<red>", "<bold>", "<reset>"
	t.Cleanup(func() {
		colorGreen, colorYellow, colorRed, colorBold, colorReset = oldGreen, oldYellow, oldRed, oldBold, oldReset
	})

	for status, want := range map[string]string{
		"up_to_date":                             "<bold><green>Up to date<reset>",
		string(engine.SkillInSync):               "<bold><green>In sync<reset>",
		string(engine.SkillCacheUpdateAvailable): "<bold><yellow>Cache update available<reset>",
		string(engine.SkillLocalDrift):           "<bold><yellow>Local drift<reset>",
		string(engine.SkillMissing):              "<bold><red>Missing<reset>",
		string(engine.SkillError):                "<bold><red>Error<reset>",
	} {
		if got := styledStatus(status); got != want {
			t.Errorf("styledStatus(%q) = %q; want %q", status, got, want)
		}
	}
}

func TestCLISyncExitCodes(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	// 1: the Cache was never fetched, so Sync cannot converge on its own.
	out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("uncached Source should exit 1, got err=%v:\n%s", err, out)
	}

	// 0: everything declared is in place.
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	if out, err = runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("converged Sync should exit 0: %v\n%s", err, out)
	}
	if out, err = runCLI(t, "sync", "--dry-run", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("converged dry-run should exit 0: %v\n%s", err, out)
	}
	resetSubcommandFlags()

	// 1 again: a local edit is protected rather than overwritten.
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("local drift should exit 1, got err=%v:\n%s", err, out)
	}
	if !strings.Contains(out, "1 blocked skill") || !strings.Contains(out, "--force") {
		t.Fatalf("blocked Sync must say what is blocked and what to do:\n%s", out)
	}

	// 2: something is actually broken — an Agent path Sync does not manage.
	if out, err = runCLI(t, "sync", "--force", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("force Sync should converge: %v\n%s", err, out)
	}
	resetSubcommandFlags()
	// A custom --skills-dir is Project-scoped, so the Agent directory lives
	// beside it rather than under the user's home.
	foreign := filepath.Join(root, ".claude", "skills", "sample")
	if err := os.RemoveAll(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("an unmanaged Agent path should exit 2, got err=%v:\n%s", err, out)
	}
	if !strings.Contains(out, "Failed to apply availability for sample") {
		t.Fatalf("failure must name the Skill:\n%s", out)
	}
}

// Without a terminal, Sync leaves one "ok" line per Skill it applied and
// words only what stands in the way.
func TestCLISyncReportsEachSkillOnceWithoutATerminal(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	writeCLIGitSkill(t, origin, "drifted")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	config.AddRemoteSkillEntry(cfg, "owner/repo", "drifted", "drifted", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample", "drifted"); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("first Sync: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "drifted", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if ExitCode(err) != 1 {
		t.Fatalf("drift should exit 1, got err=%v:\n%s", err, out)
	}
	body := out
	// runCLI goes around Execute, which keeps exit 1 from reading as an error.
	body, _, _ = strings.Cut(body, "Error: ")
	want := "  Skipped drifted: local_drift\n" +
		"ok  sample\n" +
		"Sync did not converge. 1 blocked skill.\n" +
		"Next: inspect the changes, then re-run with 'skills sync --force' to overwrite them.\n"
	if body != want {
		t.Fatalf("output = %q\nwant     %q", body, want)
	}
}

func TestCLISyncDryRunNeverEntersApply(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	configFile, skillsDir := filepath.Join(root, "skills.json"), filepath.Join(root, "skills")
	marker := filepath.Join(root, "check-ran")

	cfg := config.DefaultConfig()
	config.AddLocalCommandEntry(cfg, "tool", "touch "+filepath.Join(root, "installed"), "touch "+marker, "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "--dry-run", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatalf("dry-run with pending work should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "[Dry-run] Would execute:") {
		t.Fatalf("dry-run did not preview the installer:\n%s", out)
	}
	for _, path := range []string{marker, filepath.Join(root, "installed"), skillsDir} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("dry-run reached apply: %s exists (%v)", path, statErr)
		}
	}
}

// Declining to replace a Skill without a baseline leaves it blocked while Sync
// reconciles the rest: the Scope still does not match its Config, so it exits
// 1 (ADR-0002), not 2 (#172).
func TestCLISyncInteractiveUnknownBaselineDeclineLeavesItBlocked(t *testing.T) {
	// Update reaches the same question through its Sync; its JSON
	// never asks, since stdout belongs to the document.
	for _, tc := range []struct {
		args     []string
		prompted bool
	}{
		{[]string{"sync"}, true},
		{[]string{"update"}, true},
		{[]string{"update", "--json"}, false},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			resetRootCmdFlags()
			isolateHome(t)
			root := t.TempDir()
			configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
			writeCLIGitSkill(t, origin, "sample")
			cfg := config.DefaultConfig()
			config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
			if err := config.SaveConfig(cfg, configFile); err != nil {
				t.Fatal(err)
			}
			cachePath, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample")
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.MaterializeRemoteSkill("sample", "sample", cachePath, skillsDir); err != nil {
				t.Fatal(err)
			}
			manualPath := filepath.Join(skillsDir, "sample", "SKILL.md")
			if err := os.WriteFile(manualPath, []byte("manual\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			prompted := false
			oldTerminal, oldPrompt := syncIsTerminal, syncPromptUnknown
			syncIsTerminal = func() bool { return true }
			syncPromptUnknown = func(io.Writer, []engine.SkillFreshness) (bool, error) { prompted = true; return false, nil }
			t.Cleanup(func() { syncIsTerminal, syncPromptUnknown = oldTerminal, oldPrompt })
			out, err := runCLI(t, append(tc.args, "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)...)
			if err == nil || ExitCode(err) != 1 {
				t.Fatalf("error = %v (exit %d); want exit 1\n%s", err, ExitCode(err), out)
			}
			if prompted != tc.prompted {
				t.Fatalf("prompted = %v; want %v\n%s", prompted, tc.prompted, out)
			}
			want := []string{"Skipped sample: unknown_baseline", "Sync did not converge. 1 blocked skill."}
			if !tc.prompted {
				want = []string{`"blocked": 1`}
			}
			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Fatalf("output does not say %q:\n%s", w, out)
				}
			}
			got, _ := os.ReadFile(manualPath)
			if string(got) != "manual\n" {
				t.Fatalf("declining wrote Scope content: %q", got)
			}
		})
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

func writeCLIGitSkill(t *testing.T, repo, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, name, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"}} {
		cliRunGit(t, repo, args...)
	}
}

func cliRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
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
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: sample\n---\n# Sample\n"), 0o644); err != nil {
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

// The policy is saved before it is applied, so a Skill whose Agent path is
// not Availability's to change must not keep the rest from following it.
func TestCLIConfigAgentDefaultsApplyPastARefusedSkill(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	for _, name := range []string{"alpha", "beta"} {
		source := filepath.Join(home, name+"-source")
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		resetSubcommandFlags()
		if _, err := runCLI(t, "add", "--symlink", source, "--skill", name, "--yes", "--config", configFile, "--skills-dir", skillsDir); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".continue", "skills", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "config", "set", "defaultAgents", "claude,continue", "--config", configFile, "--skills-dir", skillsDir)
	if ExitCode(err) != 2 {
		t.Fatalf("config set = %v; want exit 2:\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".continue", "skills", "beta")); err != nil {
		t.Fatalf("beta was not applied past alpha's failure: %v", err)
	}
	for _, want := range []string{"Set defaultAgents in", "Failed to apply availability for alpha", "Next: run 'skills doctor"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("a runtime failure printed usage:\n%s", out)
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
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: sample\n---\n# Sample\n"), 0o644); err != nil {
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
	out, err := runCLI(t, "agents", "sample", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("agents inspect: %v", err)
	}
	if !strings.Contains(out, "Linked by policy: claude-code") || !strings.Contains(out, "Automatically available: ") || !strings.Contains(out, "codex") {
		t.Fatalf("agents output does not separate managed and Automatically available Agents:\n%s", out)
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
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: sample\n---\n# Sample\n"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(inRepo, "SKILL.md"), []byte("---\nname: inrepo\n---\n# In repo"), 0644); err != nil {
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
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("---\nname: external\n---\n# External"), 0644); err != nil {
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
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(filepath.Dir(skillsDir), "local-src", "alpha"), "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	claudeSkills := filepath.Join(project, ".claude", "skills")
	plantManagedAgentLink(t, skillsDir, "alpha", "claude")
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
	if !isSymlink(filepath.Join(claudeSkills, "alpha")) {
		t.Fatal("healthy managed link should remain")
	}
}

// A legacy branchless Cache root (#177) is a plain removal under --fix: no
// rebuild, no network, and so no progress region — only the finding and its
// repair outcome.
func TestCLIDoctorFixRemovesLegacyCacheRootWithoutRebuilding(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	legacyRoot := filepath.Join(cacheDir, "owner", "repo")
	cliRunGit(t, "", "clone", origin, legacyRoot)
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Skills: map[string]string{}}
	configFile := filepath.Join(root, "skills.json")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Removed legacy Cache artifact: "+legacyRoot) {
		t.Fatalf("output = %q; want the removal reported", out)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy Cache root still exists: %v", err)
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

	// An unmanaged directory with no master skill behind it is left as-is,
	// not a repair target (D1); the unmanaged broken symlink is the one
	// issue --fix still cannot repair.
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
		t.Fatalf("doctor --fix should fail while the unmanaged broken symlink remains unrepaired:\n%s", out)
	}
	if !strings.Contains(out, "Unmanaged directories left as-is: no-master") {
		t.Fatalf("expected the unmanaged directory reported as a warning:\n%s", out)
	}
	if strings.Contains(out, "Cannot replace unmanaged directory") {
		t.Fatalf("--fix must not claim it attempted to repair an unmanaged directory:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(orphan, "SKILL.md")); err != nil {
		t.Fatalf("doctor --fix modified unmanaged data: %v", err)
	}
	if _, err := os.Lstat(unmanagedBroken); err != nil {
		t.Fatalf("doctor --fix removed unmanaged broken symlink: %v", err)
	}
}

func TestCLIDoctorFixExplainsForeignAvailabilityPathWithoutTerminal(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	claudeDir := filepath.Join(home, ".claude with space")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "sample", filepath.Join(filepath.Dir(skillsDir), "local-src", "sample"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	foreignTarget := filepath.Join(home, "terminal-browser", "sample")
	if err := os.MkdirAll(foreignTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(claudeDir, "skills", "sample")
	if err := os.MkdirAll(filepath.Dir(foreignPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreignTarget, foreignPath); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil {
		t.Fatalf("doctor --fix should leave foreign Availability unchanged without a terminal:\n%s", out)
	}
	for _, want := range []string{"occupied path", "~/.claude with space/skills/sample", "~/terminal-browser/sample", "Remove it manually: rm -- $HOME/'.claude with space/skills/sample'"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
	gotTarget, readErr := os.Readlink(foreignPath)
	if readErr != nil || gotTarget != foreignTarget {
		t.Fatalf("foreign path changed: target=%q err=%v", gotTarget, readErr)
	}
}

func TestCLIDoctorFixReplacesConfirmedForeignAvailabilityPath(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "sample", filepath.Join(filepath.Dir(skillsDir), "local-src", "sample"), "")
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	foreignTarget := filepath.Join(home, "terminal-browser", "sample")
	if err := os.MkdirAll(foreignTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(home, ".claude", "skills", "sample")
	if err := os.MkdirAll(filepath.Dir(foreignPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreignTarget, foreignPath); err != nil {
		t.Fatal(err)
	}

	oldTerminal, oldConfirm := doctorIsTerminal, doctorConfirm
	doctorIsTerminal = func() bool { return true }
	var prompt string
	doctorConfirm = func(message string, defaultYes bool) (bool, error) {
		prompt = message
		if defaultYes {
			t.Fatal("replacement confirmation must default to No")
		}
		return true, nil
	}
	t.Cleanup(func() { doctorIsTerminal, doctorConfirm = oldTerminal, oldConfirm })

	out, err := runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	if prompt != "Replace these paths with managed Availability?" {
		t.Fatalf("replacement prompt = %q", prompt)
	}
	for _, want := range []string{"~/.claude/skills/sample", "~/terminal-browser/sample", "symlink"} {
		if !strings.Contains(out, want) {
			t.Fatalf("interactive prompt output missing %q:\n%s", want, out)
		}
	}
	if !isSymlink(foreignPath) {
		t.Fatalf("doctor --fix did not replace %s with managed Availability", foreignPath)
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
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	// An unmanaged broken symlink still counts as an issue (an unmanaged
	// directory no longer does, D1), so it is what makes this run fail.
	unmanagedBroken := filepath.Join(project, ".claude", "skills", "custom-broken")
	if err := os.Symlink(filepath.Join(project, "gone"), unmanagedBroken); err != nil {
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
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(filepath.Dir(skillsDir), "local-src", "alpha"), "")
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

	if !strings.Contains(out, "Added 2 skill(s)") {
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
	if !strings.Contains(out, "Added 1 skill(s)") {
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

	var items []map[string]any
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
	if err == nil || ExitCode(err) != 2 || err.Error() != "Add did not complete: 1 failure, 0 blocked skills" {
		t.Fatalf("error = %v (exit %d); want the failed installer, exit 2\n%s", err, ExitCode(err), out)
	}
	for _, want := range []string{
		"Failed to run installer for cmd-skill",
		"Added 1 skill(s) [cmd-skill] to skills.json; 1 failed.",
		"Next: follow the reason given for each skill above, then run 'skills sync'.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output does not say %q:\n%s", want, out)
		}
	}
	// Add words its own progress; Sync's success line would only repeat it.
	if strings.Contains(out, "Running installer for") {
		t.Fatalf("output repeats Sync's progress line:\n%s", out)
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local["cmd-skill"].Command != "exit 1" {
		t.Fatalf("command = %#v", cfg.Local)
	}
}

func TestCLICommandAddCheckFailureStillSaves(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	out, err := runCLI(t, "add", "--command", "echo ok", "--check", "exit 1", "--skill", "cmd-skill", "-y", "--config", configFile, "--skills-dir", skillsDir)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("error = %v (exit %d); a check that does not pass blocks the Skill, exit 1\n%s", err, ExitCode(err), out)
	}
	for _, want := range []string{
		"Command check 'exit 1' failed, skipping cmd-skill",
		"Added 1 skill(s) [cmd-skill] to skills.json; 1 blocked.",
		"Next: follow the reason given for each skill above, then run 'skills sync'.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output does not say %q:\n%s", want, out)
		}
	}
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local["cmd-skill"].Command != "echo ok" {
		t.Fatalf("command = %#v", cfg.Local)
	}
}

// A Scope whose only finding is an untracked real Skill used to print a
// yellow warning and then claim everything was in top condition, which is
// what made `doctor --fix` read as unable to handle it. The exit code stays 0
// — untracked occupancy is not Drift (ADR-0002), and doctor must not suggest
// add (that path destroyed the Skill).
func TestCLIDoctorLeavesUntrackedRealDirectoryAsOccupancy(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	orphan := filepath.Join(project, ".agents", "skills", "orphan")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--fix", "-p")
	if err != nil {
		t.Fatalf("doctor --fix must stay clean for an untracked skill: %v\n%s", err, out)
	}
	if strings.Contains(out, "Everything is in top condition") {
		t.Fatalf("doctor claimed top condition above an untracked warning:\n%s", out)
	}
	if !strings.Contains(out, "No issues detected. 1 untracked skill is not in Config.") {
		t.Fatalf("doctor did not say the untracked skill is occupancy:\n%s", out)
	}
	if strings.Contains(out, "skills add") {
		t.Fatalf("doctor must not suggest add for a real untracked directory:\n%s", out)
	}
	if !strings.Contains(out, "skills prune -p --yes") {
		t.Fatalf("doctor did not say prune --yes will not remove it:\n%s", out)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("doctor --fix removed an untracked skill: %v", err)
	}
}

func TestCLIAddRefusesLocalSourceInsideSkillsDir(t *testing.T) {
	project := projectScope(t)
	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	dest := filepath.Join(project, ".agents", "skills", "my-project-skill")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	skillMd := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(skillMd, []byte("---\nname: my-project-skill\n---\n# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "add", "-p", "--symlink", dest, "--skill", "my-project-skill", "--yes")
	if err == nil {
		t.Fatalf("add should refuse a Source inside the skills directory\n%s", out)
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d; want 2 (%v)\n%s", ExitCode(err), err, out)
	}
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination must remain a real directory: mode=%v err=%v", info.Mode(), err)
	}
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("SKILL.md must survive: %v", err)
	}
	cfg, err := config.LoadConfig(filepath.Join(project, ".agents", "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Local["my-project-skill"]; ok {
		t.Fatal("Config must not record the refused Skill")
	}
}

func TestCLISyncIllegalLocalSourceLeavesRealDirectory(t *testing.T) {
	project := projectScope(t)
	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	dest := filepath.Join(project, ".agents", "skills", "mine")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	skillMd := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(skillMd, []byte("# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "mine", ".agents/skills/mine", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "sync", "-p")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("illegal local Source should fail Materialize with exit 2, got err=%v:\n%s", err, out)
	}
	info, lerr := os.Lstat(dest)
	if lerr != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination must remain a real directory: mode=%v err=%v", info.Mode(), lerr)
	}
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("SKILL.md must survive: %v", err)
	}
}

func TestCLIDoctorTreatsSelfSymlinkLoopAsBroken(t *testing.T) {
	project := projectScope(t)
	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	dest := filepath.Join(project, ".agents", "skills", "mine")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("mine", dest); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "mine", ".agents/skills/mine", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("a self-symlink loop should exit 1, got err=%v:\n%s", err, out)
	}
	if strings.Contains(out, "leave the directory") {
		t.Fatalf("a destroyed loop must not be told to leave the directory:\n%s", out)
	}
	if !strings.Contains(out, "missing SKILL.md") && !strings.Contains(out, "Broken") {
		t.Fatalf("a self-symlink loop should be reported as broken:\n%s", out)
	}
}

func TestCLIDoctorIllegalLocalSourceDoesNotRecommendRm(t *testing.T) {
	project := projectScope(t)
	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	dest := filepath.Join(project, ".agents", "skills", "mine")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "mine", ".agents/skills/mine", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("illegal local Source should exit 1, got err=%v:\n%s", err, out)
	}
	if !strings.Contains(out, "Local source for mine is inside the skills directory.") {
		t.Fatalf("doctor did not report the illegal Source:\n%s", out)
	}
	if !strings.Contains(out, "delete the local entry for mine from skills.json") {
		t.Fatalf("doctor did not say to drop the Config entry:\n%s", out)
	}
	if strings.Contains(out, "skills rm") {
		t.Fatalf("doctor must not recommend rm:\n%s", out)
	}
}

// An Illegal-local Skill's Scope path is its own Source, so rm drops the
// declaration and keeps the directory rather than deleting the user's work.
func TestCLIRmKeepsAnIllegalLocalSource(t *testing.T) {
	project := projectScope(t)
	dest := filepath.Join(project, ".agents", "skills", "mine")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "mine", ".agents/skills/mine", "")
	configPath := filepath.Join(project, ".agents", "skills.json")
	if err := config.SaveConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "rm", "-p", "mine", "--yes")
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("rm deleted the Skill's own Source: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Left ") || strings.Contains(out, "Removed master directory") {
		t.Fatalf("rm did not say it kept the directory:\n%s", out)
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, declared := config.FindSkillSource(loaded, "mine"); declared {
		t.Fatal("rm left the Skill declared")
	}
}

// A real directory Config does not declare is content Sync never wrote, so
// rm removes it only once the user confirms, and never without a terminal
// unless --yes says so.
func TestCLIRmConfirmsAnUntrackedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		answer   bool
		yes      bool
		wantErr  bool
		removed  bool
	}{
		{name: "no terminal", wantErr: true},
		{name: "declined", terminal: true},
		{name: "confirmed", terminal: true, answer: true, removed: true},
		{name: "--yes", yes: true, removed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := projectScope(t)
			resetSubcommandFlags()
			t.Cleanup(resetSubcommandFlags)
			loose := filepath.Join(project, ".agents", "skills", "loose")
			if err := os.MkdirAll(loose, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(loose, "SKILL.md"), []byte("# Loose\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			oldTerminal, oldConfirm := rmIsTerminal, rmConfirm
			rmIsTerminal = func() bool { return tc.terminal }
			rmConfirm = func(string) (bool, error) { return tc.answer, nil }
			t.Cleanup(func() { rmIsTerminal, rmConfirm = oldTerminal, oldConfirm })

			args := []string{"rm", "-p", "loose"}
			if tc.yes {
				args = append(args, "--yes")
			}
			out, err := runCLI(t, args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("rm = %v; want error %v\n%s", err, tc.wantErr, out)
			}
			if _, statErr := os.Stat(loose); (statErr != nil) != tc.removed {
				t.Fatalf("directory removed = %v; want %v\n%s", statErr != nil, tc.removed, out)
			}
		})
	}
}

func TestCLIPruneYesRemovesLeftoverMasterSymlink(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "elsewhere", "orphan")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "orphan")); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "prune", "--yes", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --yes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Removed master skill: orphan") {
		t.Fatalf("leftover master symlink should be removed:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(skillsDir, "orphan")); !os.IsNotExist(err) {
		t.Fatal("leftover master symlink should be gone")
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("Source outside the skills directory must survive: %v", err)
	}
}

// An unreadable Scope state leaves Baselines alone but must not stop prune:
// everything else is removed, then prune warns and exits 0 (ADR-0002).
func TestCLIPruneProceedsPastUnreadableScopeState(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	source := filepath.Join(home, "elsewhere", "orphan")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "orphan")); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	statePath, bad := makeScopeStateUnreadable(t, skillsDir)

	out, err := runCLI(t, "prune", "--yes", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("prune --yes error = %v (exit %d); want exit 0\n%s", err, ExitCode(err), out)
	}
	if !strings.Contains(out, "Removed master skill: orphan") {
		t.Fatalf("prune must still remove the untracked link:\n%s", out)
	}
	assertScopeStateWarning(t, out, true)
	if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
		t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
	}
}

// The summary line used to promise that --fix or Sync would repair every
// counted issue. An invalid folder is counted and neither repairs it, so the
// line now points at the per-finding next actions instead.
func TestCLIDoctorSummaryPointsAtPerFindingNextActions(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "broken"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "broken", "broken", "github", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil {
		t.Fatalf("doctor found an invalid folder but exited 0:\n%s", out)
	}
	if strings.Contains(out, "Run with --fix or 'skills sync' to repair") {
		t.Fatalf("doctor still promised a repair it cannot deliver:\n%s", out)
	}
	if !strings.Contains(out, "See the next action for each, or run with --fix.") {
		t.Fatalf("doctor did not point at the per-finding next actions:\n%s", out)
	}
	if !strings.Contains(out, "then run 'skills sync -p' to re-materialize it.") {
		t.Fatalf("doctor did not name the way out for the invalid folder:\n%s", out)
	}
}

// doctor is ADR-0002's third adopter. A finding it can name a next action for
// is a state (1), not a command that failed; 2 stays reserved for work that
// genuinely broke, so CI can tell "this Scope needs reconciling" from "this
// Scope is broken".
func TestCLIDoctorExitCodes(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(filepath.Dir(skillsDir), "local-src", "alpha"), "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	// 1: alpha is declared but has no Availability link yet — drift doctor
	// reports with a next action, not a command failure. Routed through
	// Execute, like the outdated test, so SilenceErrors matches the binary
	// and the absence of an Error: line means something.
	var output bytes.Buffer
	RootCmd.SetOut(&output)
	RootCmd.SetErr(&output)
	RootCmd.SetArgs([]string{"doctor", "-p"})
	oldSilenceErrors := RootCmd.SilenceErrors
	t.Cleanup(func() { RootCmd.SilenceErrors = oldSilenceErrors })
	err := Execute()
	out := output.String()
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("availability drift should exit 1, got err=%v:\n%s", err, out)
	}
	if strings.Contains(out, "Error:") {
		t.Fatalf("a state report must not be styled as an error:\n%s", out)
	}

	// 0: --fix reconciles it.
	if out, err = runCLI(t, "doctor", "--fix", "-p"); err != nil {
		t.Fatalf("doctor --fix should converge and exit 0: %v\n%s", err, out)
	}

	// 2: an unmanaged directory squats the Availability path, so the repair
	// itself cannot complete. Without a terminal there is no one to approve
	// replacing it, and doctor never removes an unmanaged path unasked.
	foreign := filepath.Join(project, ".claude", "skills", "alpha")
	if err := os.RemoveAll(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreign, 0755); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "doctor", "--fix", "-p")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("a failed repair should exit 2, got err=%v:\n%s", err, out)
	}
	if !strings.Contains(out, "Failed to reconcile availability for alpha") {
		t.Fatalf("the failure must name what broke:\n%s", out)
	}
}

// The end-to-end shape of the same defect: doctor used to exit 0 on a Scope
// where nothing was actually available, while sync failed loudly on it.
func TestCLIDoctorDoesNotPassAScopeWithNoUsableAgentDir(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	alpha := filepath.Join(skillsDir, "alpha")
	if err := os.MkdirAll(alpha, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "SKILL.md"), []byte("# Alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(filepath.Dir(skillsDir), "local-src", "alpha"), "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("an unobservable availability path should exit 1, got err=%v:\n%s", err, out)
	}
	if strings.Contains(out, "Everything is in top condition") {
		t.Fatalf("doctor passed a Scope where nothing is available:\n%s", out)
	}
	if !strings.Contains(out, "Cannot observe availability for alpha") {
		t.Fatalf("doctor did not name what it could not observe:\n%s", out)
	}
}

// denyLinkPrivilege makes symbolic link creation fail the way Windows does
// when the process holds no link privilege. engine.CreateSymbolicLink is
// exported for exactly this: the copy fallback fires on an errno no other
// platform produces, so without the seam its user-visible output could only
// ever be asserted on Windows.
func denyLinkPrivilege(t *testing.T) {
	t.Helper()
	previous := engine.CreateSymbolicLink
	engine.CreateSymbolicLink = func(target, link string) error {
		return &os.LinkError{Op: "symlink", Old: target, New: link, Err: engine.ErrLinkPrivilegeNotHeld}
	}
	t.Cleanup(func() { engine.CreateSymbolicLink = previous })
}

// copiedAvailabilityScope declares two remote Skills whose Cache is ready, so
// a Scope with more than one Skill can show that the copy notice is said once.
func copiedAvailabilityScope(t *testing.T) (configFile, skillsDir, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	configFile = filepath.Join(root, ".agents", "skills.json")
	skillsDir = filepath.Join(root, ".agents", "skills")
	cacheDir = filepath.Join(root, "cache")
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "alpha")
	writeCLIGitSkill(t, origin, "beta")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "alpha", "alpha", "git", origin)
	config.AddRemoteSkillEntry(cfg, "owner/repo", "beta", "beta", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "alpha", "beta"); err != nil {
		t.Fatal(err)
	}
	return configFile, skillsDir, cacheDir
}

// A Windows user with Developer Mode off gets working Availability, plus one
// line saying it is copies and why. Once per Sync, not once per Skill, or a
// Scope with thirty Skills buries its own result.
func TestCLISyncSaysOnceThatAvailabilityWasAppliedByCopying(t *testing.T) {
	resetRootCmdFlags()
	configFile, skillsDir, cacheDir := copiedAvailabilityScope(t)
	denyLinkPrivilege(t)

	out, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("copying is a working Availability, so Sync must converge: %v\n%s", err, out)
	}
	if got := strings.Count(out, "applied by copying"); got != 1 {
		t.Fatalf("copy notice appeared %d times; want exactly one:\n%s", got, out)
	}
	for _, want := range []string{"Availability applied by copying: 2 paths", "A symbolic link needs a privilege this machine did not grant", "Enable Developer Mode on Windows"} {
		if !strings.Contains(out, want) {
			t.Fatalf("copy notice missing %q:\n%s", want, out)
		}
	}
	// The suggested command has to name the Scope the user chose, not the one
	// this --skills-dir happens to look like (root.go, and the same invariant
	// ls and config already assert).
	if !strings.Contains(out, "run 'skills sync' to switch") {
		t.Fatalf("a custom --skills-dir without --project must not suggest the Project Scope:\n%s", out)
	}
}

// The same fact, asked later: doctor counts the copies on disk, so the user
// never has to re-run Sync to find out why their Agent directories hold files.
func TestCLIDoctorCountsAvailabilityPathsAppliedByCopying(t *testing.T) {
	resetRootCmdFlags()
	configFile, skillsDir, cacheDir := copiedAvailabilityScope(t)
	denyLinkPrivilege(t)
	if _, err := runCLI(t, "sync", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("copies are not Drift, so doctor must exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Availability applied by copying: 2 paths (claude-code 2)") {
		t.Fatalf("doctor did not count the copies:\n%s", out)
	}
	if !strings.Contains(out, "Developer Mode") {
		t.Fatalf("doctor did not say how to switch to links:\n%s", out)
	}

	// A copy is not Drift, so --fix has nothing to repair or claim.
	out, err = runCLI(t, "doctor", "--fix", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	if strings.Contains(out, "Fixed availability drift") {
		t.Fatalf("doctor --fix claimed to repair copies:\n%s", out)
	}
	if !strings.Contains(out, "Availability applied by copying: 2 paths") {
		t.Fatalf("doctor --fix dropped the copy notice:\n%s", out)
	}
}

// A teammate's Windows clone turns a committed symlinked Skill into a text
// file holding its target path, and the Agent reads that as the Skill.
func TestCLIDoctorReportsASkillThatArrivedAsATextStub(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "shared"), []byte("../../my-skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "shared", "my-skill", "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("a stub is a state with a next action, so it should exit 1: err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "Skill arrived as a text stub instead of a directory: shared") {
		t.Fatalf("doctor did not recognize the stub:\n%s", out)
	}
	if !strings.Contains(out, "git config core.symlinks true") || !strings.Contains(out, "skills sync -p") {
		t.Fatalf("doctor did not name the way out:\n%s", out)
	}
	if strings.Contains(out, "Installed folder missing SKILL.md: shared") {
		t.Fatalf("a stub must not also be reported as an invalid folder:\n%s", out)
	}
}

// Another Scope's update left the shared Cache without this Scope's Skill:
// outdated names update, not sync alone, and exits 1 (ADR 0002, ADR 0004).
func TestCLIOutdatedReportsIncompleteCache(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "alpha")
	writeCLIGitSkill(t, origin, "beta")
	branch := cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
	if _, err := engine.NewCache("owner/repo", origin, branch, cacheDir).Refresh(false, "alpha"); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "alpha", "alpha", "git", origin)
	config.AddRemoteSkillEntry(cfg, "owner/repo", "beta", "beta", "git", origin)
	repo := cfg.Remote["owner/repo"]
	repo.Branch = branch
	cfg.Remote["owner/repo"] = repo
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "outdated", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if got := ExitCode(err); err == nil || got != 1 {
		t.Fatalf("outdated exit code = %d (err=%v); want 1:\n%s", got, err, out)
	}
	for _, want := range []string{"Cache: Cache incomplete", "beta: Unverified", "run 'skills update'"} {
		if !strings.Contains(out, want) {
			t.Fatalf("outdated output lacks %q:\n%s", want, out)
		}
	}
}

func TestCLIFollowsASkillRenamedUpstream(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeSkill := func(name, frontmatter string) {
		t.Helper()
		dir := filepath.Join(origin, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n"+frontmatter+"---\n# Skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) {
		t.Helper()
		cliRunGit(t, origin, "add", "-A")
		cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", message)
	}
	writeSkill("old", "")
	writeSkill("gone", "")
	cliRunGit(t, root, "init", origin)
	commit("initial")
	branch := cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{Type: "git", URL: origin, Branch: branch, Skills: map[string]string{"old": "skills/old", "gone": "skills/gone"}}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	scope := []string{"--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}
	run := func(args ...string) (string, error) {
		t.Helper()
		resetSubcommandFlags()
		return runCLI(t, append(args, scope...)...)
	}
	if out, err := run("update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := run("sync"); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	if err := os.RemoveAll(filepath.Join(origin, "skills")); err != nil {
		t.Fatal(err)
	}
	writeSkill("new", "metadata:\n  replaces: old\n")
	commit("rename old, remove gone")

	// Another Scope declaring the same Source updates the shared Cache. Its
	// update follows the rename in that Scope and leaves this one to Sync.
	otherConfig, otherSkills := filepath.Join(root, "other", "skills.json"), filepath.Join(root, "other", "skills")
	if err := config.SaveConfig(cfg, otherConfig); err != nil {
		t.Fatal(err)
	}
	resetSubcommandFlags()
	out, err := runCLI(t, "update", "--config", otherConfig, "--skills-dir", otherSkills, "--cache-dir", cacheDir)
	if ExitCode(err) != 1 || !strings.Contains(out, "Renamed old") || !strings.Contains(out, "run 'skills rm gone'") {
		t.Fatalf("update should follow the rename and leave gone blocked: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(otherSkills, "new", "SKILL.md")); err != nil {
		t.Fatalf("update did not sync the renamed Skill: %v", err)
	}
	out, err = run("outdated")
	if ExitCode(err) != 1 || !strings.Contains(out, "Renamed") || !strings.Contains(out, "→ ") || !strings.Contains(out, "run 'skills rm gone'") {
		t.Fatalf("outdated should show the rename and the removal: %v\n%s", err, out)
	}
	out, err = run("sync", "--dry-run")
	if ExitCode(err) != 1 || !strings.Contains(out, "Would rename old to new") {
		t.Fatalf("dry-run should preview the rename: %v\n%s", err, out)
	}
	if loaded, _ := config.LoadConfig(configFile); len(loaded.Remote["owner/repo"].Skills) != 2 || loaded.Remote["owner/repo"].Skills["old"] == "" {
		t.Fatalf("dry-run changed Config: %#v", loaded.Remote)
	}

	out, err = run("sync")
	if ExitCode(err) != 1 {
		t.Fatalf("the removed Skill still blocks, so sync exits 1: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Renamed old") || !strings.Contains(out, "no longer in Source owner/repo; run 'skills rm gone'") {
		t.Fatalf("sync should rename old and name rm for gone:\n%s", out)
	}
	if strings.Contains(out, "--force") || strings.Contains(out, "run 'skills update' first") {
		t.Fatalf("sync advice must not send the user to --force or update:\n%s", out)
	}
	loaded, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Remote["owner/repo"].Skills, map[string]string{"new": "skills/new", "gone": "skills/gone"}) {
		t.Fatalf("Config after rename = %#v", loaded.Remote["owner/repo"].Skills)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "new", "SKILL.md")); err != nil {
		t.Fatalf("new Skill not synced: %v", err)
	}

	if out, err := run("rm", "gone"); err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if out, err := run("sync"); err != nil {
		t.Fatalf("sync after rm should converge: %v\n%s", err, out)
	}
}

// makeScopeStateUnreadable writes a Scope state for skillsDir that does not
// decode, at the path the engine keys it by: the SHA-256 of the canonical
// skills directory. It returns the path and bytes so a test can check nothing
// rewrote it. A file standing in for the state directory would not do: on
// Windows opening a child of a file is ERROR_PATH_NOT_FOUND, which reads as a
// missing (empty) state rather than an unreadable one.
func makeScopeStateUnreadable(t *testing.T, skillsDir string) (string, []byte) {
	t.Helper()
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(canonical)))
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills-manager", "scope-state", hex.EncodeToString(sum[:])+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []byte("{not json")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, bad
}

// ADR-0002 counts a Baseline that could not be recorded as a failure: Add
// still applies the Skill, then says why and exits 2.
func TestCLIAddReportsUnreadableScopeState(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	statePath, bad := makeScopeStateUnreadable(t, skillsDir)

	out, err := runCLI(t, "add", "owner/repo", "--url", origin, "--skill", "sample", "-y", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("add error = %v (exit %d); want exit 2\n%s", err, ExitCode(err), out)
	}
	if !strings.Contains(out, "Failed to read the Scope baseline: ") {
		t.Fatalf("output does not report the unreadable Scope state:\n%s", out)
	}
	assertScopeStateWarning(t, out, false)
	if !strings.Contains(out, "Added 1 skill(s) [sample]") {
		t.Fatalf("output does not report the applied Skill:\n%s", out)
	}
	if strings.Contains(out, "run 'skills sync'") {
		t.Fatalf("Sync cannot get past an unreadable Scope state either; do not send the user there:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("the Skill must still be Materialized: %v", err)
	}
	if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
		t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
	}
}

// Without a terminal, Add leaves one "ok" line for the fetched Source and one
// per Skill it applied, then its summary.
func TestCLIAddReportsFetchAndEachSkillOnceWithoutATerminal(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")

	out, err := runCLI(t, "add", "owner/repo", "--url", origin, "--skill", "sample", "-y", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	want := "ok  owner/repo\n" +
		"ok  sample\n" +
		"Added 1 skill(s) [sample] and updated skills.json.\n"
	if out != want {
		t.Fatalf("output = %q\nwant     %q", out, want)
	}
}

func TestCLIAddBranchIsDeclaredAndKept(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	skillFile := filepath.Join(origin, "skills", "sample", "SKILL.md")
	writeContent := func(content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillFile, []byte("---\nname: sample\n---\n"+content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cliRunGit(t, origin, "add", "-A")
		cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", content)
	}
	cliRunGit(t, root, "init", origin)
	writeContent("main")
	cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	defaultBranch := cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
	cliRunGit(t, origin, "switch", "-c", "dev")
	writeContent("dev")
	cliRunGit(t, origin, "switch", defaultBranch)
	writeContent("main v2")

	scope := []string{"--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}
	run := func(args ...string) (string, error) {
		t.Helper()
		resetSubcommandFlags()
		return runCLI(t, append(args, scope...)...)
	}
	scopeContent := func() string {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.TrimPrefix(string(got), "---\nname: sample\n---\n"))
	}

	if out, err := run("add", "owner/repo", "--url", origin, "--branch", "dev", "--skill", "sample", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if cfg, _ := config.LoadConfig(configFile); cfg.Remote["owner/repo"].Branch != "dev" {
		t.Fatalf("branch not declared: %#v", cfg.Remote["owner/repo"])
	}
	if out, err := run("update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := run("sync", "--force"); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if got := scopeContent(); got != "dev" {
		t.Fatalf("after update and sync the Skill reads %q; want dev", got)
	}

	out, err := run("add", "owner/repo", "--url", origin, "--branch", "v1.0.0", "--skill", "sample", "-y")
	if err == nil || !strings.Contains(out+err.Error(), `already declared on branch "dev"`) {
		t.Fatalf("a different branch should be refused: %v\n%s", err, out)
	}
	if cfg, _ := config.LoadConfig(configFile); cfg.Remote["owner/repo"].Branch != "dev" {
		t.Fatalf("a refused add changed Config: %#v", cfg.Remote["owner/repo"])
	}

	if out, err := run("rm", "sample", "-y"); err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if out, err := run("add", "owner/repo", "--url", origin, "--branch", "v1.0.0", "--skill", "sample", "-y"); err != nil {
		t.Fatalf("add on a tag: %v\n%s", err, out)
	}
	if out, err := run("outdated"); err != nil {
		t.Fatalf("a Source on a tag should read as current: %v\n%s", err, out)
	}
	if got := scopeContent(); got != "main" {
		t.Fatalf("tag content = %q; want main", got)
	}
}
