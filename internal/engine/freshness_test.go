package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestFreshnessSnapshotDispositionsPreserveIndependentAxes(t *testing.T) {
	tests := []struct {
		name       string
		repository FreshnessRepository
		stateError string
		want       []FreshnessDispositionKind
	}{
		{name: "no_action", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillInSync}}}, want: []FreshnessDispositionKind{FreshnessNone}},
		{name: "unobserved remote", repository: FreshnessRepository{Skills: []SkillFreshness{{Status: SkillInSync}}}, want: []FreshnessDispositionKind{FreshnessNone}},
		{name: "not cached", repository: FreshnessRepository{RemoteStatus: RemoteNotCached, Skills: []SkillFreshness{{Status: SkillUnverified}}}, want: []FreshnessDispositionKind{FreshnessUpdate, FreshnessSync}},
		{name: "remote update and safe sync", repository: FreshnessRepository{RemoteStatus: RemoteUpdateAvailable, Skills: []SkillFreshness{{Status: SkillCacheUpdateAvailable}}}, want: []FreshnessDispositionKind{FreshnessUpdate, FreshnessSync}},
		{name: "unknown baseline", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillUnknownBaseline}}}, want: []FreshnessDispositionKind{FreshnessSync}},
		{name: "unverified cache", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillUnverified}}}, want: []FreshnessDispositionKind{FreshnessSync}},
		{name: "drift remains protected", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillLocalDrift}}}, want: []FreshnessDispositionKind{FreshnessSync, FreshnessProtectDrift}},
		{name: "partial observation error", repository: FreshnessRepository{RemoteStatus: RemoteError, Skills: []SkillFreshness{{Status: SkillInSync}}}, want: []FreshnessDispositionKind{FreshnessUpdate, FreshnessInvestigate}},
		{name: "skill error", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillError}}}, want: []FreshnessDispositionKind{FreshnessSync, FreshnessInvestigate}},
		{name: "state error", repository: FreshnessRepository{RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Status: SkillInSync}}}, stateError: "corrupt", want: []FreshnessDispositionKind{FreshnessInvestigate}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := FreshnessSnapshot{Repositories: []FreshnessRepository{tt.repository}, StateError: tt.stateError}
			got := snapshot.DispositionKinds()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DispositionKinds() = %#v, want %#v", got, tt.want)
			}
			if tt.want[0] == FreshnessNone {
				if reason := snapshot.Dispositions()[0].Reason; reason != "no_action" {
					t.Fatalf("None reason = %q, want no_action", reason)
				}
			}
		})
	}
}

func TestFreshnessSnapshotDispositionsRetainEveryAffectedSkill(t *testing.T) {
	snapshot := FreshnessSnapshot{Repositories: []FreshnessRepository{{
		Source:       "owner/repo",
		RemoteStatus: RemoteUpToDate,
		Skills: []SkillFreshness{
			{Name: "one", Status: SkillMissing},
			{Name: "two", Status: SkillCacheUpdateAvailable},
		},
	}}}

	dispositions := snapshot.Dispositions()
	if len(dispositions) != 2 {
		t.Fatalf("Dispositions() = %#v, want one disposition per affected Skill", dispositions)
	}
	if dispositions[0].Skill != "one" || dispositions[1].Skill != "two" {
		t.Fatalf("Dispositions() Skills = %q, %q", dispositions[0].Skill, dispositions[1].Skill)
	}
	if got := snapshot.DispositionKinds(); !reflect.DeepEqual(got, []FreshnessDispositionKind{FreshnessSync}) {
		t.Fatalf("DispositionKinds() = %#v", got)
	}
}

func TestFreshnessSnapshotDispositionsUseGlobalActionOrder(t *testing.T) {
	snapshot := FreshnessSnapshot{Repositories: []FreshnessRepository{
		{Source: "a/scope-only", RemoteStatus: RemoteUpToDate, Skills: []SkillFreshness{{Name: "sample", Status: SkillMissing}}},
		{Source: "b/remote-update", RemoteStatus: RemoteUpdateAvailable, Skills: []SkillFreshness{{Name: "current", Status: SkillInSync}}},
	}}

	want := []FreshnessDispositionKind{FreshnessUpdate, FreshnessSync}
	if got := snapshot.DispositionKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DispositionKinds() = %#v, want %#v", got, want)
	}
}

