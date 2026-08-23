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

type agentHealth struct {
	Name            string
	Dir             string
	Broken          []string
	UnmanagedBroken []string
	Physical        []string
}

type staleUniversalLinks struct {
	Agent string
	Dir   string
	Names []string
}

type driftFinding struct {
	Skill      string
	Source     string
	Missing    []string
	Unexpected []string
}

type healthFix struct {
	Agent string
	Name  string
	Err   error
}

type healthPlan struct {
	SkillsDir      string
	MasterMissing  bool
	Agents         []agentHealth
	StaleUniversal []staleUniversalLinks
	LeftoverEmpty  []AgentDir
	Drift          []driftFinding
	Missing        []string
	Untracked      []string
	Invalid        []string
	UnknownAgents  []UnknownAgentReference
}

type healthFixResult struct {
	RemovedBroken   []healthFix
	FailedBroken    []healthFix
	RemovedStale    []healthFix
	FailedStale     []healthFix
	RemovedLeftover []AgentDir
	FailedLeftover  []healthFix
	FixedDrift      []string
	FailedDrift     []healthFix
}

// Doctor diagnoses and optionally repairs one Scope's Skill, Agent directory,
// and Availability health.
type Doctor struct {
	cfg          *config.Config
	skillsDir    string
	availability *Availability
	links        AgentLinkManager
}

// DoctorOutcome is the ordered report plus the issues that remain after Run.
// Remaining is unavailable when Run returns an execution error.
type DoctorOutcome struct {
	Findings  []Finding
	Remaining int
}

func NewDoctor(cfg *config.Config, skillsDir string) *Doctor {
	availability := NewAvailability(cfg, skillsDir)
	return &Doctor{
		cfg:          availability.cfg,
		skillsDir:    availability.skillsDir,
		availability: availability,
		links:        NewAgentLinkManager(availability.skillsDir),
	}
}

// Run diagnoses the Scope. With fix, it repairs independent findings, keeps
// their action report, then diagnoses again to count the actual remaining state.
func (d *Doctor) Run(fix bool) (DoctorOutcome, error) {
	plan, err := d.diagnose()
	if err != nil {
		return DoctorOutcome{}, err
	}
	if !fix {
		return DoctorOutcome{Findings: plan.findings(nil), Remaining: plan.issueCount()}, nil
	}

	result := d.repair(plan)
	outcome := DoctorOutcome{Findings: plan.findings(&result)}
	after, err := d.diagnose()
	if err != nil {
		return outcome, err
	}
	outcome.Remaining = after.issueCount()
	return outcome, nil
}

func availabilitySource(item models.SkillItem) string {
	if strings.HasPrefix(item.SourceType, "local_") {
		return "local"
	}
	return item.Source
}

// diagnose records untracked Skills as warnings. Missing Skills and invalid
// folders are issues but are not repaired.
func (d *Doctor) diagnose() (healthPlan, error) {
	plan := healthPlan{SkillsDir: d.skillsDir}
	if _, err := os.Stat(d.skillsDir); os.IsNotExist(err) {
		plan.MasterMissing = true
	}

	knownAgents := models.GetAgentsForSkillsDir(d.skillsDir)
	universalDirs := models.GetUniversalAgentSkillDirs(d.skillsDir)
	configuredAgents := d.availability.ConfiguredAgentDirs()

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
		broken, unmanaged, physical := d.links.DiagnoseHealth(agentDir)
		plan.Agents = append(plan.Agents, agentHealth{
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
		stale := d.links.FindStaleLinks(agentDir)
		if len(stale) == 0 {
			continue
		}
		plan.StaleUniversal = append(plan.StaleUniversal, staleUniversalLinks{
			Agent: agentName,
			Dir:   agentDir,
			Names: stale,
		})
	}

	plan.LeftoverEmpty = LeftoverEmptyAgentDirs(knownAgents, configuredAgents)
	plan.UnknownAgents = d.availability.UnknownAgentReferences()

	inv, err := Inventory(d.cfg, d.skillsDir)
	if err != nil {
		return healthPlan{}, err
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
		missing, unexpected := d.availability.Drift(s.Name)
		if len(missing) == 0 && len(unexpected) == 0 {
			continue
		}
		plan.Drift = append(plan.Drift, driftFinding{
			Skill:      s.Name,
			Source:     source,
			Missing:    missing,
			Unexpected: unexpected,
		})
	}
	return plan, nil
}

// IssueCount is the number of issues doctor reports without --fix.
// Untracked Skills are warnings only.
func (p healthPlan) issueCount() int {
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

// repair leaves physical dirs, unmanaged broken links, and missing/untracked/
// invalid Skills unchanged. Independent repair failures do not stop the run.
func (d *Doctor) repair(plan healthPlan) healthFixResult {
	result := healthFixResult{}
	for _, agent := range plan.Agents {
		for _, name := range agent.Broken {
			path := filepath.Join(agent.Dir, name)
			fix := healthFix{Agent: agent.Name, Name: name}
			if !d.links.RemoveManagedPath(path, name) {
				fix.Err = fmt.Errorf("failed to remove broken symlink %s", name)
				result.FailedBroken = append(result.FailedBroken, fix)
				continue
			}
			result.RemovedBroken = append(result.RemovedBroken, fix)
		}
	}
	for _, stale := range plan.StaleUniversal {
		for _, name := range stale.Names {
			fix := healthFix{Agent: stale.Agent, Name: name}
			path := filepath.Join(stale.Dir, name)
			if !d.links.RemoveManagedPath(path, name) {
				fix.Err = fmt.Errorf("failed to remove stale link %s", name)
				result.FailedStale = append(result.FailedStale, fix)
				continue
			}
			result.RemovedStale = append(result.RemovedStale, fix)
		}
	}
	for _, leftover := range plan.LeftoverEmpty {
		if err := d.links.RemoveEmptyDir(leftover.Dir); err != nil {
			result.FailedLeftover = append(result.FailedLeftover, healthFix{
				Agent: leftover.Name,
				Name:  leftover.Dir,
				Err:   err,
			})
			continue
		}
		result.RemovedLeftover = append(result.RemovedLeftover, leftover)
	}
	for _, drift := range plan.Drift {
		if err := d.availability.Apply(drift.Skill); err != nil {
			result.FailedDrift = append(result.FailedDrift, healthFix{Name: drift.Skill, Err: err})
			continue
		}
		result.FixedDrift = append(result.FixedDrift, drift.Skill)
	}
	return result
}
