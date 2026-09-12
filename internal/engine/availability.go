package engine

import (
	"cmp"
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
	desired   map[string]struct{}
	known     map[string]string
	skillsDir string
}

func (a *Availability) state(skillName string) availabilityState {
	return availabilityState{
		skillName: skillName,
		desired:   agentSet(a.ManagedAgents(skillName)),
		known:     a.known,
		skillsDir: a.skillsDir,
	}
}

func (s availabilityState) isManagedPath(path string) bool {
	return isManagedSkillPath(path, s.skillName, s.skillsDir)
}

func (s availabilityState) drift() AvailabilityDrift {
	var observation AvailabilityDrift
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		_, want := s.desired[agent]
		if want {
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
			continue
		}
		if s.isManagedPath(linkPath) {
			observation.Unexpected = append(observation.Unexpected, agent)
		}
	}
	slices.Sort(observation.Missing)
	slices.Sort(observation.Unexpected)
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
			if _, err := ensureAgentSymlink(s.skillName, agent, s.skillsDir); err != nil {
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

func (d AvailabilityDrift) Empty() bool {
	return len(d.Missing) == 0 && len(d.Unexpected) == 0 && len(d.Broken) == 0 && len(d.Foreign) == 0 && len(d.Unobservable) == 0
}

// ObserveAvailability reports Drift for one Skill without touching the
// filesystem. It is the single comparison behind every caller that needs to
// know about Drift before deciding whether to act on it.
func (a *Availability) ObserveAvailability(skill string) AvailabilityDrift {
	observation := a.state(skill).drift()
	observation.Skill = skill
	return observation
}

// LeftoverPath is one managed Availability path that declared Availability
// does not call for.
type LeftoverPath struct {
	Agent    string
	Skill    string
	Path     string
	Dangling bool
}

// LeftoverOccupancy is leftover occupancy observed once. ApplyLeftover takes
// this value; filtering it is a pure transformation, not a second observation.
type LeftoverOccupancy struct {
	Paths []LeftoverPath
	Empty []AgentDir
}

func (o LeftoverOccupancy) WithoutEmpty() LeftoverOccupancy {
	return LeftoverOccupancy{Paths: slices.Clone(o.Paths)}
}

func (o LeftoverOccupancy) ForSkills(names []string) LeftoverOccupancy {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	var paths []LeftoverPath
	for _, path := range o.Paths {
		if _, ok := want[path.Skill]; ok {
			paths = append(paths, path)
		}
	}
	return LeftoverOccupancy{Paths: paths, Empty: slices.Clone(o.Empty)}
}

// LeftoverFailure is one leftover path ApplyLeftover could not remove.
type LeftoverFailure struct {
	Path LeftoverPath
	Err  error
}

// LeftoverEmptyFailure is one leftover empty Agent directory ApplyLeftover
// could not remove.
type LeftoverEmptyFailure struct {
	Dir AgentDir
	Err error
}

// LeftoverApplyResult is what ApplyLeftover did with one occupancy snapshot.
type LeftoverApplyResult struct {
	RemovedPaths []LeftoverPath
	SkippedPaths []LeftoverPath
	FailedPaths  []LeftoverFailure
	RemovedEmpty []AgentDir
	FailedEmpty  []LeftoverEmptyFailure
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

// ObserveLeftover reports leftover occupancy on Agent directories: managed
// paths on automatically available Agents, managed paths for Skills Config
// does not declare, and empty Agent directories the current policy does not
// select.
func (a *Availability) ObserveLeftover() LeftoverOccupancy {
	declared := a.declaredSkills()
	var occupancy LeftoverOccupancy
	seen := make(map[string]struct{})
	addDir := func(agent, dir string, include func(string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || !include(name) {
				continue
			}
			path := filepath.Join(dir, name)
			if !isManagedSkillPath(path, name, a.skillsDir) {
				continue
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			_, err := os.Stat(path)
			occupancy.Paths = append(occupancy.Paths, LeftoverPath{
				Agent: agent, Skill: name, Path: path, Dangling: err != nil,
			})
		}
	}
	for agent, dir := range models.GetUniversalAgentSkillDirs(a.skillsDir) {
		if _, known := a.known[agent]; known {
			continue
		}
		addDir(agent, dir, func(string) bool { return true })
	}
	for agent, dir := range a.known {
		addDir(agent, dir, func(name string) bool {
			_, ok := declared[name]
			return !ok
		})
	}
	slices.SortFunc(occupancy.Paths, func(a, b LeftoverPath) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Skill, b.Skill), cmp.Compare(a.Path, b.Path))
	})
	occupancy.Empty = leftoverEmptyAgentDirs(a.known, a.ConfiguredAgentDirs())
	return occupancy
}

// ApplyLeftover removes the leftover occupancy in occupancy, revalidating each
// path immediately before deletion.
func (a *Availability) ApplyLeftover(occupancy LeftoverOccupancy) LeftoverApplyResult {
	result := LeftoverApplyResult{}
	for _, path := range occupancy.Paths {
		managed, err := removeManagedSkillPath(path.Path, path.Skill, a.skillsDir)
		if !managed {
			result.SkippedPaths = append(result.SkippedPaths, path)
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			result.FailedPaths = append(result.FailedPaths, LeftoverFailure{Path: path, Err: err})
			continue
		}
		result.RemovedPaths = append(result.RemovedPaths, path)
	}
	stopAt := models.ScopeRoot(a.skillsDir)
	for _, empty := range occupancy.Empty {
		if err := removeEmptyAgentDir(empty.Dir, stopAt); err != nil {
			result.FailedEmpty = append(result.FailedEmpty, LeftoverEmptyFailure{Dir: empty, Err: err})
			continue
		}
		result.RemovedEmpty = append(result.RemovedEmpty, empty)
	}
	return result
}
