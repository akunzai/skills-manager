package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
)

// The wording bug the "findings as the interface" refactor was meant to
// catch: leftover dirs are excluded from configuredAgents by
// Availability.ConfiguredAgentDirs, which unions defaultAgents with every per-Skill
// Include — not defaultAgents alone.
func TestFindingsLeftoverWordingCoversWholePolicyNotJustDefaults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	outcome, err := engine.NewDoctor(cfg, skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	findings := doctorFindings(outcome.Report, outcome.Repair)
	var leftover *Finding
	for i := range findings {
		if strings.Contains(findings[i].Message, "leftover empty agent director") {
			leftover = &findings[i]
		}
	}
	if leftover == nil {
		t.Fatalf("expected a leftover finding, got %#v", findings)
	}
	if strings.Contains(leftover.Message, "not in defaultAgents") {
		t.Errorf("leftover finding still says %q; that undersells what Availability.ConfiguredAgentDirs actually excludes (defaultAgents + every per-Skill include)", leftover.Message)
	}
	if leftover.Severity != SeverityWarning {
		t.Errorf("leftover severity = %v; want SeverityWarning", leftover.Severity)
	}
}

// A fixing run reports repair actions, then derives Remaining from a fresh
// diagnosis rather than subtracting successful actions from the old plan.
func TestDoctorRunFindingsReflectFixOutcome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	continueDir := filepath.Join(project, ".continue", "skills")
	if err := os.MkdirAll(continueDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	doctor := engine.NewDoctor(cfg, skillsDir)
	beforeOutcome, err := doctor.Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := doctorFindings(beforeOutcome.Report, beforeOutcome.Repair)
	if !containsMessage(before, "Warning:") {
		t.Fatalf("expected a pre-fix warning finding, got %#v", before)
	}

	afterOutcome, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := doctorFindings(afterOutcome.Report, afterOutcome.Repair)
	if containsMessage(after, "Warning:") {
		t.Errorf("post-fix findings still contain a pre-fix warning: %#v", after)
	}
	if !containsMessage(after, "Removed") {
		t.Errorf("expected a Removed finding after fixing the leftover dir, got %#v", after)
	}
}

