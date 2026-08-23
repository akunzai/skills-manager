package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

func agentSet(agents []string) map[string]struct{} {
	set := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		set[models.NormalizeAgentName(agent)] = struct{}{}
	}
	return set
}

// Availability owns Config policy interpretation and managed filesystem state
// for one Scope. Callers create it once and reuse it across Skills.
type Availability struct {
	cfg       *config.Config
	skillsDir string
	known     map[string]string
	automatic []string
	links     AgentLinkManager
}

// Field values for UnknownAgentReference match the skills.json key each names.
const (
	AgentRefDefaultAgents = "defaultAgents"
	AgentRefInclude       = "include"
	AgentRefExclude       = "exclude"
)

// UnknownAgentReference is a policy entry naming an Agent this Scope does not
// recognize. It remains declared but is excluded from filesystem mutation.
type UnknownAgentReference struct {
	Skill string // empty for cfg.Settings.DefaultAgents
	Field string // one of the AgentRef* constants
	Agent string
}

func NewAvailability(cfg *config.Config, skillsDir string) *Availability {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	return &Availability{
		cfg:       cfg,
		skillsDir: skillsDir,
		known:     models.GetAgentsForSkillsDir(skillsDir),
		automatic: models.GetAutomaticallyAvailableAgents(skillsDir),
		links:     NewAgentLinkManager(skillsDir),
	}
}

// ManagedAgents is the linkable Agents selected by defaults, plus Include,
// minus Exclude. Automatically available Agents are reported separately.
func (a *Availability) ManagedAgents(skill string) []string {
	defaults := a.cfg.Settings.DefaultAgents
	if len(defaults) == 0 {
		defaults = []string{"claude"}
	}
	override := a.cfg.Settings.Availability[skill]
	excluded := agentSet(override.Exclude)
	candidates := append(append([]string{}, defaults...), override.Include...)
	seen := make(map[string]struct{}, len(candidates))
	managed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		norm := models.NormalizeAgentName(candidate)
		if _, duplicate := seen[norm]; duplicate {
			continue
		}
		seen[norm] = struct{}{}
		if _, skip := excluded[norm]; skip {
			continue
		}
		if _, known := a.known[norm]; known {
			managed = append(managed, norm)
		}
	}
	sort.Strings(managed)
	return managed
}

func (a *Availability) AutomaticallyAvailable() []string {
	return append([]string{}, a.automatic...)
}

