package engine

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// ManagedAgentPath is one managed Availability path on an Agent directory.
type ManagedAgentPath struct {
	Agent string
	Skill string
	Path  string
}

// LeftoverPath is one managed Availability path that declared Availability
// does not call for.
type LeftoverPath struct {
	ManagedAgentPath
	Dangling bool
	Repair   ItemRepair
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
	SkippedEmpty []AgentDir
	FailedEmpty  []LeftoverEmptyFailure
}

// Occupancy is a Scope's Agent directory occupancy, observed once (see
// CONTEXT.md): the health of each configured Agent directory, the Leftover
// occupancy across every Agent directory, and the Unexpected managed paths of
// declared Skills. Each path lands in at most one of them, and Drift reads a
// Skill's Unexpected paths from here rather than scanning again, so Sync,
// Doctor, prune and rm classify a path under the same rule.
type Occupancy struct {
	Agents     []AgentHealth
	Leftover   LeftoverOccupancy
	Unexpected []ManagedAgentPath

	availability *Availability
}

// Drift is skill's Availability Drift: its Unexpected paths from this
// observation, and what each of its desired Agent paths holds now.
func (o Occupancy) Drift(skill string) AvailabilityDrift {
	drift := o.availability.state(skill).drift()
	drift.Skill = skill
	for _, path := range o.Unexpected {
		if path.Skill == skill {
			drift.Unexpected = append(drift.Unexpected, path.Agent)
		}
	}
	return drift
}

// ObserveOccupancy reads each known and leftover-root Agent directory once and
// classifies its entries under one set of rules. Leftover occupancy is managed
// paths on Automatically available Agents, managed paths for Skills Config
// does not declare, managed paths on a name the Agent reserves, and empty
// Agent directories the current policy does not select. Unexpected is the
// Drift half of the same scan: managed paths of declared Skills, whatever
// their master's state, on known Agents their Availability does not select. A
// path on a leftover root is leftover occupancy, never also Unexpected. Agent
// health covers configured directories only, and leaves out a real directory
// on a path Availability selects for a declared Skill: Drift reports that one
// as Foreign.
func (a *Availability) ObserveOccupancy() Occupancy {
	type listing struct {
		entries []os.DirEntry
		err     error
	}
	listings := make(map[string]listing)
	readDir := func(dir string) ([]os.DirEntry, error) {
		if l, ok := listings[dir]; ok {
			return l.entries, l.err
		}
		entries, err := os.ReadDir(dir)
		listings[dir] = listing{entries, err}
		return entries, err
	}

	observation := Occupancy{availability: a}
	declared := a.declaredSkills()
	knownDirs := a.agents.KnownDirs()
	configured := a.ConfiguredAgentDirs()
	for _, agent := range slices.Sorted(maps.Keys(configured)) {
		dir := configured[agent]
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err == nil && !info.IsDir() {
			observation.Agents = append(observation.Agents, AgentHealth{Name: agent, Dir: dir, Unusable: "not a directory"})
			continue
		}
		entries, _ := readDir(dir)
		observation.Agents = append(observation.Agents, a.agentHealth(agent, dir, entries, declared))
	}

	seen := make(map[string]struct{})
	addPaths := func(agent, dir string, leftoverRoot bool) {
		entries, err := readDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			// An Agent's reserved entry is its own content, unless this tool
			// put a managed path there (before v0.15.0 a declared Skill of
			// that name could be linked). Availability never selects that
			// path now, so such a path is leftover occupancy; anything else
			// on the name is skipped below as not managed.
			reserved := a.agents.IsReserved(agent, name)
			_, isDeclared := declared[name]
			if isDeclared && !leftoverRoot && !reserved && slices.Contains(a.ManagedAgents(name), agent) {
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
			if isDeclared && !leftoverRoot && !reserved {
				observation.Unexpected = append(observation.Unexpected, ManagedAgentPath{Agent: agent, Skill: name, Path: path})
				continue
			}
			_, err := os.Stat(path)
			observation.Leftover.Paths = append(observation.Leftover.Paths, LeftoverPath{
				Agent: agent, Skill: name, Path: path, Dangling: err != nil,
			})
		}
	}
	// Leftover roots first, so a path on a directory that is both a root and
	// a known Agent directory is leftover occupancy.
	for agent, dir := range a.agents.LeftoverRoots() {
		addPaths(agent, dir, true)
	}
	for agent, dir := range knownDirs {
		addPaths(agent, dir, false)
	}
	slices.SortFunc(observation.Leftover.Paths, func(a, b LeftoverPath) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Skill, b.Skill), cmp.Compare(a.Path, b.Path))
	})
	slices.SortFunc(observation.Unexpected, func(a, b ManagedAgentPath) int {
		return cmp.Or(cmp.Compare(a.Skill, b.Skill), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Path, b.Path))
	})

	for agent, dir := range knownDirs {
		if _, ok := configured[agent]; ok {
			continue
		}
		// Dot entries (e.g. .DS_Store) are not Skills. A reserved entry is
		// the Agent's own content, so it keeps the directory.
		entries, err := readDir(dir)
		if err == nil && !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !strings.HasPrefix(e.Name(), ".") }) {
			observation.Leftover.Empty = append(observation.Leftover.Empty, AgentDir{Name: agent, Dir: dir})
		}
	}
	slices.SortFunc(observation.Leftover.Empty, func(a, b AgentDir) int { return cmp.Compare(a.Name, b.Name) })
	return observation
}

// agentHealth classifies the entries of one configured Agent directory:
// dangling links this tool never created, and real directories that are
// neither a copy this tool made, reserved by the Agent, nor on a path
// Availability selects for a declared Skill.
func (a *Availability) agentHealth(agent, dir string, entries []os.DirEntry, declared map[string]struct{}) AgentHealth {
	health := AgentHealth{Name: agent, Dir: dir}
	for _, entry := range entries {
		name := entry.Name()
		if a.agents.IsReserved(agent, name) {
			continue
		}
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil {
			continue
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			// A dangling managed link is Drift or leftover occupancy,
			// reported with the Skill it belongs to.
			if _, err := os.Stat(path); err != nil && !isManagedSkillLink(path, name, a.skillsDir) {
				health.UnmanagedBroken = append(health.UnmanagedBroken, name)
			}
		case fi.IsDir() && !strings.HasPrefix(name, "."):
			if !isManagedSkillCopy(path, name, a.skillsDir) && !a.selects(declared, agent, name) {
				health.Physical = append(health.Physical, name)
			}
		}
	}
	return health
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
		// removeEmptyAgentDir leaves a directory that is no longer empty and
		// returns nil, so ask first: whatever changed since the observation
		// owns the directory now.
		if isEmpty, err := isDirEffectivelyEmpty(empty.Dir); err != nil || !isEmpty {
			result.SkippedEmpty = append(result.SkippedEmpty, empty)
			continue
		}
		if err := removeEmptyAgentDir(empty.Dir, stopAt); err != nil {
			result.FailedEmpty = append(result.FailedEmpty, LeftoverEmptyFailure{Dir: empty, Err: err})
			continue
		}
		result.RemovedEmpty = append(result.RemovedEmpty, empty)
	}
	return result
}

// selects reports whether declared Availability puts skill on agent. Such a
// path is Drift's to report, whatever it holds.
func (a *Availability) selects(declared map[string]struct{}, agent, skill string) bool {
	if _, ok := declared[skill]; !ok {
		return false
	}
	return slices.Contains(a.ManagedAgents(skill), agent)
}
