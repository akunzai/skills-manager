package engine

import (
	"fmt"
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
		}
	}

	if len(p.Missing) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Warning: Configured but missing skills: " + strings.Join(p.Missing, ", "), Blank: true})
	}
	if len(p.Untracked) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Warning: Untracked skills in %s: %s", models.ToTildePath(p.SkillsDir), strings.Join(p.Untracked, ", ")), Blank: true})
	}
	if len(p.Invalid) > 0 {
		add(Finding{Severity: SeverityError, Message: "Installed folders missing SKILL.md: " + strings.Join(p.Invalid, ", "), Blank: true})
	}
	if p.StateError != "" {
		add(Finding{Severity: SeverityError, Message: "Corrupted Scope state: " + p.StateError, Blank: true})
	}
	if len(p.StaleState) > 0 {
		add(Finding{Severity: SeverityWarning, Message: "Obsolete Scope state entries: " + strings.Join(p.StaleState, ", "), Blank: true})
	}
	if len(p.LegacyCache) > 0 {
		add(Finding{Severity: SeverityWarning, Message: fmt.Sprintf("Legacy branchless Cache entries: %s", strings.Join(p.LegacyCache, ", ")), Blank: true})
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
		for _, path := range result.RemovedLegacy {
			add(Finding{Severity: SeverityOK, Message: "Removed legacy Cache: " + path})
		}
		for _, failure := range result.FailedLegacy {
			add(Finding{Severity: SeverityError, Message: fmt.Sprintf("Failed to remove legacy Cache %s: %s", failure.Name, failure.Err)})
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
