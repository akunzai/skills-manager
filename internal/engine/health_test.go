package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// localFileURL renders a local path as a file:// URL git accepts. A Windows
// absolute path opens with a drive letter rather than a separator, so it needs
// forward slashes and one leading slash of its own.
func localFileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

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
	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(outcome.Report.Untracked, "orphan") {
		t.Fatalf("Untracked = %#v; want orphan", outcome.Report.Untracked)
	}
	if len(outcome.Report.Leftover.Empty) == 0 {
		t.Fatal("diagnosis did not report the leftover empty agent directory")
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want 1 (untracked must not count)", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(project, ".continue", "skills")); err != nil {
		t.Fatalf("diagnose-only run mutated the filesystem: %v", err)
	}
}

func TestDoctorRunCountsWorkingLeftoverOccupancy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	source := filepath.Join(project, "src", "sample")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalSymlinkEntry(cfg, "sample", source, "")
	if _, err := NewAvailability(cfg, skillsDir).Apply("sample"); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(project, ".codex", "skills")
	if err := os.MkdirAll(codex, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "sample"), filepath.Join(codex, "sample")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	doctor := NewDoctor(cfg, skillsDir)
	outcome, err := doctor.Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Report.Leftover.Paths) != 1 || outcome.Report.Leftover.Paths[0].Agent != "codex" {
		t.Fatalf("Leftover.Paths = %#v; want the live Codex path", outcome.Report.Leftover.Paths)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; working leftover occupancy must count", outcome.Remaining)
	}

	fixed, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Remaining != 0 {
		t.Fatalf("Remaining after --fix = %d; want 0", fixed.Remaining)
	}
	if len(fixed.Report.Leftover.Paths) != 1 || fixed.Report.Leftover.Paths[0].Repair.Status != RepairSucceeded {
		t.Fatalf("leftover path repair = %#v; want Succeeded on the Codex path", fixed.Report.Leftover.Paths)
	}
	if _, err := os.Lstat(filepath.Join(codex, "sample")); !os.IsNotExist(err) {
		t.Fatal("working leftover path should have been removed")
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); err != nil {
		t.Fatal("declared Availability must remain")
	}
}

// A real directory where a declared Skill's Availability should be is one
// finding, not two: it is Foreign, which --fix can replace with consent, and
// not also a stray Physical directory on the same Agent directory.
func TestDoctorRunCountsForeignDirectoryOnDeclaredPathOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(project, ".claude", "skills", "sample")
	if err := os.MkdirAll(foreign, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", ".", "github", "")

	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if paths := outcome.Report.foreignAvailabilityPaths(); len(paths) != 1 || paths[0].Path != foreign {
		t.Fatalf("Foreign = %#v; want the directory on sample's Claude path", paths)
	}
	for _, agent := range outcome.Report.Agents {
		if len(agent.Physical) > 0 {
			t.Fatalf("%s Physical = %#v; the Foreign directory must not count twice", agent.Name, agent.Physical)
		}
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want 1", outcome.Remaining)
	}
}

