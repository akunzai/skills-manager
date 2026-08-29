package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// Severity is how prominently doctor renders one Finding. It carries no
// presentation: color is a CLI concern (docs/design.md). An int rather than
// SyncEvent-style string constants (sync.go) because ordering is part of its
// meaning — OK < Warning < Error — even though nothing compares severities
// today.
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

// findings renders the plan as an ordered list doctor prints as-is. With
// result nil this is the diagnosis; with result set, entries that
// repair touched report its outcome instead. This is the one place that
// interprets a health plan for display, so a new finding is added here
// once rather than in a second, hand-synced printer.
func (p healthPlan) findings(result *healthFixResult) []Finding {
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
		if len(agent.Broken) > 0 {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("  [%s] Broken symlinks: %s", agent.Name, strings.Join(agent.Broken, ", "))})
			if result != nil {
				findings = append(findings, agentFixFindings(result.RemovedBroken, result.FailedBroken, agent.Name, "broken symlink", true)...)
			}
		} else {
			add(Finding{Severity: SeverityOK, Message: fmt.Sprintf("  [%s] Symlinks healthy (%s).", agent.Name, models.ToTildePath(agent.Dir))})
		}
		if len(agent.UnmanagedBroken) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  Warning: [%s] Unmanaged broken symlinks were left unchanged: %s", agent.Name, strings.Join(agent.UnmanagedBroken, ", "))})
		}
		if len(agent.Physical) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  Warning: [%s] Physical directories found instead of symlinks: %s", agent.Name, strings.Join(agent.Physical, ", "))})
			if result != nil {
				for _, pName := range agent.Physical {
					add(Finding{Severity: SeverityError, Message: fmt.Sprintf("    Cannot replace unmanaged directory %s in %s.", pName, agent.Name)})
				}
			}
		}
	}

	for _, stale := range p.StaleUniversal {
		add(Finding{Severity: SeverityError, Message: fmt.Sprintf("  [%s] Stale links to removed skills: %s", stale.Agent, strings.Join(stale.Names, ", "))})
		if result != nil {
			// includeErr is false here, matching pre-refactor behavior: unlike
			// FailedBroken, FailedStale.Err was never surfaced to the user.
			findings = append(findings, agentFixFindings(result.RemovedStale, result.FailedStale, stale.Agent, "stale link", false)...)
		}
	}

	if result == nil {
		if len(p.LeftoverEmpty) > 0 {
			add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("  Warning: %d leftover empty agent directories (not covered by any configured Agent policy): %s", len(p.LeftoverEmpty), strings.Join(leftoverAgentNames(p.LeftoverEmpty), ", "))})
		}
	} else {
		for _, fix := range result.FailedLeftover {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("  Failed to remove leftover %s dir %s: %s", fix.Agent, models.ToTildePath(fix.Name), fix.Err)})
		}
		if len(result.RemovedLeftover) > 0 {
			add(Finding{Severity: SeverityOK, Message: fmt.Sprintf("  Removed %d leftover empty agent directories: %s.", len(result.RemovedLeftover), strings.Join(leftoverAgentNames(result.RemovedLeftover), ", "))})
		}
	}

	if result == nil {
		for _, d := range p.Drift {
			if len(d.Missing) > 0 {
				add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Availability drift for %s; missing links: %s", d.Skill, strings.Join(d.Missing, ", ")), Blank: true})
			}
			for _, foreign := range d.Foreign {
				add(Finding{Severity: SeverityWarning, Message: foreignAvailabilityFinding(d.Skill, foreign), Blank: true})
			}
			for _, unobservable := range d.Unobservable {
				add(Finding{Severity: SeverityError, Message: unobservableAvailabilityFinding(d.Skill, unobservable), Blank: true})
				add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Next: inspect %s, then re-run 'skills doctor%s'.", models.ToTildePath(unobservable.Dir), p.scopeFlag())})
			}
			if len(d.Unexpected) > 0 {
				add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Availability drift for %s; unexpected links: %s", d.Skill, strings.Join(d.Unexpected, ", ")), Blank: true})
			}
		}
	} else {
		for _, skill := range result.FixedDrift {
			add(Finding{Severity: SeverityOK, Message: fmt.Sprintf("Fixed availability drift for %s.", skill), Blank: true})
		}
		for _, fix := range result.FailedDrift {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to reconcile availability for %s: %s", fix.Name, fix.Err), Blank: true})
			for _, drift := range p.Drift {
				if drift.Skill != fix.Name {
					continue
				}
				for _, foreign := range drift.Foreign {
					add(Finding{Severity: SeverityWarning, Message: foreignAvailabilityFinding(drift.Skill, foreign)})
					remove := "rm -- "
					if foreign.Kind == ForeignAvailabilityDirectory {
						remove = "rm -rf -- "
					}
					add(Finding{Severity: SeverityInfo, Message: "  Remove it manually: " + remove + shellQuotePath(foreign.Path)})
				}
			}
		}
	}

	if len(p.Missing) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Warning: Configured but missing skills: " + strings.Join(p.Missing, ", "), Blank: true})
	}
	if len(p.Untracked) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Warning: Untracked skills in %s: %s", models.ToTildePath(p.SkillsDir), strings.Join(p.Untracked, ", ")), Blank: true})
		// Untracked Skills are the one finding --fix deliberately never acts
		// on, so the way out has to be spelled here: doctor restores declared
		// state, discarding undeclared data belongs to prune.
		add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf(
			"  Declare %s with 'skills add%s', or remove %s with 'skills prune%s --skills-only'.",
			objectPronoun(len(p.Untracked)), p.scopeFlag(), objectPronoun(len(p.Untracked)), p.scopeFlag())})
	}
	for _, invalid := range p.Invalid {
		add(Finding{Severity: SeverityError, Message: "Installed folder missing SKILL.md: " + invalid.Name, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: "  " + p.invalidNextAction(invalid)})
	}
	if p.StateError != "" {
		add(Finding{Severity: SeverityError, Message: "Corrupted Scope state: " + p.StateError, Blank: true})
	}
	if len(p.StaleState) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Obsolete Scope state entries: " + strings.Join(p.StaleState, ", "), Blank: true})
	}
	if len(p.LegacyCache) > 0 {
		paths := make([]string, 0, len(p.LegacyCache))
		for _, migration := range p.LegacyCache {
			paths = append(paths, migration.Root)
		}
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Legacy branchless Cache entries: %s", strings.Join(paths, ", ")), Blank: true})
	}
	for _, artifact := range p.CacheRecovery {
		add(Finding{Severity: SeverityError, Message: "Manual Cache recovery required; preserved artifact: " + artifact, Blank: true})
		add(Finding{Severity: SeverityInfo, Message: "  Inspect the preserved Cache, restore it to the intended Cache path if needed, then remove the artifact."})
	}
	for _, artifact := range p.StaleScopes {
		add(Finding{Severity: SeverityWarning, Message: "Scope state references missing path: " + artifact.ScopePath, Blank: true})
	}
	if result != nil && (p.StateError != "" || len(p.StaleState) > 0) {
		if result.StateRepairErr != nil {
			add(Finding{Severity: SeverityError, Message: "Failed to repair Scope state: " + result.StateRepairErr.Error()})
		} else {
			add(Finding{Severity: SeverityOK, Message: "Repaired Scope state."})
		}
	}
	if result != nil {
		for _, migration := range result.LegacyResults {
			switch migration.Status {
			case legacyCacheRebuilt:
				add(Finding{Severity: SeverityOK, Message: "Rebuilt branch-aware Cache and removed legacy Cache: " + migration.Plan.Root})
			case legacyCacheRecoveryNeeded:
				add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Manual Cache recovery required after rebuilding %s: %s; preserved artifacts: %s", migration.Plan.Root, migration.Err, strings.Join(migration.Artifacts, ", "))})
				add(Finding{Severity: SeverityInfo, Message: fmt.Sprintf("  Inspect the preserved Cache trees, restore the desired tree to %s if needed, then remove the artifacts.", migration.Plan.Root)})
			case legacyCacheFailed:
				add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to rebuild legacy Cache %s: %s", migration.Plan.Root, migration.Err)})
			}
		}
		for _, path := range result.RemovedScopes {
			add(Finding{Severity: SeverityOK, Message: "Removed state for missing Scope: " + path})
		}
		for _, failure := range result.FailedScopes {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to remove stale Scope state %s: %s", failure.Name, failure.Err)})
		}
	}
	for _, ref := range p.UnknownAgents {
		where := "settings." + ref.Field
		if ref.Skill != "" {
			where = fmt.Sprintf("settings.availability.%s.%s", ref.Skill, ref.Field)
		}
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Warning: unknown agent %q in %s", ref.Agent, where), Blank: true})
	}

	return findings
}

