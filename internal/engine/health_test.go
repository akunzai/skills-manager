package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
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
	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil)
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
	outcome, err := doctor.Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Report.Leftover.Paths) != 1 || outcome.Report.Leftover.Paths[0].Agent != "codex" {
		t.Fatalf("Leftover.Paths = %#v; want the live Codex path", outcome.Report.Leftover.Paths)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; working leftover occupancy must count", outcome.Remaining)
	}

	fixed, err := doctor.Run(true, nil)
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

	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil)
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
			outcome, err := doctor.Run(false, nil)
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

			fixed, err := doctor.Run(true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if fixed.Failed != 0 || fixed.Report.Drift[0].Repair.Status != RepairSucceeded {
				t.Fatalf("Drift repair = %#v; want Succeeded", fixed.Report.Drift)
			}
			after, err := doctor.Run(false, nil)
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

// Only a remote Skill has a Baseline. An entry whose name is not declared
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
	store, err := newScopeStateStore(skillsDir)
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
	outcome, err := doctor.Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outcome.Report.StaleState, want) {
		t.Fatalf("Doctor StaleState = %v; want %v", outcome.Report.StaleState, want)
	}
	if _, err := doctor.Run(true, nil); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(state.Skills)); !slices.Equal(got, []string{"remote"}) {
		t.Fatalf("Baselines after --fix = %v; want [remote]", got)
	}
}

// The summary reads Warnings off the outcome. --fix leaves untracked links
// alone today, so this pins that the count is there with and without --fix;
// it cannot tell a pre-fix count from a post-fix one.
func TestDoctorRunCountsUntrackedLinksAsWarnings(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	source := filepath.Join(project, "elsewhere", "untracked")
	mustWriteScopeStateTestFile(t, filepath.Join(source, "SKILL.md"), []byte("# Untracked\n"))
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "untracked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, fix := range []bool{false, true} {
		outcome, err := NewDoctor(config.DefaultConfig(), skillsDir).Run(fix, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []DoctorWarning{{Kind: DoctorFindingUntrackedLink, Count: 1}}
		if !reflect.DeepEqual(outcome.Warnings, want) {
			t.Fatalf("fix=%v: Warnings = %#v; want %#v", fix, outcome.Warnings, want)
		}
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

	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil)
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
	outcome, err := NewDoctor(cfg, skillsDir).Run(true, nil)
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

// newLegacyCacheTestFixture writes a declared remote Source whose Cache is
// still a legacy branchless clone: a `.git` directly at
// <cacheDir>/owner/repo, rather than nested under a branch (ADR-0004).
func newLegacyCacheTestFixture(t *testing.T) (cfg *config.Config, skillsDir, cacheDir, legacyRoot string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	skillsDir = filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheDir = filepath.Join(root, "cache")
	legacyRoot = filepath.Join(cacheDir, "owner", "repo")
	if _, _, err := runGit("", "clone", origin, legacyRoot); err != nil {
		t.Fatal(err)
	}
	// An empty Skills map declares the Source without declaring any Skill
	// from it, so the only finding this fixture produces is the legacy Cache
	// root itself.
	cfg = config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Skills: map[string]string{}}
	return cfg, skillsDir, cacheDir, legacyRoot
}

// A legacy branchless Cache root (#177) is a plain removal under --fix: no
// rebuild, no network. The next `skills update` fetches a branch-aware Cache
// in its place.
func TestDoctorRunFixRemovesLegacyBranchlessCacheRoot(t *testing.T) {
	cfg, skillsDir, cacheDir, legacyRoot := newLegacyCacheTestFixture(t)

	before, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(before.Report.LegacyCacheRoots(), legacyRoot) {
		t.Fatalf("LegacyCacheRoots = %#v; want %q", before.Report.LegacyCacheRoots(), legacyRoot)
	}
	if before.Remaining == 0 {
		t.Fatal("a legacy branchless Cache root must count as an issue")
	}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy Cache root still exists: %v", err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0 after removing the legacy root", outcome.Remaining)
	}
	found := false
	for _, removal := range outcome.Report.CacheRemovals {
		if removal.Path == legacyRoot {
			found = true
			if removal.Repair.Status != RepairSucceeded {
				t.Fatalf("removal.Repair = %#v; want RepairSucceeded", removal.Repair)
			}
		}
	}
	if !found {
		t.Fatalf("CacheRemovals = %#v; want an entry for %q", outcome.Report.CacheRemovals, legacyRoot)
	}
}

