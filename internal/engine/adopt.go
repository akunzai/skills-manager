package engine

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AdoptAction is how Adopt declares one Untracked Skill.
type AdoptAction string

const (
	// AdoptDeclareRemote declares the remote Source an installer lock file
	// records, leaving the copy where it is.
	AdoptDeclareRemote AdoptAction = "remote"
	// AdoptMoveLocal moves the directory out of the skills directory and
	// declares it as a local Source, which Illegal-local forbids inside it.
	AdoptMoveLocal AdoptAction = "move"
	// AdoptLinkSource declares where a user's symlink on an Agent directory
	// points as a local Source, moving nothing.
	AdoptLinkSource AdoptAction = "link"
)

// AgentCopyRole is what Adopt does with one copy of a Skill found on an Agent
// directory.
type AgentCopyRole string

const (
	// AgentCopyAdopt is the copy the Skill is adopted from.
	AgentCopyAdopt AgentCopyRole = "adopt"
	// AgentCopyReplace is a copy with the same content as the adopted one; it
	// is replaced by an Availability link.
	AgentCopyReplace AgentCopyRole = "replace"
	// AgentCopyKeep is a copy whose content differs from the adopted one. It
	// stays untouched and its Agent gets an Exclude.
	AgentCopyKeep AgentCopyRole = "keep"
)

// AgentCopy is one Unmanaged path on an Agent directory that holds a Skill: a
// real directory, or a user's symlink.
type AgentCopy struct {
	Agent string
	Path  string
	// Symlink says the path is a user's symlink; Target is where it points,
	// made absolute.
	Symlink bool
	Target  string
	// Problem is why this copy cannot be adopted: a symlink whose target is
	// missing or inside the skills directory.
	Problem string
	Role    AgentCopyRole

	digests map[string]string
}

// AdoptItem is one Untracked real directory on the Scope skills directory
// and how Adopt would declare it.
type AdoptItem struct {
	Name   string
	Action AdoptAction
	// Record is what an installer lock file records about the Skill.
	// Recorded says whether one does at all, whichever the Action: that
	// installer may still write to the same directory.
	Record   InstallerLockRecord
	Recorded bool
	// GitCheckout is a directory that is its own git checkout. It moves
	// with its .git, whatever a lock file records.
	GitCheckout bool
	// MoveTo is where AdoptMoveLocal moves the directory.
	MoveTo string
	// Copies are where the Skill was found on the Scope's Agent directories,
	// by Agent, each with its Role once the plan is settled. An Untracked
	// Skill on the Scope skills directory has none.
	Copies []AgentCopy
	// Include and Exclude are the Availability overrides the declaration
	// carries: the Agents a copy was found under that defaults do not cover,
	// and the Agents whose differing copy stays.
	Include []string
	Exclude []string
	// Block is why the Skill cannot be adopted as planned; Apply leaves it.
	Block string
}

// OnAgentDirectories reports whether the Skill was found on Agent directories
// rather than on the Scope skills directory.
func (i AdoptItem) OnAgentDirectories() bool { return len(i.Copies) > 0 }

// Key tells the two groups apart where one name is in both.
func (i AdoptItem) Key() string {
	if i.OnAgentDirectories() {
		return "agents/" + i.Name
	}
	return i.Name
}

// Agents is every Agent a copy was found under.
func (i AdoptItem) Agents() []string {
	agents := make([]string, 0, len(i.Copies))
	for _, c := range i.Copies {
		agents = append(agents, c.Agent)
	}
	return agents
}

// Adopted is the copy the Skill is adopted from, if the plan settled on one.
func (i AdoptItem) Adopted() (AgentCopy, bool) {
	for _, c := range i.Copies {
		if c.Role == AgentCopyAdopt {
			return c, true
		}
	}
	return AgentCopy{}, false
}

// CopiesWith is the copies that have role.
func (i AdoptItem) CopiesWith(role AgentCopyRole) []AgentCopy {
	var copies []AgentCopy
	for _, c := range i.Copies {
		if c.Role == role {
			copies = append(copies, c)
		}
	}
	return copies
}

