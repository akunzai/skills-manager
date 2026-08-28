package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
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

	plan, err := PlanSync(cfg, skillsDir, cacheDir)
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
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
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
	store, err := NewScopeStateStore(skillsDir)
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

	plan, err := PlanSync(cfg, skillsDir, cacheDir)
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
	if err == nil || report.Failures != 1 {
		t.Fatalf("expected one failure, got err=%v failures=%d", err, report.Failures)
	}
	if len(failures) != 1 || failures[0].Skill != "local" || failures[0].Err == "" {
		t.Fatalf("failure must name the Skill and say why: %#v", failures)
	}
}
