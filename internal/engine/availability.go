package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

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
	candidates := slices.Concat(defaults, override.Include)
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
	slices.Sort(managed)
	return managed
}

func (a *Availability) AutomaticallyAvailable() []string {
	return slices.Clone(a.automatic)
}

func (a *Availability) ManageableAgents() []string {
	return slices.Sorted(maps.Keys(a.known))
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
	slices.Sort(normalized)
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
	for _, skill := range slices.Sorted(maps.Keys(a.cfg.Settings.Availability)) {
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
	known := slices.Sorted(maps.Keys(a.known))
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

// availabilityState is desired Availability vs disk for one Skill.
type availabilityState struct {
	skillName string
	skillsDir string
	desired   map[string]struct{}
	known     map[string]string
	links     AgentLinkManager
}

func (a *Availability) state(skillName string) availabilityState {
	return availabilityState{
		skillName: skillName,
		skillsDir: a.skillsDir,
		desired:   agentSet(a.ManagedAgents(skillName)),
		known:     a.known,
		links:     a.links,
	}
}

// isManagedPath reports whether path is a managed symlink or a managed copy
// of this Skill (the Windows fallback when symlinks aren't available).
func (s availabilityState) isManagedPath(path string) bool {
	return s.links.IsManagedLink(path, s.skillName) || s.links.IsManagedCopy(path, s.skillName)
}

func (s availabilityState) drift() (missing, unexpected []string) {
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		_, want := s.desired[agent]
		if want {
			_, err := os.Lstat(linkPath)
			if os.IsNotExist(err) || (err == nil && !s.isManagedPath(linkPath)) {
				missing = append(missing, agent)
			}
			continue
		}
		if s.isManagedPath(linkPath) {
			unexpected = append(unexpected, agent)
		}
	}
	slices.Sort(missing)
	slices.Sort(unexpected)
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
		if err == nil && !s.isManagedPath(linkPath) {
			return fmt.Errorf("agent path already exists and is not managed by skills: %s", linkPath)
		}
	}
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		if _, shouldLink := s.desired[agent]; shouldLink {
			if s.links.IsManagedCopy(linkPath, s.skillName) {
				if err := replaceManagedCopy(filepath.Join(s.skillsDir, s.skillName), linkPath); err != nil {
					return err
				}
				continue
			}
			if _, err := s.links.EnsureLink(s.skillName, agent); err != nil {
				return err
			}
			continue
		}
		if s.isManagedPath(linkPath) && !s.links.RemoveManagedPath(linkPath, s.skillName) {
			return fmt.Errorf("failed to remove managed availability path: %s", linkPath)
		}
	}
	return nil
}

func (a *Availability) Apply(skill string) error {
	return a.state(skill).apply()
}

// AvailabilityDrift is declared Availability for one Skill measured against
// the filesystem: the managed Agents that should hold a link but do not, and
// the managed paths that exist but are no longer declared.
type AvailabilityDrift struct {
	Skill      string
	Missing    []string
	Unexpected []string
}

func (d AvailabilityDrift) Empty() bool {
	return len(d.Missing) == 0 && len(d.Unexpected) == 0
}

// ObserveAvailability reports Drift for one Skill without touching the
// filesystem. It is the single comparison behind every caller that needs to
// know about Drift before deciding whether to act on it.
func (a *Availability) ObserveAvailability(skill string) AvailabilityDrift {
	missing, unexpected := a.state(skill).drift()
	return AvailabilityDrift{Skill: skill, Missing: missing, Unexpected: unexpected}
}

// applyDeclaredAvailability applies Availability, or emits Drift when dry-run.
func (a *Availability) applyDeclared(name, source string, dryRun bool, emit func(SyncEvent)) error {
	if dryRun {
		drift := a.ObserveAvailability(name)
		if drift.Empty() {
			return nil
		}
		if emit != nil {
			emit(SyncEvent{Kind: SyncWouldDrift, Source: source, Skill: name, Missing: drift.Missing, Unexpected: drift.Unexpected})
		}
		return nil
	}
	if err := a.Apply(name); err != nil {
		return fmt.Errorf("failed to apply availability for %s: %w", name, err)
	}
	return nil
}
