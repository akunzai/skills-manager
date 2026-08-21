package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestParseSkillNameFromMD(t *testing.T) {
	tmpDir := t.TempDir()
	skillMd := filepath.Join(tmpDir, "SKILL.md")

	content := `---
name: my-awesome-skill
description: Does awesome stuff
---

# My Awesome Skill
`
	if err := os.WriteFile(skillMd, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test SKILL.md: %v", err)
	}

	name := ParseSkillNameFromMD(skillMd)
	if name != "my-awesome-skill" {
		t.Errorf("expected my-awesome-skill, got %q", name)
	}
}

func TestDiscoverSkillsInRepo(t *testing.T) {
	tmpRepo := t.TempDir()

	// Create skill 1 in repo root
	skill1Dir := filepath.Join(tmpRepo, "skills", "skill-one")
	_ = os.MkdirAll(skill1Dir, 0755)
	_ = os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte("---\nname: skill-one\n---\n"), 0644)

	// Create skill 2 in nested directory
	skill2Dir := filepath.Join(tmpRepo, "plugins", "sub", "skill-two")
	_ = os.MkdirAll(skill2Dir, 0755)
	_ = os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte("---\nname: skill-two\n---\n"), 0644)

	discovered, err := DiscoverSkillsInRepo(tmpRepo)
	if err != nil {
		t.Fatalf("DiscoverSkillsInRepo failed: %v", err)
	}

	if len(discovered) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(discovered))
	}
	if discovered["skill-one"] != "skills/skill-one" {
		t.Errorf("unexpected skill-one path: %s", discovered["skill-one"])
	}
	if discovered["skill-two"] != "plugins/sub/skill-two" {
		t.Errorf("unexpected skill-two path: %s", discovered["skill-two"])
	}
}

func TestCopySkillFolder(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := filepath.Join(t.TempDir(), "target-skill")

	_ = os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# Skill Content"), 0644)
	subDir := filepath.Join(srcDir, "scripts")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "run.sh"), []byte("#!/bin/sh\necho hi"), 0755)

	if err := CopySkillFolder(srcDir, targetDir); err != nil {
		t.Fatalf("CopySkillFolder failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(targetDir, "SKILL.md")); err != nil {
		t.Errorf("expected target SKILL.md to exist")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "scripts", "run.sh")); err != nil {
		t.Errorf("expected target scripts/run.sh to exist")
	}
}

func TestPostHooks(t *testing.T) {
	hooks := []config.PostHook{
		{
			Name:        "test-success",
			Description: "Echo test",
			Run:         "echo hook_ran",
		},
		{
			Name:        "test-skipped",
			Description: "Skipped hook",
			Condition:   "false",
			Run:         "echo should_not_run",
		},
	}

	results := ExecutePostHooks(hooks, false)
	if len(results) != 2 {
		t.Fatalf("expected 2 hook results, got %d", len(results))
	}
	if !results[0].Success {
		t.Errorf("expected first hook to succeed: %s", results[0].Message)
	}
	if !results[1].Success {
		t.Errorf("expected skipped hook to report success status")
	}
}

func TestScanAllSkills(t *testing.T) {
	tmpSkillsDir := t.TempDir()
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "skill-a", "skills/skill-a", "github", "")

	// Create physical skill-a
	skillAPath := filepath.Join(tmpSkillsDir, "skill-a")
	_ = os.MkdirAll(skillAPath, 0755)
	_ = os.WriteFile(filepath.Join(skillAPath, "SKILL.md"), []byte("# Skill A"), 0644)

	// Create untracked skill-b
	skillBPath := filepath.Join(tmpSkillsDir, "skill-b")
	_ = os.MkdirAll(skillBPath, 0755)
	_ = os.WriteFile(filepath.Join(skillBPath, "SKILL.md"), []byte("# Skill B"), 0644)

	items := ScanAllSkills(cfg, tmpSkillsDir)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	foundA, foundB := false, false
	for _, item := range items {
		if item.Name == "skill-a" {
			foundA = true
			if !item.IsInstalled || !item.IsValidSkill {
				t.Errorf("skill-a installed/valid state incorrect: %+v", item)
			}
		}
		if item.Name == "skill-b" {
			foundB = true
			if item.SourceType != "untracked" {
				t.Errorf("skill-b should be untracked, got %s", item.SourceType)
			}
		}
	}

	if !foundA || !foundB {
		t.Errorf("missing scanned items (foundA: %v, foundB: %v)", foundA, foundB)
	}
}