// A declared Skill that is not present on the skills directory — its master
// missing, or a local Source illegally inside it — can still leave a managed
// link on an Agent its Availability does not select. That link is Drift prune
// removes, so Doctor reports it too, and --fix removes it without linking the
// Skill anywhere new.
func TestDoctorRunReportsUnexpectedLinkOfAbsentSkill(t *testing.T) {
	tests := []struct {
		name    string
		declare func(t *testing.T, cfg *config.Config, skillsDir string)
	}{
		{
			name: "missing master",
			declare: func(t *testing.T, cfg *config.Config, skillsDir string) {
				config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", ".", "github", "")
			},
		},
		{
			name: "illegal local Source",
			declare: func(t *testing.T, cfg *config.Config, skillsDir string) {
				master := filepath.Join(skillsDir, "sample")
				if err := os.MkdirAll(master, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(master, "SKILL.md"), []byte("# Sample\n"), 0644); err != nil {
					t.Fatal(err)
				}
				config.AddLocalSymlinkEntry(cfg, "sample", master, "")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			project := t.TempDir()
			skillsDir := filepath.Join(project, ".agents", "skills")
			if err := os.MkdirAll(skillsDir, 0755); err != nil {
				t.Fatal(err)
			}
			cfg := config.DefaultConfig()
			cfg.Settings.DefaultAgents = []string{"claude"}
			tt.declare(t, cfg, skillsDir)
			link := plantManagedLink(t, skillsDir, filepath.Join(project, ".continue", "skills"), "sample")

			doctor := NewDoctor(cfg, skillsDir)
			outcome, err := doctor.Run(false, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(outcome.Report.Drift) != 1 || outcome.Report.Drift[0].Skill != "sample" || !slices.Equal(outcome.Report.Drift[0].Unexpected, []string{"continue"}) {
				t.Fatalf("Drift = %#v; want sample's unexpected Continue link", outcome.Report.Drift)
			}
			if outcome.Remaining != 2 {
				t.Fatalf("Remaining = %d; want 2 (the absent Skill and its unexpected link)", outcome.Remaining)
			}

			plan, err := BuildPrunePlan(cfg, skillsDir, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Unconfigured) != 1 || plan.Unconfigured[0].Path != link {
				t.Fatalf("prune Unconfigured = %#v; want the same link Doctor reports", plan.Unconfigured)
			}

			fixed, err := doctor.Run(true, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if fixed.Failed != 0 || fixed.Report.Drift[0].Repair.Status != RepairSucceeded {
				t.Fatalf("Drift repair = %#v; want Succeeded", fixed.Report.Drift)
			}
			after, err := doctor.Run(false, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Report.Drift) != 0 {
				t.Fatalf("Drift after --fix = %#v; want none", after.Report.Drift)
			}
			if _, err := os.Lstat(link); !os.IsNotExist(err) {
				t.Fatal("--fix must remove the unexpected link")
			}
			if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "sample")); !os.IsNotExist(err) {
				t.Fatal("--fix must not link a Skill that is not present")
			}
		})
	}
}

// Only a remote Skill has a baseline. An entry whose name is not declared
// remote — undeclared, or now declared local — is stale for Doctor's
// diagnosis, its --fix and prune alike.
func TestStaleBaselinesAreEntriesNotDeclaredRemote(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "remote", "remote", "github", "")
	config.AddLocalSymlinkEntry(cfg, "local", filepath.Join(project, "src", "local"), "")
	store, err := NewScopeStateStore(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ScopeState{Skills: map[string]AppliedSkillState{
		"remote": {Source: "owner/repo"}, "local": {Source: "owner/repo"}, "gone": {Source: "owner/repo"},
	}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"gone", "local"}

	plan, err := BuildPrunePlan(cfg, skillsDir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.StateSkills, want) {
		t.Fatalf("prune StateSkills = %v; want %v", plan.StateSkills, want)
	}
	doctor := NewDoctor(cfg, skillsDir)
	outcome, err := doctor.Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outcome.Report.StaleState, want) {
		t.Fatalf("Doctor StaleState = %v; want %v", outcome.Report.StaleState, want)
	}
	if _, err := doctor.Run(true, nil, nil); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(state.Skills)); !slices.Equal(got, []string{"remote"}) {
		t.Fatalf("baselines after --fix = %v; want [remote]", got)
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
	source := filepath.Join(project, "src", "broken")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing", ".", "github", "")
	config.AddLocalSymlinkEntry(cfg, "broken", source, "")

	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil, nil)
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
	outcome, err := NewDoctor(cfg, skillsDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.AttemptedFix || leftoverEmptyRepairStatus(outcome.Report, RepairSucceeded) == 0 {
		t.Fatalf("Report.Leftover.Empty = %#v; want a removed leftover directory", outcome.Report.Leftover.Empty)
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
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(legacy, ".git")); !os.IsNotExist(err) {
		t.Fatalf("legacy Cache still exists: %v", err)
	}
	branch, err := remoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	current := resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir
	if got := localRepoCommit(current); got == "" {
		t.Fatal("doctor --fix removed the legacy Cache without rebuilding a branch-aware Cache")
	}
}

