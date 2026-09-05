package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestDoctorRunCountsLeftoverButNotUntracked(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !slices.Contains(outcome.Report.Untracked, "orphan") {
		t.Fatalf("Untracked = %#v; want orphan", outcome.Report.Untracked)
	}
	if len(outcome.Report.LeftoverEmpty) == 0 {
		t.Fatal("diagnosis did not report the leftover empty agent directory")
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want 1 (untracked must not count)", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(project, ".continue", "skills")); err != nil {
		t.Fatalf("diagnose-only run mutated the filesystem: %v", err)
	}
}

func TestDoctorRunReportsMissingAndInvalidInventory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !slices.Contains(outcome.Report.Missing, "missing") {
		t.Fatalf("Missing = %#v; want the declared but unmaterialized Skill", outcome.Report.Missing)
	}
	if len(outcome.Report.Invalid) != 1 || outcome.Report.Invalid[0].Name != "broken" {
		t.Fatalf("Invalid = %#v; want the SKILL.md-less folder", outcome.Report.Invalid)
	}
}

func TestDoctorRunFixesThenRediagnosesFilesystem(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if outcome.Repair == nil || len(outcome.Repair.RemovedLeftover) == 0 {
		t.Fatalf("Repair = %#v; want a removed leftover directory", outcome.Repair)
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !hasCacheMigration(outcome.Repair, CacheMigrationFailed) {
		t.Fatalf("Repair = %#v; want a failed Cache migration", outcome.Repair)
	}
}

func TestDoctorRunKeepsInstalledCacheAndReportsRecoveryWhenBackupCleanupFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !hasCacheMigration(outcome.Repair, CacheMigrationRecoveryNeeded) {
		t.Fatalf("Repair = %#v; want a Cache migration needing manual recovery", outcome.Repair)
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !hasCacheMigration(outcome.Repair, CacheMigrationFailed) {
		t.Fatalf("Repair = %#v; want a failed Cache migration", outcome.Repair)
	}
	if hasCacheMigration(outcome.Repair, CacheMigrationRecoveryNeeded) {
		t.Fatalf("successful rollback must not require manual recovery: %#v", outcome.Repair)
	}
}

func TestDoctorRunPreservesArtifactsWhenReplacementAndRollbackRenameFail(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	artifacts := strings.Join(cacheMigrationArtifacts(outcome.Repair, CacheMigrationRecoveryNeeded), " ")
	if artifacts == "" ||
		!strings.Contains(artifacts, ".legacy-cache-") ||
		!strings.Contains(artifacts, ".doctor-cache-") {
		t.Fatalf("preserved recovery artifacts = %q; want both the legacy and doctor Cache trees", artifacts)
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if !hasUnknownAgent(before.Report.UnknownAgents, "", AgentRefDefaultAgents, "not-a-real-agent") ||
		!hasUnknownAgent(before.Report.UnknownAgents, "some-skill", AgentRefInclude, "also-fake") {
		t.Fatalf("UnknownAgents = %#v; want both policy references", before.Report.UnknownAgents)
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	if outcome.Report.SkillsDir != "" || outcome.Repair != nil {
		t.Fatalf("outcome = %#v; want no partial report when Run fails", outcome)
	}
}

func hasUnknownAgent(refs []UnknownAgentReference, skill, field, agent string) bool {
	for _, ref := range refs {
		if ref.Skill == skill && ref.Field == field && ref.Agent == agent {
			return true
		}
	}
	return false
}

func hasCacheMigration(repair *RepairOutcome, status CacheMigrationStatus) bool {
	if repair == nil {
		return false
	}
	for _, migration := range repair.CacheMigrations {
		if migration.Status == status {
			return true
		}
	}
	return false
}

func cacheMigrationArtifacts(repair *RepairOutcome, status CacheMigrationStatus) []string {
	if repair == nil {
		return nil
	}
	var artifacts []string
	for _, migration := range repair.CacheMigrations {
		if migration.Status == status {
			artifacts = append(artifacts, migration.Artifacts...)
		}
	}
	return artifacts
}
