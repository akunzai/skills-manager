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

func TestBuildHealthPlanReadsInventoryMissingAndInvalid(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(skillsDir, "broken")
	if err := os.MkdirAll(invalid, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing", ".", "github", "")
	config.AddLocalSymlinkEntry(cfg, "broken", invalid, "")

	plan := BuildHealthPlan(cfg, skillsDir)
	if len(plan.Missing) != 1 || plan.Missing[0] != "missing" {
		t.Fatalf("missing = %#v", plan.Missing)
	}
	if len(plan.Invalid) != 1 || plan.Invalid[0] != "broken" {
		t.Fatalf("invalid = %#v", plan.Invalid)
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

func TestBuildHealthPlanFlagsUnknownAgentReferences(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "not-a-real-agent"}
	cfg.Settings.Availability["some-skill"] = config.AvailabilityOverride{
		Include: []string{"also-fake"},
	}

	plan := BuildHealthPlan(cfg, skillsDir)
	if len(plan.UnknownAgents) != 2 {
		t.Fatalf("UnknownAgents = %#v; want 2 entries", plan.UnknownAgents)
	}

	var sawDefault, sawInclude bool
	for _, ref := range plan.UnknownAgents {
		switch {
		case ref.Field == "defaultAgents" && ref.Agent == "not-a-real-agent":
			sawDefault = true
		case ref.Field == "include" && ref.Skill == "some-skill" && ref.Agent == "also-fake":
			sawInclude = true
		}
	}
	if !sawDefault || !sawInclude {
		t.Fatalf("UnknownAgents = %#v; missing expected entries", plan.UnknownAgents)
	}
	if plan.IssueCount() != 2 {
		t.Fatalf("IssueCount = %d; want 2", plan.IssueCount())
	}

	result := ApplyHealthPlan(plan, cfg, skillsDir)
	if RemainingIssues(plan, result) != 2 {
		t.Fatalf("RemainingIssues = %d; want 2 (unknown agents are never auto-fixed)", RemainingIssues(plan, result))
	}
}
