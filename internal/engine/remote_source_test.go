package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestPrepareRemoteSourceRefreshesCacheAndDiscoversSkills(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")

	repoDir, discovered, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: origin}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if repoDir == "" || !reflect.DeepEqual(discovered, DiscoveredSkills{"sample": {"sample"}}) {
		t.Fatalf("repoDir = %q, discovered = %#v", repoDir, discovered)
	}
}

func TestPlanSyncReportsUnusableCacheWithoutFetching(t *testing.T) {
	project := t.TempDir()
	cacheDir := filepath.Join(project, "cache")
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: filepath.Join(project, "missing-origin"), Skills: map[string]string{"sample": "sample"}}
	skillsDir := filepath.Join(project, ".agents", "skills")

	plan, err := PlanSync(cfg, skillsDir, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("planning created Cache: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Err == "" {
		t.Fatalf("plan items = %#v", plan.Items)
	}

	var kinds []string
	if _, err := plan.Apply(SyncDecision{}, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) }); err == nil {
		t.Fatal("expected an unusable Cache to be reported as a failure")
	}
	if want := []string{SyncRepoStart, SyncFetchFailed}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

func TestRemoteSourceReconcileContinuesAfterMaterializeFailure(t *testing.T) {
	project := t.TempDir()
	repoDir := filepath.Join(project, "repo")
	good := filepath.Join(repoDir, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("# Good\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	skills := map[string]string{"bad": "missing", "good": "good"}
	remote := newRemoteSource(NewAvailability(cfg, filepath.Join(project, ".agents", "skills")), "owner/repo", config.RemoteRepo{Skills: skills}, "")
	var kinds []string
	if err := remote.reconcile(repoDir, skills, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) }); err != nil {
		t.Fatal(err)
	}
	if want := []string{SyncPathMissing, SyncMaterialized}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "good", "SKILL.md")); err != nil {
		t.Fatal("good Skill was not Materialized after prior failure")
	}
}

func TestRemoteSourceReconcileAvailabilityFailsClosed(t *testing.T) {
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

	cfg := config.DefaultConfig()
	skills := map[string]string{"sample": "sample"}
	remote := newRemoteSource(NewAvailability(cfg, filepath.Join(project, ".agents", "skills")), "owner/repo", config.RemoteRepo{Skills: skills}, "")
	if err := remote.reconcile(repoDir, skills, nil); err == nil {
		t.Fatal("expected unmanaged Availability path to fail closed")
	}
}