// AdoptPlan is every Untracked real directory on one Scope skills directory
// that holds a SKILL.md, with how Adopt would declare it. Building it writes
// nothing.
type AdoptPlan struct {
	Items []AdoptItem
	// NotSkills are Untracked real directories without a SKILL.md. They are
	// not Skills, so Adopt does not offer them.
	NotSkills []string
	// LockError is why the installer lock file could not be read. Every Skill
	// is then planned as if nothing recorded it.
	LockError string
}

// AdoptOptions are what the caller decides for BuildAdoptPlan: the installer
// lock file to read (see InstallerLockPath), the directory local Sources
// move into, empty for DefaultAdoptMoveDir, and the Agent whose copy to adopt
// where copies on Agent directories differ.
type AdoptOptions struct {
	LockPath string
	MoveDir  string
	From     string
}

// DefaultAdoptMoveDir is skills-local beside the skills directory.
func DefaultAdoptMoveDir(skillsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(skillsDir)), "skills-local")
}

// BuildAdoptPlan reads the Inventory's Untracked real directories, the
// Unmanaged paths of the Agent directory occupancy, and the installer lock
// file, and plans each Skill: a lock record naming a git Source is declared
// as that remote Source; anything else, and any git checkout, moves to the
// move directory as a local Source. A real directory on an Agent directory
// first moves onto the skills directory and is then planned the same way; a
// user's symlink there has its target declared as a local Source.
func BuildAdoptPlan(cfg *config.Config, scope models.Scope, options AdoptOptions) (AdoptPlan, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	skillsDir := cmp.Or(scope.SkillsDir, models.DefaultSkillsDir())
	moveDir, err := filepath.Abs(models.ExpandUser(cmp.Or(options.MoveDir, DefaultAdoptMoveDir(skillsDir))))
	if err != nil {
		return AdoptPlan{}, err
	}
	if models.LocalSourceInsideSkillsDir(moveDir, skillsDir) {
		return AdoptPlan{}, fmt.Errorf("move directory %s is inside the skills directory", models.ToTildePath(moveDir))
	}
	availability := NewAvailability(cfg, skillsDir)
	from := ""
	if options.From != "" {
		from = models.NormalizeAgentName(options.From)
		if _, ok := availability.agents.KnownDirs()[from]; !ok {
			return AdoptPlan{}, fmt.Errorf("unknown agent %q for this scope", options.From)
		}
	}
	inv, err := LoadInventory(cfg, skillsDir)
	if err != nil {
		return AdoptPlan{}, err
	}
	var plan AdoptPlan
	var records map[string]InstallerLockRecord
	if options.LockPath != "" {
		if records, err = ReadInstallerLock(options.LockPath); err != nil {
			plan.LockError = err.Error()
		}
	}
	for _, name := range slices.Sorted(slices.Values(inv.Untracked())) {
		dir := filepath.Join(skillsDir, name)
		if !isRealDir(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			plan.NotSkills = append(plan.NotSkills, name)
			continue
		}
		plan.Items = append(plan.Items, planDirectory(cfg, AdoptItem{Name: name}, dir, records, moveDir))
	}
	plan.Items = append(plan.Items, planAgentCopies(cfg, availability, skillsDir, from, records, moveDir)...)
	return plan, nil
}

// planDirectory plans a real directory the way an Untracked Skill is
// adopted, whether it is on the skills directory already or will be moved
// there from dir on an Agent directory.
func planDirectory(cfg *config.Config, item AdoptItem, dir string, records map[string]InstallerLockRecord, moveDir string) AdoptItem {
	item.Record, item.Recorded = records[item.Name]
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	item.GitCheckout = err == nil
	if item.Record.Remote() && !item.GitCheckout {
		item.Action = AdoptDeclareRemote
		if _, err := AddBranch(cfg, item.Record.Source, item.Record.Branch); err != nil {
			item.Block = err.Error()
		}
	} else {
		item.Action = AdoptMoveLocal
		item.MoveTo = filepath.Join(moveDir, item.Name)
		item.Block = moveTargetBlock(item.MoveTo)
	}
	return item
}