func TestDoctorRunRecordsDefaultBranchForInferredSourceURL(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	setGitConfig(t, "url."+localFileURL(origin)+".insteadOf", "https://github.com/owner/repo.git")

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{Skills: map[string]string{}}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if got := localRepoCommit(resolveCacheRepo("owner/repo", "", "", cacheDir).Dir); got == "" {
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
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	defaultBranch, err := remoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewCache("owner/repo", origin, defaultBranch, cacheDir).Refresh(false)
	if err != nil {
		t.Fatal(err)
	}
	wantCommit := localRepoCommit(current)

	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{
		URL:    filepath.Join(root, "unavailable"),
		Branch: defaultBranch,
		Skills: map[string]string{"sample": "sample"},
	}
	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0", outcome.Remaining)
	}
	if got := localRepoCommit(resolveCacheRepo("owner/repo", cfg.Remote["owner/repo"].URL, defaultBranch, cacheDir).Dir); got != wantCommit {
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
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{
		URL:    filepath.Join(root, "unavailable"),
		Branch: "main",
		Skills: map[string]string{"sample": "sample"},
	}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want failed legacy Cache repair to remain", outcome.Remaining)
	}
	if got := localRepoCommit(legacy); got == "" {
		t.Fatal("doctor --fix removed the legacy Cache after its replacement failed")
	}
	if !hasCacheMigration(outcome.Report.CacheMigrations, CacheMigrationFailed) {
		t.Fatalf("CacheMigrations = %#v; want a failed Cache migration", outcome.Report.CacheMigrations)
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
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
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

	outcome, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want recovery artifact to remain an issue", outcome.Remaining)
	}
	branch, branchErr := remoteDefaultBranch("owner/repo", origin)
	if branchErr != nil {
		t.Fatal(branchErr)
	}
	if localRepoCommit(resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir) == "" {
		t.Fatal("installed branch-aware Cache must stay in place")
	}
	if !hasCacheMigration(outcome.Report.CacheMigrations, CacheMigrationRecoveryNeeded) {
		t.Fatalf("CacheMigrations = %#v; want a Cache migration needing manual recovery", outcome.Report.CacheMigrations)
	}
}

