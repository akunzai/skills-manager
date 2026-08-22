package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigLoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "skills.json")

	// 1. Loading non-existent config should return default
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}

	// 2. Add remote skill and local skill
	AddRemoteSkillEntry(cfg, "owner/repo", "skill-a", "skills/skill-a", "github", "")
	AddLocalSymlinkEntry(cfg, "local-skill", "/tmp/local-skill", "a local test skill")
	AddLocalCommandEntry(cfg, "cmd-skill", "echo install", "which echo", "a cmd skill")

	if err := SaveConfig(cfg, configPath); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// 3. Reload config and verify contents
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig re-load failed: %v", err)
	}

	if len(loaded.Remote) != 1 {
		t.Fatalf("expected 1 remote repo, got %d", len(loaded.Remote))
	}
	if loaded.Remote["owner/repo"].Skills["skill-a"] != "skills/skill-a" {
		t.Errorf("unexpected skill-a subpath")
	}
	if len(loaded.Local) != 2 {
		t.Fatalf("expected 2 local skills, got %d", len(loaded.Local))
	}
	// 4. Test FindSkillSource
	cat, src, found := FindSkillSource(loaded, "skill-a")
	if !found || cat != "remote" || src != "owner/repo" {
		t.Errorf("FindSkillSource(skill-a) = (%v, %v, %v); want (remote, owner/repo, true)", cat, src, found)
	}

	cat, src, found = FindSkillSource(loaded, "local-skill")
	if !found || cat != "local" || src != "local-skill" {
		t.Errorf("FindSkillSource(local-skill) = (%v, %v, %v); want (local, local-skill, true)", cat, src, found)
	}

	// 5. Test RemoveSkillEntry
	if !RemoveSkillEntry(loaded, "skill-a") {
		t.Errorf("expected RemoveSkillEntry for skill-a to return true")
	}
	if len(loaded.Remote) != 0 {
		t.Errorf("expected empty remote map after removing only skill in repo")
	}
}

func TestGetConfiguredSkillNames(t *testing.T) {
	cfg := DefaultConfig()
	AddRemoteSkillEntry(cfg, "owner/repo1", "skill-b", "b", "github", "")
	AddRemoteSkillEntry(cfg, "owner/repo2", "skill-a", "a", "github", "")
	AddLocalSymlinkEntry(cfg, "skill-c", "/tmp/c", "")

	names := GetConfiguredSkillNames(cfg)
	expected := []string{"skill-a", "skill-b", "skill-c"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q; want %q", i, name, expected[i])
		}
	}
}

func TestAddSkillEntryReplacesConflictingSource(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["my-skill"] = AvailabilityOverride{Include: []string{"codex"}}

	// 1. Add local symlink skill
	AddLocalSymlinkEntry(cfg, "my-skill", "/path/to/my-skill", "local")
	if _, ok := cfg.Local["my-skill"]; !ok {
		t.Fatal("expected my-skill in local")
	}

	// 2. Add remote skill with same name -> should replace local
	AddRemoteSkillEntry(cfg, "owner/repo", "my-skill", "skills/my-skill", "github", "")
	if _, ok := cfg.Local["my-skill"]; ok {
		t.Fatal("expected local entry for my-skill to be removed")
	}
	if cfg.Remote["owner/repo"].Skills["my-skill"] != "skills/my-skill" {
		t.Fatal("expected remote entry for my-skill")
	}

	// 3. Add local command skill with same name -> should replace remote
	AddLocalCommandEntry(cfg, "my-skill", "echo hi", "", "command")
	if _, ok := cfg.Remote["owner/repo"]; ok {
		t.Fatal("expected empty remote repo to be removed")
	}
	if cfg.Local["my-skill"].Command != "echo hi" {
		t.Fatal("expected local command entry for my-skill")
	}
	if len(cfg.Settings.Availability["my-skill"].Include) != 1 {
		t.Fatal("replacing a skill source must preserve its availability")
	}
	if !RemoveSkillEntry(cfg, "my-skill") {
		t.Fatal("expected skill removal to succeed")
	}
	if _, ok := cfg.Settings.Availability["my-skill"]; ok {
		t.Fatal("removing a skill must remove its availability override")
	}
}

func TestNormalizeAvailabilityCanonicalizesAgents(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["sample"] = AvailabilityOverride{
		Include: []string{"claude", "CLAUDE-CODE", "continue"},
		Exclude: []string{"roo-code", "roo"},
	}
	if err := NormalizeAvailability(cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Settings.Availability["sample"]
	if !reflect.DeepEqual(got.Include, []string{"claude-code", "continue"}) {
		t.Fatalf("include = %#v", got.Include)
	}
	if !reflect.DeepEqual(got.Exclude, []string{"roo"}) {
		t.Fatalf("exclude = %#v", got.Exclude)
	}
}

func TestNormalizeAvailabilityRejectsConflicts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["sample"] = AvailabilityOverride{
		Include: []string{"claude"},
		Exclude: []string{"claude-code"},
	}
	if err := NormalizeAvailability(cfg); err == nil {
		t.Fatal("expected include/exclude conflict")
	}
}

func TestAvailabilityMutationsRemainConflictFree(t *testing.T) {
	cfg := DefaultConfig()
	if err := ExcludeSkillAgents(cfg, "sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := IncludeSkillAgents(cfg, "sample", "claude", "continue"); err != nil {
		t.Fatal(err)
	}
	override := cfg.Settings.Availability["sample"]
	if !reflect.DeepEqual(override.Include, []string{"claude-code", "continue"}) || len(override.Exclude) != 0 {
		t.Fatalf("override = %#v", override)
	}
	if err := ResetSkillAgents(cfg, "sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Settings.Availability["sample"].Include, []string{"continue"}) {
		t.Fatalf("reset override = %#v", cfg.Settings.Availability["sample"])
	}
	FollowDefaults(cfg, "sample")
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatal("follow-defaults did not clear override")
	}
}
