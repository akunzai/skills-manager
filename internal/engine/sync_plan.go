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
	// the decision lifts it.
	Block SyncBlock
	// Err is an observation that leaves the Skill unactionable regardless of
	// the decision, such as a Cache that cannot be read.
	Err string
	// Drift is declared Availability measured against the filesystem.
	Drift AvailabilityDrift

	// Remote Skills.
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

	// Command Skills.
	Command string
	Check   string
}

// Resolve is what this item does under decision.
func (item SyncPlanItem) Resolve(decision SyncDecision) (SyncAction, SyncBlock) {
	switch item.Block {
	case SyncBlockSourceMissing:
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

// changes reports whether acting on this item would alter the Scope.
func (item SyncPlanItem) changes(action SyncAction) bool {
	switch action {
	case SyncActionMaterialize, SyncActionCommand:
		return true
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
			if err := skill.validateCache(); err != nil {
				item.Err = err.Error()
			}
			plan.Items = append(plan.Items, item)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "symlink" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(availability, name))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "command" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(availability, name))
	}
	return plan, nil
}

// planLocalItem observes one declared local Skill. Add reuses it so a newly
// declared Skill is Materialized exactly the way Sync would.
func planLocalItem(availability *Availability, name string) SyncPlanItem {
	info := availability.cfg.Local[name]
	item := SyncPlanItem{
		Name:   name,
		Source: "local",
		Drift:  availability.ObserveAvailability(name),
	}
	if info.Type == "command" {
		item.Kind = SyncItemCommand
		item.Command = info.Command
		item.Check = info.Check
		return item
	}
	absSource := models.ResolveLocalSourcePath(info.Source, availability.skillsDir)
	item.Kind = SyncItemSymlink
	item.SourcePath = absSource
	item.LinkPath = filepath.Join(availability.skillsDir, name)
	item.LinkTarget = models.LocalSymlinkTarget(absSource, availability.skillsDir)
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

// Blocked is the Skills Apply would refuse to act on under decision.
func (plan *SyncPlan) Blocked(decision SyncDecision) []SyncPlanItem {
	var blocked []SyncPlanItem
	for _, item := range plan.Items {
		action, _ := item.Resolve(decision)
		if item.Err != "" || action == SyncActionSkip {
			blocked = append(blocked, item)
		}
	}
	return blocked
}

// Fresh reports whether the Scope already matches its Config under decision:
// nothing to change and nothing refused.
func (plan *SyncPlan) Fresh(decision SyncDecision) bool {
	return plan.StateError == "" && len(plan.Pending(decision)) == 0 && len(plan.Blocked(decision)) == 0
}

// Unresolved counts everything standing between this plan and a converged
// Scope: a Scope state that could not be read, plus every refused Skill.
func (plan *SyncPlan) Unresolved(decision SyncDecision) int {
	count := len(plan.Blocked(decision))
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
