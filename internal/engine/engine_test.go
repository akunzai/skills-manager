package engine

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

	discovered, err := DiscoverSkillsInRepo(tmpRepo, "")
	if err != nil {
		t.Fatalf("DiscoverSkillsInRepo failed: %v", err)
	}

	if len(discovered) != 2 {
		t.Fatalf("expected 2 skills, got %d (discovered: %+v)", len(discovered), discovered)
	}
	if !reflect.DeepEqual(discovered["skill-one"], []string{"skills/skill-one"}) {
		t.Errorf("unexpected skill-one paths: %v", discovered["skill-one"])
	}
	if !reflect.DeepEqual(discovered["skill-two"], []string{"plugins/sub/skill-two"}) {
		t.Errorf("unexpected skill-two paths: %v", discovered["skill-two"])
	}
	if _, ok := discovered["ignored-skill"]; ok {
		t.Errorf("expected node_modules skill to be ignored")
	}
	if _, ok := discovered["deep-skill"]; ok {
		t.Errorf("expected too deep skill to be ignored")
	}
}

func TestDiscoverSkillsInRepoCanonicalizesEquivalentMirrors(t *testing.T) {
	tmpRepo := t.TempDir()
	for _, path := range []string{"skills/duplicate/SKILL.md", ".github/plugins/example/skills/duplicate/SKILL.md"} {
		fullPath := filepath.Join(tmpRepo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("---\nname: duplicate\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	discovered, err := DiscoverSkillsInRepo(tmpRepo, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"skills/duplicate"}; !reflect.DeepEqual(discovered["duplicate"], want) {
		t.Fatalf("duplicate paths = %v, want %v", discovered["duplicate"], want)
	}
}

func TestDiscoverSkillsInRepoPreservesDivergentDuplicateCandidates(t *testing.T) {
	tmpRepo := t.TempDir()
	for path, content := range map[string]string{
		"skills/duplicate/SKILL.md":          "---\nname: duplicate\n---\nfirst\n",
		"plugins/example/duplicate/SKILL.md": "---\nname: duplicate\n---\nsecond\n",
	} {
		fullPath := filepath.Join(tmpRepo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	discovered, err := DiscoverSkillsInRepo(tmpRepo, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins/example/duplicate", "skills/duplicate"}
	if !reflect.DeepEqual(discovered["duplicate"], want) {
		t.Fatalf("duplicate paths = %v, want %v", discovered["duplicate"], want)
	}
}

func TestDiscoverSkillsInRepoCanonicalizesEquivalentGroupsIndependently(t *testing.T) {
	repo := t.TempDir()
	for path, body := range map[string]string{
		"skills/duplicate/SKILL.md":       "---\nname: duplicate\n---\nmirror\n",
		"plugins/a/duplicate/SKILL.md":    "---\nname: duplicate\n---\nmirror\n",
		"experimental/duplicate/SKILL.md": "---\nname: duplicate\n---\ndifferent\n",
	} {
		fullPath := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	discovered, err := DiscoverSkillsInRepo(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"experimental/duplicate", "skills/duplicate"}
	if !reflect.DeepEqual(discovered["duplicate"], want) {
		t.Fatalf("paths = %v, want independently canonicalized groups %v", discovered["duplicate"], want)
	}
}

func TestDiscoverSkillsInRepoAcceptsDirectSkillScope(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "skills", "sample")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: sample\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	discovered, err := DiscoverSkillsInRepo(repo, "skills/sample")
	if err != nil {
		t.Fatal(err)
	}
	want := DiscoveredSkills{"sample": {"skills/sample"}}
	if !reflect.DeepEqual(discovered, want) {
		t.Fatalf("discovered = %v, want %v", discovered, want)
	}
}

func TestDiscoverSkillsInRepoScopesTraversalAndKeepsRepositoryPaths(t *testing.T) {
	tmpRepo := t.TempDir()
	for _, path := range []string{"skills/one/SKILL.md", "plugins/two/SKILL.md"} {
		fullPath := filepath.Join(tmpRepo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("# Skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	discovered, err := DiscoverSkillsInRepo(tmpRepo, "skills")
	if err != nil {
		t.Fatal(err)
	}
	want := DiscoveredSkills{"one": {"skills/one"}}
	if !reflect.DeepEqual(discovered, want) {
		t.Fatalf("discovered = %#v, want %#v", discovered, want)
	}
}

func TestDiscoverSkillsInRepoMeasuresDepthFromScope(t *testing.T) {
	tmpRepo := t.TempDir()
	scope := filepath.Join("very", "deep", "collection", "skills")
	skillDir := filepath.Join(tmpRepo, scope, "nested", "sample")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	discovered, err := DiscoverSkillsInRepo(tmpRepo, filepath.ToSlash(scope))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.ToSlash(filepath.Join(scope, "nested", "sample"))}
	if !reflect.DeepEqual(discovered["sample"], want) {
		t.Fatalf("sample paths = %v, want %v", discovered["sample"], want)
	}
}

func TestDiscoverSkillsInRepoRejectsEscapingScope(t *testing.T) {
	_, err := DiscoverSkillsInRepo(t.TempDir(), "../outside")
	if err == nil || !strings.Contains(err.Error(), "escapes repository") {
		t.Fatalf("error = %v; want repository escape rejection", err)
	}
}

func TestDiscoverSkillsInRepoPreservesExecutableAndSymlinkDifferences(t *testing.T) {
	t.Run("executable bit", func(t *testing.T) {
		repo := t.TempDir()
		for _, root := range []string{"skills/duplicate", "mirror/duplicate"} {
			dir := filepath.Join(repo, root)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: duplicate\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			mode := os.FileMode(0o644)
			if strings.HasPrefix(root, "skills/") {
				mode = 0o755
			}
			if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("exit 0\n"), mode); err != nil {
				t.Fatal(err)
			}
		}

		discovered, err := DiscoverSkillsInRepo(repo, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(discovered["duplicate"]) != 2 {
			t.Fatalf("paths = %v; executable difference was collapsed", discovered["duplicate"])
		}
	})

	t.Run("symlink target", func(t *testing.T) {
		repo := t.TempDir()
		for _, root := range []string{"skills/duplicate", "mirror/duplicate"} {
			dir := filepath.Join(repo, root)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: duplicate\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			target := "first"
			if strings.HasPrefix(root, "mirror/") {
				target = "second"
			}
			if err := os.Symlink(target, filepath.Join(dir, "reference")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}

		discovered, err := DiscoverSkillsInRepo(repo, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(discovered["duplicate"]) != 2 {
			t.Fatalf("paths = %v; symlink difference was collapsed", discovered["duplicate"])
		}
	})
}

func TestDiscoverSkillsInRepoFailsClosedForUnsupportedBundleEntries(t *testing.T) {
	repo, err := os.MkdirTemp("/tmp", "skill-bundle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repo) })
	var listeners []net.Listener
	for _, root := range []string{"skills/duplicate", "mirror/duplicate"} {
		dir := filepath.Join(repo, root)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: duplicate\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", filepath.Join(dir, "socket"))
		if err != nil {
			t.Skipf("Unix sockets unavailable: %v", err)
		}
		listeners = append(listeners, listener)
	}
	t.Cleanup(func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	})

	discovered, err := DiscoverSkillsInRepo(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered["duplicate"]) != 2 {
		t.Fatalf("paths = %v; unsupported entries must not auto-deduplicate", discovered["duplicate"])
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
	drift := availability.ObserveAvailability("sample")
	if len(drift.Missing) != 0 || !reflect.DeepEqual(drift.Unexpected, []string{"claude-code"}) {
		t.Fatalf("drift = %#v", drift)
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

func TestObserveRemoteFreshnessSourceParsing(t *testing.T) {
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
		res := newRemoteSource(nil, tt.source, config.RemoteRepo{
			Skills: map[string]string{"foo": "skills/foo"},
		}, tmpCache).ObserveFreshness()

		if filepath.Dir(res.CachePath) != tt.expectedDest {
			t.Errorf("ObserveFreshness(%q).CachePath parent = %q; want %q", tt.source, filepath.Dir(res.CachePath), tt.expectedDest)
		}
	}
}

func TestUpdateChecksRemoteSourcesInParallel(t *testing.T) {
	oldCheck := observeRemoteSource
	entered := make(chan string, 3)
	release := make(chan struct{})
	observeRemoteSource = func(source string, _ config.RemoteRepo, _ string) FreshnessRepository {
		entered <- source
		<-release
		return FreshnessRepository{Source: source, RemoteStatus: RemoteUpToDate}
	}
	t.Cleanup(func() { observeRemoteSource = oldCheck })

	cfg := config.DefaultConfig()
	for _, source := range []string{"owner/one", "owner/two", "owner/three"} {
		cfg.Remote[source] = config.RemoteRepo{Skills: map[string]string{"sample": "sample"}}
	}
	cacheDir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := UpdateRemoteSkills(cfg, nil, false, true, cacheDir, nil)
		done <- err
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatal("Update checked remote Sources sequentially")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
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
	wantDir := filepath.Join(cache, "owner", "repo", cacheBranchKey("dev"))
	if repo.Dir != wantDir {
		t.Fatalf("Dir = %q; want %q", repo.Dir, wantDir)
	}
	over := resolveCacheRepo("owner/repo", "https://example.com/r.git", "main", cache)
	if over.URL != "https://example.com/r.git" || over.Branch != "main" {
		t.Fatalf("overrides = %#v", over)
	}
	if over.Dir == repo.Dir {
		t.Fatal("different branches must have isolated Cache paths")
	}
	defaultBranch := resolveCacheRepo("owner/repo", "https://example.com/r.git", "", cache)
	if defaultBranch.Dir == over.Dir {
		t.Fatal("an unspecified branch must not share an explicit branch Cache")
	}
	if cacheDirOrDefault("") == "" {
		t.Fatal("empty cacheDir should fall back")
	}
}

func TestEnsureGitRepoRecordsResolvedDefaultBranchIdentity(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	branch, err := GetRemoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	unspecified, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	explicit := resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir
	if unspecified != explicit {
		t.Fatalf("unspecified Cache = %q, explicit default = %q", unspecified, explicit)
	}
	if got := resolveCacheRepo("owner/repo", origin, "", cacheDir).Dir; got != explicit {
		t.Fatalf("recorded default Cache = %q", got)
	}
}

func TestGetRemoteDefaultBranchCommit(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	wantBranch, _, err := RunGit(origin, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}

	branch, commit, err := getRemoteDefaultBranchCommit("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	if branch != wantBranch {
		t.Fatalf("branch = %q; want %q", branch, wantBranch)
	}
	if want := GetLocalRepoCommit(origin); commit != want {
		t.Fatalf("commit = %q; want %q", commit, want)
	}
}

func TestGetRemoteRepoCommitMatchesExactBranch(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	want := GetLocalRepoCommit(origin)
	branch, _, err := RunGit(origin, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	decoy, _, err := RunGit(origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit-tree", "HEAD^{tree}", "-m", "decoy")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "update-ref", "refs/for/"+branch, decoy); err != nil {
		t.Fatal(err)
	}

	got, err := GetRemoteRepoCommitResult("owner/repo", origin, branch)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("commit = %q; want exact refs/heads/%s commit %q", got, branch, want)
	}
}

func TestUpdateDetectsChangedRemoteDefaultBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cacheDir := filepath.Join(root, "cache")
	first, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "checkout", "-b", "next"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "next"); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	status := newRemoteSource(nil, "owner/repo", cfg.Remote["owner/repo"], cacheDir).ObserveFreshness()
	if status.RemoteStatus != RemoteNotCached || status.Branch != "next" {
		t.Fatalf("status = %#v", status)
	}
	if _, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	second := resolveCacheRepo("owner/repo", origin, "", cacheDir).Dir
	if second == first {
		t.Fatal("changed default branch reused the old Cache identity")
	}
	if got := GetLocalRepoCommit(second); got == "" {
		t.Fatal("new default branch Cache was not created")
	}
}

func TestResolveUpdateSourcesRejectsCrossCategoryAmbiguity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Remote["team/shared"] = config.RemoteRepo{Skills: map[string]string{"one": "one"}}
	cfg.Remote["team/other"] = config.RemoteRepo{Skills: map[string]string{"team/shared": "two"}}
	if _, err := resolveUpdateSources(cfg, []string{"team/shared"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity error = %v", err)
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
	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo1", "skill-1", "skills/skill-1", "github", "")
	config.AddRemoteSkillEntry(cfg, "github:owner/repo2", "skill-2", "skills/skill-2", "github", "")
	for source, repo := range cfg.Remote {
		repo.Branch = "main"
		cfg.Remote[source] = repo
	}

	result, err := UpdateRemoteSkills(cfg, []string{"skill-1", "skill-2"}, false, true, cacheDir, nil)
	if err != nil {
		t.Fatalf("UpdateRemoteSkills dry-run failed: %v", err)
	}

	if len(result.UpdatedRepos) != 2 {
		t.Fatalf("expected 2 updated repos in dry run, got %d", len(result.UpdatedRepos))
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

func TestUpdateRemoteSkillsDoesNotReconcileAvailability(t *testing.T) {
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
	if err := MaterializeRemoteSkill("sample", "sample", resolveCacheRepo("owner/repo", origin, "", cacheDir).Dir, skillsDir); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	result, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("update errors: %#v", result.Errors)
	}
	claudeLink := filepath.Join(project, ".claude", "skills", "sample")
	continueLink := filepath.Join(project, ".continue", "skills", "sample")
	if _, err := os.Lstat(claudeLink); err != nil {
		t.Fatalf("Update changed the Claude link: %v", err)
	}
	if !IsManagedSkillLink(continueLink, "sample", skillsDir) {
		t.Fatal("declared Continue link was removed")
	}
}

func TestUpdateRemoteSkillsDryRunDoesNotApplyAvailabilityDrift(t *testing.T) {
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
	if err := MaterializeRemoteSkill("sample", "sample", resolveCacheRepo("owner/repo", origin, "", cacheDir).Dir, skillsDir); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "continue"} {
		if _, err := EnsureAgentSymlink("sample", agent, skillsDir); err != nil {
			t.Fatal(err)
		}
	}

	_, err := UpdateRemoteSkills(cfg, nil, false, true, cacheDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); err != nil {
		t.Fatal("dry-run must not apply availability")
	}
}

func TestUpdateRemoteSkillsIgnoresUnmanagedAvailabilityPath(t *testing.T) {
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
	if err := MaterializeRemoteSkill("sample", "sample", resolveCacheRepo("owner/repo", origin, "", cacheDir).Dir, skillsDir); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unmanaged); err != nil {
		t.Fatalf("Update changed unmanaged Scope content: %v", err)
	}
}

func TestUpdateRemoteSkillsReportsAggregateRefreshLifecycle(t *testing.T) {
	project := t.TempDir()
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	var kinds []string
	_, err := UpdateRemoteSkills(cfg, []string{"sample"}, true, false, cacheDir, func(ev UpdateEvent) {
		kinds = append(kinds, ev.Kind)
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{UpdateCheckStart, UpdateCheckDone, UpdateRefreshStart, UpdateStart, UpdateRepoDone, UpdateRefreshDone}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

// applyPlan plans and applies in one step for tests that are about what Sync
// does, not about the seam between the two phases.
func applyPlan(t *testing.T, cfg *config.Config, skillsDir, cacheDir string, decision SyncDecision, onProgress func(SyncEvent)) (*SyncReport, error) {
	t.Helper()
	plan, err := PlanSync(cfg, skillsDir, cacheDir)
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}
	return plan.Apply(decision, onProgress)
}

func TestSyncPlanApplyReportsLiveRemoteSourceLifecycle(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	var live []SyncEvent
	report, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, func(ev SyncEvent) {
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
	want := []string{SyncRepoStart, SyncMaterialized}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

func TestSyncPlanApplyLocalSymlinkAppliesAvailability(t *testing.T) {
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

	report, err := applyPlan(t, cfg, skillsDir, t.TempDir(), SyncDecision{}, nil)
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

func TestSyncPlanApplyCommandCheckSkipsMaterialize(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalCommandEntry(cfg, "sample", "echo install", "exit 1", "")

	report, err := applyPlan(t, cfg, skillsDir, t.TempDir(), SyncDecision{}, nil)
	if err == nil {
		t.Fatal("failed command must make Sync non-zero")
	}
	if len(report.Events) != 1 || report.Events[0].Kind != SyncCheckFailed {
		t.Fatalf("events = %#v", report.Events)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatal("failed check must not apply Availability")
	}
}

func TestSyncPlanApplyCommandFailureStillAppliesAvailability(t *testing.T) {
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

	report, err := applyPlan(t, cfg, skillsDir, t.TempDir(), SyncDecision{}, nil)
	if err == nil {
		t.Fatal("failed command must make Sync non-zero")
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

func TestAddPlanCommandFailureSavesAndAppliesAvailability(t *testing.T) {
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

func TestAddPlanSymlinkDeclaresAndMaterializes(t *testing.T) {
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

func TestAddPlanRemoteDeclaresAndMaterializes(t *testing.T) {
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
