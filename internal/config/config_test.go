package config

import (
	"path/filepath"
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