// invalidNextAction names the way out for one invalid Skill. The remedies do
// not generalize: removing the folder of a remote Skill turns it back into a
// plain Missing one that a bare Sync re-Materializes, but doing the same to a
// symlinked Skill only rebuilds the same link at the same broken Source, and
// a command installer is never re-run by Sync when its check does not pass.
// One shared sentence would therefore be wrong for someone.
func (p healthPlan) invalidNextAction(invalid invalidSkill) string {
	flag := p.scopeFlag()
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

// scopeFlag is the flag a suggested command needs to act on the Scope doctor
// just diagnosed. Printing a Global command while diagnosing a Project would
// send the user at the wrong skills directory.
func (p healthPlan) scopeFlag() string {
	if models.IsProjectScope(p.SkillsDir) {
		return " -p"
	}
	return ""
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
func unobservableAvailabilityFinding(skill string, unobservable UnobservableAvailabilityPath) string {
	return fmt.Sprintf("Cannot observe availability for %s; unreadable %s path: %s (%s)", skill, unobservable.Agent, models.ToTildePath(unobservable.Path), unobservable.Err)
}

func foreignAvailabilityFinding(skill string, foreign ForeignAvailabilityPath) string {
	return fmt.Sprintf("Availability drift for %s; occupied path for %s: %s (%s)", skill, foreign.Agent, models.ToTildePath(foreign.Path), foreign.Detail())
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

// agentFixFindings renders one agent's Fixed/Failed sub-lines for a repair
// category (broken symlinks or stale links) — the shape shared by both.
func agentFixFindings(removed, failed []healthFix, agent, noun string, includeErr bool) []Finding {
	var findings []Finding
	for _, fix := range removed {
		if fix.Agent == agent {
			findings = append(findings, Finding{Severity: SeverityOK, Message: fmt.Sprintf("    Fixed: Removed %s %s.", noun, fix.Name)})
		}
	}
	for _, fix := range failed {
		if fix.Agent != agent {
			continue
		}
		msg := fmt.Sprintf("    Failed to remove %s %s", noun, fix.Name)
		if includeErr {
			msg = fmt.Sprintf("%s: %s", msg, fix.Err)
		}
		findings = append(findings, Finding{Severity: SeverityError, Message: msg})
	}
	return findings
}

func leftoverAgentNames(dirs []AgentDir) []string {
	names := make([]string, 0, len(dirs))
	for _, leftover := range dirs {
		names = append(names, leftover.Name)
	}
	return names
}
