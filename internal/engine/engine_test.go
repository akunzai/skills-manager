package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	// Create skill in node_modules (should be ignored)
	nodeModulesDir := filepath.Join(tmpRepo, "node_modules", "ignored-skill")
	_ = os.MkdirAll(nodeModulesDir, 0755)
	_ = os.WriteFile(filepath.Join(nodeModulesDir, "SKILL.md"), []byte("---\nname: ignored-skill\n---\n"), 0644)

	// Create skill exceeding MaxScanDepth (depth 7, should be ignored)
	deepDir := filepath.Join(tmpRepo, "d1", "d2", "d3", "d4", "d5", "d6", "deep-skill")
	_ = os.MkdirAll(deepDir, 0755)
	_ = os.WriteFile(filepath.Join(deepDir, "SKILL.md"), []byte("---\nname: deep-skill\n---\n"), 0644)

	discovered, err := DiscoverSkillsInRepo(tmpRepo)
	if err != nil {
		t.Fatalf("DiscoverSkillsInRepo failed: %v", err)
	}

	if len(discovered) != 2 {
		t.Fatalf("expected 2 skills, got %d (discovered: %+v)", len(discovered), discovered)
	}
	if discovered["skill-one"] != "skills/skill-one" {
		t.Errorf("unexpected skill-one path: %s", discovered["skill-one"])
	}
	if discovered["skill-two"] != "plugins/sub/skill-two" {
		t.Errorf("unexpected skill-two path: %s", discovered["skill-two"])
	}
	if _, ok := discovered["ignored-skill"]; ok {
		t.Errorf("expected node_modules skill to be ignored")
	}
	if _, ok := discovered["deep-skill"]; ok {
		t.Errorf("expected too deep skill to be ignored")
	}
}

func TestDiscoverSkillsInRepoRejectsDuplicateSkillNames(t *testing.T) {
	tmpRepo := t.TempDir()
	for _, path := range []string{"skills/first/SKILL.md", "skills/second/SKILL.md"} {
		fullPath := filepath.Join(tmpRepo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("---\nname: duplicate\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := DiscoverSkillsInRepo(tmpRepo)
	if err == nil {
		t.Fatal("DiscoverSkillsInRepo succeeded; want duplicate-name error")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "skills/first") || !strings.Contains(err.Error(), "skills/second") {
		t.Fatalf("duplicate-name error = %q; want both conflicting paths", err)
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

func TestTargetAgentsApplyPerSkillAvailability(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	cfg.Settings.ExcludeAgents = []string{"continue"}
	cfg.Settings.AgentExclusions = map[string][]string{"continue": {"sample"}}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{
		Include: []string{"continue"},
		Exclude: []string{"claude"},
	}

	got := GetTargetAgentsForSkill("sample", "owner/repo", cfg, skillsDir)
	if !reflect.DeepEqual(got, []string{"continue"}) {
		t.Fatalf("target agents = %#v, want continue", got)
	}
}

func TestReconcileAgentSymlinksMatchesDeclaredAvailability(t *testing.T) {
	project := t.TempDir()
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
	if err := ReconcileAgentSymlinks("sample", "owner/repo", cfg, skillsDir); err != nil {
		t.Fatal(err)
	}
	claudeLink := filepath.Join(project, ".claude", "skills", "sample")
	continueLink := filepath.Join(project, ".continue", "skills", "sample")
	for _, link := range []string{claudeLink, continueLink} {
		if !IsManagedSkillLink(link, "sample", skillsDir) {
			t.Fatalf("missing managed link %s", link)
		}
	}

	unmanaged := filepath.Join(project, ".roo", "skills", "sample")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	missing, unexpected := AgentLinkDrift("sample", "owner/repo", cfg, skillsDir)
	if len(missing) != 0 || !reflect.DeepEqual(unexpected, []string{"claude-code"}) {
		t.Fatalf("drift = missing %#v, unexpected %#v", missing, unexpected)
	}
	if err := ReconcileAgentSymlinks("sample", "owner/repo", cfg, skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("excluded Claude link still exists: %v", err)
	}
	if !IsManagedSkillLink(continueLink, "sample", skillsDir) {
		t.Fatal("declared Continue link was removed")
	}
	if fi, err := os.Stat(unmanaged); err != nil || !fi.IsDir() {
		t.Fatalf("unmanaged directory was removed: %v", err)
	}
}

func TestReconcileAgentSymlinksPreservesUnmanagedTarget(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	unmanagedTarget := filepath.Join(project, "user-owned")
	if err := os.MkdirAll(unmanagedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(unmanagedTarget, "keep")
	if err := os.WriteFile(marker, []byte("user-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unmanagedTarget, unmanaged); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileAgentSymlinks("sample", "owner/repo", config.DefaultConfig(), skillsDir); err == nil {
		t.Fatal("expected unmanaged target conflict")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unmanaged target was modified: %v", err)
	}
}

func TestReconcileAgentSymlinksRemovesManagedCopy(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(copyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	absMaster, err := filepath.Abs(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, managedCopyMarker), []byte(absMaster+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsManagedSkillCopy(copyPath, "sample", skillsDir) {
		t.Fatal("copy marker was not recognized")
	}
	cfg := config.DefaultConfig()
	if err := ReconcileAgentSymlinks("sample", "owner/repo", cfg, skillsDir); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(copyPath, "SKILL.md"))
	if err != nil || string(content) != "new" {
		t.Fatalf("managed copy was not refreshed: content=%q err=%v", content, err)
	}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	if err := ReconcileAgentSymlinks("sample", "owner/repo", cfg, skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("excluded managed copy still exists: %v", err)
	}
}

func TestReplaceManagedCopyFailurePreservesExistingCopy(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "agent", "sample")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dst, "SKILL.md")
	if err := os.WriteFile(keep, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceManagedCopy(filepath.Join(root, "missing"), dst); err == nil {
		t.Fatal("expected copy failure")
	}
	content, err := os.ReadFile(keep)
	if err != nil || string(content) != "old" {
		t.Fatalf("existing copy was not preserved: content=%q err=%v", content, err)
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
