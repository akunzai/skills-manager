package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
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

func TestInventoryClassifiesConfigVsSkillsDir(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "skill-a", "skills/skill-a", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing-skill", ".", "github", "")

	skillAPath := filepath.Join(skillsDir, "skill-a")
	if err := os.MkdirAll(skillAPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillAPath, "SKILL.md"), []byte("# Skill A"), 0644); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(skillsDir, "invalid-skill")
	if err := os.MkdirAll(invalidPath, 0755); err != nil {
		t.Fatal(err)
	}
	config.AddLocalSymlinkEntry(cfg, "invalid-skill", invalidPath, "")

	skillBPath := filepath.Join(skillsDir, "skill-b")
	if err := os.MkdirAll(skillBPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillBPath, "SKILL.md"), []byte("# Skill B"), 0644); err != nil {
		t.Fatal(err)
	}

	items, err := Inventory(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]models.SkillItem, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}

	gotA, ok := byName["skill-a"]
	if !ok || !gotA.IsInstalled || !gotA.IsValidSkill {
		t.Fatalf("skill-a = %+v", gotA)
	}
	if !reflect.DeepEqual(gotA.Agents, []string{"claude-code", "continue"}) {
		t.Fatalf("skill-a agents = %#v", gotA.Agents)
	}

	gotMissing, ok := byName["missing-skill"]
	if !ok || gotMissing.IsInstalled {
		t.Fatalf("missing-skill = %+v", gotMissing)
	}

	gotInvalid, ok := byName["invalid-skill"]
	if !ok || !gotInvalid.IsInstalled || gotInvalid.IsValidSkill {
		t.Fatalf("invalid-skill = %+v", gotInvalid)
	}

	gotB, ok := byName["skill-b"]
	if !ok || gotB.SourceType != "untracked" {
		t.Fatalf("skill-b = %+v", gotB)
	}
	if len(gotB.Agents) != 0 {
		t.Fatalf("untracked agents = %#v", gotB.Agents)
	}
}

