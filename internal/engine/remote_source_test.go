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
	if len(plan.Items) != 1 || plan.Items[0].Block != SyncBlockCacheMissing {
		t.Fatalf("plan items = %#v", plan.Items)
	}

	var kinds []string
	report, err := plan.Apply(SyncDecision{}, func(ev SyncEvent) { kinds = append(kinds, ev.Kind) })
	if err != nil || report.Blocked != 1 {
		t.Fatalf("a Cache that was never fetched should block: err=%v blocked=%d", err, report.Blocked)
	}
	if want := []string{SyncRepoStart, SyncFetchFailed}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}