// A partial leftover-removal failure must not label the failed dir as
// "Removed": the outcome finding lists result.RemovedLeftover, not the
// pre-fix plan.LeftoverEmpty (which would include both).
func TestFindingsLeftoverPartialFailureNamesOnlyWhatSucceeded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission denial is unreliable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod-based permission denial does not apply to root")
	}

	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	blockedParent := filepath.Join(project, ".roo")
	if err := os.MkdirAll(filepath.Join(blockedParent, "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedParent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedParent, 0o755) })

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	outcome, err := engine.NewDoctor(cfg, skillsDir).Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	findings := doctorFindings(outcome.Report, outcome.Repair)
	var removedMsg string
	for _, f := range findings {
		if strings.HasPrefix(f.Message, "  Removed ") {
			removedMsg = f.Message
		}
	}
	if removedMsg == "" {
		t.Fatalf("expected a Removed finding, got %#v", findings)
	}
	if !strings.Contains(removedMsg, "continue") {
		t.Errorf("Removed finding %q does not name continue", removedMsg)
	}
	if strings.Contains(removedMsg, "roo") {
		t.Errorf("Removed finding %q wrongly names roo, which failed to remove", removedMsg)
	}
	if !containsMessage(findings, "Failed to remove leftover roo dir") {
		t.Errorf("expected a Failed finding for roo, got %#v", findings)
	}
	if outcome.Remaining != 1 {
		t.Errorf("Remaining = %d; want fresh diagnosis to retain roo", outcome.Remaining)
	}
}

func containsMessage(findings []Finding, substr string) bool {
	for _, f := range findings {
		if strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

// Untracked Skills are the one finding --fix never acts on, so the report has
// to name the way out — and name it for the Scope actually being diagnosed. A
// Global command printed while diagnosing a Project sends the user at the
// wrong skills directory.
func TestFindingsUntrackedNamesBothWaysOutForItsScope(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeUntracked := func(t *testing.T, skillsDir string) {
		t.Helper()
		orphan := filepath.Join(skillsDir, "orphan")
		if err := os.MkdirAll(orphan, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("project", func(t *testing.T) {
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		if err := os.MkdirAll(skillsDir, 0755); err != nil {
			t.Fatal(err)
		}
		writeUntracked(t, skillsDir)

		outcome, err := engine.NewDoctor(config.DefaultConfig(), skillsDir).Run(false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), "Declare it with 'skills add -p', or remove it with 'skills prune -p --skills-only'.") {
			t.Fatalf("untracked finding has no Project-scoped next action: %#v", doctorFindings(outcome.Report, outcome.Repair))
		}
		if outcome.Untracked != 1 {
			t.Errorf("Untracked = %d; want 1", outcome.Untracked)
		}
		if outcome.Remaining != 0 {
			t.Errorf("Remaining = %d; want 0 (untracked must not count)", outcome.Remaining)
		}
	})

	t.Run("global", func(t *testing.T) {
		t.Setenv("AGENTS_HOME", t.TempDir())
		skillsDir := models.DefaultSkillsDir()
		if err := os.MkdirAll(skillsDir, 0755); err != nil {
			t.Fatal(err)
		}
		writeUntracked(t, skillsDir)

		outcome, err := engine.NewDoctor(config.DefaultConfig(), skillsDir).Run(false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), "Declare it with 'skills add', or remove it with 'skills prune --skills-only'.") {
			t.Fatalf("untracked finding has no Global-scoped next action: %#v", doctorFindings(outcome.Report, outcome.Repair))
		}
		if containsMessage(doctorFindings(outcome.Report, outcome.Repair), "skills prune -p") {
			t.Errorf("Global finding suggested a Project command: %#v", doctorFindings(outcome.Report, outcome.Repair))
		}
	})
}

// An invalid folder's remedy depends on how the Skill was declared: removing a
// remote one lets a bare Sync re-Materialize it, while a symlinked one is only
// as valid as its Source and a command installer is not re-run by Sync. One
// shared sentence would be wrong for two of the three.
func TestFindingsInvalidNextActionFollowsHowTheSkillWasDeclared(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	source := filepath.Join(project, "src", "linked")
	for _, dir := range []string{
		filepath.Join(skillsDir, "remoted"),
		filepath.Join(skillsDir, "linked"),
		filepath.Join(skillsDir, "installed"),
		source,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "remoted", "remoted", "github", "")
	config.AddLocalSymlinkEntry(cfg, "linked", source, "")
	config.AddLocalCommandEntry(cfg, "installed", "install-me", "", "")

	outcome, err := engine.NewDoctor(cfg, skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Next: remove " + models.ToTildePath(filepath.Join(skillsDir, "remoted")) + ", then run 'skills sync -p' to re-materialize it.",
		"Next: its source " + models.ToTildePath(source) + " has no SKILL.md; fix the source, or undeclare it with 'skills rm -p linked'.",
		"Next: its installer left no SKILL.md; re-run it manually, or undeclare it with 'skills rm -p installed'.",
	}
	for _, message := range want {
		if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), message) {
			t.Errorf("missing next action %q in %#v", message, doctorFindings(outcome.Report, outcome.Repair))
		}
	}
}

// An Availability path that cannot be read at all is neither present nor
// missing. Dropping that observation is what let a Scope whose Agent directory
// was a regular file — Lstat returns ENOTDIR, not ENOENT — report as healthy
// while nothing was linked.
func TestFindingsReportAvailabilityPathsThatCannotBeObserved(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	alpha := filepath.Join(skillsDir, "alpha")
	if err := os.MkdirAll(alpha, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "SKILL.md"), []byte("# Alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	// The Agent's skills directory exists, but as a regular file.
	if err := os.WriteFile(filepath.Join(claudeDir, "skills"), []byte("not a directory\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "alpha", alpha, "")

	availability := engine.NewAvailability(cfg, skillsDir)
	drift := availability.ObserveAvailability("alpha")
	if drift.Empty() {
		t.Fatal("an unreadable availability path must not observe as no drift")
	}
	if len(drift.Unobservable) != 1 {
		t.Fatalf("Unobservable = %#v; want exactly one entry", drift.Unobservable)
	}
	if got := drift.Unobservable[0]; got.Agent != "claude-code" || got.Err != "not a directory" {
		t.Errorf("unobservable path = %#v; want claude-code / not a directory", got)
	}
	if len(drift.Missing) > 0 || len(drift.Foreign) > 0 {
		t.Errorf("an unreadable path is neither missing nor foreign: %#v", drift)
	}

	outcome, err := engine.NewDoctor(cfg, skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsMessage(doctorFindings(outcome.Report, outcome.Repair), "Symlinks healthy") {
		t.Fatalf("doctor called a non-directory Agent path healthy: %#v", doctorFindings(outcome.Report, outcome.Repair))
	}
	if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), "[claude-code] Agent directory is not usable") {
		t.Fatalf("doctor did not report the unusable Agent directory: %#v", doctorFindings(outcome.Report, outcome.Repair))
	}
	if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), "Cannot observe availability for alpha") {
		t.Fatalf("doctor did not report the unreadable availability path: %#v", doctorFindings(outcome.Report, outcome.Repair))
	}
	if !containsMessage(doctorFindings(outcome.Report, outcome.Repair), "Next: inspect "+models.ToTildePath(filepath.Join(claudeDir, "skills"))) {
		t.Fatalf("the finding names no next action: %#v", doctorFindings(outcome.Report, outcome.Repair))
	}
	if outcome.Remaining == 0 {
		t.Fatal("a Scope with no working availability must not report as clean")
	}
}
