package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestDoctorRunCountsLeftoverButNotUntracked(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(skillsDir, "orphan")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	outcome, err := NewDoctor(cfg, skillsDir).Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Untracked skills") || !containsMessage(outcome.Findings, ": orphan") {
		t.Fatalf("missing untracked finding: %#v", outcome.Findings)
	}
	if !containsMessage(outcome.Findings, "leftover empty agent director") {
		t.Fatalf("missing leftover finding: %#v", outcome.Findings)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want 1 (untracked must not count)", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(project, ".continue", "skills")); err != nil {
		t.Fatalf("diagnose-only run mutated the filesystem: %v", err)
	}
}

func TestDoctorRunReportsMissingAndInvalidInventory(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(skillsDir, "broken")
	if err := os.MkdirAll(invalid, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing", ".", "github", "")
	config.AddLocalSymlinkEntry(cfg, "broken", invalid, "")

	outcome, err := NewDoctor(cfg, skillsDir).Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Configured but missing skills: missing") {
		t.Fatalf("missing inventory finding: %#v", outcome.Findings)
	}
	if !containsMessage(outcome.Findings, "Installed folders missing SKILL.md: broken") {
		t.Fatalf("missing invalid finding: %#v", outcome.Findings)
	}
}

func TestDoctorRunFixesThenRediagnosesFilesystem(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(project, ".claude", "skills")
	continueDir := filepath.Join(project, ".continue", "skills")
	for _, dir := range []string{claudeDir, continueDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	outcome, err := NewDoctor(cfg, skillsDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Removed") {
		t.Fatalf("expected leftover removal finding: %#v", outcome.Findings)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want actual post-fix state to be healthy", outcome.Remaining)
	}
	if _, err := os.Stat(continueDir); !os.IsNotExist(err) {
		t.Fatal("continue leftover should be removed")
	}
	if _, err := os.Stat(claudeDir); err != nil {
		t.Fatal("configured claude dir must remain")
	}
}

func TestDoctorRunRebuildsLegacyCacheBeforeRemovingIt(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(legacy, ".git")); !os.IsNotExist(err) {
		t.Fatalf("legacy Cache still exists: %v", err)
	}
	branch, err := GetRemoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	current := resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir
	if got := GetLocalRepoCommit(current); got == "" {
		t.Fatal("doctor --fix removed the legacy Cache without rebuilding a branch-aware Cache")
	}
}

func TestDoctorRunRecordsDefaultBranchForInferredSourceURL(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+origin+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/owner/repo.git")

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{Skills: map[string]string{}}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if got := GetLocalRepoCommit(resolveCacheRepo("owner/repo", "", "", cacheDir).Dir); got == "" {
		t.Fatal("default-branch identity was recorded under the unresolved empty URL")
	}
}

func TestDoctorRunKeepsValidBranchAwareCacheWithoutRemoteAccess(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	defaultBranch, err := GetRemoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	current, err := EnsureGitRepo("owner/repo", origin, defaultBranch, false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	wantCommit := GetLocalRepoCommit(current)

	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{
		URL:    filepath.Join(root, "unavailable"),
		Branch: defaultBranch,
		Skills: map[string]string{"sample": "sample"},
	}
	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if got := GetLocalRepoCommit(resolveCacheRepo("owner/repo", cfg.Remote["owner/repo"].URL, defaultBranch, cacheDir).Dir); got != wantCommit {
		t.Fatalf("preserved Cache commit = %q; want %q", got, wantCommit)
	}
}

func TestDoctorRunPreservesLegacyCacheWhenRebuildFails(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{
		URL:    filepath.Join(root, "unavailable"),
		Branch: "main",
		Skills: map[string]string{"sample": "sample"},
	}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want failed legacy Cache repair to remain", outcome.Remaining)
	}
	if got := GetLocalRepoCommit(legacy); got == "" {
		t.Fatal("doctor --fix removed the legacy Cache after its replacement failed")
	}
	if !containsMessage(outcome.Findings, "Failed to rebuild legacy Cache") {
		t.Fatalf("missing rebuild failure finding: %#v", outcome.Findings)
	}
}

func TestDoctorRunKeepsInstalledCacheAndReportsRecoveryWhenBackupCleanupFails(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Skills: map[string]string{}}

	doctor := NewDoctorWithCache(cfg, skillsDir, cacheDir)
	realRemoveAll := doctor.cacheMigration.ops.removeAll
	doctor.cacheMigration.ops.removeAll = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), ".legacy-cache-") {
			return fmt.Errorf("injected backup cleanup failure")
		}
		return realRemoveAll(path)
	}

	outcome, err := doctor.Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want recovery artifact to remain an issue", outcome.Remaining)
	}
	branch, branchErr := GetRemoteDefaultBranch("owner/repo", origin)
	if branchErr != nil {
		t.Fatal(branchErr)
	}
	if GetLocalRepoCommit(resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir) == "" {
		t.Fatal("installed branch-aware Cache must stay in place")
	}
	if !containsMessage(outcome.Findings, "Manual Cache recovery required") {
		t.Fatalf("missing recovery finding: %#v", outcome.Findings)
	}
}

