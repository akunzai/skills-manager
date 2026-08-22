package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestBuildHealthPlanCountsLeftoverButNotUntracked(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(skillsDir, "orphan")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	plan := BuildHealthPlan(cfg, skillsDir)
	if len(plan.Untracked) != 1 || plan.Untracked[0] != "orphan" {
		t.Fatalf("untracked = %#v", plan.Untracked)
	}
	if len(plan.LeftoverEmpty) != 1 || plan.LeftoverEmpty[0].Name != "continue" {
		t.Fatalf("leftover = %#v", plan.LeftoverEmpty)
	}
	if plan.IssueCount() != 1 {
		t.Fatalf("IssueCount = %d; want 1 (untracked must not count)", plan.IssueCount())
	}
}

func TestApplyHealthPlanRemovesLeftoverEmptyDirs(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(project, ".claude", "skills")
	continueDir := filepath.Join(project, ".continue", "skills")
	for _, dir := range []string{claudeDir, continueDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	plan := BuildHealthPlan(cfg, skillsDir)
	result := ApplyHealthPlan(plan, cfg, skillsDir)
	if len(result.RemovedLeftover) == 0 {
		t.Fatalf("expected leftover removal, plan leftover=%#v result=%#v", plan.LeftoverEmpty, result)
	}
	if RemainingIssues(plan, result) != 0 {
		t.Fatalf("remaining = %d", RemainingIssues(plan, result))
	}
	if _, err := os.Stat(continueDir); !os.IsNotExist(err) {
		t.Fatal("continue leftover should be removed")
	}
	if _, err := os.Stat(claudeDir); err != nil {
		t.Fatal("configured claude dir must remain")
	}
}