func TestEnsureAndRemoveAgentSymlinksProjectAndGlobal(t *testing.T) {
	tmpProjectDir := t.TempDir()
	skillsDir := filepath.Join(tmpProjectDir, ".agents", "skills")
	skillDir := filepath.Join(skillsDir, "test-skill")
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill"), 0644)

	// 1. Ensure project-level symlink for claude-code
	created, err := EnsureAgentSymlink("test-skill", "claude", skillsDir)
	if err != nil {
		t.Fatalf("EnsureAgentSymlink failed: %v", err)
	}
	if !created {
		t.Fatalf("expected symlink to be created")
	}

	claudeLink := filepath.Join(tmpProjectDir, ".claude", "skills", "test-skill")
	if fi, err := os.Lstat(claudeLink); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s", claudeLink)
	}

	// 2. Remove agent symlinks in project
	removed := RemoveAgentSymlinks("test-skill", skillsDir)
	if len(removed) == 0 {
		t.Errorf("expected at least 1 agent removed")
	}
	if _, err := os.Lstat(claudeLink); err == nil {
		t.Errorf("expected symlink %s to be removed", claudeLink)
	}
}

func TestApplyPrunePlanLeavesLinkReplacedAfterPlanning(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "alpha")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, ".claude", "skills", "alpha")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "alpha"), link); err != nil {
		t.Fatal(err)
	}
	plan := PrunePlan{Unconfigured: []PruneLink{{Agent: "claude-code", Path: link}}}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("user-owned"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyPrunePlan(plan, skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatal("a link replaced after planning must not be removed")
	}
}

func TestCheckRepoUpdateStatusSourceParsing(t *testing.T) {
	tmpCache := t.TempDir()

	tests := []struct {
		source       string
		expectedDest string
	}{
		{
			source:       "owner/repo",
			expectedDest: filepath.Join(tmpCache, "owner", "repo"),
		},
		{
			source:       "github:owner/repo",
			expectedDest: filepath.Join(tmpCache, "owner", "repo"),
		},
		{
			source:       "https://github.com/owner/repo.git",
			expectedDest: filepath.Join(tmpCache, "owner", "repo"),
		},
		{
			source:       "gitlab:group/project",
			expectedDest: filepath.Join(tmpCache, "gitlab.com", "group", "project"),
		},
	}

	for _, tt := range tests {
		res := CheckRepoUpdateStatus(tt.source, config.RemoteRepo{
			Skills: map[string]string{"foo": "skills/foo"},
		}, tmpCache)

		if res.CachePath != tt.expectedDest {
			t.Errorf("CheckRepoUpdateStatus(%q).CachePath = %q; want %q", tt.source, res.CachePath, tt.expectedDest)
		}
	}
}

func TestCopySkillFolderWithReadOnlyFilesAndRemoveAll(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := filepath.Join(t.TempDir(), "target-ro-skill")

	roFile := filepath.Join(srcDir, "README.md")
	if err := os.WriteFile(roFile, []byte("read only content"), 0444); err != nil {
		t.Fatalf("failed to write read only file: %v", err)
	}

	if err := CopySkillFolder(srcDir, targetDir); err != nil {
		t.Fatalf("CopySkillFolder failed with read-only file: %v", err)
	}

	copiedFile := filepath.Join(targetDir, "README.md")
	if _, err := os.Stat(copiedFile); err != nil {
		t.Fatalf("expected copied file to exist: %v", err)
	}

	// Verify we can copy over the target again without error (even if target had 0444 permissions)
	if err := CopySkillFolder(srcDir, targetDir); err != nil {
		t.Fatalf("second CopySkillFolder failed: %v", err)
	}

	// Verify RemoveAll cleanly removes the directory
	if err := RemoveAll(targetDir); err != nil {
		t.Fatalf("RemoveAll failed on directory with read-only files: %v", err)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("expected targetDir to be completely removed")
	}
}

func TestUpdateRemoteSkillsDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	cacheDir := filepath.Join(tmpDir, "cache")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo1", "skill-1", "skills/skill-1", "github", "")
	config.AddRemoteSkillEntry(cfg, "github:owner/repo2", "skill-2", "skills/skill-2", "github", "")

	result, err := UpdateRemoteSkills(cfg, []string{"skill-1", "skill-2"}, false, true, skillsDir, cacheDir, false, nil)
	if err != nil {
		t.Fatalf("UpdateRemoteSkills dry-run failed: %v", err)
	}

	if len(result.UpdatedRepos) != 2 {
		t.Fatalf("expected 2 updated repos in dry run, got %d", len(result.UpdatedRepos))
	}
	if len(result.UpdatedSkills) != 2 {
		t.Fatalf("expected 2 updated skills in dry run, got %d", len(result.UpdatedSkills))
	}
}
