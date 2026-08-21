package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/spf13/pflag"
)

func TestCLIVersion(t *testing.T) {
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"version"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
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
	flagConfigFile = ""
	flagSkillsDir = ""
	flagCacheDir = ""
	flagProject = false
	flagGlobal = false
	RootCmd.PersistentFlags().Set("config", "")
	RootCmd.PersistentFlags().Set("skills-dir", "")
	RootCmd.PersistentFlags().Set("cache-dir", "")
	RootCmd.PersistentFlags().Set("global", "false")
	RootCmd.PersistentFlags().Set("project", "false")

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

func TestCLISyncPruneOrphans(t *testing.T) {
	flagConfigFile = ""
	flagSkillsDir = ""
	flagCacheDir = ""
	flagProject = false
	flagGlobal = true

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

	// 3. Run sync --prune
	RootCmd.SetArgs([]string{"sync", "--prune-only", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("sync --prune failed: %v", err)
	}

	// Verify orphan was pruned
	if _, err := os.Stat(orphanDir); err == nil {
		t.Fatalf("expected orphaned skill to be pruned")
	}
}

func TestCLILsFormatting(t *testing.T) {
	flagConfigFile = ""
	flagSkillsDir = ""
	flagCacheDir = ""
	flagProject = false
	flagGlobal = true

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
	flagConfigFile = ""
	flagSkillsDir = ""
	flagCacheDir = ""
	flagProject = false
	flagGlobal = true

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
	// resetRootCmdFlags marks --global as Changed, which makes GetEffectivePaths
	// force global scope and ignore -p. Clear it so project scope survives.
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
// its absolute path.
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
	if got := cfg.Local["external"].Source; !filepath.IsAbs(got) {
		t.Fatalf("stored source = %q; a source outside the project must stay absolute", got)
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
	if err := os.MkdirAll(filepath.Join(claudeSkills, "alpha"), 0755); err != nil {
		t.Fatal(err)
	}
	// A physical directory where a symlink belongs, and a dangling symlink.
	if err := os.WriteFile(filepath.Join(claudeSkills, "alpha", "SKILL.md"), []byte("# Alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "gone"), filepath.Join(claudeSkills, "broken")); err != nil {
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
	target, err := os.Readlink(filepath.Join(claudeSkills, "alpha"))
	if err != nil {
		t.Fatalf("physical dir should have become a symlink: %v", err)
	}
	if target == "" {
		t.Fatal("empty symlink target")
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

	out, err := runCLI(t, "doctor", "--fix", "-p")
	if err == nil {
		t.Fatalf("doctor --fix should fail while an issue remains unrepaired:\n%s", out)
	}
	if !strings.Contains(out, "Cannot convert") {
		t.Fatalf("expected an explanation of what could not be repaired:\n%s", out)
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
