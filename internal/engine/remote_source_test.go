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

	repoDir, discovered, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: origin}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if repoDir == "" || !reflect.DeepEqual(discovered, map[string]string{"sample": "sample"}) {
		t.Fatalf("repoDir = %q, discovered = %#v", repoDir, discovered)
	}
}

func TestRemoteSourceSyncDryRunDoesNotRefreshCache(t *testing.T) {
	project := t.TempDir()
	cacheDir := filepath.Join(project, "cache")
	cfg := config.DefaultConfig()
	repo := config.RemoteRepo{URL: filepath.Join(project, "missing-origin"), Skills: map[string]string{"sample": "sample"}}
	remote := newRemoteSource(NewAvailability(cfg, filepath.Join(project, ".agents", "skills")), "owner/repo", repo, cacheDir)

	var kinds []string
	if err := remote.sync(false, true, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created Cache: %v", err)
	}
	if want := []string{SyncRepoStart, SyncWouldSync, SyncWouldDrift}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

func TestRemoteSourceSyncReportsCacheFailure(t *testing.T) {
	project := t.TempDir()
	cfg := config.DefaultConfig()
	repo := config.RemoteRepo{URL: filepath.Join(project, "missing-origin"), Skills: map[string]string{"sample": "sample"}}
	remote := newRemoteSource(NewAvailability(cfg, filepath.Join(project, ".agents", "skills")), "owner/repo", repo, filepath.Join(project, "cache"))

	var kinds []string
	if err := remote.sync(false, false, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) }); err != nil {
		t.Fatal(err)
	}
	want := []string{SyncRepoStart, SyncRefreshStart, SyncRefreshDone, SyncFetchFailed}
	if !reflect.DeepEqual(kinds, want) {
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
	if err := remote.reconcile(repoDir, false, skills, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) }); err != nil {
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
	if err := remote.reconcile(repoDir, false, skills, nil); err == nil {
		t.Fatal("expected unmanaged Availability path to fail closed")
	}
}
