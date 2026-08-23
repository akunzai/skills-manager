package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// The wording bug the "findings as the interface" refactor was meant to
// catch: leftover dirs are excluded from configuredAgents by
// ConfiguredKnownAgents, which unions defaultAgents with every per-Skill
// Include — not defaultAgents alone.
func TestFindingsLeftoverWordingCoversWholePolicyNotJustDefaults(t *testing.T) {
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
	plan := BuildHealthPlan(cfg, skillsDir)

	findings := plan.Findings(nil)
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
		t.Errorf("leftover finding still says %q; that undersells what ConfiguredKnownAgents actually excludes (defaultAgents + every per-Skill include)", leftover.Message)
	}
	if leftover.Severity != SeverityWarning {
		t.Errorf("leftover severity = %v; want SeverityWarning", leftover.Severity)
	}
}

// Once ApplyHealthPlan has run, the same finding category reports the repair
// outcome instead of the pre-fix diagnosis — Findings(result) is what makes
// that swap, not a second hand-written printer.
func TestFindingsReflectsFixOutcome(t *testing.T) {
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
	plan := BuildHealthPlan(cfg, skillsDir)

	before := plan.Findings(nil)
	if !containsMessage(before, "Warning:") {
		t.Fatalf("expected a pre-fix warning finding, got %#v", before)
	}

	result := ApplyHealthPlan(plan, cfg, skillsDir)
	after := plan.Findings(&result)
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
	plan := BuildHealthPlan(cfg, skillsDir)
	if len(plan.LeftoverEmpty) != 2 {
		t.Fatalf("expected 2 leftover candidates (continue, roo), got %#v", plan.LeftoverEmpty)
	}

	result := ApplyHealthPlan(plan, cfg, skillsDir)
	if len(result.RemovedLeftover) != 1 || result.RemovedLeftover[0].Name != "continue" {
		t.Fatalf("expected only continue to be removed, got %#v", result.RemovedLeftover)
	}
	if len(result.FailedLeftover) != 1 {
		t.Fatalf("expected roo's removal to fail, got %#v", result.FailedLeftover)
	}

	findings := plan.Findings(&result)
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
}

func containsMessage(findings []Finding, substr string) bool {
	for _, f := range findings {
		if strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}
