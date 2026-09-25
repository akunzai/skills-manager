package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	findings := doctorFindings(outcome.Report)
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
	before := doctorFindings(beforeOutcome.Report)
	if finding, ok := findingWith(before, "leftover empty agent director"); !ok || finding.Severity != SeverityWarning {
		t.Fatalf("expected a pre-fix warning finding, got %#v", before)
	}

	afterOutcome, err := doctor.Run(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := doctorFindings(afterOutcome.Report)
	if finding, ok := findingWith(after, "leftover empty agent director"); !ok || finding.Severity != SeverityWarning {
		t.Errorf("post-fix findings dropped the leftover diagnosis: %#v", after)
	}
	if !containsMessage(after, "Removed leftover empty agent directory continue") {
		t.Errorf("expected a Removed finding after fixing the leftover dir, got %#v", after)
	}
}

// An unmanaged directory on an Agent directory (D1) is a warning, worded
// without the literal "Warning:" prefix (D4), and --fix does not claim it
// tried and failed to replace it: it was never a repair target.
func TestFindingsUnmanagedAgentDirectoryIsWarningWithoutRepairAttempt(t *testing.T) {
	report := engine.DoctorReport{
		Agents: []engine.AgentHealth{
			{Name: "goose", Dir: "/scope/goose", Physical: []string{"physical-dir"}},
		},
	}

	finding, ok := findingWith(doctorFindings(report), "physical-dir")
	if !ok {
		t.Fatalf("expected a finding naming the unmanaged directory, got %#v", doctorFindings(report))
	}
	if finding.Severity != SeverityWarning {
		t.Errorf("severity = %v; want SeverityWarning", finding.Severity)
	}
	if !strings.Contains(finding.Message, "[goose] Unmanaged directories left as-is: physical-dir") {
		t.Errorf("message = %q; want the renamed wording", finding.Message)
	}
	if strings.Contains(finding.Message, "Warning:") {
		t.Errorf("message = %q; must not carry the literal Warning: prefix", finding.Message)
	}
	if findings := doctorFindings(report); containsMessage(findings, "Cannot replace unmanaged directory") {
		t.Errorf("doctor must not claim it attempted to replace an unmanaged directory: %#v", findings)
	}
}

