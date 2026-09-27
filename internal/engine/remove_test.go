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
