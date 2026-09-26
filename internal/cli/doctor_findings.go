package cli

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
)

// Severity is how prominently doctor renders one Finding. An int rather than
// SyncEvent-style string constants (engine/sync.go) because ordering is part
// of its meaning — OK < Warning < Error — even though nothing compares
// severities today.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityOK
	SeverityWarning
	SeverityError
)

// Finding is one line doctor reports: a diagnosed fact, or — once result is
// non-nil — the outcome of attempting to repair one. Blank requests a blank
// line before it, preserving the visual grouping between finding categories.
type Finding struct {
	Severity Severity
	Message  string
	Blank    bool
}

// doctorFindings renders one DoctorReport as the ordered list doctor prints.
// Repair outcomes travel on the diagnosed items; this file only renders.
// Every doctor sentence is assembled here and nowhere else — the engine
// reports facts, this file turns them into English (see engine.DoctorOutcome).
func doctorFindings(p engine.DoctorReport, scopeFlags string) []Finding {
	var findings []Finding
	add := func(f Finding) { findings = append(findings, f) }

	if p.MasterMissing {
		add(Finding{Severity: SeverityError, Message: "Missing master skills directory: " + models.ToTildePath(p.SkillsDir)})
	} else {
		add(Finding{Severity: SeverityOK, Message: "Master skills directory: " + models.ToTildePath(p.SkillsDir)})
	}

	add(Finding{Severity: SeverityInfo, Message: "Checking Agent Directories & Symlinks:", Blank: true})
	for _, agent := range p.Agents {
		if agent.Unusable != "" {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("  [%s] Agent directory is not usable: %s (%s)", agent.Name, models.ToTildePath(agent.Dir), agent.Unusable)})
			continue
		}
		add(Finding{Severity: SeverityOK, Message: fmt.Sprintf("  [%s] Symlinks healthy (%s).", agent.Name, models.ToTildePath(agent.Dir))})
		if len(agent.UnmanagedBroken) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  [%s] Unmanaged broken symlinks were left unchanged: %s", agent.Name, strings.Join(agent.UnmanagedBroken, ", "))})
		}
		if len(agent.Physical) > 0 {
			// Not a repair target: an unmanaged directory this tool did not
			// create and Config does not declare is left alone, the same as
			// Untracked occupancy on the skills directory (ADR-0002).
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  [%s] Unmanaged directories left as-is: %s", agent.Name, strings.Join(agent.Physical, ", "))})
		}
	}

	// One line for the whole Scope rather than a badge on every row: the
	// question this answers — why are these real files instead of links — is
	// asked once.
	if copied, breakdown := describeCopiedAvailability(p.Drift); copied > 0 {
		add(Finding{Severity: SeverityInfo, Message: "  " + copiedAvailabilityNotice(copied, breakdown, scopeFlags)})
	}

	dangling, live := leftoverPathsByAgent(p.Leftover.Paths)
	for _, agent := range leftoverAgents(dangling, live) {
		if names := dangling[agent]; len(names) > 0 {
			add(Finding{Severity: leftoverSeverity(engine.DoctorFindingLeftoverDangling), Message: fmt.Sprintf("  [%s] Stale links to removed skills: %s", agent, strings.Join(names, ", "))})
		}
		if names := live[agent]; len(names) > 0 {
			add(Finding{Severity: leftoverSeverity(engine.DoctorFindingLeftoverLive), Message: fmt.Sprintf("  [%s] Leftover occupancy: managed paths declared Availability does not call for: %s", agent, strings.Join(names, ", "))})
		}
		for _, path := range p.Leftover.Paths {
			if path.Agent == agent {
				findings = append(findings, leftoverPathRepairFinding(path)...)
			}
		}
	}

	if len(p.Leftover.Empty) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  %d leftover empty agent directories (not covered by any configured Agent policy): %s", len(p.Leftover.Empty), strings.Join(leftoverAgentNames(p.Leftover.Empty), ", "))})
		for _, empty := range p.Leftover.Empty {
			findings = append(findings, leftoverEmptyRepairFinding(empty)...)
		}
	}

	for _, d := range p.Drift {
		if len(d.Broken) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Availability drift for %s; broken links: %s", d.Skill, strings.Join(d.Broken, ", ")), Blank: true})
		}
		if len(d.Missing) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Availability drift for %s; missing links: %s", d.Skill, strings.Join(d.Missing, ", ")), Blank: true})
		}
		for _, foreign := range d.Foreign {
			add(Finding{Severity: SeverityWarning, Message: foreignAvailabilityFinding(d.Skill, foreign), Blank: true})
		}
		for _, unobservable := range d.Unobservable {
			add(Finding{Severity: SeverityError, Message: unobservableAvailabilityFinding(d.Skill, unobservable), Blank: true})
			add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Next: inspect %s, then re-run 'skills doctor%s'.", models.ToTildePath(unobservable.Dir), scopeFlags)})
		}
		if len(d.Unexpected) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Availability drift for %s; unexpected links: %s", d.Skill, strings.Join(d.Unexpected, ", ")), Blank: true})
		}
		switch d.Repair.Status {
		case engine.RepairSucceeded:
			add(Finding{Severity: SeverityOK, Message: fmt.Sprintf("Fixed availability drift for %s.", d.Skill)})
		case engine.RepairFailed:
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to reconcile availability for %s: %s", d.Skill, d.Repair.Err)})
			for _, foreign := range d.Foreign {
				remove := "rm -- "
				if foreign.Kind == engine.ForeignAvailabilityDirectory {
					remove = "rm -rf -- "
				}
				add(Finding{Severity: SeverityInfo, Message: "  Remove it manually: " + remove + shellQuotePath(foreign.Path)})
			}
		}
	}

	if len(p.Missing) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Configured but missing skills: " + strings.Join(p.Missing, ", "), Blank: true})
	}
	if len(p.Untracked) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Untracked skills in %s: %s", models.ToTildePath(p.SkillsDir), strings.Join(p.Untracked, ", ")), Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf(
			"  Not in Config; left as-is. Declare %s with 'skills adopt%s', or remove %s with a TTY prune; 'skills prune%s --yes' will not.",
			objectPronoun(len(p.Untracked)), scopeFlags, objectPronoun(len(p.Untracked)), scopeFlags)})
	}
	if len(p.UntrackedLinks) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Untracked leftover symlink in %s: %s", models.ToTildePath(p.SkillsDir), strings.Join(p.UntrackedLinks, ", ")), Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf(
			"  Remove %s with 'skills prune%s'.",
			objectPronoun(len(p.UntrackedLinks)), scopeFlags)})
	}
	for _, illegal := range p.IllegalLocal {
		add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Local source for %s is inside the skills directory.", illegal.Name), Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf(
			"  Next: delete the local entry for %s from skills.json and leave the directory.",
			illegal.Name)})
	}
	for _, stub := range p.Stubs {
		add(Finding{Severity: SeverityWarning, Message: "Skill arrived as a text stub instead of a directory: " + stub, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf(
			"  Next: set 'git config core.symlinks true' and check the file out again, or remove %s and run 'skills sync%s'.",
			models.ToTildePath(filepath.Join(p.SkillsDir, stub)), scopeFlags)})
	}
	for _, invalid := range p.Invalid {
		add(Finding{Severity: SeverityError, Message: "Installed folder missing SKILL.md: " + invalid.Name, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: "  " + invalidNextAction(p, invalid, scopeFlags)})
	}
	if p.StateError != "" {
		add(Finding{Severity: SeverityError, Message: "Corrupted Scope state: " + p.StateError, Blank: true})
	}
	if p.GitError != "" {
		add(Finding{Severity: SeverityError, Message: "Remote Sources cannot be fetched: " + p.GitError, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Next: install or upgrade git, then run 'skills update%s'.", scopeFlags)})
	}
	if len(p.StaleState) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Obsolete Scope state entries: " + strings.Join(p.StaleState, ", "), Blank: true})
	}
	if roots := p.LegacyCacheRoots(); len(roots) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Legacy branchless Cache entries: %s", strings.Join(roots, ", ")), Blank: true})
	}
	for _, artifact := range p.CacheRecovery {
		add(Finding{Severity: SeverityWarning, Message: "Leftover Cache recovery artifact from an earlier release: " + artifact, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Next: run 'skills doctor --fix%s' to remove it.", scopeFlags)})
	}
	for _, artifact := range p.StaleScopes {
		add(Finding{Severity: SeverityWarning, Message: "Scope state references missing path: " + artifact.ScopePath, Blank: true})
		switch artifact.Repair.Status {
		case engine.RepairSucceeded:
			add(Finding{Severity: SeverityOK, Message: "Removed state for missing Scope: " + artifact.ScopePath})
		case engine.RepairFailed:
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to remove stale Scope state %s: %s", artifact.Path, artifact.Repair.Err)})
		}
	}
	switch p.StateRepair.Status {
	case engine.RepairFailed:
		add(Finding{Severity: SeverityError, Message: "Failed to repair Scope state: " + p.StateRepair.Err.Error()})
	case engine.RepairSucceeded:
		add(Finding{Severity: SeverityOK, Message: "Repaired Scope state."})
	}
	for _, removal := range p.CacheRemovals {
		switch removal.Repair.Status {
		case engine.RepairSucceeded:
			add(Finding{Severity: SeverityOK, Message: "Removed legacy Cache artifact: " + removal.Path})
		case engine.RepairFailed:
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to remove legacy Cache artifact %s: %s", removal.Path, removal.Repair.Err)})
		}
	}
	for _, ref := range p.UnknownAgents {
		where := "settings." + ref.Field
		if ref.Skill != "" {
			where = fmt.Sprintf("settings.availability.%s.%s", ref.Skill, ref.Field)
		}
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Unknown agent %q in %s", ref.Agent, where), Blank: true})
	}
	// Not a repair target: the Agent owns that name, so no Availability can
	// be applied there; the way out is renaming or excluding the Skill.
	for _, reserved := range p.ReservedNames {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("%s cannot be available to %s: %s reserves that directory name.", reserved.Skill, reserved.Agent, agentProductName(reserved.Agent)), Blank: true})
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Next: rename the Skill, or run 'skills agents%s %s exclude %s'.", scopeFlags, reserved.Skill, reserved.Agent)})
	}

	return findings
}

