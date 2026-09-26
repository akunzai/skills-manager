package engine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestClassifyAddKind(t *testing.T) {
	t.Run("empty spec", func(t *testing.T) {
		_, _, err := ClassifyAddKind(AddSourceSpec{})
		if err == nil {
			t.Fatal("empty spec succeeded")
		}
	})

	t.Run("symlink flag wins over command", func(t *testing.T) {
		kind, source, err := ClassifyAddKind(AddSourceSpec{Symlink: "/tmp/local", Command: "echo hi", Positional: "owner/repo"})
		if err != nil {
			t.Fatal(err)
		}
		if kind != AddSourceSymlink || source != "/tmp/local" {
			t.Fatalf("kind=%s source=%q", kind, source)
		}
	})

	t.Run("command when no symlink", func(t *testing.T) {
		kind, source, err := ClassifyAddKind(AddSourceSpec{Command: "npx foo", Positional: "my-skill"})
		if err != nil {
			t.Fatal(err)
		}
		if kind != AddSourceCommand || source != "npx foo" {
			t.Fatalf("kind=%s source=%q", kind, source)
		}
	})

	t.Run("remote positional", func(t *testing.T) {
		kind, source, err := ClassifyAddKind(AddSourceSpec{Positional: "owner/repo"})
		if err != nil {
			t.Fatal(err)
		}
		if kind != AddSourceRemote || source != "owner/repo" {
			t.Fatalf("kind=%s source=%q", kind, source)
		}
	})

	for _, raw := range []string{"/abs/skill", "~/skill", "./rel", "../up", `.\win`, `..\win`, `C:\drive`, "D:/drive"} {
		t.Run("prefix "+raw, func(t *testing.T) {
			kind, source, err := ClassifyAddKind(AddSourceSpec{Positional: raw})
			if err != nil {
				t.Fatal(err)
			}
			if kind != AddSourceSymlink || source != raw {
				t.Fatalf("kind=%s source=%q", kind, source)
			}
		})
	}

	t.Run("existing directory without prefix is local", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		if err := os.Mkdir("local-skill", 0o755); err != nil {
			t.Fatal(err)
		}
		kind, source, err := ClassifyAddKind(AddSourceSpec{Positional: "local-skill"})
		if err != nil {
			t.Fatal(err)
		}
		if kind != AddSourceSymlink || source != "local-skill" {
			t.Fatalf("kind=%s source=%q", kind, source)
		}
	})

	t.Run("url prefixes stay remote even if a directory exists", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		// git@ is a remote prefix; Windows rejects ':' in directory names, so
		// github:owner cannot be used as the colliding fixture.
		if err := os.Mkdir("git@example.com", 0o755); err != nil {
			t.Fatal(err)
		}
		kind, source, err := ClassifyAddKind(AddSourceSpec{Positional: "git@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if kind != AddSourceRemote || source != "git@example.com" {
			t.Fatalf("kind=%s source=%q", kind, source)
		}
	})
}

