package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestDoctorRunCountsLeftoverButNotUntracked(t *testing.T) {
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
	outcome, err := NewDoctor(cfg, skillsDir).Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Untracked skills") || !containsMessage(outcome.Findings, ": orphan") {
		t.Fatalf("missing untracked finding: %#v", outcome.Findings)
	}
	if !containsMessage(outcome.Findings, "leftover empty agent director") {
		t.Fatalf("missing leftover finding: %#v", outcome.Findings)
	}
	if outcome.Remaining != 1 {
		t.Fatalf("Remaining = %d; want 1 (untracked must not count)", outcome.Remaining)
	}
	if _, err := os.Stat(filepath.Join(project, ".continue", "skills")); err != nil {
		t.Fatalf("diagnose-only run mutated the filesystem: %v", err)
	}
}

func TestDoctorRunReportsMissingAndInvalidInventory(t *testing.T) {
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

	outcome, err := NewDoctor(cfg, skillsDir).Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Configured but missing skills: missing") {
		t.Fatalf("missing inventory finding: %#v", outcome.Findings)
	}
	if !containsMessage(outcome.Findings, "Installed folders missing SKILL.md: broken") {
		t.Fatalf("missing invalid finding: %#v", outcome.Findings)
	}
}

func TestDoctorRunFixesThenRediagnosesFilesystem(t *testing.T) {
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
	outcome, err := NewDoctor(cfg, skillsDir).Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(outcome.Findings, "Removed") {
		t.Fatalf("expected leftover removal finding: %#v", outcome.Findings)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("Remaining = %d; want actual post-fix state to be healthy", outcome.Remaining)
	}
	if _, err := os.Stat(continueDir); !os.IsNotExist(err) {
		t.Fatal("continue leftover should be removed")
	}
	if _, err := os.Stat(claudeDir); err != nil {
		t.Fatal("configured claude dir must remain")
	}
}

func TestDoctorRunLeavesUnknownAgentReferencesForUser(t *testing.T) {
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

	doctor := NewDoctor(cfg, skillsDir)
	before, err := doctor.Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if before.Remaining != 2 {
		t.Fatalf("Remaining = %d; want 2", before.Remaining)
	}
	if !containsMessage(before.Findings, `unknown agent "not-a-real-agent" in settings.defaultAgents`) ||
		!containsMessage(before.Findings, `unknown agent "also-fake" in settings.availability.some-skill.include`) {
		t.Fatalf("missing unknown-agent findings: %#v", before.Findings)
	}

	after, err := doctor.Run(true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Remaining != 2 {
		t.Fatalf("Remaining after fix = %d; want 2 (unknown agents are never auto-fixed)", after.Remaining)
	}
}

func TestDoctorRunPropagatesInventoryErrors(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsDir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewDoctor(config.DefaultConfig(), skillsDir).Run(false)
	if err == nil {
		t.Fatal("expected inventory error")
	}
	if len(outcome.Findings) != 0 {
		t.Fatalf("Findings = %#v; want no partial healthy report", outcome.Findings)
	}
}
