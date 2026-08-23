package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AgentHealth is one configured agent's skills directory.
type AgentHealth struct {
	Name            string
	Dir             string
	Broken          []string
	UnmanagedBroken []string
	Physical        []string
}

// StaleUniversalLinks is managed links left in a universal agent directory.
type StaleUniversalLinks struct {
	Agent string
	Dir   string
	Names []string
}

// DriftFinding is Availability Drift for one installed, configured Skill.
type DriftFinding struct {
	Skill      string
	Source     string
	Missing    []string
	Unexpected []string
}

// HealthFix is one repaired or unrepaired path (Name is a skill name or dir).
type HealthFix struct {
	Agent string
	Name  string
	Err   error
}

// Field values for UnknownAgentReference, matching the skills.json key each
// names.
const (
	AgentRefDefaultAgents = "defaultAgents"
	AgentRefInclude       = "include"
	AgentRefExclude       = "exclude"
)

// UnknownAgentReference is a defaultAgents or per-Skill include/exclude entry
// naming an Agent this Scope does not recognize — neither a known linkable
// dir nor Automatically available. Availability apply silently skips it
// (DesiredAgents only counts recognized Agents), so doctor is the only place
// a typo'd or renamed Agent name ever surfaces. Not auto-fixable: there is no
// safe guess at the intended name.
type UnknownAgentReference struct {
	Skill string // "" for cfg.Settings.DefaultAgents
	Field string // one of the AgentRef* constants
	Agent string
}

// HealthPlan is the diagnosed state of one Scope. ApplyHealthPlan repairs
// only broken managed links, stale universal links, leftover empty agent
// dirs, and Availability Drift.
type HealthPlan struct {
	SkillsDir      string
	MasterMissing  bool
	Agents         []AgentHealth
	StaleUniversal []StaleUniversalLinks
	LeftoverEmpty  []AgentDir
	Drift          []DriftFinding
	Missing        []string
	Untracked      []string
	Invalid        []string
	UnknownAgents  []UnknownAgentReference
}

// HealthFixResult is what ApplyHealthPlan did.
type HealthFixResult struct {
	RemovedBroken   []HealthFix
	FailedBroken    []HealthFix
	RemovedStale    []HealthFix
	FailedStale     []HealthFix
	RemovedLeftover []AgentDir
	FailedLeftover  []HealthFix
	FixedDrift      []string
	FailedDrift     []HealthFix
}

// unknownAgentRefs finds defaultAgents and per-Skill include/exclude entries
// that name an Agent unrecognized in this Scope.
func unknownAgentRefs(cfg *config.Config, skillsDir string, known map[string]string) []UnknownAgentReference {
	isRecognized := func(name string) bool {
		norm := models.NormalizeAgentName(name)
		if _, ok := known[norm]; ok {
			return true
		}
		return models.IsUniversalAgent(norm, skillsDir)
	}

	var refs []UnknownAgentReference
	for _, a := range cfg.Settings.DefaultAgents {
		if !isRecognized(a) {
			refs = append(refs, UnknownAgentReference{Field: AgentRefDefaultAgents, Agent: models.NormalizeAgentName(a)})
		}
	}

	skillNames := make([]string, 0, len(cfg.Settings.Availability))
	for name := range cfg.Settings.Availability {
		skillNames = append(skillNames, name)
	}
	sort.Strings(skillNames)
	for _, skill := range skillNames {
		override := cfg.Settings.Availability[skill]
		for _, a := range override.Include {
			if !isRecognized(a) {
				refs = append(refs, UnknownAgentReference{Skill: skill, Field: AgentRefInclude, Agent: models.NormalizeAgentName(a)})
			}
		}
		for _, a := range override.Exclude {
			if !isRecognized(a) {
				refs = append(refs, UnknownAgentReference{Skill: skill, Field: AgentRefExclude, Agent: models.NormalizeAgentName(a)})
			}
		}
	}
	return refs
}

func availabilitySource(item models.SkillItem) string {
	if strings.HasPrefix(item.SourceType, "local_") {
		return "local"
	}
	return item.Source
}

