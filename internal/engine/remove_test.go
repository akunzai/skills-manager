package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// untrackedRemoveFixture is a Scope whose skills directory holds "loose", a
// real directory Config does not declare, planned for removal.
func untrackedRemoveFixture(t *testing.T) (cfg *config.Config, configPath, skillsDir, loose string, plan RemovePlan) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir, configPath = filepath.Join(root, "skills"), filepath.Join(root, "skills.json")
	loose = filepath.Join(skillsDir, "loose")
	mustWriteScopeStateTestFile(t, filepath.Join(loose, "SKILL.md"), []byte("# Loose\n"))
	cfg = config.DefaultConfig()
	plan, err := BuildRemovePlan(cfg, skillsDir, []string{"loose"})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.UntrackedDirectories(); len(got) != 1 || got[0] != "loose" {
		t.Fatalf("untracked directories = %v, want [loose]", got)
	}
	return cfg, configPath, skillsDir, loose, plan
}

func TestApplyRemovePlanKeepsAnUnapprovedUntrackedDirectory(t *testing.T) {
	cfg, configPath, skillsDir, loose, plan := untrackedRemoveFixture(t)

	result, err := ApplyRemovePlan(plan, cfg, configPath, skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(loose, "SKILL.md")); err != nil {
		t.Fatalf("an unapproved untracked directory must stay: %v", err)
	}
	if got := result.Skills[0]; got.CopyRemoved || got.DirectoryKept == "" {
		t.Fatalf("result = %+v, want the directory kept with a reason", got)
	}
}

func TestApplyRemovePlanRemovesAnApprovedUntrackedDirectory(t *testing.T) {
	cfg, configPath, skillsDir, loose, plan := untrackedRemoveFixture(t)

	result, err := ApplyRemovePlan(plan.ApproveUntrackedDirectories(), cfg, configPath, skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(loose); !os.IsNotExist(err) {
		t.Fatalf("an approved untracked directory should be removed: %v", err)
	}
	if got := result.Skills[0]; !got.CopyRemoved || got.DirectoryKept != "" {
		t.Fatalf("result = %+v, want the directory removed", got)
	}
}

func TestApplyRemovePlanSkipsAnApprovedPathThatChanged(t *testing.T) {
	cfg, configPath, skillsDir, loose, plan := untrackedRemoveFixture(t)
	approved := plan.ApproveUntrackedDirectories()
	elsewhere := filepath.Join(t.TempDir(), "loose")
	if err := os.Rename(loose, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, loose); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyRemovePlan(approved, cfg, configPath, skillsDir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(elsewhere, "SKILL.md")); err != nil {
		t.Fatalf("the directory a changed path now points at must stay: %v", err)
	}
}

// A local Skill has no Baseline to forget, so an unreadable Scope state only
// warns. A remote Skill's Baseline cannot be forgotten, nor a stale one of a
// Skill Config does not declare ruled out, so either leaves the removal
// incomplete; the Skill is removed regardless (ADR-0002).
func TestApplyRemovePlanOnUnreadableScopeStateIsIncompleteOnlyWhenABaselineIsNeeded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		declare func(cfg *config.Config, root, skillsDir string)
		want    Convergence
		verdict StateVerdict
	}{
		{
			name: "local",
			declare: func(cfg *config.Config, root, skillsDir string) {
				source := filepath.Join(root, "src", "sample")
				mustWriteScopeStateTestFile(t, filepath.Join(source, "SKILL.md"), []byte("# Sample\n"))
				cfg.Local["sample"] = config.LocalEntry{Type: "symlink", Source: source}
				if err := os.Symlink(source, filepath.Join(skillsDir, "sample")); err != nil {
					t.Fatal(err)
				}
			},
			want: Converged, verdict: StateWarn,
		},
		{
			name: "remote",
			declare: func(cfg *config.Config, _, skillsDir string) {
				mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"))
				config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", "https://example.test/repo.git")
			},
			want: Incomplete, verdict: StateFail,
		},
		{
			name: "undeclared",
			declare: func(_ *config.Config, _, skillsDir string) {
				mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"))
			},
			want: Incomplete, verdict: StateFail,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			root := t.TempDir()
			skillsDir, configPath := filepath.Join(root, "skills"), filepath.Join(root, "skills.json")
			if err := os.MkdirAll(skillsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := config.DefaultConfig()
			tc.declare(cfg, root, skillsDir)
			if err := config.SaveConfig(cfg, configPath); err != nil {
				t.Fatal(err)
			}
			statePath, bad := writeUnreadableScopeState(t, skillsDir)
			plan, err := BuildRemovePlan(cfg, skillsDir, []string{"sample"})
			if err != nil {
				t.Fatal(err)
			}

			result, err := ApplyRemovePlan(plan.ApproveUntrackedDirectories(), cfg, configPath, skillsDir)

			if err != nil {
				t.Fatal(err)
			}
			if got := result.Convergence(); got != tc.want {
				t.Fatalf("Convergence() = %s; want %s", got, tc.want)
			}
			if result.State.Verdict != tc.verdict || result.State.Message == "" {
				t.Fatalf("State = %#v; want verdict %s with a reason", result.State, tc.verdict)
			}
			if _, err := os.Lstat(filepath.Join(skillsDir, "sample")); !os.IsNotExist(err) {
				t.Fatal("the Skill must still be removed")
			}
			if got, _ := os.ReadFile(statePath); string(got) != string(bad) {
				t.Fatalf("Scope state = %q; an unreadable state must never be rewritten", got)
			}
		})
	}
}
