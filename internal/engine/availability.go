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
	agents    models.Agents
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
		agents:    models.ForSkillsDir(skillsDir),
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
	known := a.agents.KnownDirs()
	for _, candidate := range candidates {
		norm := models.NormalizeAgentName(candidate)
		if _, duplicate := seen[norm]; duplicate {
			continue
		}
		seen[norm] = struct{}{}
		if _, skip := excluded[norm]; skip {
			continue
		}
		if _, ok := known[norm]; ok {
			managed = append(managed, norm)
		}
	}
	slices.Sort(managed)
	return managed
}

func (a *Availability) AutomaticallyAvailable() []string {
	return a.agents.Automatic()
}

func (a *Availability) ManageableAgents() []string {
	return slices.Sorted(maps.Keys(a.agents.KnownDirs()))
}

// ValidateManagedAgents normalizes a requested policy mutation and rejects
// unknown or Automatically available Agents for this Scope.
func (a *Availability) ValidateManagedAgents(agents []string) ([]string, error) {
	seen := make(map[string]struct{}, len(agents))
	normalized := make([]string, 0, len(agents))
	known := a.agents.KnownDirs()
	for _, value := range agents {
		agent := models.NormalizeAgentName(value)
		if a.agents.IsAutomatic(agent) {
			return nil, fmt.Errorf("%s is automatically available and does not need an agent policy", agent)
		}
		if _, ok := known[agent]; !ok {
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
	known := a.agents.KnownDirs()
	add := func(names []string) {
		for _, name := range names {
			norm := models.NormalizeAgentName(name)
			if dir, ok := known[norm]; ok {
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
	if _, ok := a.agents.KnownDirs()[norm]; ok {
		return true
	}
	return a.agents.IsAutomatic(norm)
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
	known := slices.Sorted(maps.Keys(a.agents.KnownDirs()))
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
	desired   map[string]struct{}
	known     map[string]string
	skillsDir string
}

// state leaves out every Agent that reserves skillName for its own content:
// that path is the Agent's, never desired Availability, never Drift, and so
// never a path Apply links or ReplaceForeign removes. Claude Code's synced/
// holds the account's claude.ai skills.
func (a *Availability) state(skillName string) availabilityState {
	desired := agentSet(a.ManagedAgents(skillName))
	known := a.agents.KnownDirs()
	for agent := range known {
		if a.agents.IsReserved(agent, skillName) {
			delete(known, agent)
			delete(desired, agent)
		}
	}
	return availabilityState{
		skillName: skillName,
		desired:   desired,
		known:     known,
		skillsDir: a.skillsDir,
	}
}

// ReservedAvailability lists the declared Skills whose Availability selects
// an Agent that reserves the Skill's name. Availability skips each pair.
func (a *Availability) ReservedAvailability() []ReservedAvailability {
	var out []ReservedAvailability
	for _, skill := range slices.Sorted(maps.Keys(a.declaredSkills())) {
		for _, agent := range a.ManagedAgents(skill) {
			if a.agents.IsReserved(agent, skill) {
				out = append(out, ReservedAvailability{Skill: skill, Agent: agent})
			}
		}
	}
	return out
}

func (s availabilityState) isManagedPath(path string) bool {
	return isManagedSkillPath(path, s.skillName, s.skillsDir)
}

func (s availabilityState) drift() AvailabilityDrift {
	var observation AvailabilityDrift
	for agent := range s.desired {
		agentDir, ok := s.known[agent]
		if !ok {
			continue
		}
		linkPath := filepath.Join(agentDir, s.skillName)
		// Stat the Agent directory first. Lstat of a child of a file is
		// ENOTDIR on POSIX but ERROR_PATH_NOT_FOUND on Windows, which Go
		// maps to os.ErrNotExist — the same answer as a missing link.
		// https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--0-499-
		if reason := unusableDirectory(agentDir); reason != "" {
			observation.Unobservable = append(observation.Unobservable, UnobservableAvailabilityPath{
				Agent: agent, Dir: agentDir, Path: linkPath, Err: reason,
			})
			continue
		}
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
		case isManagedSkillCopy(linkPath, s.skillName, s.skillsDir):
			observation.Copies = append(observation.Copies, agent)
		default:
			if _, statErr := os.Stat(linkPath); statErr != nil {
				observation.Broken = append(observation.Broken, agent)
			}
		}
	}
	slices.Sort(observation.Missing)
	slices.Sort(observation.Broken)
	slices.Sort(observation.Copies)
	slices.SortFunc(observation.Foreign, func(a, b ForeignAvailabilityPath) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(observation.Unobservable, func(a, b UnobservableAvailabilityPath) int { return strings.Compare(a.Path, b.Path) })
	return observation
}

func (s availabilityState) apply() ([]string, error) {
	var copied []string
	for agent := range s.desired {
		agentDir, ok := s.known[agent]
		if !ok {
			continue
		}
		linkPath := filepath.Join(agentDir, s.skillName)
		_, err := os.Lstat(linkPath)
		if err == nil && !s.isManagedPath(linkPath) {
			return copied, fmt.Errorf("agent path already exists and is not managed by skills: %s", models.ToTildePath(linkPath))
		}
	}
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		if _, shouldLink := s.desired[agent]; shouldLink {
			if err := s.link(agent); err != nil {
				return copied, err
			}
			// Read the outcome back rather than inferring it: a copy left
			// over from an earlier run is as much a copy as one made now, and
			// the user's question is why these paths are files at all.
			if isManagedSkillCopy(linkPath, s.skillName, s.skillsDir) {
				copied = append(copied, agent)
			}
			continue
		}
		if s.isManagedPath(linkPath) {
			managed, err := removeManagedSkillPath(linkPath, s.skillName, s.skillsDir)
			if !managed || (err != nil && !os.IsNotExist(err)) {
				return copied, fmt.Errorf("failed to remove managed availability path: %s", linkPath)
			}
		}
	}
	slices.Sort(copied)
	return copied, nil
}

// Apply reconciles declared Availability for skill. The returned names are
// Agents whose Availability is a copy rather than a link.
func (a *Availability) Apply(skill string) ([]string, error) {
	return a.state(skill).apply()
}

// AvailabilityOutcome is how applying one Skill's Availability ended. Copied
// is the Agents that hold a copy rather than a link (ADR-0003). Refused marks
// a failure on a path Availability does not manage or cannot inspect, which
// Doctor resolves.
type AvailabilityOutcome struct {
	Skill   string
	Copied  []string
	Err     error
	Refused bool
}

// Reconcile applies declared Availability after a policy change: to each
// named Skill, or to every declared Skill when none is named, skipping any
// not present on the Scope skills directory, since Sync applies a Skill's
// Availability when it Materializes it. One Skill failing does not stop the
// rest.
func (a *Availability) Reconcile(skills ...string) []AvailabilityOutcome {
	if len(skills) == 0 {
		skills = slices.Sorted(maps.Keys(a.declaredSkills()))
	}
	var outcomes []AvailabilityOutcome
	for _, skill := range skills {
		if _, err := os.Lstat(filepath.Join(a.skillsDir, skill)); err != nil {
			continue
		}
		state := a.state(skill)
		refused := state.drift().Refused()
		copied, err := state.apply()
		outcomes = append(outcomes, AvailabilityOutcome{Skill: skill, Copied: copied, Err: err, Refused: err != nil && refused})
	}
	return outcomes
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

// unusableDirectory reports why path cannot be scanned as a directory.
// Absence is not unusable: callers treat a missing directory as empty or as
// missing Availability. A present non-directory, or a Stat error other than
// NotExist, is. Doctor already asked this of the Agent directory; Availability
// observation has to ask it too, because Windows will not.
func unusableDirectory(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		if root := rootPathError(err); root != nil {
			return root.Error()
		}
		return err.Error()
	}
	if info.IsDir() {
		return ""
	}
	return "not a directory"
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
	_, err := a.Apply(skill)
	return err
}

// AvailabilityDrift is declared Availability for one Skill measured against
// the filesystem: the managed Agents that should hold a link but do not, and
// the managed paths that exist but are no longer declared.
type AvailabilityDrift struct {
	Skill        string
	Missing      []string
	Unexpected   []string
	Broken       []string
	Copies       []string
	Foreign      []ForeignAvailabilityPath
	Unobservable []UnobservableAvailabilityPath
}

// Empty reports whether nothing stands between the Skill's declared
// Availability and the filesystem. Copies are a working Availability by
// another mechanism (ADR-0003), not Drift.
func (d AvailabilityDrift) Empty() bool {
	return !d.Reconcilable() && !d.Refused()
}

// Reconcilable reports whether Apply would change the Skill's Availability: a
// link to add, repair, or remove.
func (d AvailabilityDrift) Reconcilable() bool {
	return len(d.Missing) > 0 || len(d.Unexpected) > 0 || len(d.Broken) > 0
}

// Refused reports whether Apply would fail for the Skill: a path it does not
// manage, which it fails closed on, or one it cannot inspect.
func (d AvailabilityDrift) Refused() bool {
	return len(d.Foreign) > 0 || len(d.Unobservable) > 0
}

func (a *Availability) declaredSkills() map[string]struct{} {
	names := make(map[string]struct{})
	for _, repo := range a.cfg.Remote {
		for name := range repo.Skills {
			names[name] = struct{}{}
		}
	}
	for name := range a.cfg.Local {
		names[name] = struct{}{}
	}
	return names
}

// ReservedAvailability is a declared Skill whose name its Agent reserves for
// its own content (models.Agents.IsReserved), so the Skill cannot be available
// to that Agent.
type ReservedAvailability struct {
	Skill string
	Agent string
}