func TestInspectFreshnessClassifiesRemoteSkillContent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	skillsDir := filepath.Join(root, "skills")
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	cachePath, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Repositories[0].Skills[0].Status; got != SkillMissing {
		t.Fatalf("status = %q", got)
	}

	if err := MaterializeRemoteSkill("sample", "sample", cachePath, skillsDir); err != nil {
		t.Fatal(err)
	}
	plan, _ = InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if got := plan.Repositories[0].Skills[0].Status; got != SkillInSync {
		t.Fatalf("status = %q", got)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnknownBaseline {
		t.Fatalf("status = %q", got)
	}

	store, _ := NewScopeStateStore(skillsDir)
	applied, _ := DigestSkillContent(filepath.Join(skillsDir, "sample"))
	state, _ := store.Load()
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: cachePath, AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	plan, _ = InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if got := plan.Repositories[0].Skills[0].Status; got != SkillCacheUpdateAvailable {
		t.Fatalf("status = %q", got)
	}
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: filepath.Join(root, "other-cache"), AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	plan, _ = InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnknownBaseline {
		t.Fatalf("changed Cache identity status = %q", got)
	}
	state.Skills["sample"] = AppliedSkillState{Source: "owner/repo", CacheIdentity: cachePath, AppliedCommit: "old", ContentDigests: applied}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("more manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if got := plan.Repositories[0].Skills[0].Status; got != SkillLocalDrift {
		t.Fatalf("status = %q", got)
	}
}

func TestInspectFreshnessReportsUnverifiedWithoutCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", filepath.Join(t.TempDir(), "origin"))
	plan, err := InspectFreshness(cfg, t.TempDir(), t.TempDir(), FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Repositories[0].Skills[0].Status; got != SkillUnverified {
		t.Fatalf("status = %q", got)
	}
}

func TestInspectFreshnessClassifiesSkillsAgainstObservedCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	resolved := filepath.Join(root, "resolved-cache")
	if err := os.MkdirAll(filepath.Join(resolved, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "sample", "SKILL.md"), []byte("# Cached\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := observeRemoteSource
	t.Cleanup(func() { observeRemoteSource = old })
	observeRemoteSource = func(source string, _ config.RemoteRepo, _ string) FreshnessRepository {
		return FreshnessRepository{Source: source, CachePath: resolved, LocalSHA: "abc", RemoteStatus: RemoteUpToDate}
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", filepath.Join(root, "origin"))
	snapshot, err := InspectFreshness(cfg, filepath.Join(root, "skills"), filepath.Join(root, "cache"), FreshnessOptions{ObserveRemote: true, ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Repositories[0].CachePath != resolved {
		t.Fatalf("repository CachePath = %q", snapshot.Repositories[0].CachePath)
	}
	skill := snapshot.Repositories[0].Skills[0]
	if skill.CachePath != filepath.Join(resolved, "sample") {
		t.Fatalf("skill CachePath = %q; want classification against observed Cache", skill.CachePath)
	}
	if skill.Status != SkillMissing {
		t.Fatalf("status = %q", skill.Status)
	}
}

func TestSyncUsesCacheBaselineAndProtectsLocalDrift(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cacheDir, skillsDir, origin := filepath.Join(root, "cache"), filepath.Join(root, "skills"), filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Cached v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunGit(origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md"))
	if string(got) != "# Cached v2\n" {
		t.Fatalf("Scope content = %q", got)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err == nil {
		t.Fatal("local drift should fail normal Sync")
	}
	got, _ = os.ReadFile(filepath.Join(skillsDir, "sample", "SKILL.md"))
	if string(got) != "manual\n" {
		t.Fatalf("local drift was overwritten: %q", got)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{Force: true}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMissingCacheFailsWithoutNetworkAndContinues(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "bad/repo", "bad", "bad", "git", filepath.Join(root, "missing-origin"))
	local := filepath.Join(root, "local")
	mustWriteScopeStateTestFile(t, filepath.Join(local, "SKILL.md"), []byte("# Local\n"))
	config.AddLocalSymlinkEntry(cfg, "local", local, "")
	report, err := applyPlan(t, cfg, filepath.Join(root, "skills"), filepath.Join(root, "cache"), SyncDecision{}, nil)
	if err == nil || report.Failures == 0 {
		t.Fatal("missing Cache should make Sync fail")
	}
	if _, statErr := os.Lstat(filepath.Join(root, "skills", "local")); statErr != nil {
		t.Fatalf("unrelated local Skill did not converge: %v", statErr)
	}
}