// BuildHealthPlan diagnoses one Scope. Untracked Skills are recorded but are
// not issues; missing Skills and invalid folders are issues but not repaired.
func BuildHealthPlan(cfg *config.Config, skillsDir string) HealthPlan {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	plan := HealthPlan{SkillsDir: skillsDir}
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		plan.MasterMissing = true
	}

	knownAgents := models.GetAgentsForSkillsDir(skillsDir)
	universalDirs := models.GetUniversalAgentSkillDirs(skillsDir)
	configuredAgents := ConfiguredKnownAgents(cfg, skillsDir)

	configuredNames := make([]string, 0, len(configuredAgents))
	for name := range configuredAgents {
		configuredNames = append(configuredNames, name)
	}
	sort.Strings(configuredNames)
	for _, agentName := range configuredNames {
		agentDir := configuredAgents[agentName]
		if _, err := os.Stat(agentDir); os.IsNotExist(err) {
			continue
		}
		broken, unmanaged, physical := DiagnoseAgentDirHealth(agentDir, skillsDir)
		plan.Agents = append(plan.Agents, AgentHealth{
			Name:            agentName,
			Dir:             agentDir,
			Broken:          broken,
			UnmanagedBroken: unmanaged,
			Physical:        physical,
		})
	}

	universalNames := make([]string, 0, len(universalDirs))
	for name := range universalDirs {
		universalNames = append(universalNames, name)
	}
	sort.Strings(universalNames)
	for _, agentName := range universalNames {
		if _, ok := configuredAgents[agentName]; ok {
			continue
		}
		agentDir := universalDirs[agentName]
		stale := FindStaleManagedLinks(agentDir, skillsDir)
		if len(stale) == 0 {
			continue
		}
		plan.StaleUniversal = append(plan.StaleUniversal, StaleUniversalLinks{
			Agent: agentName,
			Dir:   agentDir,
			Names: stale,
		})
	}

	plan.LeftoverEmpty = LeftoverEmptyAgentDirs(knownAgents, configuredAgents)
	plan.UnknownAgents = unknownAgentRefs(cfg, skillsDir, knownAgents)

	inv, err := Inventory(cfg, skillsDir)
	if err != nil {
		return plan
	}
	for _, s := range inv {
		if !s.IsInstalled {
			plan.Missing = append(plan.Missing, s.Name)
			continue
		}
		if isUntracked(s) {
			plan.Untracked = append(plan.Untracked, s.Name)
			continue
		}
		if !s.IsValidSkill {
			plan.Invalid = append(plan.Invalid, s.Name)
		}
		source := availabilitySource(s)
		missing, unexpected := AvailabilityDrift(s.Name, cfg, skillsDir)
		if len(missing) == 0 && len(unexpected) == 0 {
			continue
		}
		plan.Drift = append(plan.Drift, DriftFinding{
			Skill:      s.Name,
			Source:     source,
			Missing:    missing,
			Unexpected: unexpected,
		})
	}
	return plan
}

// IssueCount is the number of issues doctor reports without --fix.
// Untracked Skills are warnings only.
func (p HealthPlan) IssueCount() int {
	n := 0
	if p.MasterMissing {
		n++
	}
	for _, a := range p.Agents {
		n += len(a.Broken) + len(a.UnmanagedBroken) + len(a.Physical)
	}
	for _, s := range p.StaleUniversal {
		n += len(s.Names)
	}
	n += len(p.LeftoverEmpty)
	for _, d := range p.Drift {
		n += len(d.Missing) + len(d.Unexpected)
	}
	n += len(p.Missing) + len(p.Invalid)
	n += len(p.UnknownAgents)
	return n
}

// ApplyHealthPlan repairs fixable findings. Physical dirs and unmanaged
// broken links are left unchanged. Missing/untracked/invalid Skills are not
// materialized or pruned.
func ApplyHealthPlan(plan HealthPlan, cfg *config.Config, skillsDir string) HealthFixResult {
	result := HealthFixResult{}
	for _, agent := range plan.Agents {
		for _, name := range agent.Broken {
			path := filepath.Join(agent.Dir, name)
			fix := HealthFix{Agent: agent.Name, Name: name}
			if err := removeManagedAvailability(path, name, skillsDir); err != nil {
				fix.Err = err
				result.FailedBroken = append(result.FailedBroken, fix)
				continue
			}
			result.RemovedBroken = append(result.RemovedBroken, fix)
		}
	}
	for _, stale := range plan.StaleUniversal {
		for _, name := range stale.Names {
			fix := HealthFix{Agent: stale.Agent, Name: name}
			path := filepath.Join(stale.Dir, name)
			if !IsManagedSkillLink(path, name, skillsDir) {
				fix.Err = fmt.Errorf("failed to remove stale link %s", name)
				result.FailedStale = append(result.FailedStale, fix)
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				fix.Err = err
				result.FailedStale = append(result.FailedStale, fix)
				continue
			}
			result.RemovedStale = append(result.RemovedStale, fix)
		}
	}
	stopAt := models.ScopeRoot(skillsDir)
	for _, leftover := range plan.LeftoverEmpty {
		if err := RemoveEmptyAgentDir(leftover.Dir, stopAt); err != nil {
			result.FailedLeftover = append(result.FailedLeftover, HealthFix{
				Agent: leftover.Name,
				Name:  leftover.Dir,
				Err:   err,
			})
			continue
		}
		result.RemovedLeftover = append(result.RemovedLeftover, leftover)
	}
	for _, d := range plan.Drift {
		if err := ApplyAvailability(d.Skill, cfg, skillsDir); err != nil {
			result.FailedDrift = append(result.FailedDrift, HealthFix{Name: d.Skill, Err: err})
			continue
		}
		result.FixedDrift = append(result.FixedDrift, d.Skill)
	}
	return result
}

// RemainingIssues is the number of issues after --fix: unrepaired items plus
// findings --fix never touches. A failed Drift reconcile counts as one issue
// (not missing+unexpected), matching prior doctor --fix arithmetic.
func RemainingIssues(plan HealthPlan, result HealthFixResult) int {
	n := 0
	if plan.MasterMissing {
		n++
	}
	n += len(result.FailedBroken)
	n += len(result.FailedStale)
	n += len(result.FailedLeftover)
	n += len(result.FailedDrift)
	for _, a := range plan.Agents {
		n += len(a.UnmanagedBroken) + len(a.Physical)
	}
	n += len(plan.Missing) + len(plan.Invalid)
	n += len(plan.UnknownAgents)
	return n
}

func removeManagedAvailability(path, skillName, skillsDir string) error {
	if IsManagedSkillLink(path, skillName, skillsDir) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if IsManagedSkillCopy(path, skillName, skillsDir) {
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return fmt.Errorf("failed to remove broken symlink %s", skillName)
}
