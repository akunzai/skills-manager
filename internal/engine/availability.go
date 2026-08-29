package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

func (s availabilityState) drift() AvailabilityDrift {
	var observation AvailabilityDrift
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		_, want := s.desired[agent]
		if want {
			_, err := os.Lstat(linkPath)
			switch {
			case os.IsNotExist(err):
				observation.Missing = append(observation.Missing, agent)
			case err != nil:
				// Anything but ENOENT means the answer is unknown, not "no
				// Drift". Silently dropping it let a Scope whose Agent
				// directory was a regular file — Lstat returns ENOTDIR —
				// report as healthy while nothing was linked at all.
				observation.Unobservable = append(observation.Unobservable, describeUnobservableAvailabilityPath(agent, agentDir, linkPath, err))
			case !s.isManagedPath(linkPath):
				observation.Foreign = append(observation.Foreign, describeForeignAvailabilityPath(agent, linkPath))
			}
			continue
		}
		if s.isManagedPath(linkPath) {
			observation.Unexpected = append(observation.Unexpected, agent)
		}
	}
	slices.Sort(observation.Missing)
	slices.Sort(observation.Unexpected)
	slices.SortFunc(observation.Foreign, func(a, b ForeignAvailabilityPath) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(observation.Unobservable, func(a, b UnobservableAvailabilityPath) int { return strings.Compare(a.Path, b.Path) })
	return observation
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
			return fmt.Errorf("agent path already exists and is not managed by skills: %s", models.ToTildePath(linkPath))
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

// ForeignAvailabilityPath is an existing Agent path that does not belong to
// this Scope. Target is populated for symlinks so a repair prompt can show
// exactly what would be replaced.
type ForeignAvailabilityPath struct {
	Agent  string
	Path   string
	Kind   ForeignAvailabilityPathKind
	Target string
}

type ForeignAvailabilityPathKind string

const (
	ForeignAvailabilityFile      ForeignAvailabilityPathKind = "file"
	ForeignAvailabilityDirectory ForeignAvailabilityPathKind = "directory"
	ForeignAvailabilitySymlink   ForeignAvailabilityPathKind = "symlink"
)

func (p ForeignAvailabilityPath) Detail() string {
	detail := string(p.Kind)
	if p.Target != "" {
		detail += " -> " + models.ToTildePath(p.Target)
	}
	return detail
}

// UnobservableAvailabilityPath is an Availability path whose state could not
// be read at all. It is deliberately not a ForeignAvailabilityPath: doctor
// offers to replace a foreign path, and offering to replace something it
// cannot even stat would propose a repair that is bound to fail. Dir is the
// Agent directory the path lives in, which is usually what is actually wrong.
type UnobservableAvailabilityPath struct {
	Agent string
	Dir   string
	Path  string
	Err   string
}

func describeUnobservableAvailabilityPath(agent, agentDir, path string, err error) UnobservableAvailabilityPath {
	reason := err.Error()
	if root := rootPathError(err); root != nil {
		reason = root.Error()
	}
	return UnobservableAvailabilityPath{Agent: agent, Dir: agentDir, Path: path, Err: reason}
}

func describeForeignAvailabilityPath(agent, path string) ForeignAvailabilityPath {
	foreign := ForeignAvailabilityPath{Agent: agent, Path: path, Kind: ForeignAvailabilityFile}
	info, err := os.Lstat(path)
	if err != nil {
		return foreign
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		foreign.Kind = ForeignAvailabilitySymlink
		foreign.Target, _ = os.Readlink(path)
	case info.IsDir():
		foreign.Kind = ForeignAvailabilityDirectory
	}
	return foreign
}

// ReplaceForeign removes only paths that still match the diagnosed values,
// then applies declared Availability. It is reserved for Doctor after an
// interactive confirmation; ordinary Apply remains fail-closed.
func (a *Availability) ReplaceForeign(skill string, diagnosed []ForeignAvailabilityPath) error {
	for _, expected := range diagnosed {
		current := describeForeignAvailabilityPath(expected.Agent, expected.Path)
		if current != expected {
			return fmt.Errorf("agent path changed after confirmation: %s", models.ToTildePath(expected.Path))
		}
	}
	for _, foreign := range diagnosed {
		if err := os.RemoveAll(foreign.Path); err != nil {
			return fmt.Errorf("remove unmanaged agent path %s: %w", models.ToTildePath(foreign.Path), err)
		}
	}
	return a.Apply(skill)
}

// AvailabilityDrift is declared Availability for one Skill measured against
// the filesystem: the managed Agents that should hold a link but do not, and
// the managed paths that exist but are no longer declared.
type AvailabilityDrift struct {
	Skill        string
	Missing      []string
	Unexpected   []string
	Foreign      []ForeignAvailabilityPath
	Unobservable []UnobservableAvailabilityPath
}

func (d AvailabilityDrift) Empty() bool {
	return len(d.Missing) == 0 && len(d.Unexpected) == 0 && len(d.Foreign) == 0 && len(d.Unobservable) == 0
}

// ObserveAvailability reports Drift for one Skill without touching the
// filesystem. It is the single comparison behind every caller that needs to
// know about Drift before deciding whether to act on it.
func (a *Availability) ObserveAvailability(skill string) AvailabilityDrift {
	observation := a.state(skill).drift()
	observation.Skill = skill
	return observation
}