// D1 end to end: a Scope whose only standing fact is an unmanaged directory
// on an Agent directory still matches its Config, so doctor exits 0 with or
// without --fix.
func TestCLIDoctorExitsZeroWithOnlyAnUnmanagedAgentDirectory(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".agents", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(project, ".claude", "skills", "unmanaged")
	if err := os.MkdirAll(unmanaged, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, "SKILL.md"), []byte("# Unmanaged"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "doctor", "-p")
	if err != nil {
		t.Fatalf("doctor should exit 0 with only an unmanaged Agent directory present: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Unmanaged directories left as-is: unmanaged") {
		t.Fatalf("expected the unmanaged-directory warning:\n%s", out)
	}
	if !strings.Contains(out, "No issues detected. 1 unmanaged Agent directory left as-is.") || strings.Contains(out, "top condition") {
		t.Fatalf("summary must not claim top condition above the warning:\n%s", out)
	}
	if strings.Contains(out, "Warning:") {
		t.Fatalf("doctor line kept the literal Warning: prefix:\n%s", out)
	}

	fixedOut, err := runCLI(t, "doctor", "--fix", "-p")
	if err != nil {
		t.Fatalf("doctor --fix should also exit 0: %v\n%s", err, fixedOut)
	}
	if strings.Contains(fixedOut, "Cannot replace unmanaged directory") {
		t.Fatalf("doctor --fix should not claim to repair an unmanaged directory:\n%s", fixedOut)
	}
}

// A declared Skill named after Claude Code's own synced/ directory is a
// warning about the declaration, not Drift: doctor exits 0 with or without
// --fix, and --fix never touches the account's claude.ai skills in there.
func TestCLIDoctorWarnsAboutAnAgentReservedSkillName(t *testing.T) {
	project := projectScope(t)

	if _, err := runCLI(t, "init", "-p"); err != nil {
		t.Fatalf("init -p: %v", err)
	}
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "synced"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "synced", "SKILL.md"), []byte("# Synced\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddLocalSymlinkEntry(cfg, "synced", filepath.Join(project, "local-src", "synced"), "")
	if err := config.SaveConfig(cfg, filepath.Join(project, ".agents", "skills.json")); err != nil {
		t.Fatal(err)
	}
	claudeFile := filepath.Join(project, ".claude", "skills", "Synced", "account", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(claudeFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeFile, []byte("# claude.ai skill\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const warning = "synced cannot be available to claude-code: Claude Code reserves that directory name."
	for _, args := range [][]string{{"doctor", "-p"}, {"doctor", "--fix", "-p"}} {
		out, err := runCLI(t, args...)
		if err != nil {
			t.Fatalf("%v should exit 0 with only a reserved Skill name: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, warning) {
			t.Fatalf("%v did not warn about the reserved name:\n%s", args, out)
		}
		for _, want := range []string{"Next: rename the Skill, or run 'skills agents -p synced exclude claude-code'.", "No issues detected. 1 Skill cannot be available to an Agent."} {
			if !strings.Contains(out, want) {
				t.Fatalf("%v does not say %q:\n%s", args, want, out)
			}
		}
		if got, err := os.ReadFile(claudeFile); err != nil || string(got) != "# claude.ai skill\n" {
			t.Fatalf("%v touched Claude Code's synced directory: %q, %v", args, got, err)
		}
	}
}

// A partial leftover-removal failure must not label the failed dir as
// Removed: each empty directory carries its own repair status.
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
	findings := doctorFindings(outcome.Report)
	var removedMsg string
	for _, f := range findings {
		if strings.Contains(f.Message, "Removed leftover empty agent directory") {
			removedMsg = f.Message
			if strings.Contains(removedMsg, "roo") {
				t.Errorf("Removed finding %q wrongly names roo, which failed to remove", removedMsg)
			}
		}
	}
	if removedMsg == "" {
		t.Fatalf("expected a Removed finding, got %#v", findings)
	}
	if !strings.Contains(removedMsg, "continue") {
		t.Errorf("Removed finding %q does not name continue", removedMsg)
	}
	if !containsMessage(findings, "Failed to remove leftover roo dir") {
		t.Errorf("expected a Failed finding for roo, got %#v", findings)
	}
	if outcome.Remaining != 1 {
		t.Errorf("Remaining = %d; want fresh diagnosis to retain roo", outcome.Remaining)
	}
}

func TestFindingsReportsWorkingLeftoverOccupancyApartFromDangling(t *testing.T) {
	findings := doctorFindings(engine.DoctorReport{
		Leftover: engine.LeftoverOccupancy{
			Paths: []engine.LeftoverPath{
				{Agent: "codex", Skill: "gone", Path: "/tmp/gone", Dangling: true},
				{Agent: "codex", Skill: "sample", Path: "/tmp/sample"},
			},
		},
	})
	if !containsMessage(findings, "Stale links to removed skills: gone") {
		t.Fatalf("expected dangling leftover as stale links, got %#v", findings)
	}
	if !containsMessage(findings, "Leftover occupancy: managed paths declared Availability does not call for: sample") {
		t.Fatalf("expected working leftover occupancy, got %#v", findings)
	}
	if leftoverSeverity(engine.DoctorFindingLeftoverDangling) != SeverityError {
		t.Errorf("dangling leftover kind severity = %v; want Error", leftoverSeverity(engine.DoctorFindingLeftoverDangling))
	}
	if leftoverSeverity(engine.DoctorFindingLeftoverLive) != SeverityWarning {
		t.Errorf("live leftover kind severity = %v; want Warning", leftoverSeverity(engine.DoctorFindingLeftoverLive))
	}
	if finding, ok := findingWith(findings, "Stale links to removed skills"); !ok || finding.Severity != leftoverSeverity(engine.DoctorFindingLeftoverDangling) {
		t.Errorf("dangling leftover finding = %#v; want Error from leftover-dangling", finding)
	}
	if finding, ok := findingWith(findings, "Leftover occupancy:"); !ok || finding.Severity != leftoverSeverity(engine.DoctorFindingLeftoverLive) {
		t.Errorf("live leftover finding = %#v; want Warning from leftover-live", finding)
	}
}

func findingWith(findings []Finding, substr string) (Finding, bool) {
	for _, f := range findings {
		if strings.Contains(f.Message, substr) {
			return f, true
		}
	}
	return Finding{}, false
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
		if containsMessage(doctorFindings(outcome.Report), "skills add") {
			t.Fatalf("untracked real directory must not suggest add: %#v", doctorFindings(outcome.Report))
		}
		if !containsMessage(doctorFindings(outcome.Report), "Not in Config; left as-is. A TTY prune can remove it; 'skills prune -p --yes' will not.") {
			t.Fatalf("untracked finding has no Project-scoped next action: %#v", doctorFindings(outcome.Report))
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
		if containsMessage(doctorFindings(outcome.Report), "skills add") {
			t.Fatalf("untracked real directory must not suggest add: %#v", doctorFindings(outcome.Report))
		}
		if !containsMessage(doctorFindings(outcome.Report), "Not in Config; left as-is. A TTY prune can remove it; 'skills prune --yes' will not.") {
			t.Fatalf("untracked finding has no Global-scoped next action: %#v", doctorFindings(outcome.Report))
		}
		if containsMessage(doctorFindings(outcome.Report), "skills prune -p") {
			t.Errorf("Global finding suggested a Project command: %#v", doctorFindings(outcome.Report))
		}
	})
}

func TestFindingsLeftoverMasterSymlinkNamesPrune(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Orphan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(skillsDir, "orphan")); err != nil {
		t.Fatal(err)
	}

	outcome, err := engine.NewDoctor(config.DefaultConfig(), skillsDir).Run(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(doctorFindings(outcome.Report), "Remove it with 'skills prune -p'.") {
		t.Fatalf("leftover symlink has no prune next action: %#v", doctorFindings(outcome.Report))
	}
	if containsMessage(doctorFindings(outcome.Report), "skills add") {
		t.Fatalf("leftover symlink must not suggest add: %#v", doctorFindings(outcome.Report))
	}
	if outcome.Remaining != 0 {
		t.Errorf("Remaining = %d; want 0", outcome.Remaining)
	}
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
		if !containsMessage(doctorFindings(outcome.Report), message) {
			t.Errorf("missing next action %q in %#v", message, doctorFindings(outcome.Report))
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
	config.AddLocalSymlinkEntry(cfg, "alpha", filepath.Join(filepath.Dir(skillsDir), "local-src", "alpha"), "")

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
	if containsMessage(doctorFindings(outcome.Report), "Symlinks healthy") {
		t.Fatalf("doctor called a non-directory Agent path healthy: %#v", doctorFindings(outcome.Report))
	}
	if !containsMessage(doctorFindings(outcome.Report), "[claude-code] Agent directory is not usable") {
		t.Fatalf("doctor did not report the unusable Agent directory: %#v", doctorFindings(outcome.Report))
	}
	if !containsMessage(doctorFindings(outcome.Report), "Cannot observe availability for alpha") {
		t.Fatalf("doctor did not report the unreadable availability path: %#v", doctorFindings(outcome.Report))
	}
	if !containsMessage(doctorFindings(outcome.Report), "Next: inspect "+models.ToTildePath(filepath.Join(claudeDir, "skills"))) {
		t.Fatalf("the finding names no next action: %#v", doctorFindings(outcome.Report))
	}
	if outcome.Remaining == 0 {
		t.Fatal("a Scope with no working availability must not report as clean")
	}
}

func TestFindingsGitErrorNamesTheScopedUpdate(t *testing.T) {
	report := engine.DoctorReport{
		SkillsDir: filepath.Join(t.TempDir(), ".agents", "skills"),
		GitError:  "git 2.35 or newer is required for the sparse Cache",
	}
	findings := doctorFindings(report)
	if !containsMessage(findings, "Remote Sources cannot be fetched: git 2.35 or newer is required") {
		t.Fatalf("git error not reported: %#v", findings)
	}
	if !containsMessage(findings, "Next: install or upgrade git, then run 'skills update -p'.") {
		t.Fatalf("git error has no Project-scoped next action: %#v", findings)
	}
}

// Every exported DoctorReport field reaches the printed report. The engine's
// TestEveryDoctorReportFieldIsClassified guards Remaining; this guards the
// other walk: a field doctorFindings never reads is diagnosed and counted,
// then never shown. A new field fails here until it is set below and its
// subject is expected in the output.
func TestDoctorFindingsPrintEveryReportField(t *testing.T) {
	report := engine.DoctorReport{
		SkillsDir:     "/scope/skills-dir",
		MasterMissing: true,
		Agents: []engine.AgentHealth{
			{Name: "unusable-agent", Dir: "/scope/unusable", Unusable: "not a directory"},
			{Name: "goose", Dir: "/scope/goose", UnmanagedBroken: []string{"broken-link"}, Physical: []string{"physical-dir"}},
		},
		Leftover: engine.LeftoverOccupancy{
			Paths: []engine.LeftoverPath{
				{Agent: "codex", Skill: "dangling-skill", Dangling: true},
				{Agent: "codex", Skill: "live-skill"},
			},
			Empty: []engine.AgentDir{{Name: "empty-agent", Dir: "/scope/empty"}},
		},
		Drift: []engine.SkillDrift{{
			Skill:        "drift-skill",
			Missing:      []string{"missing-agent"},
			Unexpected:   []string{"unexpected-agent"},
			Broken:       []string{"broken-agent"},
			Foreign:      []engine.ForeignAvailabilityPath{{Agent: "claude-code", Path: "/scope/foreign-path", Kind: engine.ForeignAvailabilityFile}},
			Unobservable: []engine.UnobservableAvailabilityPath{{Agent: "claude-code", Dir: "/scope", Path: "/scope/unobservable-path", Err: "denied"}},
		}},
		Missing:         []string{"missing-skill"},
		Untracked:       []string{"untracked-skill"},
		UntrackedLinks:  []string{"untracked-link"},
		IllegalLocal:    []engine.IllegalLocalSource{{Name: "illegal-skill"}},
		Invalid:         []engine.InvalidSkill{{Name: "invalid-skill"}},
		Stubs:           []string{"stub-skill"},
		UnknownAgents:   []engine.UnknownAgentReference{{Agent: "unknown-agent", Field: "default_agents"}},
		ReservedNames:   []engine.ReservedAvailability{{Skill: "reserved-skill", Agent: "claude-code"}},
		StateError:      "state-error",
		StaleState:      []string{"stale-baseline"},
		StateRepair:     engine.ItemRepair{Status: engine.RepairFailed, Err: errors.New("state-repair-error")},
		CacheRecovery:   []string{"recovery-artifact"},
		CacheMigrations: []engine.CacheMigrationOutcome{{Root: "migration-root", Status: engine.CacheMigrationFailed, Err: errors.New("migration-error")}},
		StaleScopes:     []engine.ScopeStateArtifact{{ScopePath: "stale-scope-path"}},
		GitError:        "git-error",
	}
	want := map[string][]string{
		"SkillsDir":       {"/scope/skills-dir"},
		"MasterMissing":   {"Missing master skills directory"},
		"Agents":          {"unusable-agent", "[goose]", "broken-link", "physical-dir"},
		"Leftover":        {"[codex]", "dangling-skill", "live-skill", "empty-agent"},
		"Drift":           {"drift-skill", "missing-agent", "unexpected-agent", "broken-agent", "for claude-code", "foreign-path", "unreadable claude-code path", "unobservable-path"},
		"Missing":         {"missing-skill"},
		"Untracked":       {"untracked-skill"},
		"UntrackedLinks":  {"untracked-link"},
		"IllegalLocal":    {"illegal-skill"},
		"Invalid":         {"invalid-skill"},
		"Stubs":           {"stub-skill"},
		"UnknownAgents":   {`Unknown agent "unknown-agent"`},
		"ReservedNames":   {"reserved-skill cannot be available to claude-code: Claude Code reserves that directory name."},
		"StateError":      {"state-error"},
		"StaleState":      {"stale-baseline"},
		"StateRepair":     {"state-repair-error"},
		"CacheRecovery":   {"recovery-artifact"},
		"CacheMigrations": {"migration-root"},
		"StaleScopes":     {"stale-scope-path"},
		"GitError":        {"git-error"},
	}

	var out strings.Builder
	for _, finding := range doctorFindings(report) {
		out.WriteString(finding.Message + "\n")
	}
	fields := reflect.TypeFor[engine.DoctorReport]()
	value := reflect.ValueOf(report)
	for i := range fields.NumField() {
		field := fields.Field(i)
		if !field.IsExported() {
			continue
		}
		subjects, ok := want[field.Name]
		if !ok || value.Field(i).IsZero() {
			t.Errorf("DoctorReport.%s is not set and expected here; set it and name what doctorFindings must print for it", field.Name)
			continue
		}
		for _, subject := range subjects {
			if !strings.Contains(out.String(), subject) {
				t.Errorf("DoctorReport.%s: output does not mention %q:\n%s", field.Name, subject, out.String())
			}
		}
	}
}