// invalidNextAction names the way out for one invalid Skill. The remedies do
// not generalize: removing the folder of a remote Skill turns it back into a
// plain Missing one that a bare Sync re-Materializes, but doing the same to a
// symlinked Skill only rebuilds the same link at the same broken Source, and
// a command installer is never re-run by Sync when its check does not pass.
// One shared sentence would therefore be wrong for someone.
func invalidNextAction(p engine.DoctorReport, invalid engine.InvalidSkill, flag string) string {
	switch invalid.SourceType {
	case "local_symlink":
		source := models.ToTildePath(models.ResolveLocalSourcePath(invalid.Source, p.SkillsDir))
		return fmt.Sprintf("Next: its source %s has no SKILL.md; fix the source, or undeclare it with 'skills rm%s %s'.", source, flag, invalid.Name)
	case "local_command":
		return fmt.Sprintf("Next: its installer left no SKILL.md; re-run it manually, or undeclare it with 'skills rm%s %s'.", flag, invalid.Name)
	default:
		path := models.ToTildePath(filepath.Join(p.SkillsDir, invalid.Name))
		return fmt.Sprintf("Next: remove %s, then run 'skills sync%s' to re-materialize it.", path, flag)
	}
}

// agentProductName is how a sentence names an Agent's product, for the Agents
// whose own behaviour a finding describes. Any other Agent is named by key.
func agentProductName(agent string) string {
	if agent == "claude-code" {
		return "Claude Code"
	}
	return agent
}

func objectPronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// unobservableAvailabilityFinding reports a path doctor could not read at all.
// It is an error rather than a warning: doctor cannot say whether the Skill is
// available for that Agent, and reporting nothing is what let a broken Scope
// pass as healthy.
func unobservableAvailabilityFinding(skill string, unobservable engine.UnobservableAvailabilityPath) string {
	return fmt.Sprintf("Cannot observe availability for %s; unreadable %s path: %s (%s)", skill, unobservable.Agent, models.ToTildePath(unobservable.Path), unobservable.Err)
}

func foreignAvailabilityFinding(skill string, foreign engine.ForeignAvailabilityPath) string {
	return fmt.Sprintf("Availability drift for %s; occupied path for %s: %s (%s)", skill, foreign.Agent, models.ToTildePath(foreign.Path), foreignAvailabilityDetail(foreign))
}

func foreignAvailabilityDetail(foreign engine.ForeignAvailabilityPath) string {
	detail := string(foreign.Kind)
	if foreign.Target != "" {
		detail += " -> " + models.ToTildePath(foreign.Target)
	}
	return detail
}

func leftoverSeverity(kind engine.DoctorFindingKind) Severity {
	switch kind {
	case engine.DoctorFindingLeftoverDangling:
		return SeverityError
	case engine.DoctorFindingLeftoverLive:
		return SeverityWarning
	default:
		return SeverityWarning
	}
}