func TestLegacyCacheMigrationRejectsPlanWhenLegacyCacheChanged(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
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
	wantCommit := localRepoCommit(legacy)

	results := migrator.apply(plans, nil)
	if len(results) != 1 || results[0].Status != legacyCacheFailed {
		t.Fatalf("results = %#v; want one failed stale migration", results)
	}
	if got := localRepoCommit(legacy); got != wantCommit {
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

	outcome, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want restored legacy Cache to remain", outcome.Remaining)
	}
	if localRepoCommit(legacy) == "" {
		t.Fatal("legacy Cache was not restored after replacement rename failed")
	}
	if !hasCacheMigration(outcome.Report.CacheMigrations, CacheMigrationFailed) {
		t.Fatalf("CacheMigrations = %#v; want a failed Cache migration", outcome.Report.CacheMigrations)
	}
	if hasCacheMigration(outcome.Report.CacheMigrations, CacheMigrationRecoveryNeeded) {
		t.Fatalf("successful rollback must not require manual recovery: %#v", outcome.Report.CacheMigrations)
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

	outcome, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 2 {
		t.Fatalf("Remaining = %d; want backup and staging recovery artifacts", outcome.Remaining)
	}
	artifacts := strings.Join(cacheMigrationArtifacts(outcome.Report.CacheMigrations, CacheMigrationRecoveryNeeded), " ")
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
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
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
	defaultBranch, err := remoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGit(origin, "checkout", "-b", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGit(origin, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGit(origin, "commit", "-m", "dev"); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache")
	legacy := filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := runGit("", "clone", origin, legacy); err != nil {
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
	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, func(event DoctorEvent) {
		rebuilt = append(rebuilt, event.Source)
	}, nil)
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
		if got := localRepoCommit(resolveCacheRepo("owner/repo", origin, branch, cacheDir).Dir); got == "" {
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
	before, err := doctor.Run(false, nil, nil)
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

	after, err := doctor.Run(true, nil, nil)
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

	outcome, err := NewDoctor(config.DefaultConfig(), skillsDir).Run(false, nil, nil)
	if err == nil {
		t.Fatal("expected inventory error")
	}
	if outcome.Report.SkillsDir != "" || outcome.AttemptedFix {
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

func TestAttachLeftoverRepairsRecordsSkip(t *testing.T) {
	path := LeftoverPath{Agent: "codex", Skill: "sample", Path: "/tmp/sample"}
	got := attachLeftoverRepairs(
		LeftoverOccupancy{Paths: []LeftoverPath{path}},
		LeftoverApplyResult{SkippedPaths: []LeftoverPath{path}},
	)
	if got.Paths[0].Repair.Status != RepairSkipped {
		t.Fatalf("repair = %#v; want Skipped", got.Paths[0].Repair)
	}
}

func leftoverEmptyRepairStatus(report DoctorReport, status RepairStatus) int {
	n := 0
	for _, empty := range report.Leftover.Empty {
		if empty.Repair.Status == status {
			n++
		}
	}
	return n
}

func hasCacheMigration(migrations []CacheMigrationOutcome, status CacheMigrationStatus) bool {
	for _, migration := range migrations {
		if migration.Status == status {
			return true
		}
	}
	return false
}

func cacheMigrationArtifacts(migrations []CacheMigrationOutcome, status CacheMigrationStatus) []string {
	var artifacts []string
	for _, migration := range migrations {
		if migration.Status == status {
			artifacts = append(artifacts, migration.Artifacts...)
		}
	}
	return artifacts
}

func TestDoctorReportsGitThatCannotMaintainTheCache(t *testing.T) {
	previous := gitVersionErr
	gitVersionErr = func() error { return fmt.Errorf("git 2.35 or newer is required") }
	t.Cleanup(func() { gitVersionErr = previous })
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	outcome, err := NewDoctorWithCache(cfg, skillsDir, filepath.Join(project, "cache")).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Report.GitError != "" {
		t.Fatalf("a Scope without remote Sources does not need git: %q", outcome.Report.GitError)
	}

	cfg.Remote["owner/repo"] = config.RemoteRepo{Skills: map[string]string{}}
	outcome, err = NewDoctorWithCache(cfg, skillsDir, filepath.Join(project, "cache")).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Report.GitError == "" || outcome.Remaining == 0 {
		t.Fatalf("GitError = %q, Remaining = %d", outcome.Report.GitError, outcome.Remaining)
	}
}

// A local clone of a partial clone fails on the blobs it never fetched, so the
// migration copies a sparse Cache without the network (ADR 0004).
func TestDoctorRunKeepsSparsePartialCacheWithoutRemoteAccess(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	origin, url := writeSparseOrigin(t)
	branch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	mustGit(t, "", "clone", "--no-checkout", url, filepath.Join(cacheDir, "owner", "repo"))
	current, err := NewCache("owner/repo", url, branch, cacheDir).Refresh(false, "skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	wantCommit := localRepoCommit(current)

	cfg := config.DefaultConfig()
	unavailable := localFileURL(filepath.Join(root, "unavailable"))
	mustGit(t, current, "remote", "set-url", "origin", unavailable)
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: unavailable, Branch: branch, Skills: map[string]string{"alpha": "skills/alpha"}}
	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range outcome.Report.CacheMigrations {
		if migration.Err != nil {
			t.Fatalf("migration of %s failed: %v", migration.Root, migration.Err)
		}
	}
	migrated := resolveCacheRepo("owner/repo", unavailable, branch, cacheDir).Dir
	if got := localRepoCommit(migrated); got != wantCommit {
		t.Fatalf("preserved Cache commit = %q; want %q", got, wantCommit)
	}
	assertCachePaths(t, migrated, []string{"skills/alpha/notes.txt"}, []string{"skills/beta", "fixtures"})
}

func TestLeftoverPathFindingKindSplitsDanglingFromLive(t *testing.T) {
	if got := (LeftoverPath{Dangling: true}).FindingKind(); got != DoctorFindingLeftoverDangling {
		t.Fatalf("dangling leftover kind = %q; want %q", got, DoctorFindingLeftoverDangling)
	}
	if got := (LeftoverPath{}).FindingKind(); got != DoctorFindingLeftoverLive {
		t.Fatalf("live leftover kind = %q; want %q", got, DoctorFindingLeftoverLive)
	}
}

func TestDoctorFindingKindCountsAsIssue(t *testing.T) {
	tests := []struct {
		kind DoctorFindingKind
		want bool
	}{
		{findingUntracked, false},
		{findingUntrackedLink, false},
		{findingUnknownAgent, true},
		{DoctorFindingLeftoverDangling, true},
		{DoctorFindingLeftoverLive, true},
	}
	for _, tt := range tests {
		if got := tt.kind.CountsAsIssue(); got != tt.want {
			t.Errorf("%q.CountsAsIssue() = %v; want %v", tt.kind, got, tt.want)
		}
	}
}

func TestDoctorReportFindingsClassifyIssues(t *testing.T) {
	report := DoctorReport{
		MasterMissing: true,
		Agents: []AgentHealth{
			{Unusable: "not a directory"},
			{UnmanagedBroken: []string{"a", "b"}, Physical: []string{"c"}},
		},
		Leftover: LeftoverOccupancy{
			Paths: []LeftoverPath{
				{Agent: "codex", Skill: "gone", Dangling: true},
				{Agent: "codex", Skill: "sample"},
			},
			Empty: []AgentDir{{Name: "continue"}},
		},
		Drift: []SkillDrift{{
			Missing:      []string{"claude-code"},
			Unexpected:   []string{"codex"},
			Broken:       []string{"gemini"},
			Copies:       []string{"copied-must-not-count"},
			Foreign:      []ForeignAvailabilityPath{{Agent: "claude-code"}},
			Unobservable: []UnobservableAvailabilityPath{{Agent: "claude-code"}},
		}},
		Missing:        []string{"missing"},
		Untracked:      []string{"orphan"},
		UntrackedLinks: []string{"orphan-link"},
		IllegalLocal:   []IllegalLocalSource{{Name: "loop"}},
		Invalid:        []InvalidSkill{{Name: "broken"}},
		Stubs:          []string{"stub"},
		UnknownAgents:  []UnknownAgentReference{{Agent: "nope"}},
		StateError:     "corrupt",
		StaleState:     []string{"old"},
		GitError:       "git too old",
		CacheRecovery:  []string{"artifact"},
		StaleScopes:    []ScopeStateArtifact{{ScopePath: "gone"}},
		legacyCache:    []legacyCacheMigrationPlan{{Root: "legacy"}},
	}

	got := make(map[DoctorFindingKind]int)
	issues := 0
	for _, kind := range report.findings() {
		got[kind]++
		if kind.CountsAsIssue() {
			issues++
		}
	}
	want := map[DoctorFindingKind]int{
		findingMasterMissing:          1,
		findingAgentUnusable:          1,
		findingAgentUnmanagedBroken:   2,
		findingAgentPhysical:          1,
		DoctorFindingLeftoverDangling: 1,
		DoctorFindingLeftoverLive:     1,
		findingLeftoverEmpty:          1,
		findingDriftMissing:           1,
		findingDriftUnexpected:        1,
		findingDriftBroken:            1,
		findingDriftForeign:           1,
		findingDriftUnobservable:      1,
		findingMissingSkill:           1,
		findingUntracked:              1,
		findingUntrackedLink:          1,
		findingIllegalLocal:           1,
		findingInvalid:                1,
		findingStub:                   1,
		findingUnknownAgent:           1,
		findingStateError:             1,
		findingGitError:               1,
		findingStaleState:             1,
		findingLegacyCache:            1,
		findingCacheRecovery:          1,
		findingStaleScope:             1,
	}
	for kind, n := range want {
		if got[kind] != n {
			t.Errorf("kind %q count = %d; want %d", kind, got[kind], n)
		}
		delete(got, kind)
	}
	if len(got) != 0 {
		t.Errorf("unexpected finding kinds: %v", got)
	}
	if issues != 24 {
		t.Errorf("CountsAsIssue total = %d; want 24", issues)
	}
	if report.issueCount() != 24 {
		t.Errorf("issueCount = %d; want 24", report.issueCount())
	}
}