func TestInventoryIgnoresAgentDirEntries(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(master, filepath.Join(project, ".continue", "skills", "sample")); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "github", "")

	items, err := Inventory(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "sample" {
		t.Fatalf("items = %#v", items)
	}
	if !reflect.DeepEqual(items[0].Agents, []string{"claude-code"}) {
		t.Fatalf("agents = %#v; disk continue link must not appear", items[0].Agents)
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

func TestAvailabilityManagedAgentsApplyPerSkillPolicy(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{
		Include: []string{"continue"},
		Exclude: []string{"claude"},
	}

	got := NewAvailability(cfg, skillsDir).ManagedAgents("sample")
	if !reflect.DeepEqual(got, []string{"continue"}) {
		t.Fatalf("target agents = %#v, want continue", got)
	}
}

func TestAvailabilityApplyMatchesDeclaredPolicy(t *testing.T) {
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
	availability := NewAvailability(cfg, skillsDir)
	if err := availability.Apply("sample"); err != nil {
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
	missing, unexpected := availability.Drift("sample")
	if len(missing) != 0 || !reflect.DeepEqual(unexpected, []string{"claude-code"}) {
		t.Fatalf("drift = missing %#v, unexpected %#v", missing, unexpected)
	}
	if err := availability.Apply("sample"); err != nil {
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

func TestAvailabilityApplyPreservesUnmanagedTarget(t *testing.T) {
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
	if err := NewAvailability(config.DefaultConfig(), skillsDir).Apply("sample"); err == nil {
		t.Fatal("expected unmanaged target conflict")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unmanaged target was modified: %v", err)
	}
}

func TestAvailabilityApplyRemovesManagedCopy(t *testing.T) {
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
	availability := NewAvailability(cfg, skillsDir)
	if err := availability.Apply("sample"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(copyPath, "SKILL.md"))
	if err != nil || string(content) != "new" {
		t.Fatalf("managed copy was not refreshed: content=%q err=%v", content, err)
	}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	if err := availability.Apply("sample"); err != nil {
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

func TestApplyRemovePlanDropsConfigBeforeMaster(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalCommandEntry(cfg, "sample", "echo ok", "", "")
	if err := config.SaveConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAgentSymlink("sample", "claude", skillsDir); err != nil {
		t.Fatal(err)
	}

	plan := BuildRemovePlan(cfg, skillsDir, []string{"sample"})
	if len(plan.Skills) != 1 || !plan.Skills[0].InConfig || !plan.Skills[0].MasterExists {
		t.Fatalf("plan = %#v", plan)
	}
	result, err := ApplyRemovePlan(plan, cfg, configPath, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || !result.Skills[0].RemovedFromConfig || !result.Skills[0].RemovedMaster {
		t.Fatalf("result = %#v", result.Skills)
	}
	if len(result.Skills[0].Unlinked) == 0 {
		t.Fatal("expected Availability unlink")
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Local["sample"]; ok {
		t.Fatal("config still declares sample")
	}
	if _, err := os.Lstat(master); !os.IsNotExist(err) {
		t.Fatal("master still exists")
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("agent link still exists")
	}
}

func TestApplyRemovePlanSavesConfigWhenMasterMissing(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalCommandEntry(cfg, "ghost", "echo ok", "", "")
	if err := config.SaveConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	plan := BuildRemovePlan(cfg, skillsDir, []string{"ghost"})
	if !plan.Skills[0].InConfig || plan.Skills[0].MasterExists {
		t.Fatalf("plan = %#v", plan.Skills[0])
	}
	result, err := ApplyRemovePlan(plan, cfg, configPath, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skills[0].RemovedFromConfig || result.Skills[0].RemovedMaster {
		t.Fatalf("result = %#v", result.Skills[0])
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Local["ghost"]; ok {
		t.Fatal("config still declares ghost")
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

func TestResolveCacheRepoDefaultsURLBranchAndDir(t *testing.T) {
	cache := t.TempDir()
	repo := resolveCacheRepo("https://github.com/owner/repo/tree/dev", "", "", cache)
	if repo.SourceKey != "owner/repo" {
		t.Fatalf("SourceKey = %q", repo.SourceKey)
	}
	if repo.URL != "https://github.com/owner/repo.git" {
		t.Fatalf("URL = %q", repo.URL)
	}
	if repo.Branch != "dev" {
		t.Fatalf("Branch = %q", repo.Branch)
	}
	wantDir := filepath.Join(cache, "owner", "repo")
	if repo.Dir != wantDir {
		t.Fatalf("Dir = %q; want %q", repo.Dir, wantDir)
	}
	over := resolveCacheRepo("owner/repo", "https://example.com/r.git", "main", cache)
	if over.URL != "https://example.com/r.git" || over.Branch != "main" {
		t.Fatalf("overrides = %#v", over)
	}
	if cacheDirOrDefault("") == "" {
		t.Fatal("empty cacheDir should fall back")
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

	result, err := UpdateRemoteSkills(cfg, []string{"skill-1", "skill-2"}, false, true, skillsDir, cacheDir, nil)
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

func TestMaterializeRemoteSkillCopiesAndReportsMissingPath(t *testing.T) {
	repo := t.TempDir()
	src := filepath.Join(repo, "sample")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	skillsDir := t.TempDir()
	if err := MaterializeRemoteSkill("sample", "sample", repo, skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeRemoteSkill("missing", "nope", repo, skillsDir); err == nil {
		t.Fatal("expected missing path")
	}
}

func TestUpdateRemoteSkillsReconcilesAvailabilityWhenUpToDate(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}

	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeRemoteSkill("sample", "sample", filepath.Join(cacheDir, "owner", "repo"), skillsDir); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	result, err := UpdateRemoteSkills(cfg, nil, false, false, skillsDir, cacheDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("update errors: %#v", result.Errors)
	}
	claudeLink := filepath.Join(project, ".claude", "skills", "sample")
	continueLink := filepath.Join(project, ".continue", "skills", "sample")
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("excluded Claude link still exists: %v", err)
	}
	if !IsManagedSkillLink(continueLink, "sample", skillsDir) {
		t.Fatal("declared Continue link was removed")
	}
}

func TestUpdateRemoteSkillsDryRunReportsAvailabilityDrift(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}

	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeRemoteSkill("sample", "sample", filepath.Join(cacheDir, "owner", "repo"), skillsDir); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	var drifted []string
	_, err := UpdateRemoteSkills(cfg, nil, false, true, skillsDir, cacheDir, func(ev UpdateEvent) {
		if ev.Kind != UpdateWouldDrift {
			return
		}
		if ev.Skill == "sample" && len(ev.Unexpected) > 0 {
			drifted = ev.Unexpected
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drifted, []string{"claude-code"}) {
		t.Fatalf("dry-run drift unexpected = %#v", drifted)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); err != nil {
		t.Fatal("dry-run must not apply availability")
	}
}

func TestUpdateRemoteSkillsApplyAvailabilityFailsClosed(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)

	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeRemoteSkill("sample", "sample", filepath.Join(cacheDir, "owner", "repo"), skillsDir); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := UpdateRemoteSkills(cfg, nil, false, false, skillsDir, cacheDir, nil)
	if err == nil {
		t.Fatal("expected availability apply failure to fail the update")
	}
}

func TestUpdateRemoteSkillsReportsAggregateRefreshLifecycle(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	var kinds []string
	_, err := UpdateRemoteSkills(cfg, []string{"sample"}, true, false, skillsDir, cacheDir, func(ev UpdateEvent) {
		kinds = append(kinds, ev.Kind)
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{UpdateRefreshStart, UpdateRefreshDone, UpdateStart, UpdateSkillRestored, UpdateRepoDone}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

func TestSyncDeclaredReportsLiveRemoteSourceLifecycle(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	var live []SyncEvent
	report, err := SyncDeclared(cfg, skillsDir, cacheDir, false, false, func(ev SyncEvent) {
		live = append(live, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live, report.Events) {
		t.Fatalf("live events = %#v, report = %#v", live, report.Events)
	}

	var kinds []string
	for _, ev := range live {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{SyncRepoStart, SyncRefreshStart, SyncRefreshDone, SyncMaterialized}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

func TestSyncDeclaredLocalSymlinkAppliesAvailability(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	src := filepath.Join(project, "skill-src")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddLocalSymlinkEntry(cfg, "sample", src, "")
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}

	report, err := SyncDeclared(cfg, skillsDir, t.TempDir(), false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Configured) != 1 {
		t.Fatalf("configured = %#v", report.Configured)
	}
	master := filepath.Join(skillsDir, "sample")
	if _, err := os.Lstat(master); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("excluded Claude link exists")
	}
	if !IsManagedSkillLink(filepath.Join(project, ".continue", "skills", "sample"), "sample", skillsDir) {
		t.Fatal("declared Continue link missing")
	}
}

func TestSyncDeclaredCommandCheckSkipsMaterialize(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalCommandEntry(cfg, "sample", "echo install", "exit 1", "")

	report, err := SyncDeclared(cfg, skillsDir, t.TempDir(), false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 1 || report.Events[0].Kind != SyncCheckFailed {
		t.Fatalf("events = %#v", report.Events)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("failed check must not apply Availability")
	}
}

func TestSyncDeclaredCommandFailureStillAppliesAvailability(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddLocalCommandEntry(cfg, "sample", "exit 1", "", "")
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	report, err := SyncDeclared(cfg, skillsDir, t.TempDir(), false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var failed bool
	for _, ev := range report.Events {
		if ev.Kind == SyncCommandFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected command failure event, got %#v", report.Events)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("excluded Claude link still exists")
	}
	if !IsManagedSkillLink(filepath.Join(project, ".continue", "skills", "sample"), "sample", skillsDir) {
		t.Fatal("declared Continue link missing")
	}
}

func TestCommandAdderFailureSavesAndAppliesAvailability(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	master := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	plan := BuildAddPlan(cfg, configPath, skillsDir, NewCommandAddSource("exit 1", "", ""), map[string]string{"sample": "."}, nil)
	_, err := ApplyAddPlan(plan, cfg, nil)
	if err == nil {
		t.Fatal("expected materialize error")
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Local["sample"].Command != "exit 1" {
		t.Fatalf("saved %#v", loaded.Local)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("excluded Claude link still exists")
	}
	if !IsManagedSkillLink(filepath.Join(project, ".continue", "skills", "sample"), "sample", skillsDir) {
		t.Fatal("declared Continue link missing")
	}
}

func TestSymlinkAdderRunDeclaresAndMaterializes(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "source")
	writeLocalGitSkill(t, source, "sample")
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	plan := BuildAddPlan(cfg, configPath, skillsDir, NewSymlinkAddSource(source, "local sample", false), map[string]string{"sample": "sample"}, nil)
	_, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Local["sample"]; got.Type != "symlink" || got.Description != "local sample" {
		t.Fatalf("saved local entry = %#v", got)
	}
	if target, err := os.Readlink(filepath.Join(skillsDir, "sample")); err != nil || target == "" {
		t.Fatalf("local Skill was not Materialized as a symlink: target=%q err=%v", target, err)
	}
}

func TestRemoteAdderRunDeclaresAndMaterializes(t *testing.T) {
	project := t.TempDir()
	repoDir := filepath.Join(project, "cache")
	writeLocalGitSkill(t, repoDir, "sample")
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	plan := BuildAddPlan(cfg, configPath, skillsDir, NewRemoteAddSource("owner/repo", "github", "", repoDir), map[string]string{"sample": "sample"}, nil)
	_, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Remote["owner/repo"].Skills["sample"]; got != "sample" {
		t.Fatalf("saved remote subpath = %q", got)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("remote Skill was not Materialized: %v", err)
	}
}

func writeLocalGitSkill(t *testing.T, repo, skill string) {
	t.Helper()
	dir := filepath.Join(repo, skill)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		stdout, stderr, err := RunGit(repo, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s\n%s", args, err, stdout, stderr)
		}
	}
}