func shellQuotePath(path string) string {
	path = models.ToTildePath(path)
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return "$HOME/" + shellQuote(rest)
	}
	return shellQuote(path)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func leftoverPathsByAgent(paths []engine.LeftoverPath) (dangling, live map[string][]string) {
	dangling = make(map[string][]string)
	live = make(map[string][]string)
	for _, path := range paths {
		switch path.FindingKind() {
		case engine.DoctorFindingLeftoverDangling:
			dangling[path.Agent] = append(dangling[path.Agent], path.Skill)
		case engine.DoctorFindingLeftoverLive:
			live[path.Agent] = append(live[path.Agent], path.Skill)
		}
	}
	return dangling, live
}

func leftoverAgents(groups ...map[string][]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for agent := range group {
			seen[agent] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

func leftoverPathRepairFinding(path engine.LeftoverPath) []Finding {
	switch path.Repair.Status {
	case engine.RepairSucceeded:
		return []Finding{{Severity: SeverityOK, Message: fmt.Sprintf("    Fixed: Removed leftover occupancy %s.", path.Skill)}}
	case engine.RepairFailed:
		return []Finding{{Severity: SeverityError, Message: fmt.Sprintf("    Failed to remove leftover occupancy %s: %s", path.Skill, path.Repair.Err)}}
	case engine.RepairSkipped:
		return []Finding{{Severity: SeverityInfo, Message: fmt.Sprintf("    Skipped leftover occupancy %s.", path.Skill)}}
	default:
		return nil
	}
}

func leftoverEmptyRepairFinding(empty engine.AgentDir) []Finding {
	switch empty.Repair.Status {
	case engine.RepairSucceeded:
		return []Finding{{Severity: SeverityOK, Message: fmt.Sprintf("    Removed leftover empty agent directory %s.", empty.Name)}}
	case engine.RepairFailed:
		return []Finding{{Severity: SeverityError, Message: fmt.Sprintf("    Failed to remove leftover %s dir %s: %s", empty.Name, models.ToTildePath(empty.Dir), empty.Repair.Err)}}
	case engine.RepairSkipped:
		return []Finding{{Severity: SeverityInfo, Message: fmt.Sprintf("    Skipped leftover agent directory %s: it is no longer empty.", empty.Name)}}
	default:
		return nil
	}
}

func leftoverAgentNames(dirs []engine.AgentDir) []string {
	names := make([]string, 0, len(dirs))
	for _, leftover := range dirs {
		names = append(names, leftover.Name)
	}
	return names
}
