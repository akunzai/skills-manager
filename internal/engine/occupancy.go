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

// LeftoverOccupancy is leftover occupancy observed once. RemoveLeftover takes
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

// UnmanagedAgentPath is an entry on a linkable Agent directory that this tool
// did not create and declared Availability does not select: a real directory
// (an Unmanaged directory) or a symlink the user placed there, which may
// dangle.
type UnmanagedAgentPath struct {
	Agent   string
	Name    string
	Path    string
	Symlink bool
	// Dangling is a symlink whose target is gone.
	Dangling bool
}

// Occupancy is a Scope's Agent directory occupancy, observed once (see
// CONTEXT.md): the health of each configured Agent directory, the Leftover
// occupancy across every Agent directory, the Unexpected managed paths of
// declared Skills, and the Unmanaged paths on every linkable Agent directory.
// Each path lands in at most one of Leftover, Unexpected and Unmanaged, and
// Drift reads a Skill's Unexpected paths from here rather than scanning again,
// so Sync, Doctor, prune, rm and adopt classify a path under the same rule.
// Agent health is a per-directory view of Unmanaged on configured Agent
// directories: its Physical entries are the real directories, and its
// UnmanagedBroken ones the dangling symlinks, so no path is reported both as
// Agent health and as Drift.
type Occupancy struct {
	Agents     []AgentHealth
	Leftover   LeftoverOccupancy
	Unexpected []ManagedAgentPath
	Unmanaged  []UnmanagedAgentPath

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

	seenUnmanaged := make(map[string]struct{})
	for agent, dir := range knownDirs {
		entries, err := readDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if _, dup := seenUnmanaged[path]; dup {
				continue
			}
			if symlink, ok := a.unmanaged(agent, path, declared); ok {
				seenUnmanaged[path] = struct{}{}
				dangling := false
				if symlink {
					_, err := os.Stat(path)
					dangling = err != nil
				}
				observation.Unmanaged = append(observation.Unmanaged, UnmanagedAgentPath{Agent: agent, Name: entry.Name(), Path: path, Symlink: symlink, Dangling: dangling})
			}
		}
	}
	slices.SortFunc(observation.Unmanaged, func(a, b UnmanagedAgentPath) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Name, b.Name))
	})

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
		observation.Agents = append(observation.Agents, observation.agentHealth(agent, dir))
	}

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

// agentHealth is the Unmanaged paths on one configured Agent directory, as
// Doctor words them per directory. A directory several Agents share is
// grouped by where the path is, not by the Agent it was observed under.
func (o Occupancy) agentHealth(agent, dir string) AgentHealth {
	health := AgentHealth{Name: agent, Dir: dir}
	for _, path := range o.Unmanaged {
		switch {
		case filepath.Dir(path.Path) != dir:
		case !path.Symlink:
			health.Physical = append(health.Physical, path.Name)
		case path.Dangling:
			health.UnmanagedBroken = append(health.UnmanagedBroken, path.Name)
		}
	}
	slices.Sort(health.Physical)
	slices.Sort(health.UnmanagedBroken)
	return health
}

// unmanaged is the one rule for an Unmanaged path on agent's directory: a
// real directory or a symlink, not a dot entry, not a name the Agent reserves,
// not Availability this tool made, and not on a path declared Availability
// selects (Drift reports that one as Foreign). It reports whether the path is
// a symlink.
func (a *Availability) unmanaged(agent, path string, declared map[string]struct{}) (symlink, ok bool) {
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") || a.agents.IsReserved(agent, name) || a.selects(declared, agent, name) {
		return false, false
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return false, false
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return true, !isManagedSkillLink(path, name, a.skillsDir)
	case fi.IsDir():
		return false, !isManagedSkillCopy(path, name, a.skillsDir)
	}
	return false, false
}

// RemoveLeftover removes the leftover occupancy in occupancy and returns it
// with each item's Repair recorded.
func (a *Availability) RemoveLeftover(occupancy LeftoverOccupancy) LeftoverOccupancy {
	removed := LeftoverOccupancy{Paths: slices.Clone(occupancy.Paths), Empty: slices.Clone(occupancy.Empty)}
	paths := make([]ManagedAgentPath, len(removed.Paths))
	for i, path := range removed.Paths {
		paths[i] = path.ManagedAgentPath
	}
	for i, repair := range removeManagedPaths(a.skillsDir, paths) {
		removed.Paths[i].Repair = repair
	}
	for i, repair := range removeEmptyAgentDirs(a.skillsDir, removed.Empty) {
		removed.Empty[i].Repair = repair
	}
	return removed
}

// removeManagedPaths is the one step that removes managed paths from Agent
// directories. Each path is checked again immediately before removal, since
// it can change while a confirmation prompt is open: one that is no longer a
// managed path for its Skill is Skipped and left alone. The repairs are in
// the order of paths.
func removeManagedPaths(skillsDir string, paths []ManagedAgentPath) []ItemRepair {
	repairs := make([]ItemRepair, len(paths))
	for i, path := range paths {
		managed, err := removeManagedSkillPath(path.Path, path.Skill, skillsDir)
		switch {
		case !managed:
			repairs[i] = ItemRepair{Status: RepairSkipped}
		case err != nil && !os.IsNotExist(err):
			repairs[i] = ItemRepair{Status: RepairFailed, Err: err}
		default:
			repairs[i] = ItemRepair{Status: RepairSucceeded}
		}
	}
	return repairs
}

// removeEmptyAgentDirs removes each Agent directory that is still empty, and
// the empty parents it leaves, without escaping the Scope. One that is gone
// or has gained an entry is Skipped: whatever changed since the observation
// owns it now. The repairs are in the order of dirs.
func removeEmptyAgentDirs(skillsDir string, dirs []AgentDir) []ItemRepair {
	repairs := make([]ItemRepair, len(dirs))
	stopAt := models.ScopeRoot(skillsDir)
	for i, dir := range dirs {
		if empty, err := isDirEffectivelyEmpty(dir.Dir); err != nil || !empty {
			repairs[i] = ItemRepair{Status: RepairSkipped}
			continue
		}
		repairs[i] = itemRepairFromErr(removeEmptyAgentDir(dir.Dir, stopAt))
	}
	return repairs
}

// selects reports whether declared Availability puts skill on agent. Such a
// path is Drift's to report, whatever it holds.
func (a *Availability) selects(declared map[string]struct{}, agent, skill string) bool {
	if _, ok := declared[skill]; !ok {
		return false
	}
	return slices.Contains(a.ManagedAgents(skill), agent)
}
