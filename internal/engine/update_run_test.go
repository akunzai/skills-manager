package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// Preparing Update refreshes the Cache before observing the Sync plan, but
// leaves the Scope alone until the decision is applied.
func TestPreparedUpdateRefreshesBeforeSync(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin, skillsDir, cacheDir := filepath.Join(root, "origin"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	writeLocalGitSkill(t, origin, "sample")
	branch, _, err := runGit(origin, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: branch, Skills: map[string]string{"sample": "sample"}}
	prepared, err := PrepareUpdate(cfg, filepath.Join(root, "skills.json"), skillsDir, cacheDir, UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path != UpdateApply || len(prepared.Refresh.UpdatedRepos) != 1 || prepared.Sync.Summary(SyncDecision{}).Pending != 1 {
		t.Fatalf("prepared Update = %#v; want a refreshed Source and a pending Sync", prepared)
	}
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Fatalf("prepare wrote the Scope: %v", err)
	}
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdateComplete || !outcome.Summary.Converged() || outcome.Report == nil {
		t.Fatalf("outcome = %#v; want completed Sync", outcome)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedUpdatePreviewLeavesCacheAndScopeUntouched(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin, skillsDir, cacheDir := filepath.Join(root, "origin"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	writeLocalGitSkill(t, origin, "sample")
	branch, _, err := runGit(origin, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: branch, Skills: map[string]string{"sample": "sample"}}
	prepared, err := PrepareUpdate(cfg, "", skillsDir, cacheDir, UpdateOptions{DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path != UpdatePreview {
		t.Fatalf("path = %v; want preview", prepared.Path)
	}
	outcome, err := prepared.Finish(SyncDecision{Force: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdatePendingSync || outcome.Report != nil || len(outcome.Refresh.UpdatedRepos) != 1 {
		t.Fatalf("outcome = %#v; want planned refresh and blocked Sync, without a report", outcome)
	}
	for _, path := range []string{cacheDir, skillsDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote %s: %v", filepath.Base(path), err)
		}
	}
}

// Refresh failure determines the outcome even beside a Sync failure, without
// discarding either phase or preventing another Source from being synced.
func TestPreparedUpdateRefreshFailureKeepsSyncResults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin, skillsDir, cacheDir := filepath.Join(root, "origin"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	writeLocalGitSkill(t, origin, "sample")
	branch, _, err := runGit(origin, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: branch, Skills: map[string]string{"sample": "sample"}}
	cfg.Remote["owner/missing"] = config.RemoteRepo{URL: filepath.Join(root, "missing"), Branch: "main", Skills: map[string]string{"lost": "lost"}}
	config.AddLocalCommandEntry(cfg, "broken", "exit 1", "", "")
	prepared, err := PrepareUpdate(cfg, filepath.Join(root, "skills.json"), skillsDir, cacheDir, UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdateRefreshFailed || len(outcome.Refresh.Errors) != 1 || outcome.Summary.Failed != 1 || outcome.Summary.Blocked != 1 {
		t.Fatalf("outcome = %#v; want refresh failure beside failed and blocked Sync work", outcome)
	}
	if outcome.Report == nil || len(outcome.Report.Events) == 0 {
		t.Fatal("Sync diagnostics were lost")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedUpdateConvergedDryRunStillNeedsRefresh(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin, skillsDir, cacheDir := filepath.Join(root, "origin"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	writeLocalGitSkill(t, origin, "sample")
	branch, _, err := runGit(origin, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: branch, Skills: map[string]string{"sample": "sample"}}
	prepared, err := PrepareUpdate(cfg, "", skillsDir, cacheDir, UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Finish(SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	mustWriteScopeStateTestFile(t, filepath.Join(origin, "sample", "SKILL.md"), []byte("# Updated\n"))
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "update"}} {
		if _, _, err := runGit(origin, args...); err != nil {
			t.Fatal(err)
		}
	}
	before := treeSnapshot(t, skillsDir)
	cache := NewCache("owner/repo", origin, branch, cacheDir)
	head := cache.head()
	prepared, err = PrepareUpdate(cfg, "", skillsDir, cacheDir, UpdateOptions{DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path != UpdateConverged {
		t.Fatalf("path = %v; want already converged", prepared.Path)
	}
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdatePendingRefresh || !outcome.Summary.Converged() || outcome.Report != nil {
		t.Fatalf("outcome = %#v; want pending refresh without applying Sync", outcome)
	}
	if cache.head() != head {
		t.Fatal("dry-run refreshed the Cache")
	}
	if after := treeSnapshot(t, skillsDir); !reflect.DeepEqual(before, after) {
		t.Fatal("dry-run changed the Scope")
	}
}

func TestPreparedUpdateReportsSyncFailureWithoutRefreshFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cfg := config.DefaultConfig()
	config.AddLocalCommandEntry(cfg, "broken", "exit 1", "", "")
	prepared, err := PrepareUpdate(cfg, "", filepath.Join(root, "skills"), filepath.Join(root, "cache"), UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdateSyncFailed || outcome.Summary.Failed != 1 || len(outcome.Refresh.Errors) != 0 {
		t.Fatalf("outcome = %#v; want a Sync failure", outcome)
	}
}

func TestPreparedUpdateFatalApplyErrorOutranksRefreshFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	cfg := config.DefaultConfig()
	cfg.Remote["owner/missing"] = config.RemoteRepo{URL: filepath.Join(root, "missing"), Branch: "main", Skills: map[string]string{"lost": "lost"}}
	prepared, err := PrepareUpdate(cfg, "", skillsDir, filepath.Join(root, "cache"), UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Refresh.Errors) != 1 {
		t.Fatal("fixture did not fail refresh")
	}
	// The Scope becomes unavailable between preparation and application.
	mustWriteScopeStateTestFile(t, skillsDir, []byte("occupied"))
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to create skills dir") || outcome != nil {
		t.Fatalf("Finish = %#v, %v; want fatal Apply error without a partial outcome", outcome, err)
	}
}

func TestPreparedUpdateConvergedScopeSkipsCommandChecks(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	cfg := config.DefaultConfig()
	cfg.Settings.Availability["installed"] = config.AvailabilityOverride{Exclude: []string{"claude"}}
	config.AddLocalCommandEntry(cfg, "installed", "exit 1", "exit 1", "")
	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "installed", "SKILL.md"), []byte("# Installed\n"))
	prepared, err := PrepareUpdate(cfg, "", skillsDir, filepath.Join(root, "cache"), UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path != UpdateConverged {
		t.Fatalf("path = %v; want converged", prepared.Path)
	}
	outcome, err := prepared.Finish(SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdateComplete || outcome.Report != nil {
		t.Fatalf("outcome = %#v; the failing check must not run in a converged Scope", outcome)
	}
}

func TestPreparedUpdateAppliesTheUnknownBaselineDecision(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin, skillsDir, cacheDir := filepath.Join(root, "origin"), filepath.Join(root, "skills"), filepath.Join(root, "cache")
	writeLocalGitSkill(t, origin, "sample")
	branch, _, err := runGit(origin, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: branch, Skills: map[string]string{"sample": "sample"}}
	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Unknown copy\n"))
	prepared, err := PrepareUpdate(cfg, "", skillsDir, cacheDir, UpdateOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Sync.Unknown()) != 1 || prepared.Sync.Summary(SyncDecision{}).Blocked != 1 {
		t.Fatal("fixture must require confirmation")
	}
	outcome, err := prepared.Finish(SyncDecision{AllowUnknown: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != UpdateComplete || outcome.Summary.Blocked != 0 || outcome.Report == nil {
		t.Fatalf("outcome = %#v; want the accepted decision reflected in the final summary", outcome)
	}
	if data, err := os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil || string(data) != "# Sample\n" {
		t.Fatalf("Scope copy = %q, %v; want the Cache content", data, err)
	}
}

func TestPrepareUpdateRejectsUnknownTargetsBeforeSync(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	prepared, err := PrepareUpdate(config.DefaultConfig(), "", skillsDir, filepath.Join(root, "cache"), UpdateOptions{Targets: []string{"typo"}}, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown update target "typo"`) || prepared != nil {
		t.Fatalf("PrepareUpdate = %#v, %v; want the target rejected", prepared, err)
	}
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Fatal("a rejected request changed the Scope")
	}
}