// planAgentCopies groups the occupancy's Unmanaged paths that hold a Skill by
// name and plans one Skill per name.
func planAgentCopies(cfg *config.Config, availability *Availability, skillsDir, from string, records map[string]InstallerLockRecord, moveDir string) []AdoptItem {
	byName := make(map[string][]AgentCopy)
	for _, path := range availability.ObserveOccupancy().Unmanaged {
		if c, ok := observeAgentCopy(path, skillsDir); ok {
			byName[path.Name] = append(byName[path.Name], c)
		}
	}
	var items []AdoptItem
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		copies := byName[name]
		if _, _, declared := config.FindSkillSource(cfg, name); declared {
			// A copy the user chose to keep when adopting another is not
			// offered again.
			excluded := agentSet(cfg.Settings.Availability[name].Exclude)
			copies = slices.DeleteFunc(copies, func(c AgentCopy) bool {
				_, ok := excluded[c.Agent]
				return ok
			})
			if len(copies) == 0 {
				continue
			}
		}
		items = append(items, planAgentItem(cfg, availability, skillsDir, name, copies, from, records, moveDir))
	}
	return items
}

// observeAgentCopy reads one Unmanaged path. A real directory or a symlink to
// a directory without SKILL.md is not a Skill, and neither is a symlink to a
// file; a dangling symlink is a copy Adopt refuses.
func observeAgentCopy(path UnmanagedAgentPath, skillsDir string) (AgentCopy, bool) {
	c := AgentCopy{Agent: path.Agent, Path: path.Path, Symlink: path.Symlink}
	content := path.Path
	if path.Symlink {
		target, err := os.Readlink(path.Path)
		if err != nil {
			return c, false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path.Path), target)
		}
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		c.Target = filepath.Clean(target)
		info, err := os.Stat(c.Target)
		if err != nil {
			c.Problem = fmt.Sprintf("its target %s does not exist", models.ToTildePath(c.Target))
			return c, true
		}
		if !info.IsDir() {
			return c, false
		}
		content = c.Target
		resolved, _ := filepath.EvalSymlinks(c.Target)
		if models.LocalSourceInsideSkillsDir(c.Target, skillsDir) || models.LocalSourceInsideSkillsDir(resolved, skillsDir) {
			c.Problem = fmt.Sprintf("its target %s is inside the skills directory", models.ToTildePath(c.Target))
			return c, true
		}
	}
	if _, err := os.Stat(filepath.Join(content, "SKILL.md")); err != nil {
		return c, false
	}
	c.digests, _ = DigestSkillContent(content)
	return c, true
}

// contentPath is where the copy's content is.
func (c AgentCopy) contentPath() string {
	if c.Symlink {
		return c.Target
	}
	return c.Path
}

func (c AgentCopy) sameContent(other AgentCopy) bool {
	return c.Problem == "" && other.Problem == "" && c.digests != nil && reflect.DeepEqual(c.digests, other.digests)
}

// planAgentItem settles which copy of name is adopted and what becomes of the
// others, or why none can be.
func planAgentItem(cfg *config.Config, availability *Availability, skillsDir, name string, copies []AgentCopy, from string, records map[string]InstallerLockRecord, moveDir string) AdoptItem {
	item := AdoptItem{Name: name, Copies: copies}
	if _, _, declared := config.FindSkillSource(cfg, name); declared {
		item.Block = "already declared in Config"
		return item
	}
	if _, err := os.Lstat(filepath.Join(skillsDir, name)); err == nil {
		item.Block = "an Untracked Skill of that name is on the skills directory; adopt that one first"
		return item
	}

	chosen := -1
	if from != "" {
		chosen = slices.IndexFunc(copies, func(c AgentCopy) bool { return c.Agent == from })
	}
	if chosen < 0 {
		for _, c := range copies[1:] {
			if !c.sameContent(copies[0]) {
				paths := make([]string, len(copies))
				for i, c := range copies {
					paths[i] = models.ToTildePath(c.Path)
				}
				item.Block = fmt.Sprintf("its copies differ: %s; choose one with --from <agent>", strings.Join(paths, ", "))
				return item
			}
		}
		// Identical copies: a real directory is adopted rather than a
		// symlink, since the symlink may point at it.
		chosen = max(0, slices.IndexFunc(copies, func(c AgentCopy) bool { return !c.Symlink }))
	}
	adopted := copies[chosen]
	if adopted.Problem != "" {
		item.Block = adopted.Problem
		return item
	}
	for i := range item.Copies {
		c := &item.Copies[i]
		switch {
		case i == chosen:
			c.Role = AgentCopyAdopt
		case c.sameContent(adopted):
			c.Role = AgentCopyReplace
		default:
			c.Role = AgentCopyKeep
		}
		if adopted.Symlink && c.Role == AgentCopyReplace && !c.Symlink && overlaps(adopted.Target, c.Path) {
			item.Block = fmt.Sprintf("its target %s is the copy on %s; adopt that one with --from %s", models.ToTildePath(adopted.Target), c.Agent, c.Agent)
			return item
		}
	}

	defaults := agentSet(availability.ManagedAgents(name))
	for _, c := range item.Copies {
		_, covered := defaults[c.Agent]
		switch {
		case c.Role == AgentCopyKeep:
			item.Exclude = append(item.Exclude, c.Agent)
		case !covered:
			item.Include = append(item.Include, c.Agent)
		}
	}

	if adopted.Symlink {
		item.Action = AdoptLinkSource
		return item
	}
	return planDirectory(cfg, item, adopted.Path, records, moveDir)
}