// A `.legacy-cache-*` / `.doctor-cache-*` directory an interrupted rebuild
// from skills-manager v0.8–v0.18 left behind is detected and removed the
// same way as a legacy root.
func TestDoctorRunFixRemovesCacheRecoveryArtifacts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cacheDir := t.TempDir()
	legacyArtifact := filepath.Join(cacheDir, ".legacy-cache-abc123")
	doctorArtifact := filepath.Join(cacheDir, ".doctor-cache-def456")
	for _, dir := range []string{legacyArtifact, doctorArtifact} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skillsDir := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()

	before, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(before.Report.CacheRecovery, legacyArtifact) || !slices.Contains(before.Report.CacheRecovery, doctorArtifact) {
		t.Fatalf("CacheRecovery = %#v; want both artifacts", before.Report.CacheRecovery)
	}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{legacyArtifact, doctorArtifact} {
		if _, err := os.Stat(artifact); !os.IsNotExist(err) {
			t.Fatalf("recovery artifact %s still exists: %v", artifact, err)
		}
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0 after removing both artifacts", outcome.Remaining)
	}
}

// A removal that cannot complete is reported like any other failed repair
// (ADR-0002): the finding stays, Failed counts it, and --fix does not claim
// success.
func TestDoctorRunReportsLegacyCacheRemovalFailure(t *testing.T) {
	cfg, skillsDir, cacheDir, legacyRoot := newLegacyCacheTestFixture(t)
	doctor := NewDoctorWithCache(cfg, skillsDir, cacheDir)
	doctor.cacheDetector.removeAll = func(path string) error {
		return fmt.Errorf("injected removal failure")
	}

	outcome, err := doctor.Run(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyRoot); err != nil {
		t.Fatalf("legacy Cache root removed despite the injected failure: %v", err)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want the unrepaired legacy root to remain", outcome.Remaining)
	}
	if outcome.Failed != 1 {
		t.Fatalf("Failed = %d; want 1", outcome.Failed)
	}
	removal, ok := findCacheRemoval(outcome.Report.CacheRemovals, legacyRoot)
	if !ok || removal.Repair.Status != RepairFailed {
		t.Fatalf("CacheRemovals = %#v; want a failed removal for %q", outcome.Report.CacheRemovals, legacyRoot)
	}
}

// A branch-aware Cache (ADR-0004) has no `.git` directly at
// <cache>/<SourceKey> — only nested under its branch — so it is never a
// legacy root and --fix must not touch it.
func TestDoctorRunLeavesNonLegacyCacheUntouched(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	branch, err := remoteDefaultBranch("owner/repo", origin)
	if err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	current, err := NewCache("owner/repo", origin, branch, cacheDir).Refresh(false)
	if err != nil {
		t.Fatal(err)
	}
	wantCommit := localRepoCommit(current)

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Report.LegacyCacheRoots()) != 0 {
		t.Fatalf("LegacyCacheRoots = %#v; want none for a branch-aware Cache", outcome.Report.LegacyCacheRoots())
	}
	if len(outcome.Report.CacheRemovals) != 0 {
		t.Fatalf("CacheRemovals = %#v; want no removal of a non-legacy Cache", outcome.Report.CacheRemovals)
	}
	if got := localRepoCommit(current); got != wantCommit {
		t.Fatalf("branch-aware Cache commit = %q; want %q unchanged", got, wantCommit)
	}
}

// Without --fix, doctor only reports the legacy Cache artifacts it found; it
// never removes them.
func TestDoctorRunWithoutFixOnlyReportsLegacyCacheArtifacts(t *testing.T) {
	cfg, skillsDir, cacheDir, legacyRoot := newLegacyCacheTestFixture(t)
	artifact := filepath.Join(cacheDir, ".legacy-cache-abc123")
	if err := os.MkdirAll(artifact, 0o755); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.AttemptedFix {
		t.Fatal("a plain diagnosis must not attempt a fix")
	}
	if _, err := os.Stat(legacyRoot); err != nil {
		t.Fatalf("legacy Cache root was removed without --fix: %v", err)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("recovery artifact was removed without --fix: %v", err)
	}
	if len(outcome.Report.CacheRemovals) != 0 {
		t.Fatalf("CacheRemovals = %#v; want none without --fix", outcome.Report.CacheRemovals)
	}
	if outcome.Remaining != 2 {
		t.Fatalf("Remaining = %d; want both the legacy root and the recovery artifact counted", outcome.Remaining)
	}
}

