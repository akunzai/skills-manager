package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// treeSnapshot records every path under root with its content or link target,
// so a test can prove that an operation left the Scope untouched.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			snapshot[rel] = "link:" + target
			return linkErr
		case entry.IsDir():
			snapshot[rel] = "dir"
		default:
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(data)
			snapshot[rel] = hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

// planSyncFixture declares one remote Skill whose Cache is ready but whose
// Scope copy is missing, one local symlink Skill, and one command Skill whose
// check would leave a trace if it ran.
func planSyncFixture(t *testing.T) (cfg *config.Config, root, skillsDir, cacheDir, checkMarker string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root = t.TempDir()
	skillsDir = filepath.Join(root, "skills")
	cacheDir = filepath.Join(root, "cache")
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(root, "local")
	mustWriteScopeStateTestFile(t, filepath.Join(local, "SKILL.md"), []byte("# Local\n"))
	checkMarker = filepath.Join(root, "check-ran")

	cfg = config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	config.AddLocalSymlinkEntry(cfg, "local", local, "")
	config.AddLocalCommandEntry(cfg, "tool", "true", "touch "+checkMarker, "")
	return cfg, root, skillsDir, cacheDir, checkMarker
}

func TestPlanSyncWritesNothing(t *testing.T) {
	cfg, root, skillsDir, cacheDir, checkMarker := planSyncFixture(t)
	before := treeSnapshot(t, root)

	plan, err := PlanSync(cfg, "", skillsDir, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("plan items = %#v", plan.Items)
	}
	if after := treeSnapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("planning changed the Scope:\nbefore %#v\nafter  %#v", before, after)
	}
	if _, err := os.Stat(checkMarker); !os.IsNotExist(err) {
		t.Fatalf("planning ran a Skill-supplied check command: %v", err)
	}
	if plan.Fresh(SyncDecision{}) {
		t.Fatal("a Scope with a missing Skill should not read as fresh")
	}
}

func TestSyncPlanResolvesEachDecision(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir, cacheDir, origin := filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "drifted")
	writeLocalGitSkill(t, origin, "unknown")
	writeLocalGitSkill(t, origin, "missing")
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "drifted", "unknown", "missing"); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	for _, name := range []string{"drifted", "unknown", "missing"} {
		config.AddRemoteSkillEntry(cfg, "owner/repo", name, name, "git", origin)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	// One Skill edited in place, one edited with its baseline forgotten, one
	// removed.
	if err := os.WriteFile(filepath.Join(skillsDir, "drifted", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "unknown", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := newScopeStateStore(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	delete(state.Skills, "unknown")
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(skillsDir, "missing")); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanSync(cfg, "", skillsDir, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		decision SyncDecision
		want     map[string]SyncAction
	}{
		{SyncDecision{}, map[string]SyncAction{"drifted": SyncActionSkip, "unknown": SyncActionSkip, "missing": SyncActionMaterialize}},
		{SyncDecision{AllowUnknown: true}, map[string]SyncAction{"drifted": SyncActionSkip, "unknown": SyncActionMaterialize, "missing": SyncActionMaterialize}},
		{SyncDecision{Force: true}, map[string]SyncAction{"drifted": SyncActionMaterialize, "unknown": SyncActionMaterialize, "missing": SyncActionMaterialize}},
	} {
		got := make(map[string]SyncAction, len(plan.Items))
		for _, item := range plan.Items {
			action, _ := item.Resolve(tc.decision)
			got[item.Name] = action
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("decision %+v resolved to %#v, want %#v", tc.decision, got, tc.want)
		}
	}
	if unknown := plan.Unknown(); len(unknown) != 1 || unknown[0].Name != "unknown" {
		t.Fatalf("plan.Unknown() = %#v", unknown)
	}
}

func TestSyncApplyNamesTheSkillWhoseAvailabilityFailed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".agents", "skills")
	local := filepath.Join(root, "local")
	mustWriteScopeStateTestFile(t, filepath.Join(local, "SKILL.md"), []byte("# Local\n"))
	// An unrelated tool left its own link where the Agent's Availability link
	// belongs, so applying Availability has to fail closed.
	unmanaged := filepath.Join(root, ".claude", "skills", "local")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(local, unmanaged); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "local", local, "")

	var failures []SyncEvent
	report, err := applyPlan(t, cfg, skillsDir, t.TempDir(), SyncDecision{}, func(ev SyncEvent) {
		if ev.Kind == SyncAvailabilityFailed {
			failures = append(failures, ev)
		}
	})
	if err != nil || report.Failed != 1 || report.Blocked != 0 {
		t.Fatalf("expected one failure, got err=%v failed=%d blocked=%d", err, report.Failed, report.Blocked)
	}
	if len(failures) != 1 || failures[0].Skill != "local" || failures[0].Err == "" {
		t.Fatalf("failure must name the Skill and say why: %#v", failures)
	}
}