func TestLegacyCacheMigrationRejectsPlanWhenLegacyCacheChanged(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Skills: map[string]string{}}
	migrator := newLegacyCacheMigrator(cfg, cacheDir)
	plans, _, err := migrator.detect()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %#v; want one legacy Cache migration", plans)
	}

	if err := os.WriteFile(filepath.Join(legacy, "changed"), []byte("new state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantCommit := GetLocalRepoCommit(legacy)

	results := migrator.apply(plans, nil)
	if len(results) != 1 || results[0].Status != legacyCacheFailed {
		t.Fatalf("results = %#v; want one failed stale migration", results)
	}
	if got := GetLocalRepoCommit(legacy); got != wantCommit {
		t.Fatalf("legacy commit = %q; want changed commit %q preserved", got, wantCommit)
	}
	if _, err := os.Stat(filepath.Join(legacy, "changed")); err != nil {
		t.Fatalf("uncommitted change was not preserved: %v", err)
	}
}

func TestDoctorRunRestoresLegacyCacheWhenReplacementRenameFails(t *testing.T) {
	doctor, legacy := newLegacyMigrationTestDoctor(t)
	realRename := doctor.cacheMigration.ops.rename
	renameCalls := 0
	doctor.cacheMigration.ops.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return fmt.Errorf("injected replacement rename failure")
		}
		return realRename(oldPath, newPath)
	}

	outcome, err := doctor.Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want restored legacy Cache to remain", outcome.Remaining)
	}
	if GetLocalRepoCommit(legacy) == "" {
		t.Fatal("legacy Cache was not restored after replacement rename failed")
	}
	if !containsMessage(outcome.Findings, "Failed to rebuild legacy Cache") {
		t.Fatalf("missing migration failure finding: %#v", outcome.Findings)
	}
	if containsMessage(outcome.Findings, "Manual Cache recovery required") {
		t.Fatalf("successful rollback must not require manual recovery: %#v", outcome.Findings)
	}
}

func TestDoctorRunPreservesArtifactsWhenReplacementAndRollbackRenameFail(t *testing.T) {
	doctor, _ := newLegacyMigrationTestDoctor(t)
	realRename := doctor.cacheMigration.ops.rename
	renameCalls := 0
	doctor.cacheMigration.ops.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls >= 2 {
			return fmt.Errorf("injected rename failure %d", renameCalls)
		}
		return realRename(oldPath, newPath)
	}

	outcome, err := doctor.Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 2 {
		t.Fatalf("Remaining = %d; want backup and staging recovery artifacts", outcome.Remaining)
	}
	if !containsMessage(outcome.Findings, "Manual Cache recovery required") ||
		!containsMessage(outcome.Findings, ".legacy-cache-") ||
		!containsMessage(outcome.Findings, ".doctor-cache-") {
		t.Fatalf("missing preserved recovery artifacts: %#v", outcome.Findings)
	}
}

func newLegacyMigrationTestDoctor(t *testing.T) (*Doctor, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Skills: map[string]string{}}
	return NewDoctorWithCache(cfg, skillsDir, cacheDir), legacy
}

func TestDoctorRunRebuildsEveryConfiguredBranchForLegacyCache(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	defaultBranch, err := GetRemoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "checkout", "-b", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "commit", "-m", "dev"); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := RunGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: defaultBranch, Skills: map[string]string{}}
	cfg.Remote["https://github.com/owner/repo/tree/dev"] = config.RemoteRepo{URL: origin, Branch: "dev", Skills: map[string]string{}}
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var rebuilt []string
	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).RunWithProgress(true, func(event DoctorEvent) {
		rebuilt = append(rebuilt, event.Source)
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if len(rebuilt) != 2 {
		t.Fatalf("rebuilt Sources = %q; want both configured branches", rebuilt)
	}
	for _, branch := range []string{defaultBranch, "dev"} {
		if got := GetLocalRepoCommit(resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir); got == "" {
			t.Fatalf("branch %s Cache was not rebuilt", branch)
		}
	}
}

func TestDoctorRunLeavesUnknownAgentReferencesForUser(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "not-a-real-agent"}
	cfg.Settings.Availability["some-skill"] = config.AvailabilityOverride{
		Include: []string{"also-fake"},
	}

	doctor := NewDoctor(cfg, skillsDir)
	before, err := doctor.Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if before.Remaining != 2 {
		t.Fatalf("Remaining = %d; want 2", before.Remaining)
	}
	if !containsMessage(before.Findings, `unknown agent "not-a-real-agent" in settings.defaultAgents`) ||
		!containsMessage(before.Findings, `unknown agent "also-fake" in settings.availability.some-skill.include`) {
		t.Fatalf("missing unknown-agent findings: %#v", before.Findings)
	}

	after, err := doctor.Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Remaining != 2 {
		t.Fatalf("Remaining after fix = %d; want 2 (unknown agents are never auto-fixed)", after.Remaining)
	}
}

func TestDoctorRunPropagatesInventoryErrors(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsDir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewDoctor(config.DefaultConfig(), skillsDir).Run(false)
	if err == nil {
		t.Fatal("expected inventory error")
	}
	if len(outcome.Findings) != 0 {
		t.Fatalf("Findings = %#v; want no partial healthy report", outcome.Findings)
	}
}
