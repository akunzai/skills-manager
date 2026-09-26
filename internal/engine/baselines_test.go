package engine

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func baselinesScope(t *testing.T) (skillsDir string, skill SkillFreshness) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	skillsDir = filepath.Join(t.TempDir(), "skills")
	skill = SkillFreshness{Name: "sample", Source: "owner/repo", ScopePath: filepath.Join(skillsDir, "sample")}
	mustWriteScopeStateTestFile(t, filepath.Join(skill.ScopePath, "SKILL.md"), []byte("# Sample\n"))
	return skillsDir, skill
}

// A recorded Baseline is what the Scope copy is compared with, and it
// survives reopening.
func TestBaselinesRecordThenCompareScopeCopy(t *testing.T) {
	tests := []struct {
		name  string
		after func(t *testing.T, skill SkillFreshness)
		want  ScopeCopy
	}{
		{name: "unchanged", after: func(*testing.T, SkillFreshness) {}, want: ScopeCopyClean},
		{name: "edited", after: func(t *testing.T, skill SkillFreshness) {
			mustWriteScopeStateTestFile(t, filepath.Join(skill.ScopePath, "SKILL.md"), []byte("# Edited\n"))
		}, want: ScopeCopyDrift},
		{name: "removed", after: func(t *testing.T, skill SkillFreshness) {
			if err := os.RemoveAll(skill.ScopePath); err != nil {
				t.Fatal(err)
			}
		}, want: ScopeCopyAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skillsDir, skill := baselinesScope(t)
			if err := OpenBaselines(skillsDir).Record(skill, "cache", "abc123"); err != nil {
				t.Fatal(err)
			}
			tt.after(t, skill)

			baselines := OpenBaselines(skillsDir)
			applied, ok := baselines.Applied("sample")
			if !ok || applied.Source != "owner/repo" || applied.CacheIdentity != "cache" || applied.AppliedCommit != "abc123" {
				t.Fatalf("Applied = %#v, %v; want the recorded Baseline", applied, ok)
			}
			if got := baselines.CompareScopeCopy("sample", skill.ScopePath); got != tt.want {
				t.Fatalf("CompareScopeCopy = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestBaselinesCompareScopeCopyWithoutBaselineIsUnknown(t *testing.T) {
	skillsDir, skill := baselinesScope(t)

	if got := OpenBaselines(skillsDir).CompareScopeCopy("sample", skill.ScopePath); got != ScopeCopyUnknown {
		t.Fatalf("CompareScopeCopy = %q; want %q", got, ScopeCopyUnknown)
	}
}

// Only a remote Skill has a Baseline: one now declared local is as stale as
// one not declared at all.
func TestBaselinesForgetStaleKeepsOnlyDeclaredRemoteSkills(t *testing.T) {
	skillsDir, skill := baselinesScope(t)
	baselines := OpenBaselines(skillsDir)
	for _, name := range []string{"remote", "local", "gone"} {
		skill.Name = name
		if err := baselines.Record(skill, "cache", "abc123"); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "remote", "remote", "github", "")
	config.AddLocalSymlinkEntry(cfg, "local", skill.ScopePath, "")

	if got, want := baselines.Stale(cfg), []string{"gone", "local"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Stale = %v; want %v", got, want)
	}
	if err := baselines.ForgetStale(cfg); err != nil {
		t.Fatal(err)
	}
	reopened := OpenBaselines(skillsDir)
	for name, want := range map[string]bool{"remote": true, "local": false, "gone": false} {
		if _, ok := reopened.Applied(name); ok != want {
			t.Fatalf("Applied(%q) present = %v; want %v", name, ok, want)
		}
	}
}

// Forgetting a Baseline a Scope never recorded must not create its state.
func TestBaselinesForgetDoesNotCreateMissingState(t *testing.T) {
	skillsDir, _ := baselinesScope(t)
	store, err := newScopeStateStore(skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := OpenBaselines(skillsDir).Forget("absent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("Forget created the Scope state: %v", err)
	}
}

// A Scope state that cannot be read is never rewritten: Err says why,
// recording and forgetting report that nothing was recorded, and nothing
// compares clean against it. Only Reset replaces it.
func TestBaselinesLeaveUnreadableStateAlone(t *testing.T) {
	skillsDir, skill := baselinesScope(t)
	statePath, bad := writeUnreadableScopeState(t, skillsDir)
	cfg := config.DefaultConfig()

	baselines := OpenBaselines(skillsDir)
	if baselines.Err() == nil {
		t.Fatal("Err = nil; want why the Scope state could not be read")
	}
	for name, err := range map[string]error{
		"Record":      baselines.Record(skill, "cache", "abc123"),
		"Forget":      baselines.Forget("sample"),
		"ForgetStale": baselines.ForgetStale(cfg),
	} {
		if !errors.Is(err, ErrNotRecorded) || !errors.Is(err, baselines.Err()) {
			t.Fatalf("%s error = %v; want ErrNotRecorded, saying why", name, err)
		}
	}
	if got := baselines.CompareScopeCopy("sample", skill.ScopePath); got != ScopeCopyUnknown {
		t.Fatalf("CompareScopeCopy = %q; want %q", got, ScopeCopyUnknown)
	}
	if got, _ := os.ReadFile(statePath); !reflect.DeepEqual(got, bad) {
		t.Fatalf("Scope state = %q; want it untouched", got)
	}

	if err := baselines.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("Reset left the Scope state: %v", err)
	}
	if err := OpenBaselines(skillsDir).Err(); err != nil {
		t.Fatalf("Err after Reset = %v; want a readable, empty state", err)
	}
}

// ADR-0002: an unreadable Scope state fails a command only when it touched a
// Skill that needed a Baseline; any other command warns. A readable one is
// fine either way.
func TestBaselinesVerdict(t *testing.T) {
	tests := []struct {
		name       string
		unreadable bool
		needed     bool
		want       StateVerdict
	}{
		{name: "needed and unreadable", unreadable: true, needed: true, want: StateFail},
		{name: "not needed and unreadable", unreadable: true, want: StateWarn},
		{name: "needed and readable", needed: true, want: StateOK},
		{name: "not needed and readable", want: StateOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skillsDir, _ := baselinesScope(t)
			if tt.unreadable {
				writeUnreadableScopeState(t, skillsDir)
			}

			if got := OpenBaselines(skillsDir).Verdict(tt.needed); got != tt.want {
				t.Fatalf("Verdict(%v) = %v; want %v", tt.needed, got, tt.want)
			}
		})
	}
}
