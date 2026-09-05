package engine

import (
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// SyncItemKind is how one declared Skill reaches the Scope skills directory.
type SyncItemKind string

const (
	SyncItemRemote  SyncItemKind = "remote"
	SyncItemSymlink SyncItemKind = "symlink"
	SyncItemCommand SyncItemKind = "command"
)

// SyncAction is what Apply does with one planned Skill.
type SyncAction string

const (
	SyncActionNone        SyncAction = "none"
	SyncActionMaterialize SyncAction = "materialize"
	SyncActionSymlink     SyncAction = "symlink"
	SyncActionCommand     SyncAction = "command"
	SyncActionSkip        SyncAction = "skip"
)

// SyncBlock is what stands between a planned Skill and being written. Only
// local drift and an unknown baseline can be lifted by a SyncDecision; the
// rest are refusals whatever the user answers.
type SyncBlock string

const (
	SyncBlockNone            SyncBlock = ""
	SyncBlockLocalDrift      SyncBlock = SyncBlock(SkillLocalDrift)
	SyncBlockUnknownBaseline SyncBlock = SyncBlock(SkillUnknownBaseline)
	SyncBlockSourceMissing   SyncBlock = "source_missing"
	SyncBlockCacheMissing    SyncBlock = "cache_missing"
)

// SyncDecision is the user's answer to the blocks a plan reports. It reaches
// the plan as a pure transformation: resolving a plan under a decision never
// observes the filesystem a second time.
type SyncDecision struct {
	Force        bool
	AllowUnknown bool
}

// SyncPlanItem is one declared Skill as observed, with everything Apply needs
// to act on it. Items are flat and carry their Source; grouping by Source is
// presentation.
type SyncPlanItem struct {
	Name   string
	Kind   SyncItemKind
	Source string

	// Block is what was observed to stand in the way. Resolve decides whether
	// the decision lifts it; BlockReason carries any detail worth printing.
	Block       SyncBlock
	BlockReason string
	// Err is an observation that leaves the Skill unactionable regardless of
	// the decision, such as a Cache that cannot be read.
	Err string
	// Drift is declared Availability measured against the filesystem.
	Drift AvailabilityDrift

	// Remote Skills. CachePath is the Source's Cache working copy, the root
	// the Skill's Subpath is resolved against; Freshness carries the Skill's
	// own path inside it.
	Freshness SkillFreshness
	CachePath string
	LocalSHA  string
	// NeedsWrite is set when the Cache is not known to match the Scope copy.
	NeedsWrite bool

	// Local symlink Skills.
	SourcePath  string
	LinkPath    string
	LinkTarget  string
	LinkCurrent bool

	// Command Skills. Installed records whether the Skill is already present
	// in the Scope; an installer's own state is not observable without
	// running its check, which planning must not do.
	Command   string
	Check     string
	Installed bool
}

// Resolve is what this item does under decision.
func (item SyncPlanItem) Resolve(decision SyncDecision) (SyncAction, SyncBlock) {
	switch item.Block {
	case SyncBlockSourceMissing, SyncBlockCacheMissing:
		return SyncActionSkip, item.Block
	case SyncBlockLocalDrift:
		if !decision.Force {
			return SyncActionSkip, item.Block
		}
	case SyncBlockUnknownBaseline:
		if !decision.Force && !decision.AllowUnknown {
			return SyncActionSkip, item.Block
		}
	}
	switch item.Kind {
	case SyncItemSymlink:
		return SyncActionSymlink, SyncBlockNone
	case SyncItemCommand:
		return SyncActionCommand, SyncBlockNone
	}
	if item.NeedsWrite || decision.Force {
		return SyncActionMaterialize, SyncBlockNone
	}
	return SyncActionNone, SyncBlockNone
}

// changes reports whether acting on this item would alter the Scope. A
// command Skill already present in the Scope does not count: its installer is
// guarded by its own check, and running that check to find out is exactly what
// planning must not do.
func (item SyncPlanItem) changes(action SyncAction) bool {
	switch action {
	case SyncActionMaterialize:
		return true
	case SyncActionCommand:
		return !item.Installed
	case SyncActionSymlink:
		return !item.LinkCurrent
	}
	return false
}

// SyncPlan is every declared Skill of one Scope, observed once. It is the same
// value from preview through confirmation to Apply: the decision the user makes
// in between is applied to the plan, not re-derived from the filesystem.
type SyncPlan struct {
	Sources    []string
	Items      []SyncPlanItem
	StateError string

	cfg          *config.Config
	skillsDir    string
	availability *Availability
}

// PlanSync observes the Scope and derives what Sync would do. It writes
// nothing, and runs no Skill-supplied command.
func PlanSync(cfg *config.Config, skillsDir, cacheDir string) (*SyncPlan, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	snapshot, err := InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		return nil, err
	}
	availability := NewAvailability(cfg, skillsDir)
	plan := &SyncPlan{
		StateError:   snapshot.StateError,
		cfg:          cfg,
		skillsDir:    skillsDir,
		availability: availability,
	}
	for _, repository := range snapshot.Repositories {
		plan.Sources = append(plan.Sources, repository.Source)
		for _, skill := range repository.Skills {
			item := SyncPlanItem{
				Name:       skill.Name,
				Kind:       SyncItemRemote,
				Source:     repository.Source,
				Drift:      availability.ObserveAvailability(skill.Name),
				Freshness:  skill,
				CachePath:  repository.CachePath,
				LocalSHA:   repository.LocalSHA,
				NeedsWrite: skill.Status == SkillMissing || skill.Status == SkillCacheUpdateAvailable || skill.Status == SkillUnknownBaseline,
			}
			switch skill.Status {
			case SkillLocalDrift:
				item.Block = SyncBlockLocalDrift
			case SkillUnknownBaseline:
				item.Block = SyncBlockUnknownBaseline
			}
			// A Cache that was never fetched is a Block: Update is the way
			// out, and no decision here can substitute for it. A Cache that
			// cannot be read is a genuine failure.
			if err := skill.validateCache(); err != nil {
				if skill.Status == SkillUnverified {
					item.Block = SyncBlockCacheMissing
					item.BlockReason = err.Error()
				} else {
					item.Err = err.Error()
				}
			}
			plan.Items = append(plan.Items, item)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "symlink" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(cfg, skillsDir, availability.ObserveAvailability(name), name))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "command" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(cfg, skillsDir, availability.ObserveAvailability(name), name))
	}
	return plan, nil
}