func TestBuildAddPlanDetectsRemoteConflicts(t *testing.T) {
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "original/repo", "my-skill", "subpath", "github", "")

	source := NewRemoteAddSource("new/repo", "github", "", "/tmp/cache")
	plan := BuildAddPlan(cfg, "/tmp/skills.json", "/tmp/skills", source, map[string]string{"my-skill": "subpath"}, AddAvailabilityIntent{})

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "my-skill" {
		t.Errorf("expected skill my-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[remote] original/repo" {
		t.Errorf("unexpected current source: %s", conflict.CurrentSrc)
	}
}

func TestApplyAddPlanRefusesSourceInsideSkillsDir(t *testing.T) {
	skillsDir := t.TempDir()
	dest := filepath.Join(skillsDir, "mine")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	skillMd := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(skillMd, []byte("# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir, NewSymlinkAddSource(dest, ""), map[string]string{"mine": "."}, AddAvailabilityIntent{})
	if _, err := ApplyAddPlan(plan, cfg, nil); err == nil {
		t.Fatal("expected refusal of a Source inside the skills directory")
	}
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination must remain a real directory: mode=%v err=%v", info.Mode(), err)
	}
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("SKILL.md must survive: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("Config must not be written")
	}
}

func TestBuildAddPlanDetectsUntrackedDiskConflicts(t *testing.T) {
	skillsDir := t.TempDir()
	untrackedDir := filepath.Join(skillsDir, "existing-skill")
	if err := os.MkdirAll(untrackedDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	source := NewSymlinkAddSource("/tmp/local-source", "local test")
	plan := BuildAddPlan(cfg, "/tmp/skills.json", skillsDir, source, map[string]string{"existing-skill": "."}, AddAvailabilityIntent{})

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "existing-skill" {
		t.Errorf("expected skill existing-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[untracked directory]" {
		t.Errorf("expected [untracked directory], got %s", conflict.CurrentSrc)
	}
}

// Add must record the same applied baseline Sync does. Without it a Skill
// added today is classified SkillUnknownBaseline after the next Update, so
// Sync blocks a routine upstream change instead of applying it.
func TestApplyAddPlanRecordsBaselineSoUpdateIsNotUnknown(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	configPath := filepath.Join(project, ".agents", "skills.json")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cfg := config.DefaultConfig()
	repoDir, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", origin, repoDir),
		map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Sample v2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "v2"}} {
		if _, _, err := runGit(origin, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := UpdateRemoteSkills(cfg, nil, true, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}

	snapshot, err := InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Repositories[0].Skills[0]
	if got.Status != SkillCacheUpdateAvailable {
		t.Errorf("status after add then update = %q; want %q", got.Status, SkillCacheUpdateAvailable)
	}
}

// Re-adding a Skill whose Scope copy was edited overwrites it: the user
// already confirmed the overwrite, so the local Drift Sync would block on
// does not block Add.
func TestApplyAddPlanOverwritesALocallyEditedCopy(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")
	repoDir, err := NewCache("owner/repo", origin, "", filepath.Join(project, "cache")).Refresh(false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	add := func() AddResult {
		t.Helper()
		plan := BuildAddPlan(cfg, configPath, skillsDir,
			NewRemoteAddSource("owner/repo", "git", origin, repoDir),
			map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
		result, err := ApplyAddPlan(plan, cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	add()
	edited := filepath.Join(skillsDir, "sample", "SKILL.md")
	if err := os.WriteFile(edited, []byte("# Edited here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := add()

	if result.Blocked != 0 || result.Failed != 0 {
		t.Fatalf("Blocked=%d Failed=%d; want the edited copy overwritten", result.Blocked, result.Failed)
	}
	if got, err := os.ReadFile(edited); err != nil || string(got) == "# Edited here\n" {
		t.Fatalf("SKILL.md = %q, %v; want the Source's copy back", got, err)
	}
}

// An unreadable Scope state must not stop Add from applying the Skill, nor
// pass silently: the next Sync would find no Baseline and block the Skill.
func TestApplyAddPlanReportsUnreadableScopeState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	repoDir := filepath.Join(project, "repo")
	mustWriteScopeStateTestFile(t, filepath.Join(repoDir, "sample", "SKILL.md"), []byte("# Sample\n"))
	skillsDir := filepath.Join(project, ".agents", "skills")
	statePath, bad := writeUnreadableScopeState(t, skillsDir)

	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, filepath.Join(project, ".agents", "skills.json"), skillsDir,
		NewRemoteAddSource("owner/repo", "git", "", repoDir),
		map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	result, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("Failed = %d; an unrecorded Baseline is a failure", result.Failed)
	}
	if result.StateError == "" {
		t.Fatal("StateError is empty; want why the Baseline was not recorded")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("the Skill must still be Materialized: %v", err)
	}
	if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
		t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
	}
}

func TestApplyAddPlanAvailabilityFailsClosed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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

	skillsDir := filepath.Join(project, ".agents", "skills")
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", "", repoDir),
		map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	result, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || !slices.ContainsFunc(result.Events, func(ev SyncEvent) bool { return ev.Kind == SyncAvailabilityFailed }) {
		t.Fatalf("Failed=%d Events=%#v; want the unmanaged Availability path to fail closed", result.Failed, result.Events)
	}
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Remote["owner/repo"].Skills["sample"] != "sample" {
		t.Fatalf("Config must be saved before apply fails: %#v", loaded.Remote)
	}
}

// Config already declares every selected Skill before any is applied, so a
// failed Skill does not stop the rest: each gets its own outcome, as in Sync.
func TestApplyAddPlanContinuesPastAFailedSkill(t *testing.T) {
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
	configPath := filepath.Join(project, ".agents", "skills.json")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, configPath, skillsDir,
		NewRemoteAddSource("owner/repo", "git", "", repoDir),
		map[string]string{"bad": "missing", "good": "good"}, AddAvailabilityIntent{})
	var progress []string
	result, err := ApplyAddPlan(plan, cfg, func(ev AddSkillEvent) {
		progress = append(progress, ev.Name+":"+string(ev.Outcome))
	})
	if err != nil {
		t.Fatalf("ApplyAddPlan error = %v; a failed Skill is an outcome, not an error", err)
	}
	// Each Skill is announced before it is applied and again with its outcome.
	if want := []string{"bad:", "bad:failed", "good:", "good:done"}; !slices.Equal(progress, want) {
		t.Fatalf("progress = %q, want %q", progress, want)
	}
	if result.Failed != 1 || result.Blocked != 0 {
		t.Fatalf("Failed=%d Blocked=%d; want 1 failed", result.Failed, result.Blocked)
	}
	if !slices.ContainsFunc(result.Events, func(ev SyncEvent) bool { return ev.Kind == SyncPathMissing && ev.Skill == "bad" }) {
		t.Fatalf("Events = %#v; want bad's missing path", result.Events)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "good", "SKILL.md")); err != nil {
		t.Fatalf("good must still be Materialized after bad failed: %v", err)
	}
}

// A command check that does not pass leaves the Skill declared but not
// installed: blocked, as Sync calls it, not failed.
func TestApplyAddPlanCountsAFailedCheckAsBlocked(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, filepath.Join(project, ".agents", "skills.json"), skillsDir,
		NewCommandAddSource("echo ok", "exit 1", ""),
		map[string]string{"cmd-skill": "."}, AddAvailabilityIntent{})

	result, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatalf("ApplyAddPlan error = %v; a blocked Skill is an outcome, not an error", err)
	}
	if result.Blocked != 1 || result.Failed != 0 {
		t.Fatalf("Blocked=%d Failed=%d; want 1 blocked", result.Blocked, result.Failed)
	}
	if !slices.ContainsFunc(result.Events, func(ev SyncEvent) bool { return ev.Kind == SyncCheckFailed }) {
		t.Fatalf("Events = %#v; want the failed check", result.Events)
	}
}

func TestApplyAddPlanAvailabilityIntent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	apply := func(t *testing.T, seed *config.AvailabilityOverride, intent AddAvailabilityIntent) *config.Config {
		t.Helper()
		project := t.TempDir()
		source := filepath.Join(project, "source")
		writeLocalGitSkill(t, source, "sample")
		skillsDir := filepath.Join(project, ".agents", "skills")
		configPath := filepath.Join(project, ".agents", "skills.json")
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := config.DefaultConfig()
		if seed != nil {
			cfg.Settings.Availability["sample"] = *seed
		}
		plan := BuildAddPlan(cfg, configPath, skillsDir, NewSymlinkAddSource(source, ""), map[string]string{"sample": "sample"}, intent)
		if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.LoadConfig(configPath)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}

	t.Run("preserve keeps override", func(t *testing.T) {
		loaded := apply(t, &config.AvailabilityOverride{Include: []string{"continue"}}, AddAvailabilityIntent{})
		got := loaded.Settings.Availability["sample"]
		if len(got.Include) != 1 || got.Include[0] != "continue" {
			t.Fatalf("override = %#v", got)
		}
	})

	t.Run("follow defaults clears override", func(t *testing.T) {
		loaded := apply(t, &config.AvailabilityOverride{Include: []string{"continue"}}, AddAvailabilityIntent{Kind: AddAvailabilityFollowDefaults})
		if _, ok := loaded.Settings.Availability["sample"]; ok {
			t.Fatalf("override survived: %#v", loaded.Settings.Availability["sample"])
		}
	})

	t.Run("include records agents", func(t *testing.T) {
		loaded := apply(t, nil, AddAvailabilityIntent{Kind: AddAvailabilityInclude, Agents: []string{"continue"}})
		got := loaded.Settings.Availability["sample"]
		if len(got.Include) != 1 || got.Include[0] != "continue" {
			t.Fatalf("override = %#v", got)
		}
	})

	t.Run("set managed stores minimal override", func(t *testing.T) {
		loaded := apply(t, nil, AddAvailabilityIntent{Kind: AddAvailabilitySetManaged, Agents: []string{"continue"}})
		got := loaded.Settings.Availability["sample"]
		if len(got.Include) != 1 || got.Include[0] != "continue" || len(got.Exclude) != 1 || got.Exclude[0] != "claude-code" {
			t.Fatalf("override = %#v", got)
		}
	})
}

func TestApplyAddPlanChecksOutSelectedSkillsInSparseCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, url := writeSparseOrigin(t)
	project := t.TempDir()
	repoDir, _, _, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: url}, filepath.Join(project, "cache"), "")
	if err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	cfg := config.DefaultConfig()
	plan := BuildAddPlan(cfg, filepath.Join(project, ".agents", "skills.json"), skillsDir,
		NewRemoteAddSource("owner/repo", "git", url, repoDir),
		map[string]string{"alpha": "skills/alpha"}, AddAvailabilityIntent{})
	if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "alpha", "notes.txt")); err != nil {
		t.Fatalf("selected Skill was not Materialized in full: %v", err)
	}
	assertCachePaths(t, repoDir, []string{"skills/alpha/notes.txt"}, []string{"skills/beta", "fixtures"})
}

// Discovery narrows an earlier release's full Cache before the user picks, so
// add must put back what this Scope already declares from the Source.
func TestApplyAddPlanKeepsDeclaredSkillsInConvertedCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "alpha")
	writeLocalGitSkill(t, origin, "beta")
	branch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	project := t.TempDir()
	cacheDir := filepath.Join(project, "cache")
	cache := resolveCacheRepo("owner/repo", origin, branch, cacheDir)
	if err := os.MkdirAll(filepath.Dir(cache.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "clone", origin, cache.Dir)

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "alpha", "alpha", "git", origin)
	repoDir, _, _, err := PrepareRemoteSource("owner/repo", config.RemoteRepo{URL: origin, Branch: branch}, cacheDir, "")
	if err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	plan := BuildAddPlan(cfg, filepath.Join(project, ".agents", "skills.json"), skillsDir,
		NewRemoteAddSource("owner/repo", "git", origin, repoDir),
		map[string]string{"beta": "beta"}, AddAvailabilityIntent{})
	if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir, []string{"alpha/SKILL.md", "beta/SKILL.md"}, nil)
}
