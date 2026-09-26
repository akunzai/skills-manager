package engine

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// retireFixture is a Project Scope declaring the remote Skill "sample", with
// its Scope copy, a managed Availability link for Claude Code, and a
// recorded Baseline: everything Retire takes away.
type retireFixture struct {
	cfg        *config.Config
	configPath string
	skillsDir  string
	copyPath   string
	link       string
}

func newRetireFixture(t *testing.T) retireFixture {
	t.Helper()
	project := t.TempDir()
	f := retireFixture{
		cfg:        config.DefaultConfig(),
		configPath: filepath.Join(project, ".agents", "skills.json"),
		skillsDir:  filepath.Join(project, ".agents", "skills"),
	}
	f.copyPath = filepath.Join(f.skillsDir, "sample")
	mustWriteScopeStateTestFile(t, filepath.Join(f.copyPath, "SKILL.md"), []byte("# Sample\n"))
	f.cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(f.cfg, "owner/repo", "sample", "sample", "git", "https://example.test/repo.git")
	if err := config.SaveConfig(f.cfg, f.configPath); err != nil {
		t.Fatal(err)
	}
	f.link = plantManagedLink(t, f.skillsDir, filepath.Join(project, ".claude", "skills"), "sample")
	store, err := newScopeStateStore(f.skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ScopeState{Skills: map[string]AppliedSkillState{"sample": {Source: "owner/repo"}}}); err != nil {
		t.Fatal(err)
	}
	config.RemoveSkillEntry(f.cfg, "sample")
	return f
}

func (f retireFixture) baselineRecorded(t *testing.T) bool {
	t.Helper()
	_, ok := OpenBaselines(f.skillsDir).Applied("sample")
	return ok
}

func (f retireFixture) declared(t *testing.T) bool {
	t.Helper()
	loaded, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, ok := config.FindSkillSource(loaded, "sample")
	return ok
}

func TestRetireRemovesLinksCopyAndBaseline(t *testing.T) {
	t.Parallel()
	f := newRetireFixture(t)

	retired, err := Retire(f.cfg, f.configPath, f.skillsDir, []string{"sample"}, OpenBaselines(f.skillsDir))

	if err != nil {
		t.Fatal(err)
	}
	want := []RetiredSkill{{
		Name:        "sample",
		Unlinked:    []ManagedAgentPath{{Agent: "claude-code", Skill: "sample", Path: f.link}},
		CopyPath:    f.copyPath,
		CopyRemoved: true,
	}}
	if !reflect.DeepEqual(retired, want) {
		t.Fatalf("retired = %#v; want %#v", retired, want)
	}
	if f.declared(t) {
		t.Fatal("Config still declares the Skill")
	}
	if exists(f.link) || exists(f.copyPath) || f.baselineRecorded(t) {
		t.Fatalf("link %v, copy %v, Baseline %v; want all gone", exists(f.link), exists(f.copyPath), f.baselineRecorded(t))
	}
}

func TestRetireTouchesNothingWhenConfigCannotBeSaved(t *testing.T) {
	t.Parallel()
	f := newRetireFixture(t)
	// A directory where the Config file should be cannot be written over.
	unwritable := filepath.Join(filepath.Dir(f.configPath), "taken")
	if err := os.MkdirAll(filepath.Join(unwritable, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	retired, err := Retire(f.cfg, unwritable, f.skillsDir, []string{"sample"}, OpenBaselines(f.skillsDir))

	if err == nil {
		t.Fatalf("Retire saved a Config over a directory: %#v", retired)
	}
	if retired != nil {
		t.Fatalf("retired = %#v; want nothing retired", retired)
	}
	if !exists(f.link) || !exists(f.copyPath) || !f.baselineRecorded(t) {
		t.Fatalf("link %v, copy %v, Baseline %v; a failed save must leave all of them", exists(f.link), exists(f.copyPath), f.baselineRecorded(t))
	}
}

func TestRetireCountsAMissingCopyAsRemoved(t *testing.T) {
	t.Parallel()
	f := newRetireFixture(t)
	if err := os.RemoveAll(f.copyPath); err != nil {
		t.Fatal(err)
	}

	retired, err := Retire(f.cfg, f.configPath, f.skillsDir, []string{"sample"}, OpenBaselines(f.skillsDir))

	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || !retired[0].CopyRemoved || retired[0].CopyErr != nil || retired[0].Err() != nil {
		t.Fatalf("retired = %#v; a missing copy is removed", retired)
	}
}

func TestRetireReportsALinkItCouldNotRemove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this user removing its entries")
	}
	t.Parallel()
	f := newRetireFixture(t)
	agentDir := filepath.Dir(f.link)
	if err := os.Chmod(agentDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentDir, 0o755) })

	retired, err := Retire(f.cfg, f.configPath, f.skillsDir, []string{"sample"}, OpenBaselines(f.skillsDir))

	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 {
		t.Fatalf("retired = %#v", retired)
	}
	got := retired[0]
	if len(got.Unlinked) != 0 || len(got.FailedLinks) != 1 || got.FailedLinks[0].Path != f.link || got.FailedLinks[0].Err == nil {
		t.Fatalf("Unlinked = %#v, FailedLinks = %#v; want the link reported failed", got.Unlinked, got.FailedLinks)
	}
	if !errors.Is(got.Err(), got.FailedLinks[0].Err) {
		t.Fatalf("Err() = %v; want the link failure", got.Err())
	}
	if !exists(f.link) {
		t.Fatal("the link was removed from a read-only directory")
	}
	if !got.CopyRemoved || f.baselineRecorded(t) {
		t.Fatal("a failed link must not stop the copy and the Baseline going")
	}
}

// An unreadable Scope state is the Scope's verdict, not one Skill's failure:
// Retire reports the Baseline not recorded and leaves the file alone.
func TestRetireLeavesAnUnreadableScopeStateAlone(t *testing.T) {
	t.Parallel()
	f := newRetireFixture(t)
	statePath, bad := writeUnreadableScopeState(t, f.skillsDir)

	retired, err := Retire(f.cfg, f.configPath, f.skillsDir, []string{"sample"}, OpenBaselines(f.skillsDir))

	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || !errors.Is(retired[0].BaselineErr, ErrNotRecorded) {
		t.Fatalf("retired = %#v; want the Baseline not recorded", retired)
	}
	if retired[0].Err() != nil {
		t.Fatalf("Err() = %v; not recorded is not this Skill's failure", retired[0].Err())
	}
	if exists(f.link) || exists(f.copyPath) {
		t.Fatal("an unreadable Scope state must not stop the link and the copy going")
	}
	if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
		t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
	}
}