// planLocalItem observes one declared local Skill. Add reuses it so a newly
// declared Skill is Materialized exactly the way Sync would.
//
// It takes the Config, the skills directory and the observed Drift rather than
// an *Availability, because it needs none of that type's behavior: it used to
// take one and reach through it for cfg and skillsDir, which read as a
// dependency it does not have.
func planLocalItem(cfg *config.Config, skillsDir string, drift AvailabilityDrift, name string) SyncPlanItem {
	info := cfg.Local[name]
	item := SyncPlanItem{
		Name:   name,
		Source: "local",
		Drift:  drift,
	}
	if info.Type == "command" {
		item.Kind = SyncItemCommand
		item.Command = info.Command
		item.Check = info.Check
		_, err := os.Stat(filepath.Join(skillsDir, name))
		item.Installed = err == nil
		return item
	}
	absSource := models.ResolveLocalSourcePath(info.Source, skillsDir)
	item.Kind = SyncItemSymlink
	item.SourcePath = absSource
	item.LinkPath = filepath.Join(skillsDir, name)
	item.LinkTarget = models.LocalSymlinkTarget(absSource, skillsDir)
	if _, err := os.Stat(absSource); err != nil {
		item.Block = SyncBlockSourceMissing
		return item
	}
	target, err := os.Readlink(item.LinkPath)
	item.LinkCurrent = err == nil && target == item.LinkTarget
	return item
}

// Unknown is the Skills whose Scope copy has no recorded baseline. Sync asks
// about these before it will overwrite them.
func (plan *SyncPlan) Unknown() []SkillFreshness {
	var unknown []SkillFreshness
	for _, item := range plan.Items {
		if item.Block == SyncBlockUnknownBaseline {
			unknown = append(unknown, item.Freshness)
		}
	}
	return unknown
}

// Pending is the Skills that Apply would change under decision.
func (plan *SyncPlan) Pending(decision SyncDecision) []SyncPlanItem {
	var pending []SyncPlanItem
	for _, item := range plan.Items {
		action, _ := item.Resolve(decision)
		if item.Err != "" || action == SyncActionSkip {
			continue
		}
		if item.changes(action) || !item.Drift.Empty() {
			pending = append(pending, item)
		}
	}
	return pending
}

// Blocked is the Skills Apply would refuse to act on under decision, and that
// a decision or another command could still unblock.
func (plan *SyncPlan) Blocked(decision SyncDecision) []SyncPlanItem {
	var blocked []SyncPlanItem
	for _, item := range plan.Items {
		if item.Err != "" {
			continue
		}
		if action, _ := item.Resolve(decision); action == SyncActionSkip {
			blocked = append(blocked, item)
		}
	}
	return blocked
}

// Failed is the Skills whose observation itself did not succeed, plus a Scope
// baseline that could not be read. Nothing the user answers changes these.
func (plan *SyncPlan) Failed() []SyncPlanItem {
	var failed []SyncPlanItem
	for _, item := range plan.Items {
		if item.Err != "" {
			failed = append(failed, item)
		}
	}
	return failed
}

// Fresh reports whether the Scope already matches its Config under decision:
// nothing to change, nothing refused, nothing broken.
func (plan *SyncPlan) Fresh(decision SyncDecision) bool {
	return plan.FailedCount() == 0 && len(plan.Pending(decision)) == 0 && len(plan.Blocked(decision)) == 0
}

// FailedCount counts the failures observable before anything is applied.
func (plan *SyncPlan) FailedCount() int {
	count := len(plan.Failed())
	if plan.StateError != "" {
		count++
	}
	return count
}

// Names is every declared Skill in the plan, sorted.
func (plan *SyncPlan) Names() []string {
	names := make(map[string]struct{}, len(plan.Items))
	for _, item := range plan.Items {
		names[item.Name] = struct{}{}
	}
	return slices.Sorted(maps.Keys(names))
}

// SourceItems is the remote Skills declared for one Source, in plan order.
func (plan *SyncPlan) SourceItems(source string) []SyncPlanItem {
	var items []SyncPlanItem
	for _, item := range plan.Items {
		if item.Kind == SyncItemRemote && item.Source == source {
			items = append(items, item)
		}
	}
	return items
}

// LocalItems is the local symlink and command Skills, in plan order.
func (plan *SyncPlan) LocalItems() []SyncPlanItem {
	var items []SyncPlanItem
	for _, item := range plan.Items {
		if item.Kind != SyncItemRemote {
			items = append(items, item)
		}
	}
	return items
}
