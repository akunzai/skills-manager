package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestPlanScopeFreshnessClassifiesRemoteSkillContent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	skillsDir := filepath.Join(root, "skills")
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	cachePath, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Repositories[0].Skills[0].Status; got != SkillMissing {
		t.Fatalf("status = %q", got)
	}

	if err := MaterializeRemoteSkill("sample", "sample", cachePath, skillsDir); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if got := plan.Repositories[0].Skills[0].Status; got != SkillInSync {
		t.Fatalf("status = %q", got)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnknownBaseline {
		t.Fatalf("status = %q", got)
	}

	store, _ := NewScopeStateStore(skillsDir)
	applied, _ := DigestSkillContent(filepath.Join(skillsDir, "sample"))
	state, _ := store.Load()
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: cachePath, AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if got := plan.Repositories[0].Skills[0].Status; got != SkillCacheUpdateAvailable {
		t.Fatalf("status = %q", got)
	}
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: filepath.Join(root, "other-cache"), AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnknownBaseline {
		t.Fatalf("changed Cache identity status = %q", got)
	}
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: cachePath, AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("more manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if got := plan.Repositories[0].Skills[0].Status; got != SkillLocalDrift {
		t.Fatalf("status = %q", got)
	}
}

func TestPlanScopeFreshnessReportsUnverifiedWithoutCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", filepath.Join(t.TempDir(), "origin"))
	plan, err := PlanScopeFreshness(cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnverified {
		t.Fatalf("status = %q", got)
	}
}

func TestSyncUsesCacheBaselineAndProtectsLocalDrift(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cacheDir, skillsDir, origin := filepath.Join(root, "cache"), filepath.Join(root, "skills"), filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDeclared(cfg, skillsDir, cacheDir, false, false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Cached v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDeclared(cfg, skillsDir, cacheDir, false, false, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md"))
	if string(got) != "# Cached v2\n" {
		t.Fatalf("Scope content = %q", got)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncDeclared(cfg, skillsDir, cacheDir, false, false, nil); err == nil {
		t.Fatal("local drift should fail normal Sync")
	}
	got, _ = os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md"))
	if string(got) != "manual\n" {
		t.Fatalf("local drift was overwritten: %q", got)
	}
	if _, err := SyncDeclared(cfg, skillsDir, cacheDir, true, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMissingCacheFailsWithoutNetworkAndContinues(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "bad/repo", "bad", "bad", "git", filepath.Join(root, "missing-origin"))
	local := filepath.Join(root, "local")
	mustWriteScopeStateTestFile(t, filepath.Join(local, "SKILL.md"), []byte("# Local\n"))
	config.AddLocalSymlinkEntry(cfg, "local", local, "")
	report, err := SyncDeclared(cfg, filepath.Join(root, "skills"), filepath.Join(root, "cache"), false, false, nil)
	if err == nil || report.Failures == 0 {
		t.Fatal("missing Cache should make Sync fail")
	}
	if _, statErr := os.Lstat(filepath.Join(root, "skills", "local")); statErr != nil {
		t.Fatalf("unrelated local Skill did not converge: %v", statErr)
	}
}