func (a *Availability) ManageableAgents() []string {
	agents := make([]string, 0, len(a.known))
	for agent := range a.known {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	return agents
}

// ValidateManagedAgents normalizes a requested policy mutation and rejects
// unknown or Automatically available Agents for this Scope.
func (a *Availability) ValidateManagedAgents(agents []string) ([]string, error) {
	seen := make(map[string]struct{}, len(agents))
	normalized := make([]string, 0, len(agents))
	for _, value := range agents {
		agent := models.NormalizeAgentName(value)
		if models.IsUniversalAgent(agent, a.skillsDir) {
			return nil, fmt.Errorf("%s is automatically available and does not need an agent policy", agent)
		}
		if _, ok := a.known[agent]; !ok {
			return nil, fmt.Errorf("unknown agent %q for this scope", value)
		}
		if _, duplicate := seen[agent]; duplicate {
			continue
		}
		seen[agent] = struct{}{}
		normalized = append(normalized, agent)
	}
	sort.Strings(normalized)
	return normalized, nil
}

// ConfiguredAgentDirs is every managed Agent directory referenced by defaults
// or a per-Skill Include. Exclude does not make the directory unmanaged.
func (a *Availability) ConfiguredAgentDirs() map[string]string {
	out := make(map[string]string)
	add := func(names []string) {
		for _, name := range names {
			norm := models.NormalizeAgentName(name)
			if dir, ok := a.known[norm]; ok {
				out[norm] = dir
			}
		}
	}
	defaults := a.cfg.Settings.DefaultAgents
	if len(defaults) == 0 {
		defaults = []string{"claude"}
	}
	add(defaults)
	for _, override := range a.cfg.Settings.Availability {
		add(override.Include)
	}
	return out
}

func (a *Availability) recognized(name string) bool {
	norm := models.NormalizeAgentName(name)
	if _, ok := a.known[norm]; ok {
		return true
	}
	return models.IsUniversalAgent(norm, a.skillsDir)
}

func (a *Availability) UnknownAgentReferences() []UnknownAgentReference {
	var refs []UnknownAgentReference
	for _, agent := range a.cfg.Settings.DefaultAgents {
		if !a.recognized(agent) {
			refs = append(refs, UnknownAgentReference{Field: AgentRefDefaultAgents, Agent: models.NormalizeAgentName(agent)})
		}
	}
	skills := make([]string, 0, len(a.cfg.Settings.Availability))
	for skill := range a.cfg.Settings.Availability {
		skills = append(skills, skill)
	}
	sort.Strings(skills)
	for _, skill := range skills {
		override := a.cfg.Settings.Availability[skill]
		for _, agent := range override.Include {
			if !a.recognized(agent) {
				refs = append(refs, UnknownAgentReference{Skill: skill, Field: AgentRefInclude, Agent: models.NormalizeAgentName(agent)})
			}
		}
		for _, agent := range override.Exclude {
			if !a.recognized(agent) {
				refs = append(refs, UnknownAgentReference{Skill: skill, Field: AgentRefExclude, Agent: models.NormalizeAgentName(agent)})
			}
		}
	}
	return refs
}

func (a *Availability) Include(skill string, agents ...string) error {
	agents, err := a.ValidateManagedAgents(agents)
	if err != nil {
		return err
	}
	a.ensureOverrides()
	override := a.cfg.Settings.Availability[skill]
	override.Include = append(override.Include, agents...)
	override.Exclude = removeAgents(override.Exclude, agents)
	a.cfg.Settings.Availability[skill] = override
	return config.NormalizeAvailability(a.cfg)
}

func (a *Availability) Exclude(skill string, agents ...string) error {
	agents, err := a.ValidateManagedAgents(agents)
	if err != nil {
		return err
	}
	a.ensureOverrides()
	override := a.cfg.Settings.Availability[skill]
	override.Exclude = append(override.Exclude, agents...)
	override.Include = removeAgents(override.Include, agents)
	a.cfg.Settings.Availability[skill] = override
	return config.NormalizeAvailability(a.cfg)
}

func (a *Availability) Reset(skill string, agents ...string) error {
	agents, err := a.ValidateManagedAgents(agents)
	if err != nil {
		return err
	}
	a.ensureOverrides()
	override := a.cfg.Settings.Availability[skill]
	override.Include = removeAgents(override.Include, agents)
	override.Exclude = removeAgents(override.Exclude, agents)
	a.cfg.Settings.Availability[skill] = override
	return config.NormalizeAvailability(a.cfg)
}

func (a *Availability) FollowDefaults(skill string) {
	a.ensureOverrides()
	delete(a.cfg.Settings.Availability, skill)
}

func (a *Availability) ensureOverrides() {
	if a.cfg.Settings.Availability == nil {
		a.cfg.Settings.Availability = make(map[string]config.AvailabilityOverride)
	}
}

func removeAgents(current, removed []string) []string {
	remove := agentSet(removed)
	kept := current[:0]
	for _, agent := range current {
		if _, found := remove[models.NormalizeAgentName(agent)]; !found {
			kept = append(kept, agent)
		}
	}
	return kept
}

// SetManagedAgents stores the smallest Include/Exclude override that produces
// selected relative to the Scope defaults.
func (a *Availability) SetManagedAgents(skill string, selected []string) error {
	selected, err := a.ValidateManagedAgents(selected)
	if err != nil {
		return err
	}
	selectedSet := agentSet(selected)
	a.FollowDefaults(skill)
	defaults := agentSet(a.ManagedAgents(skill))
	known := make([]string, 0, len(a.known))
	for agent := range a.known {
		known = append(known, agent)
	}
	sort.Strings(known)
	var include, exclude []string
	for _, agent := range known {
		_, chosen := selectedSet[agent]
		_, defaulted := defaults[agent]
		if chosen && !defaulted {
			include = append(include, agent)
		}
		if !chosen && defaulted {
			exclude = append(exclude, agent)
		}
	}
	if err := a.Include(skill, include...); err != nil {
		return err
	}
	return a.Exclude(skill, exclude...)
}

func isManagedAvailabilityPath(path, skillName, skillsDir string) bool {
	return IsManagedSkillLink(path, skillName, skillsDir) || IsManagedSkillCopy(path, skillName, skillsDir)
}

// availabilityState is desired Availability vs disk for one Skill.
type availabilityState struct {
	skillName string
	skillsDir string
	desired   map[string]struct{}
	known     map[string]string
}

func (a *Availability) state(skillName string) availabilityState {
	return availabilityState{
		skillName: skillName,
		skillsDir: a.skillsDir,
		desired:   agentSet(a.ManagedAgents(skillName)),
		known:     a.known,
	}
}

func (s availabilityState) drift() (missing, unexpected []string) {
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		_, want := s.desired[agent]
		if want {
			_, err := os.Lstat(linkPath)
			if os.IsNotExist(err) || (err == nil && !isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir)) {
				missing = append(missing, agent)
			}
			continue
		}
		if isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) {
			unexpected = append(unexpected, agent)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}

func (s availabilityState) apply() error {
	for agent := range s.desired {
		agentDir, ok := s.known[agent]
		if !ok {
			continue
		}
		linkPath := filepath.Join(agentDir, s.skillName)
		_, err := os.Lstat(linkPath)
		if err == nil && !isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) {
			return fmt.Errorf("agent path already exists and is not managed by skills: %s", linkPath)
		}
	}
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		if _, shouldLink := s.desired[agent]; shouldLink {
			if IsManagedSkillCopy(linkPath, s.skillName, s.skillsDir) {
				if err := replaceManagedCopy(filepath.Join(s.skillsDir, s.skillName), linkPath); err != nil {
					return err
				}
				continue
			}
			if _, err := EnsureAgentSymlink(s.skillName, agent, s.skillsDir); err != nil {
				return err
			}
			continue
		}
		if isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) && !RemoveManagedSkillPath(linkPath, s.skillName, s.skillsDir) {
			return fmt.Errorf("failed to remove managed availability path: %s", linkPath)
		}
	}
	return nil
}

func (a *Availability) Apply(skill string) error {
	return a.state(skill).apply()
}

func (a *Availability) Drift(skill string) (missing, unexpected []string) {
	return a.state(skill).drift()
}

// applyDeclaredAvailability applies Availability, or emits Drift when dry-run.
func (a *Availability) applyDeclared(name, source string, dryRun bool, emit func(SyncEvent)) error {
	if dryRun {
		missing, unexpected := a.Drift(name)
		if len(missing) == 0 && len(unexpected) == 0 {
			return nil
		}
		if emit != nil {
			emit(SyncEvent{Kind: SyncWouldDrift, Source: source, Skill: name, Missing: missing, Unexpected: unexpected})
		}
		return nil
	}
	if err := a.Apply(name); err != nil {
		return fmt.Errorf("failed to apply availability for %s: %w", name, err)
	}
	return nil
}
