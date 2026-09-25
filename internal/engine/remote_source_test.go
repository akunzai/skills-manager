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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	cacheDir := filepath.Join(project, "cache")
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: filepath.Join(project, "missing-origin"), Skills: map[string]string{"sample": "sample"}}
	skillsDir := filepath.Join(project, ".agents", "skills")

	plan, err := PlanSync(cfg, "", skillsDir, cacheDir)
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
	if want := []string{SyncRepoStart, SyncItemStart, SyncFetchFailed, SyncItemDone}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, want)
	}
}

// Add discovers every Skill from its SKILL.md alone, compares duplicate
// candidates by committed tree, and leaves the Cache's cone as it was.
func TestPrepareRemoteSourceDiscoversFromSkillMDOnly(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	repoDir, discovered, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: url}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := DiscoveredSkills{"alpha": {"skills/alpha"}, "beta": {"skills/beta"}}
	if !reflect.DeepEqual(discovered, want) {
		t.Fatalf("discovered = %#v; want %#v", discovered, want)
	}
	assertCachePaths(t, repoDir, []string{"README.md"}, []string{"skills", "mirror", "fixtures"})
}

func TestPrepareRemoteSourceKeepsDivergentMirrorsApart(t *testing.T) {
	t.Parallel()
	origin, url := writeSparseOrigin(t)
	if err := os.WriteFile(filepath.Join(origin, "mirror", "alpha", "notes.txt"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "commit", "-am", "diverge")
	_, discovered, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: url}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := discovered["alpha"]; !reflect.DeepEqual(got, []string{"mirror/alpha", "skills/alpha"}) {
		t.Fatalf("alpha candidates = %#v", got)
	}
}

func TestPrepareRemoteSourceScopedToDirectoryWithoutSkills(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	_, discovered, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: url}, t.TempDir(), "fixtures")
	if err != nil {
		t.Fatalf("a committed directory without Skills is not an error: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("discovered = %#v", discovered)
	}
	if _, _, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: url}, t.TempDir(), "absent"); err == nil {
		t.Fatal("a directory the commit does not have must still fail")
	}
}