func TestSyncApplyContinuesAfterMaterializeFailure(t *testing.T) {
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
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "bad", "missing", "git", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "good", "good", "git", "")
	plan := &SyncPlan{
		Sources: []string{"owner/repo"},
		Items: []SyncPlanItem{
			{
				Name: "bad", Kind: SyncItemRemote, Source: "owner/repo",
				CachePath: repoDir, NeedsWrite: true,
				Freshness: SkillFreshness{Name: "bad", Source: "owner/repo", Subpath: "missing", ScopePath: filepath.Join(skillsDir, "bad")},
			},
			{
				Name: "good", Kind: SyncItemRemote, Source: "owner/repo",
				CachePath: repoDir, NeedsWrite: true,
				Freshness: SkillFreshness{Name: "good", Source: "owner/repo", Subpath: "good", ScopePath: filepath.Join(skillsDir, "good")},
			},
		},
		cfg:          cfg,
		skillsDir:    skillsDir,
		availability: NewAvailability(cfg, skillsDir),
	}
	var kinds []string
	report, err := plan.Apply(SyncDecision{}, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed=%d events=%#v", report.Failed, kinds)
	}
	if !slices.Contains(kinds, SyncPathMissing) || !slices.Contains(kinds, SyncMaterialized) {
		t.Fatalf("event kinds = %#v", kinds)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "good", "SKILL.md")); err != nil {
		t.Fatal("good Skill was not Materialized after prior failure")
	}
}

// A Scope state that became unreadable after the plan read it is reported
// once, like one the plan could not read: the Skill is still applied, its
// Baseline is not recorded, and the state is left as it is.
func TestSyncApplyReportsScopeStateUnreadableSincePlanning(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	repoDir := filepath.Join(project, "repo")
	mustWriteScopeStateTestFile(t, filepath.Join(repoDir, "good", "SKILL.md"), []byte("# Good\n"))
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "good", "good", "git", "")
	plan := &SyncPlan{
		Sources: []string{"owner/repo"},
		Items: []SyncPlanItem{{
			Name: "good", Kind: SyncItemRemote, Source: "owner/repo",
			CachePath: repoDir, NeedsWrite: true,
			Freshness: SkillFreshness{Name: "good", Source: "owner/repo", Subpath: "good", ScopePath: filepath.Join(skillsDir, "good")},
		}},
		cfg:          cfg,
		skillsDir:    skillsDir,
		availability: NewAvailability(cfg, skillsDir),
	}
	statePath, bad := writeUnreadableScopeState(t, skillsDir)

	var kinds []string
	report, err := plan.Apply(SyncDecision{}, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || !slices.Contains(kinds, SyncStateFailed) {
		t.Fatalf("failed=%d events=%#v; want one Scope state failure", report.Failed, kinds)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "good", "SKILL.md")); err != nil {
		t.Fatalf("the Skill must still be Materialized: %v", err)
	}
	if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
		t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
	}
}

