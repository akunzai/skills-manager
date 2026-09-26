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
		plan.Items = append(plan.Items, planDirectory(AdoptItem{Name: name}, dir, records, moveDir))
	}
	plan.Items = append(plan.Items, planAgentCopies(cfg, availability, skillsDir, from, records, moveDir)...)
	return plan, nil
}

// planDirectory plans a real directory the way an Untracked Skill is
// adopted, whether it is on the skills directory already or will be moved
// there from dir on an Agent directory.
func planDirectory(item AdoptItem, dir string, records map[string]InstallerLockRecord, moveDir string) AdoptItem {
	item.Record, item.Recorded = records[item.Name]
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	item.GitCheckout = err == nil
	if item.Record.Remote() && !item.GitCheckout {
		item.Action = AdoptDeclareRemote
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
		target, err := linkTarget(path.Path)
		if err != nil {
			return c, false
		}
		c.Target = target
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
	return planDirectory(item, adopted.Path, records, moveDir)
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

// AdoptState is the one state each Skill Adopt was asked to adopt ends in.
// The CLI words each and maps it to an exit code (ADR-0002).
type AdoptState string

const (
	// AdoptAdopted is a Skill declared and applied: the Scope matches Config
	// for it, and a remote Skill has its Baseline.
	AdoptAdopted AdoptState = "adopted"
	// AdoptDeclaredWithoutBaseline is a remote Skill declared whose copy
	// differs from its Source, so it has no Baseline and the next Sync asks
	// before overwriting it.
	AdoptDeclaredWithoutBaseline AdoptState = "declared-without-baseline"
	// AdoptDeclaredWithCopiesLeft is a Skill declared whose copies on Agent
	// directories, which were to become Availability links, are left in place
	// (CopiesLeft). Nothing further was applied; Sync does it once they are
	// gone.
	AdoptDeclaredWithCopiesLeft AdoptState = "declared-with-copies-left"
	// AdoptSkipped is a Skill left as it was, for Reason.
	AdoptSkipped AdoptState = "skipped"
	// AdoptFailed is a Skill whose adoption broke, for Reason. Before the
	// declaration, Config and any move are restored; after it, the
	// declaration stays and Sync finishes the Skill once the cause is fixed.
	AdoptFailed AdoptState = "failed"
)

// AdoptOutcome is what became of one planned Skill: facts the CLI words,
// never sentences.
type AdoptOutcome struct {
	AdoptItem
	State AdoptState
	// Declared says Config declares the Skill: always in the declared states
	// and AdoptAdopted, never in AdoptSkipped, and in AdoptFailed whether the
	// failure came after the declaration.
	Declared bool
	// Reason is why the Skill was skipped or failed.
	Reason string
	// CopiesLeft are the copies on Agent directories left in place, in
	// AdoptDeclaredWithCopiesLeft.
	CopiesLeft []string
	// Subpath is the Source subpath a remote Skill was declared with.
	Subpath string
	// Available is the Agents declared Availability selects for a declared
	// Skill.
	Available []string
}

// AdoptResult records the outcome of applying an AdoptPlan.
type AdoptResult struct {
	Skills []AdoptOutcome
	// StateError is why the Scope state could not be read when a remote
	// Skill needed its Baseline recorded: a failure.
	StateError string
	// StateWarning is why the Scope state could not be read when no adopted
	// Skill needed a Baseline: a warning, not a failure (ADR-0002).
	StateWarning string
}

// Adopted is the Skills Config now declares, whatever their state.
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
// one that is skipped or fails. Config is saved after each Skill, so one that
// fails later does not take the others with it. An installer lock file is
// never written.
func ApplyAdoptPlan(plan AdoptPlan, cfg *config.Config, scope models.Scope) AdoptResult {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	scope.SkillsDir = cmp.Or(scope.SkillsDir, models.DefaultSkillsDir())
	scope.ConfigPath = cmp.Or(scope.ConfigPath, models.DefaultConfigFile())
	var result AdoptResult
	baselines := OpenBaselines(scope.SkillsDir)
	needsBaselines := false
	for _, item := range plan.Items {
		a := &adoption{cfg: cfg, scope: scope, baselines: baselines, outcome: AdoptOutcome{AdoptItem: item}}
		outcome := a.run()
		needsBaselines = needsBaselines || a.neededBaseline
		if outcome.Declared {
			outcome.Available = NewAvailability(cfg, scope.SkillsDir).ManagedAgents(item.Name)
		}
		result.Skills = append(result.Skills, outcome)
	}
	switch baselines.Verdict(needsBaselines) {
	case StateFail:
		result.StateError = baselines.Err().Error()
	case StateWarn:
		result.StateWarning = baselines.Err().Error()
	}
	return result
}

// adoption is one Skill going through the step all four of Adopt's paths
// share: check it is still as planned, fetch a remote Source, move the
// content where the path says, declare it and save Config, then clear the
// copies it replaces and apply it as Sync would. Anything that stops it
// before the declaration undoes the moves and Config.
type adoption struct {
	cfg       *config.Config
	scope     models.Scope
	baselines *Baselines
	outcome   AdoptOutcome

	// snapshot is Config before this Skill; configExisted and wroteConfig say
	// whether its file existed then and has been written since.
	snapshot      *config.Config
	configExisted bool
	wroteConfig   bool
	// moves are the renames made so far, as from and to, undone in reverse.
	moves [][2]string
	// neededBaseline says a remote Skill reached the step that records its
	// Baseline, which makes an unreadable Scope state a failure.
	neededBaseline bool
}

// remoteAdoption is a remote Source prepared through Remote intake, with the
// subpath the Skill is declared from.
type remoteAdoption struct {
	intake  *RemoteIntake
	subpath string
}

func (r *remoteAdoption) cachePath() string {
	return filepath.Join(r.intake.cache.dir(), filepath.FromSlash(r.subpath))
}

func (a *adoption) run() AdoptOutcome {
	item := a.outcome.AdoptItem
	a.snapshot = cloneConfig(a.cfg)
	if item.Block != "" {
		return a.undo(AdoptSkipped, item.Block)
	}
	if reason := a.changedSincePlan(); reason != "" {
		return a.undo(AdoptSkipped, reason)
	}
	var remote *remoteAdoption
	if item.Action == AdoptDeclareRemote {
		prepared, reason, err := prepareRemoteAdoption(a.cfg, a.scope.CacheDir, item)
		if err != nil {
			return a.undo(AdoptFailed, err.Error())
		}
		if reason != "" {
			return a.undo(AdoptSkipped, reason)
		}
		remote = prepared
	}
	content, reason, err := a.stage()
	if err != nil {
		return a.undo(AdoptFailed, err.Error())
	}
	if reason != "" {
		return a.undo(AdoptSkipped, reason)
	}
	if err := a.declare(remote, content); err != nil {
		if _, conflict := errors.AsType[branchConflictError](err); conflict {
			return a.undo(AdoptSkipped, err.Error())
		}
		return a.undo(AdoptFailed, err.Error())
	}
	return a.apply(remote, content)
}

// changedSincePlan re-checks, right before acting, that the Skill is still
// what the plan saw: a confirmation prompt may have been open for a while.
func (a *adoption) changedSincePlan() string {
	item := a.outcome.AdoptItem
	if _, _, declared := config.FindSkillSource(a.cfg, item.Name); declared {
		return "already declared in Config"
	}
	if !item.OnAgentDirectories() {
		if !isRealDir(filepath.Join(a.scope.SkillsDir, item.Name)) {
			return "no longer a directory on the skills directory"
		}
		return ""
	}
	adopted, ok := item.Adopted()
	if !ok {
		return "no copy to adopt"
	}
	if reason := adopted.changed(); reason != "" {
		return reason
	}
	if adopted.Symlink {
		if _, err := os.Stat(filepath.Join(adopted.Target, "SKILL.md")); err != nil {
			return fmt.Sprintf("its target %s no longer holds a Skill", models.ToTildePath(adopted.Target))
		}
	}
	return ""
}

// prepareRemoteAdoption fetches the recorded Source through Remote intake and
// finds the Skill in it: at the recorded path, or as the one Skill of its
// name when the lock file records none.
func prepareRemoteAdoption(cfg *config.Config, cacheDir string, item AdoptItem) (*remoteAdoption, string, error) {
	record := item.Record
	intake, err := PrepareRemoteIntake(cfg, models.ParsedRepoSource{
		SourceKey: record.Source,
		URL:       record.URL,
		RepoType:  record.RepoType,
		Branch:    record.Branch,
		Subpath:   record.Subpath,
	}, cacheDir)
	if err != nil {
		return nil, "", err
	}
	if record.Subpath != "" {
		for paths := range maps.Values(intake.Discovered) {
			if slices.Contains(paths, record.Subpath) {
				return &remoteAdoption{intake: intake, subpath: record.Subpath}, "", nil
			}
		}
		return nil, "", fmt.Errorf("Source %s has no Skill at %s", record.Source, record.Subpath)
	}
	if paths := intake.Discovered[item.Name]; len(paths) == 1 {
		return &remoteAdoption{intake: intake, subpath: paths[0]}, "", nil
	}
	return nil, fmt.Sprintf("the installer lock file records no path for it, and Source %s does not have exactly one Skill named %s", record.Source, item.Name), nil
}

// stage moves the Skill's content to where its Source is declared and
// returns that place, or why it cannot move there. A real directory on an
// Agent directory moves onto the skills directory first, with its
// Availability overrides saved before (ADR-0009); a directory with no known
// Source moves on to the move directory; a user's symlink moves nothing.
func (a *adoption) stage() (content, skip string, err error) {
	item := a.outcome.AdoptItem
	scopePath := filepath.Join(a.scope.SkillsDir, item.Name)
	if adopted, ok := item.Adopted(); ok {
		if adopted.Symlink {
			// Nothing moves, but the Availability link goes onto the skills
			// directory.
			return adopted.Target, "", os.MkdirAll(a.scope.SkillsDir, 0o755)
		}
		if len(item.Include) > 0 || len(item.Exclude) > 0 {
			if err := declareAvailability(a.cfg, a.scope.SkillsDir, item); err != nil {
				return "", "", err
			}
			if err := a.save(); err != nil {
				return "", "", err
			}
		}
		if skip, err := a.move(adopted.Path, scopePath); skip != "" || err != nil {
			return "", skip, err
		}
	}
	if item.Action != AdoptMoveLocal {
		return scopePath, "", nil
	}
	if skip, err := a.move(scopePath, item.MoveTo); skip != "" || err != nil {
		return "", skip, err
	}
	return item.MoveTo, "", nil
}

// move renames from to to, refusing anything already at to.
func (a *adoption) move(from, to string) (skip string, err error) {
	if reason := moveTargetBlock(to); reason != "" {
		return reason, nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", fmt.Errorf("move to %s: %w", models.ToTildePath(to), err)
	}
	a.moves = append(a.moves, [2]string{from, to})
	return "", nil
}

// declare records the Skill's Source, a remote one through Remote intake,
// with its Availability overrides, and saves Config.
func (a *adoption) declare(remote *remoteAdoption, content string) error {
	item := a.outcome.AdoptItem
	if remote != nil {
		if err := remote.intake.Declare(a.cfg, map[string]string{item.Name: remote.subpath}); err != nil {
			return err
		}
	} else {
		config.AddLocalSymlinkEntry(a.cfg, item.Name, models.StoreLocalSourcePath(content, a.scope.SkillsDir), "")
	}
	if err := declareAvailability(a.cfg, a.scope.SkillsDir, item); err != nil {
		return err
	}
	return a.save()
}

func (a *adoption) save() error {
	if !a.wroteConfig {
		_, err := os.Stat(a.scope.ConfigPath)
		a.configExisted = err == nil
	}
	if err := config.SaveConfig(a.cfg, a.scope.ConfigPath); err != nil {
		return err
	}
	a.wroteConfig = true
	return nil
}

// undo ends a Skill that stopped before its declaration: Config goes back to
// what it was, in memory and on disk, and each move is reversed. Anything
// that cannot be undone makes it a failure that says what stays where.
func (a *adoption) undo(state AdoptState, reason string) AdoptOutcome {
	*a.cfg = *a.snapshot
	var errs []error
	if a.wroteConfig {
		var err error
		if a.configExisted {
			err = config.SaveConfig(a.cfg, a.scope.ConfigPath)
		} else {
			err = os.Remove(a.scope.ConfigPath)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("restore Config: %w", err))
		}
	}
	for _, m := range slices.Backward(a.moves) {
		if err := os.Rename(m[1], m[0]); err != nil {
			errs = append(errs, fmt.Errorf("the directory stays at %s: %w", models.ToTildePath(m[1]), err))
		}
	}
	if len(errs) > 0 {
		state = AdoptFailed
		reason = errors.Join(append([]error{errors.New(reason)}, errs...)...).Error()
	}
	a.outcome.State, a.outcome.Reason = state, reason
	return a.outcome
}

// apply finishes a declared Skill: it clears the copies it replaces, then
// applies it as Sync would. A remote Skill whose copy differs from its Source
// gets its Availability but no Baseline.
func (a *adoption) apply(remote *remoteAdoption, content string) AdoptOutcome {
	a.outcome.Declared = true
	name, skillsDir := a.outcome.Name, a.scope.SkillsDir
	if remote != nil {
		a.outcome.Subpath = remote.subpath
	}
	if left := clearAgentCopies(a.outcome.AdoptItem, content); len(left) > 0 {
		a.outcome.State, a.outcome.CopiesLeft = AdoptDeclaredWithCopiesLeft, left
		return a.outcome
	}
	availability := NewAvailability(a.cfg, skillsDir)
	drift := availability.ObserveOccupancy().Drift(name)
	var applied SyncOutcome
	var err error
	switch {
	case remote == nil:
		applied, err = applyLocalItem(availability, skillsDir, planLocalItem(a.cfg, skillsDir, drift, name), nil)
	case !sameSkillContent(content, remote.cachePath()):
		if _, err := availability.Apply(name); err != nil {
			a.outcome.State, a.outcome.Reason = AdoptFailed, err.Error()
			return a.outcome
		}
		a.outcome.State = AdoptDeclaredWithoutBaseline
		return a.outcome
	default:
		a.neededBaseline = true
		item := planRecordedRemoteItem(remote.intake.spec.SourceKey, remote.intake.cache, SkillFreshness{
			Name:      name,
			Source:    remote.intake.spec.SourceKey,
			Subpath:   remote.subpath,
			ScopePath: content,
		}, drift)
		applied, err = applyRemoteItem(availability, skillsDir, item, SyncDecision{}, a.baselines, nil)
	}
	if applied != SyncDone {
		a.outcome.State, a.outcome.Reason = AdoptFailed, err.Error()
		return a.outcome
	}
	a.outcome.State = AdoptAdopted
	return a.outcome
}

// sameSkillContent compares two Skill directories the way Baselines digest
// them.
func sameSkillContent(a, b string) bool {
	da, errA := DigestSkillContent(a)
	db, errB := DigestSkillContent(b)
	return errA == nil && errB == nil && reflect.DeepEqual(da, db)
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

// cloneConfig is a copy of cfg that shares nothing mutable with it.
func cloneConfig(cfg *config.Config) *config.Config {
	c := *cfg
	c.Settings.DefaultAgents = slices.Clone(cfg.Settings.DefaultAgents)
	if cfg.Settings.Availability != nil {
		c.Settings.Availability = make(map[string]config.AvailabilityOverride, len(cfg.Settings.Availability))
		for name, o := range cfg.Settings.Availability {
			c.Settings.Availability[name] = config.AvailabilityOverride{Include: slices.Clone(o.Include), Exclude: slices.Clone(o.Exclude)}
		}
	}
	if cfg.Remote != nil {
		c.Remote = make(map[string]config.RemoteRepo, len(cfg.Remote))
		for key, repo := range cfg.Remote {
			repo.Skills = maps.Clone(repo.Skills)
			c.Remote[key] = repo
		}
	}
	c.Local = maps.Clone(cfg.Local)
	return &c
}

// linkTarget is the absolute, cleaned directory the symlink at path names.
// Observing a copy and checking it again before apply both use it, so the
// two can never disagree about where a user symlink points.
func linkTarget(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	return filepath.Clean(target), nil
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
		target, err := linkTarget(c.Path)
		if err != nil {
			return err.Error()
		}
		if target != c.Target {
			return fmt.Sprintf("%s changed since the plan", models.ToTildePath(c.Path))
		}
	}
	return ""
}

// clearAgentCopies makes room for Availability links once the Skill is
// declared: it removes the user's symlink the Skill was adopted from, and
// each copy whose content is the adopted content, checked again first. It
// never removes anything else. It returns the copies it left in place: one
// that no longer matches the plan, or that could not be removed.
func clearAgentCopies(item AdoptItem, adoptedContent string) []string {
	var left []string
	for _, c := range item.Copies {
		if c.Role != AgentCopyReplace && !(c.Role == AgentCopyAdopt && c.Symlink) {
			continue
		}
		if _, err := os.Lstat(c.Path); err != nil {
			continue // gone already: nothing is in the way
		}
		var err error
		switch {
		case c.changed() != "", !c.Symlink && !sameSkillContent(c.Path, adoptedContent):
			left = append(left, c.Path)
			continue
		case c.Symlink:
			err = os.Remove(c.Path)
		default:
			err = RemoveAll(c.Path)
		}
		if err != nil {
			left = append(left, c.Path)
		}
	}
	return left
}
