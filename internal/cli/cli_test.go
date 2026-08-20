package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
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

func TestCLIOutdatedNotCached(t *testing.T) {
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
	RootCmd.SetArgs([]string{"outdated", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("outdated failed: %v", err)
	}
}