func TestPlanSyncTreatsInstalledCommandSkillAsConverged(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	cfg := config.DefaultConfig()
	// Codex is Automatically available, so no Availability link is managed and
	// the command Skill is the only thing the plan can call work.
	cfg.Settings.DefaultAgents = []string{"codex"}
	config.AddLocalCommandEntry(cfg, "tool", "install-tool", "which tool", "")

	plan, err := PlanSync(cfg, "", skillsDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pending(SyncDecision{})) != 1 {
		t.Fatal("a command Skill missing from the Scope is work to do")
	}

	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "tool", "SKILL.md"), []byte("# Tool\n"))
	plan, err = PlanSync(cfg, "", skillsDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fresh(SyncDecision{}) {
		t.Fatalf("an installed command Skill must not keep the gate red: %#v", plan.Pending(SyncDecision{}))
	}
}

func TestPlanRemoteItem(t *testing.T) {
	drift := AvailabilityDrift{Skill: "sample", Missing: []string{"codex"}}
	skill := SkillFreshness{Name: "sample", Source: "owner/repo", Subpath: "sample", ScopePath: "/scope/sample"}
	for _, tc := range []struct {
		name      string
		status    SkillFreshnessStatus
		err       string
		wantWrite bool
		wantBlock SyncBlock
		wantErr   bool
	}{
		{name: "in sync", status: SkillInSync},
		{name: "missing", status: SkillMissing, wantWrite: true},
		{name: "cache update", status: SkillCacheUpdateAvailable, wantWrite: true},
		{name: "unknown baseline", status: SkillUnknownBaseline, wantWrite: true, wantBlock: SyncBlockUnknownBaseline},
		{name: "local drift", status: SkillLocalDrift, wantBlock: SyncBlockLocalDrift},
		{name: "unverified", status: SkillUnverified, wantBlock: SyncBlockCacheMissing},
		{name: "error", status: SkillError, err: "boom", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			freshness := skill
			freshness.Status = tc.status
			freshness.Error = tc.err
			item := planRemoteItem("owner/repo", "/cache/repo", "abc123", freshness, drift)
			if item.Kind != SyncItemRemote || item.Name != "sample" || item.Source != "owner/repo" {
				t.Fatalf("identity = %+v", item)
			}
			if item.CachePath != "/cache/repo" || item.LocalSHA != "abc123" {
				t.Fatalf("CachePath=%q LocalSHA=%q", item.CachePath, item.LocalSHA)
			}
			if !reflect.DeepEqual(item.Freshness, freshness) {
				t.Fatalf("Freshness = %+v; want %+v", item.Freshness, freshness)
			}
			if !reflect.DeepEqual(item.Drift, drift) {
				t.Fatalf("Drift = %+v; want %+v", item.Drift, drift)
			}
			if item.NeedsWrite != tc.wantWrite {
				t.Fatalf("NeedsWrite = %v; want %v", item.NeedsWrite, tc.wantWrite)
			}
			if item.Block != tc.wantBlock {
				t.Fatalf("Block = %q; want %q", item.Block, tc.wantBlock)
			}
			if tc.wantErr {
				if item.Err == "" {
					t.Fatal("Err is empty; want the classified error")
				}
			} else if item.Err != "" {
				t.Fatalf("Err = %q; want empty", item.Err)
			}
		})
	}
}

// A just-declared Skill, from Add or the new name of a Rename, is written
// whatever the Scope holds: Add has already asked before overwriting, and a
// Rename has already protected the old copy.
func TestPlanDeclaredRemoteItemIsAlwaysWritten(t *testing.T) {
	drift := AvailabilityDrift{Skill: "sample", Missing: []string{"codex"}}
	skill := SkillFreshness{Name: "sample", Source: "owner/repo", Subpath: "sample", ScopePath: "/scope/sample"}

	item := planDeclaredRemoteItem("owner/repo", "/cache/repo", "abc123", skill, drift)

	want := SyncPlanItem{
		Name: "sample", Kind: SyncItemRemote, Source: "owner/repo",
		Drift: drift, Freshness: skill, CachePath: "/cache/repo", LocalSHA: "abc123",
		NeedsWrite: true,
	}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("item = %+v; want %+v", item, want)
	}
	if action, block := item.Resolve(SyncDecision{}); action != SyncActionMaterialize || block != SyncBlockNone {
		t.Fatalf("Resolve = %q, %q; want materialize", action, block)
	}
}