func findCacheRemoval(removals []CacheRemoval, path string) (CacheRemoval, bool) {
	for _, removal := range removals {
		if removal.Path == path {
			return removal, true
		}
	}
	return CacheRemoval{}, false
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
	before, err := doctor.Run(false, nil)
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

	after, err := doctor.Run(true, nil)
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

	outcome, err := NewDoctor(config.DefaultConfig(), skillsDir).Run(false, nil)
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

func leftoverEmptyRepairStatus(report DoctorReport, status RepairStatus) int {
	n := 0
	for _, empty := range report.Leftover.Empty {
		if empty.Repair.Status == status {
			n++
		}
	}
	return n
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
	outcome, err := NewDoctorWithCache(cfg, skillsDir, filepath.Join(project, "cache")).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Report.GitError != "" {
		t.Fatalf("a Scope without remote Sources does not need git: %q", outcome.Report.GitError)
	}

	cfg.Remote["owner/repo"] = config.RemoteRepo{Skills: map[string]string{}}
	outcome, err = NewDoctorWithCache(cfg, skillsDir, filepath.Join(project, "cache")).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Report.GitError == "" || outcome.Remaining == 0 {
		t.Fatalf("GitError = %q, Remaining = %d", outcome.Report.GitError, outcome.Remaining)
	}
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
		{DoctorFindingUntracked, false},
		{DoctorFindingUntrackedLink, false},
		{DoctorFindingUnmanagedDirectory, false},
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

// classifiedDoctorReport sets every finding field of a DoctorReport, so each
// kind findings() can emit is present once or more.
func classifiedDoctorReport() DoctorReport {
	return DoctorReport{
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
		ReservedNames:  []ReservedAvailability{{Skill: "synced", Agent: "claude-code"}},
		StateError:     "corrupt",
		StaleState:     []string{"old"},
		GitError:       "git too old",
		CacheRecovery:  []string{"artifact"},
		StaleScopes:    []ScopeStateArtifact{{ScopePath: "gone"}},
		legacyCache:    []string{"legacy"},
	}
}

func TestDoctorReportFindingsClassifyIssues(t *testing.T) {
	report := classifiedDoctorReport()

	got := make(map[DoctorFindingKind]int)
	issues := 0
	for _, kind := range report.findings() {
		got[kind]++
		if kind.CountsAsIssue() {
			issues++
		}
	}
	want := map[DoctorFindingKind]int{
		findingMasterMissing:            1,
		findingAgentUnusable:            1,
		findingAgentUnmanagedBroken:     2,
		DoctorFindingUnmanagedDirectory: 1,
		DoctorFindingLeftoverDangling:   1,
		DoctorFindingLeftoverLive:       1,
		findingLeftoverEmpty:            1,
		findingDriftMissing:             1,
		findingDriftUnexpected:          1,
		findingDriftBroken:              1,
		findingDriftForeign:             1,
		findingDriftUnobservable:        1,
		findingMissingSkill:             1,
		DoctorFindingUntracked:          1,
		DoctorFindingUntrackedLink:      1,
		findingIllegalLocal:             1,
		findingInvalid:                  1,
		findingStub:                     1,
		findingUnknownAgent:             1,
		DoctorFindingReservedName:       1,
		findingStateError:               1,
		findingGitError:                 1,
		findingStaleState:               1,
		findingLegacyCache:              1,
		findingCacheRecovery:            1,
		findingStaleScope:               1,
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
	if issues != 23 {
		t.Errorf("CountsAsIssue total = %d; want 23", issues)
	}
	if report.issueCount() != 23 {
		t.Errorf("issueCount = %d; want 23", report.issueCount())
	}
}

// A real directory on a configured Agent directory that this tool did not
// create and Config does not declare is reported (Agents[].Physical) but
// does not stand in the way of a clean doctor run, by the same reasoning as
// Untracked occupancy on the skills directory (ADR-0002).
func TestDoctorRunReportsUnmanagedAgentDirectoryButNotAsIssue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "unmanaged")
	if err := os.MkdirAll(unmanaged, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, "SKILL.md"), []byte("# Unmanaged\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	outcome, err := NewDoctor(cfg, skillsDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var physical []string
	for _, agent := range outcome.Report.Agents {
		physical = append(physical, agent.Physical...)
	}
	if !slices.Contains(physical, "unmanaged") {
		t.Fatalf("Agents[].Physical = %#v; want the unmanaged directory reported", outcome.Report.Agents)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want 0 (an unmanaged Agent directory must not count as an issue)", outcome.Remaining)
	}
}

// Every exported DoctorReport field is either a finding findings() counts or
// named here as not one. A field added to DoctorReport without either is what
// used to vanish from Remaining unnoticed; this test is where it is caught.
func TestEveryDoctorReportFieldIsClassified(t *testing.T) {
	findingFields := map[string][]DoctorFindingKind{
		"MasterMissing":  {findingMasterMissing},
		"Agents":         {findingAgentUnusable, findingAgentUnmanagedBroken, DoctorFindingUnmanagedDirectory},
		"Leftover":       {DoctorFindingLeftoverDangling, DoctorFindingLeftoverLive, findingLeftoverEmpty},
		"Drift":          {findingDriftMissing, findingDriftUnexpected, findingDriftBroken, findingDriftForeign, findingDriftUnobservable},
		"Missing":        {findingMissingSkill},
		"Untracked":      {DoctorFindingUntracked},
		"UntrackedLinks": {DoctorFindingUntrackedLink},
		"IllegalLocal":   {findingIllegalLocal},
		"Invalid":        {findingInvalid},
		"Stubs":          {findingStub},
		"UnknownAgents":  {findingUnknownAgent},
		"ReservedNames":  {DoctorFindingReservedName},
		"StateError":     {findingStateError},
		"StaleState":     {findingStaleState},
		"CacheRecovery":  {findingCacheRecovery},
		"StaleScopes":    {findingStaleScope},
		"GitError":       {findingGitError},
	}
	nonFindingFields := map[string]string{
		"SkillsDir":     "names the diagnosed Scope",
		"StateRepair":   "what --fix did to StateError or StaleState",
		"CacheRemovals": "what --fix did to legacy Cache entries",
	}

	full := reflect.ValueOf(classifiedDoctorReport())
	fields := reflect.TypeFor[DoctorReport]()
	for i := range fields.NumField() {
		field := fields.Field(i)
		if !field.IsExported() {
			continue
		}
		want, isFinding := findingFields[field.Name]
		if _, ok := nonFindingFields[field.Name]; ok == isFinding {
			t.Errorf("DoctorReport.%s must be in exactly one of findingFields or nonFindingFields here; "+
				"if it is a finding, add its kinds to DoctorReport.findings() and a line to cli.doctorFindings", field.Name)
			continue
		}
		if !isFinding {
			continue
		}
		var only DoctorReport
		reflect.ValueOf(&only).Elem().Field(i).Set(full.Field(i))
		got := make(map[DoctorFindingKind]bool)
		for _, kind := range only.findings() {
			got[kind] = true
		}
		for _, kind := range want {
			if !got[kind] {
				t.Errorf("DoctorReport.%s set alone yields kinds %v; want %s among them", field.Name, got, kind)
			}
		}
	}
}

// Doctor must never offer Claude Code's own synced/ directory for
// replacement: approving every foreign-path prompt would otherwise delete the
// account's claude.ai skills. The pair is reported as a warning instead.
func TestDoctorFixLeavesAgentReservedNameAlone(t *testing.T) {
	_, cfg, skillsDir, claudeFile := reservedNameScope(t)
	var offered []ForeignAvailabilityPath
	approveAll := func(paths []ForeignAvailabilityPath) (bool, error) {
		offered = append(offered, paths...)
		return true, nil
	}

	outcome, err := NewDoctor(cfg, skillsDir).Run(true, approveAll)
	if err != nil {
		t.Fatal(err)
	}

	assertClaudeReservedDirIntact(t, claudeFile)
	if len(offered) != 0 {
		t.Fatalf("doctor offered to replace %#v", offered)
	}
	want := []ReservedAvailability{{Skill: "synced", Agent: "claude-code"}}
	if !reflect.DeepEqual(outcome.Report.ReservedNames, want) {
		t.Fatalf("ReservedNames = %#v; want %#v", outcome.Report.ReservedNames, want)
	}
	if outcome.Remaining != 0 || outcome.Failed != 0 {
		t.Fatalf("Remaining = %d, Failed = %d; a reserved name is a warning, not an issue", outcome.Remaining, outcome.Failed)
	}
}

// Before v0.15.0 a declared Skill named synced could get a managed link on
// Claude Code's reserved name. Availability now never selects that path, so
// the link is leftover occupancy that doctor --fix and prune remove (#178).
func TestLeftoverManagedLinkOnAReservedNameIsCleanedUp(t *testing.T) {
	scope := func(t *testing.T) (*config.Config, string, string) {
		t.Helper()
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		project := t.TempDir()
		skillsDir := filepath.Join(project, ".agents", "skills")
		mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "synced", "SKILL.md"), []byte("# Synced\n"))
		cfg := config.DefaultConfig()
		cfg.Settings.DefaultAgents = []string{"claude"}
		config.AddRemoteSkillEntry(cfg, "owner/repo", "synced", "synced", "github", "main")
		link := plantManagedLink(t, skillsDir, filepath.Join(project, ".claude", "skills"), "synced")
		leftover := NewAvailability(cfg, skillsDir).ObserveOccupancy().Leftover.Paths
		if len(leftover) != 1 || leftover[0].Path != link || leftover[0].Agent != "claude-code" || leftover[0].Skill != "synced" {
			t.Fatalf("Leftover = %#v; want the old managed synced link", leftover)
		}
		return cfg, skillsDir, link
	}
	assertRemoved := func(t *testing.T, skillsDir, link string) {
		t.Helper()
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("the old managed link survived: %v", err)
		}
		if _, err := os.Stat(filepath.Join(skillsDir, "synced", "SKILL.md")); err != nil {
			t.Fatalf("removing the link must not touch the Skill it pointed to: %v", err)
		}
	}

	t.Run("doctor --fix", func(t *testing.T) {
		cfg, skillsDir, link := scope(t)
		if _, err := NewDoctor(cfg, skillsDir).Run(true, nil); err != nil {
			t.Fatal(err)
		}
		assertRemoved(t, skillsDir, link)
	})
	t.Run("prune", func(t *testing.T) {
		cfg, skillsDir, link := scope(t)
		plan, err := BuildPrunePlan(cfg, skillsDir, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Unconfigured) != 1 || plan.Unconfigured[0].Path != link {
			t.Fatalf("prune Unconfigured = %#v; want the old managed synced link", plan.Unconfigured)
		}
		if _, err := ApplyPrunePlan(plan, skillsDir); err != nil {
			t.Fatal(err)
		}
		assertRemoved(t, skillsDir, link)
	})
}

// A warning is exactly a kind that does not count as an issue, and Warnings
// counts every one of them in DoctorWarningKinds order.
func TestDoctorWarningsCountEveryNonIssueKindInOrder(t *testing.T) {
	report := classifiedDoctorReport()
	var want []DoctorWarning
	counts := make(map[DoctorFindingKind]int)
	for _, kind := range report.findings() {
		if !kind.CountsAsIssue() {
			counts[kind]++
		}
	}
	for _, kind := range DoctorWarningKinds() {
		if kind.CountsAsIssue() {
			t.Errorf("warning kind %s counts as an issue", kind)
		}
		if counts[kind] > 0 {
			want = append(want, DoctorWarning{Kind: kind, Count: counts[kind]})
		}
		delete(counts, kind)
	}
	if len(counts) > 0 {
		t.Errorf("non-issue kinds missing from DoctorWarningKinds: %v", counts)
	}
	if got := report.warnings(); !reflect.DeepEqual(got, want) || len(got) != len(DoctorWarningKinds()) {
		t.Fatalf("warnings = %#v; want one per warning kind, %#v", got, want)
	}
}