// overlaps reports whether one path is the other or inside it, after
// resolving symlinks.
func overlaps(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	return models.LocalSourceInsideSkillsDir(ra, rb) || models.LocalSourceInsideSkillsDir(rb, ra)
}

// moveTargetBlock refuses a move onto anything already there.
func moveTargetBlock(target string) string {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Sprintf("%s already exists", models.ToTildePath(target))
	}
	return ""
}

func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// Names is every Skill in the plan, in plan order, each name once.
func (p AdoptPlan) Names() []string {
	names := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		if !slices.Contains(names, item.Name) {
			names = append(names, item.Name)
		}
	}
	return names
}

// Unknown is the names the plan does not offer.
func (p AdoptPlan) Unknown(names []string) []string {
	offered := setOf(p.Names())
	var unknown []string
	for _, name := range names {
		if !offered[name] {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

// Select narrows the plan to the named Skills, in either group. Every Skill
// Apply adopts is one the user named or selected; nothing else is moved or
// declared.
func (p AdoptPlan) Select(names []string) AdoptPlan {
	selected := setOf(names)
	return p.narrow(func(item AdoptItem) bool { return selected[item.Name] })
}

// SelectKeys narrows the plan to the Skills whose Key was picked.
func (p AdoptPlan) SelectKeys(keys []string) AdoptPlan {
	selected := setOf(keys)
	return p.narrow(func(item AdoptItem) bool { return selected[item.Key()] })
}

func (p AdoptPlan) narrow(keep func(AdoptItem) bool) AdoptPlan {
	narrowed := AdoptPlan{LockError: p.LockError}
	for _, item := range p.Items {
		if keep(item) {
			narrowed.Items = append(narrowed.Items, item)
		}
	}
	return narrowed
}

// AdoptOutcome is what became of one planned Skill. Declared says whether
// Config now declares it, which a blocked Skill may be too. Reason says why it
// was blocked or failed. Subpath is the Source subpath a remote Skill was
// declared with, and Baseline whether its copy matched the Cache and so has
// one.
type AdoptOutcome struct {
	AdoptItem
	Outcome  SyncOutcome
	Declared bool
	Reason   string
	Subpath  string
	Baseline bool
	// Available is the Agents declared Availability selects for a declared
	// Skill.
	Available []string
}

// AdoptResult records the outcome of applying an AdoptPlan, counted like Sync
// counts its Skills (ADR-0002).
type AdoptResult struct {
	SyncTally
	Skills []AdoptOutcome
	// Events are what applying each Skill reported, in Sync's words.
	Events []SyncEvent
	// StateError is why the Scope state could not be read when a remote
	// Skill needed its Baseline recorded: a failure.
	StateError string
	// StateWarning is why the Scope state could not be read when no adopted
	// Skill needed a Baseline: a warning, not a failure (ADR-0002).
	StateWarning string
}

// Adopted is the Skills Config now declares, whatever their outcome.
func (r AdoptResult) Adopted() []AdoptOutcome {
	var adopted []AdoptOutcome
	for _, skill := range r.Skills {
		if skill.Declared {
			adopted = append(adopted, skill)
		}
	}
	return adopted
}

// ApplyAdoptPlan declares each planned Skill, one at a time, continuing past
// one that is blocked or fails. Config is saved after each Skill, so one that
// fails later does not take the others with it. An installer lock file is
// never written.
func ApplyAdoptPlan(plan AdoptPlan, cfg *config.Config, scope models.Scope) AdoptResult {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	scope.SkillsDir = cmp.Or(scope.SkillsDir, models.DefaultSkillsDir())
	scope.ConfigPath = cmp.Or(scope.ConfigPath, models.DefaultConfigFile())
	var result AdoptResult
	emit := func(ev SyncEvent) { result.Events = append(result.Events, ev) }
	baselines := OpenBaselines(scope.SkillsDir)
	needsBaselines := false
	for _, item := range plan.Items {
		outcome := AdoptOutcome{AdoptItem: item}
		switch {
		case item.Block != "":
			outcome = adoptBlocked(outcome, item.Block)
		case item.OnAgentDirectories():
			needsBaselines = needsBaselines || item.Action == AdoptDeclareRemote
			outcome = adoptFromAgent(cfg, scope, outcome, baselines, emit)
		case item.Action == AdoptDeclareRemote:
			needsBaselines = true
			outcome = adoptRemote(cfg, scope, outcome, baselines, emit)
		default:
			outcome = adoptLocal(cfg, scope, outcome, emit)
		}
		if outcome.Declared {
			outcome.Available = NewAvailability(cfg, scope.SkillsDir).ManagedAgents(item.Name)
		}
		result.tally(outcome.Outcome)
		result.Skills = append(result.Skills, outcome)
	}
	switch baselines.Verdict(needsBaselines) {
	case StateFail:
		result.StateError = baselines.Err().Error()
		result.tally(SyncFailed)
	case StateWarn:
		result.StateWarning = baselines.Err().Error()
	}
	return result
}

func adoptBlocked(outcome AdoptOutcome, reason string) AdoptOutcome {
	outcome.Outcome, outcome.Reason = SyncBlocked, reason
	return outcome
}

func adoptFailed(outcome AdoptOutcome, err error) AdoptOutcome {
	outcome.Outcome, outcome.Reason = SyncFailed, err.Error()
	return outcome
}

// stillUntracked re-checks, right before acting, that the directory is still
// an Untracked real directory: a confirmation prompt may have been open for a
// while.
func stillUntracked(cfg *config.Config, skillsDir, name string) string {
	if _, _, declared := config.FindSkillSource(cfg, name); declared {
		return "already declared in Config"
	}
	if !isRealDir(filepath.Join(skillsDir, name)) {
		return "no longer a directory on the skills directory"
	}
	return ""
}

// adoptRemote declares the recorded Source, fetches it into the Cache as Add
// would, and records a Baseline only when the copy on disk is exactly the
// Cache content; a copy that differs is declared without one, so the next
// Sync blocks and asks rather than overwriting it.
func adoptRemote(cfg *config.Config, scope models.Scope, outcome AdoptOutcome, baselines *Baselines, emit func(SyncEvent)) AdoptOutcome {
	record, name := outcome.Record, outcome.Name
	if reason := stillUntracked(cfg, scope.SkillsDir, name); reason != "" {
		return adoptBlocked(outcome, reason)
	}
	branch, err := AddBranch(cfg, record.Source, record.Branch)
	if err != nil {
		return adoptBlocked(outcome, err.Error())
	}
	cache := NewCache(record.Source, record.URL, branch, scope.CacheDir)
	subpaths := declaredSubpaths(cfg.Remote[record.Source])
	if record.Subpath != "" {
		subpaths = append(subpaths, record.Subpath)
	}
	repoDir, err := cache.Refresh(true, subpaths...)
	if err != nil {
		return adoptFailed(outcome, fmt.Errorf("refresh Source %s: %w", record.Source, err))
	}
	subpath := record.Subpath
	if subpath == "" {
		found, _, err := discoverRemoteSkills(cache, "")
		if err != nil {
			return adoptFailed(outcome, fmt.Errorf("discover Skills in %s: %w", record.Source, err))
		}
		if len(found[name]) != 1 {
			return adoptBlocked(outcome, fmt.Sprintf("the installer lock file records no path for it, and Source %s does not have exactly one Skill named %s", record.Source, name))
		}
		subpath = found[name][0]
		if err := cache.Cover(subpath); err != nil {
			return adoptFailed(outcome, err)
		}
	}
	outcome.Subpath = subpath
	cachePath := filepath.Join(repoDir, filepath.FromSlash(subpath))
	if _, err := os.Stat(filepath.Join(cachePath, "SKILL.md")); err != nil {
		return adoptFailed(outcome, fmt.Errorf("Source %s has no Skill at %s", record.Source, subpath))
	}

	config.AddRemoteSkillEntry(cfg, record.Source, name, subpath, record.RepoType, record.StoredURL())
	if branch != "" {
		repo := cfg.Remote[record.Source]
		repo.Branch = branch
		cfg.Remote[record.Source] = repo
	}
	if err := declareAvailability(cfg, scope.SkillsDir, outcome.AdoptItem); err != nil {
		return adoptFailed(outcome, err)
	}
	if err := config.SaveConfig(cfg, scope.ConfigPath); err != nil {
		return adoptFailed(outcome, err)
	}
	outcome.Declared = true

	scopePath := filepath.Join(scope.SkillsDir, name)
	if err := clearAgentCopies(outcome.AdoptItem, scopePath); err != nil {
		return adoptBlocked(outcome, err.Error())
	}
	availability := NewAvailability(cfg, scope.SkillsDir)
	if !sameSkillContent(scopePath, cachePath) {
		if _, err := availability.Apply(name); err != nil {
			// The event says why, in Sync's words.
			emit(SyncEvent{Kind: SyncAvailabilityFailed, Source: record.Source, Skill: name, Err: err.Error()})
			outcome.Outcome = SyncFailed
			return outcome
		}
		return adoptBlocked(outcome, "its copy differs from the Source, so the next Sync asks before overwriting it")
	}
	item := baseRemoteItem(record.Source, repoDir, localRepoCommit(repoDir), SkillFreshness{
		Name:      name,
		Source:    record.Source,
		Subpath:   subpath,
		ScopePath: scopePath,
		CachePath: cachePath,
	}, availability.ObserveOccupancy().Drift(name))
	outcome.Outcome = applyItem(availability, scope.SkillsDir, item, SyncDecision{}, baselines, emit)
	outcome.Baseline = outcome.Outcome == SyncDone && baselines.Err() == nil
	return outcome
}

// sameSkillContent compares two Skill directories the way Baselines digest
// them.
func sameSkillContent(a, b string) bool {
	da, errA := DigestSkillContent(a)
	db, errB := DigestSkillContent(b)
	return errA == nil && errB == nil && reflect.DeepEqual(da, db)
}

// adoptLocal moves the directory to its planned place, declares it there as a
// local Source, and links it back onto the skills directory as Sync would.
func adoptLocal(cfg *config.Config, scope models.Scope, outcome AdoptOutcome, emit func(SyncEvent)) AdoptOutcome {
	name := outcome.Name
	if reason := stillUntracked(cfg, scope.SkillsDir, name); reason != "" {
		return adoptBlocked(outcome, reason)
	}
	if reason := moveTargetBlock(outcome.MoveTo); reason != "" {
		return adoptBlocked(outcome, reason)
	}
	source := filepath.Join(scope.SkillsDir, name)
	if err := os.MkdirAll(filepath.Dir(outcome.MoveTo), 0o755); err != nil {
		return adoptFailed(outcome, err)
	}
	if err := os.Rename(source, outcome.MoveTo); err != nil {
		return adoptFailed(outcome, fmt.Errorf("move to %s: %w", models.ToTildePath(outcome.MoveTo), err))
	}
	if err := declareLocal(cfg, scope, outcome.AdoptItem, outcome.MoveTo); err != nil {
		if moveErr := os.Rename(outcome.MoveTo, source); moveErr != nil {
			err = errors.Join(err, fmt.Errorf("the directory stays at %s: %w", models.ToTildePath(outcome.MoveTo), moveErr))
		}
		return adoptFailed(outcome, err)
	}
	outcome.Declared = true
	if err := clearAgentCopies(outcome.AdoptItem, outcome.MoveTo); err != nil {
		return adoptBlocked(outcome, err.Error())
	}
	availability := NewAvailability(cfg, scope.SkillsDir)
	item := planLocalItem(cfg, scope.SkillsDir, availability.ObserveOccupancy().Drift(name), name)
	// Why a link or Availability failed is in the events applyItem emits.
	outcome.Outcome = applyItem(availability, scope.SkillsDir, item, SyncDecision{}, nil, emit)
	return outcome
}

// declareLocal declares source as the Skill's local symlink Source, with the
// item's Availability overrides, and saves Config. On failure Config in memory
// is as it was.
func declareLocal(cfg *config.Config, scope models.Scope, item AdoptItem, source string) error {
	previous, hadOverride := cfg.Settings.Availability[item.Name]
	config.AddLocalSymlinkEntry(cfg, item.Name, models.StoreLocalSourcePath(source, scope.SkillsDir), "")
	err := declareAvailability(cfg, scope.SkillsDir, item)
	if err == nil {
		err = config.SaveConfig(cfg, scope.ConfigPath)
	}
	if err != nil {
		delete(cfg.Local, item.Name)
		if hadOverride {
			cfg.Settings.Availability[item.Name] = previous
		} else {
			delete(cfg.Settings.Availability, item.Name)
		}
	}
	return err
}

// declareAvailability records the item's Include and Exclude, so the Skill
// stays available where it was found and a copy the user keeps is left out.
func declareAvailability(cfg *config.Config, skillsDir string, item AdoptItem) error {
	if len(item.Include) == 0 && len(item.Exclude) == 0 {
		return nil
	}
	availability := NewAvailability(cfg, skillsDir)
	if err := availability.Include(item.Name, item.Include...); err != nil {
		return err
	}
	return availability.Exclude(item.Name, item.Exclude...)
}

// changed says how a copy on an Agent directory is no longer what the plan
// observed, or "" when it still is.
func (c AgentCopy) changed() string {
	info, err := os.Lstat(c.Path)
	switch {
	case err != nil:
		return fmt.Sprintf("%s is gone", models.ToTildePath(c.Path))
	case c.Symlink && info.Mode()&os.ModeSymlink == 0, !c.Symlink && !info.IsDir():
		return fmt.Sprintf("%s changed since the plan", models.ToTildePath(c.Path))
	case c.Symlink:
		target, err := os.Readlink(c.Path)
		if err != nil {
			return err.Error()
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(c.Path), target)
		}
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		if filepath.Clean(target) != c.Target {
			return fmt.Sprintf("%s changed since the plan", models.ToTildePath(c.Path))
		}
	}
	return ""
}

// clearAgentCopies makes room for Availability links once the Skill is
// declared: it removes the user's symlink the Skill was adopted from, and
// each copy whose content is the adopted content, checked again first. It
// never removes anything else. A copy that no longer matches is left, and
// the error names it.
func clearAgentCopies(item AdoptItem, adoptedContent string) error {
	var errs []error
	for _, c := range item.Copies {
		if c.Role != AgentCopyReplace && !(c.Role == AgentCopyAdopt && c.Symlink) {
			continue
		}
		if reason := c.changed(); reason != "" {
			if c.Role == AgentCopyAdopt || !strings.HasSuffix(reason, " is gone") {
				errs = append(errs, fmt.Errorf("left %s in place: %s", models.ToTildePath(c.Path), reason))
			}
			continue
		}
		if c.Symlink {
			if err := os.Remove(c.Path); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if !sameSkillContent(c.Path, adoptedContent) {
			errs = append(errs, fmt.Errorf("left %s in place: its content changed since the plan", models.ToTildePath(c.Path)))
			continue
		}
		if err := RemoveAll(c.Path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// adoptFromAgent adopts a Skill found on Agent directories. A real directory
// moves onto the skills directory first, so an interruption from then on
// leaves an Untracked Skill that adopting again completes, and is then
// adopted exactly as one. A user's symlink has its target declared in place.
func adoptFromAgent(cfg *config.Config, scope models.Scope, outcome AdoptOutcome, baselines *Baselines, emit func(SyncEvent)) AdoptOutcome {
	name := outcome.Name
	if _, _, declared := config.FindSkillSource(cfg, name); declared {
		return adoptBlocked(outcome, "already declared in Config")
	}
	scopePath := filepath.Join(scope.SkillsDir, name)
	if _, err := os.Lstat(scopePath); err == nil {
		return adoptBlocked(outcome, "an Untracked Skill of that name is on the skills directory; adopt that one first")
	}
	adopted, ok := outcome.Adopted()
	if !ok {
		return adoptBlocked(outcome, "no copy to adopt")
	}
	if reason := adopted.changed(); reason != "" {
		return adoptBlocked(outcome, reason)
	}
	if adopted.Symlink {
		return adoptLinkSource(cfg, scope, outcome, adopted, emit)
	}
	if outcome.Action == AdoptMoveLocal {
		if reason := moveTargetBlock(outcome.MoveTo); reason != "" {
			return adoptBlocked(outcome, reason)
		}
	}
	if err := os.MkdirAll(scope.SkillsDir, 0o755); err != nil {
		return adoptFailed(outcome, err)
	}
	// The Availability overrides are saved before the move: once the
	// directory is an Untracked Skill, they are all that records where it was
	// found, so adopting it again keeps it available there.
	previous, hadOverride := cfg.Settings.Availability[name]
	restore := func() {
		if hadOverride {
			cfg.Settings.Availability[name] = previous
		} else {
			delete(cfg.Settings.Availability, name)
		}
	}
	if len(outcome.Include) > 0 || len(outcome.Exclude) > 0 {
		err := declareAvailability(cfg, scope.SkillsDir, outcome.AdoptItem)
		if err == nil {
			err = config.SaveConfig(cfg, scope.ConfigPath)
		}
		if err != nil {
			restore()
			return adoptFailed(outcome, err)
		}
	}
	if err := os.Rename(adopted.Path, scopePath); err != nil {
		restore()
		err = fmt.Errorf("move to %s: %w", models.ToTildePath(scopePath), err)
		if len(outcome.Include) > 0 || len(outcome.Exclude) > 0 {
			if saveErr := config.SaveConfig(cfg, scope.ConfigPath); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
		}
		return adoptFailed(outcome, err)
	}
	if outcome.Action == AdoptDeclareRemote {
		return adoptRemote(cfg, scope, outcome, baselines, emit)
	}
	return adoptLocal(cfg, scope, outcome, emit)
}

// adoptLinkSource declares where the user's symlink points as a local Source
// and replaces the symlink with an Availability link.
func adoptLinkSource(cfg *config.Config, scope models.Scope, outcome AdoptOutcome, adopted AgentCopy, emit func(SyncEvent)) AdoptOutcome {
	if _, err := os.Stat(filepath.Join(adopted.Target, "SKILL.md")); err != nil {
		return adoptBlocked(outcome, fmt.Sprintf("its target %s no longer holds a Skill", models.ToTildePath(adopted.Target)))
	}
	if err := os.MkdirAll(scope.SkillsDir, 0o755); err != nil {
		return adoptFailed(outcome, err)
	}
	if err := declareLocal(cfg, scope, outcome.AdoptItem, adopted.Target); err != nil {
		return adoptFailed(outcome, err)
	}
	outcome.Declared = true
	if err := clearAgentCopies(outcome.AdoptItem, adopted.Target); err != nil {
		return adoptBlocked(outcome, err.Error())
	}
	availability := NewAvailability(cfg, scope.SkillsDir)
	item := planLocalItem(cfg, scope.SkillsDir, availability.ObserveOccupancy().Drift(outcome.Name), outcome.Name)
	outcome.Outcome = applyItem(availability, scope.SkillsDir, item, SyncDecision{}, nil, emit)
	return outcome
}
