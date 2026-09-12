package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestBuildAddPlanDetectsRemoteConflicts(t *testing.T) {
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "original/repo", "my-skill", "subpath", "github", "")

	source := NewRemoteAddSource("new/repo", "github", "", "/tmp/cache")
	plan := BuildAddPlan(cfg, "/tmp/skills.json", "/tmp/skills", source, map[string]string{"my-skill": "subpath"}, nil)

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "my-skill" {
		t.Errorf("expected skill my-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[remote] original/repo" {
		t.Errorf("unexpected current source: %s", conflict.CurrentSrc)
	}
}

func TestApplyAddPlanRefusesSourceInsideSkillsDir(t *testing.T) {
	skillsDir := t.TempDir()
	dest := filepath.Join(skillsDir, "mine")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	skillMd := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(skillMd, []byte("# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir, NewSymlinkAddSource(dest, ""), map[string]string{"mine": "."}, nil)
	if _, err := ApplyAddPlan(plan, cfg, nil); err == nil {
		t.Fatal("expected refusal of a Source inside the skills directory")
	}
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination must remain a real directory: mode=%v err=%v", info.Mode(), err)
	}
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("SKILL.md must survive: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("Config must not be written")
	}
}

func TestBuildAddPlanDetectsUntrackedDiskConflicts(t *testing.T) {
	skillsDir := t.TempDir()
	untrackedDir := filepath.Join(skillsDir, "existing-skill")
	if err := os.MkdirAll(untrackedDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	source := NewSymlinkAddSource("/tmp/local-source", "local test")
	plan := BuildAddPlan(cfg, "/tmp/skills.json", skillsDir, source, map[string]string{"existing-skill": "."}, nil)

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "existing-skill" {
		t.Errorf("expected skill existing-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[untracked directory]" {
		t.Errorf("expected [untracked directory], got %s", conflict.CurrentSrc)
	}
}

// Add must record the same applied baseline Sync does. Without it a Skill
// added today is classified SkillUnknownBaseline after the next Update, so
// Sync blocks a routine upstream change instead of applying it.
func TestApplyAddPlanRecordsBaselineSoUpdateIsNotUnknown(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	configPath := filepath.Join(project, ".agents", "skills.json")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	repoDir, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", origin, repoDir),
		map[string]string{"sample": "sample"}, nil)
	if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Sample v2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "v2"}} {
		if _, _, err := RunGit(origin, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := UpdateRemoteSkills(cfg, nil, true, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}

	snapshot, err := InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Repositories[0].Skills[0]
	if got.Status != SkillCacheUpdateAvailable {
		t.Errorf("status after add then update = %q; want %q", got.Status, SkillCacheUpdateAvailable)
	}
}

func TestApplyAddPlanAvailabilityFailsClosed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	repoDir := filepath.Join(project, "repo")
	skillDir := filepath.Join(repoDir, "sample")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}

	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", "", repoDir),
		map[string]string{"sample": "sample"}, nil)
	if _, err := ApplyAddPlan(plan, cfg, nil); err == nil {
		t.Fatal("expected unmanaged Availability path to fail closed")
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Remote["owner/repo"].Skills["sample"] != "sample" {
		t.Fatalf("Config must be saved before apply fails: %#v", loaded.Remote)
	}
}

func TestApplyAddPlanStopsAfterFirstRemoteFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	repoDir := filepath.Join(project, "repo")
	good := filepath.Join(repoDir, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("# Good\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", "", repoDir),
		map[string]string{"bad": "missing", "good": "good"}, nil)
	if _, err := ApplyAddPlan(plan, cfg, nil); err == nil {
		t.Fatal("expected the missing Skill to fail apply")
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Remote["owner/repo"].Skills["good"]; !ok {
		t.Fatal("Config must already list every selected Skill")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "good", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("second Skill must not be Materialized after the first fails")
	}
}
