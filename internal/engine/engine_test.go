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

