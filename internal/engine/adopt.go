package engine

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

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
)

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
	// Block is why the Skill cannot be adopted as planned; Apply leaves it.
	Block string
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
// lock file to read (see InstallerLockPath) and the directory local Sources
// move into, empty for DefaultAdoptMoveDir.
type AdoptOptions struct {
	LockPath string
	MoveDir  string
}

// DefaultAdoptMoveDir is skills-local beside the skills directory.
func DefaultAdoptMoveDir(skillsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(skillsDir)), "skills-local")
}

// BuildAdoptPlan reads the Inventory's Untracked real directories and the
// installer lock file, and plans each: a lock record naming a git Source is
// declared as that remote Source; anything else, and any git checkout, moves
// to the move directory as a local Source.
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
		item := AdoptItem{Name: name}
		item.Record, item.Recorded = records[name]
		_, err := os.Lstat(filepath.Join(dir, ".git"))
		item.GitCheckout = err == nil
		if item.Record.Remote() && !item.GitCheckout {
			item.Action = AdoptDeclareRemote
			if _, err := AddBranch(cfg, item.Record.Source, item.Record.Branch); err != nil {
				item.Block = err.Error()
			}
		} else {
			item.Action = AdoptMoveLocal
			item.MoveTo = filepath.Join(moveDir, name)
			item.Block = moveTargetBlock(item.MoveTo)
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
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

// Names is every Skill in the plan, in plan order.
func (p AdoptPlan) Names() []string {
	names := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		names = append(names, item.Name)
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

// Select narrows the plan to the named Skills. Every Skill Apply adopts is a
// real directory the user named or selected; nothing else is moved or
// declared.
func (p AdoptPlan) Select(names []string) AdoptPlan {
	selected := setOf(names)
	narrowed := AdoptPlan{LockError: p.LockError}
	for _, item := range p.Items {
		if selected[item.Name] {
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
		case item.Action == AdoptDeclareRemote:
			needsBaselines = true
			outcome = adoptRemote(cfg, scope, outcome, baselines, emit)
		default:
			outcome = adoptLocal(cfg, scope, outcome, emit)
		}
		result.tally(outcome.Outcome)
		result.Skills = append(result.Skills, outcome)
	}
	if err := baselines.Err(); err != nil && needsBaselines {
		result.StateError = err.Error()
		result.tally(SyncFailed)
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
	if err := config.SaveConfig(cfg, scope.ConfigPath); err != nil {
		return adoptFailed(outcome, err)
	}
	outcome.Declared = true

	scopePath := filepath.Join(scope.SkillsDir, name)
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
	config.AddLocalSymlinkEntry(cfg, name, models.StoreLocalSourcePath(outcome.MoveTo, scope.SkillsDir), "")
	if err := config.SaveConfig(cfg, scope.ConfigPath); err != nil {
		delete(cfg.Local, name)
		if moveErr := os.Rename(outcome.MoveTo, source); moveErr != nil {
			err = errors.Join(err, fmt.Errorf("the directory stays at %s: %w", models.ToTildePath(outcome.MoveTo), moveErr))
		}
		return adoptFailed(outcome, err)
	}
	outcome.Declared = true
	availability := NewAvailability(cfg, scope.SkillsDir)
	item := planLocalItem(cfg, scope.SkillsDir, availability.ObserveOccupancy().Drift(name), name)
	// Why a link or Availability failed is in the events applyItem emits.
	outcome.Outcome = applyItem(availability, scope.SkillsDir, item, SyncDecision{}, nil, emit)
	return outcome
}
