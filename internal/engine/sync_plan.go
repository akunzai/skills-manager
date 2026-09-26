package engine

import (
	"fmt"
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
	// SyncActionRename migrates a Skill its Source renamed: Config, the new
	// Skill, and removal of the old copy (ADR 0007).
	SyncActionRename SyncAction = "rename"
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
	// SyncBlockRemovedUpstream is a Skill its Source no longer has and no
	// Skill replaces. Only rm resolves it.
	SyncBlockRemovedUpstream SyncBlock = "removed_upstream"
	// SyncBlockRenameOccupied is a rename whose new name is already occupied
	// on the skills directory by something Config does not declare.
	SyncBlockRenameOccupied SyncBlock = "rename_target_occupied"
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
	// RenameTargetDeclared is set on a rename whose new name the Scope
	// already declares: the rename only drops the old Skill.
	RenameTargetDeclared bool

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
	case SyncBlockSourceMissing, SyncBlockCacheMissing, SyncBlockRemovedUpstream, SyncBlockRenameOccupied:
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
	if item.Freshness.Status == SkillRenamed {
		return SyncActionRename, SyncBlockNone
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
	case SyncActionMaterialize, SyncActionRename:
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
	Sources []string
	Items   []SyncPlanItem
	// StateError is why the Scope state could not be read. It is a failure
	// only when NeedsBaselines (ADR-0002).
	StateError string

	cfg          *config.Config
	configPath   string
	skillsDir    string
	availability *Availability
}

// PlanSync observes the Scope and derives what Sync would do. It writes
// nothing, and runs no Skill-supplied command. Apply saves Config to
// configPath when it migrates a renamed Skill.
func PlanSync(cfg *config.Config, configPath, skillsDir, cacheDir string) (*SyncPlan, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	snapshot, err := InspectFreshness(cfg, skillsDir, cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		return nil, err
	}
	availability := NewAvailability(cfg, skillsDir)
	occupancy := availability.ObserveOccupancy()
	plan := &SyncPlan{
		StateError:   snapshot.StateError,
		cfg:          cfg,
		configPath:   configPath,
		skillsDir:    skillsDir,
		availability: availability,
	}
	for _, repository := range snapshot.Repositories {
		plan.Sources = append(plan.Sources, repository.Source)
		for _, skill := range repository.Skills {
			item := planRemoteItem(
				repository.Source, repository.CachePath, repository.LocalSHA,
				skill, occupancy.Drift(skill.Name),
			)
			if skill.Status == SkillRenamed {
				item = planRename(cfg, skillsDir, item)
			}
			plan.Items = append(plan.Items, item)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "symlink" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(cfg, skillsDir, occupancy.Drift(name), name))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Local)) {
		if cfg.Local[name].Type != "command" {
			continue
		}
		plan.Items = append(plan.Items, planLocalItem(cfg, skillsDir, occupancy.Drift(name), name))
	}
	return plan, nil
}

// planDeclaredRemoteItem plans a remote Skill that was just declared, by Add or
// as the new name of a Rename. It is always Materialized: neither Drift nor a
// missing Baseline blocks it, because Add asked before overwriting and a
// Rename already protected the old copy. skill carries no Freshness status.
func planDeclaredRemoteItem(source, cachePath, localSHA string, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	item := baseRemoteItem(source, cachePath, localSHA, skill, drift)
	item.NeedsWrite = true
	return item
}

// baseRemoteItem is the part of a remote item every plan shares.
func baseRemoteItem(source, cachePath, localSHA string, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	return SyncPlanItem{
		Name:      skill.Name,
		Kind:      SyncItemRemote,
		Source:    source,
		Drift:     drift,
		Freshness: skill,
		CachePath: cachePath,
		LocalSHA:  localSHA,
	}
}

// planRemoteItem plans one declared remote Skill from its classified
// Freshness: Sync reconciling an existing declaration.
func planRemoteItem(source, cachePath, localSHA string, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	item := baseRemoteItem(source, cachePath, localSHA, skill, drift)
	item.NeedsWrite = skill.Status == SkillMissing || skill.Status == SkillCacheUpdateAvailable || skill.Status == SkillUnknownBaseline
	switch skill.Status {
	case SkillLocalDrift:
		item.Block = SyncBlockLocalDrift
	case SkillUnknownBaseline:
		item.Block = SyncBlockUnknownBaseline
	}
	switch skill.Status {
	case SkillRemovedUpstream:
		item.Block = SyncBlockRemovedUpstream
		item.BlockReason = fmt.Sprintf("no longer in Source %s; run 'skills rm %s'", source, skill.Name)
		return item
	case SkillRenamed:
		return item
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
	return item
}

// planRename decides what stands in the way of migrating a renamed Skill. The
// old copy is protected like any Scope copy: Drift and an unknown baseline
// block the whole rename, so Config never names a Skill Sync did not write.
func planRename(cfg *config.Config, skillsDir string, item SyncPlanItem) SyncPlanItem {
	skill := item.Freshness
	_, _, item.RenameTargetDeclared = config.FindSkillSource(cfg, skill.RenamedTo)
	switch skill.ScopeCopy {
	case ScopeCopyDrift:
		item.Block = SyncBlockLocalDrift
	case ScopeCopyUnknown:
		item.Block = SyncBlockUnknownBaseline
	}
	if !item.RenameTargetDeclared {
		if _, err := os.Lstat(filepath.Join(skillsDir, skill.RenamedTo)); err == nil {
			item.Block = SyncBlockRenameOccupied
			item.BlockReason = fmt.Sprintf("%s is already on %s and not declared in Config", skill.RenamedTo, skillsDir)
		}
	}
	return item
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

// Forceable reports whether --force would lift any block under decision.
// Other blocks — a missing Cache, a Skill removed upstream, an occupied rename
// target — each name their own way out.
func (plan *SyncPlan) Forceable(decision SyncDecision) bool {
	for _, item := range plan.Blocked(decision) {
		if item.Block == SyncBlockLocalDrift || item.Block == SyncBlockUnknownBaseline {
			return true
		}
	}
	return false
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

// SyncSummary is where a Scope stands against its Config: how many Skills it
// declares, how many a Sync would still change, refuses, or cannot act on,
// and whether --force would lift a refusal. A plan summarizes it before
// Apply, a SyncReport after. The CLI words it and picks the exit code
// (ADR-0002).
type SyncSummary struct {
	Configured int
	Pending    int
	Blocked    int
	Failed     int
	Forceable  bool
}

// Converged reports whether the Scope matches its Config: nothing to change,
// nothing refused, nothing broken.
func (s SyncSummary) Converged() bool {
	return s.Pending == 0 && s.Blocked == 0 && s.Failed == 0
}

// Summary is where the Scope stands if Apply ran under decision.
func (plan *SyncPlan) Summary(decision SyncDecision) SyncSummary {
	return SyncSummary{
		Configured: len(plan.Names()),
		Pending:    len(plan.Pending(decision)),
		Blocked:    len(plan.Blocked(decision)),
		Failed:     plan.FailedCount(),
		Forceable:  plan.Forceable(decision),
	}
}

// FailedCount counts the failures observable before anything is applied.
func (plan *SyncPlan) FailedCount() int {
	count := len(plan.Failed())
	if plan.StateError != "" && plan.NeedsBaselines() {
		count++
	}
	return count
}

// NeedsBaselines reports whether applying the plan records or compares a
// Baseline: whether it declares a remote Skill. Without one, an unreadable
// Scope state loses nothing and is a warning, not a failure (ADR-0002).
func (plan *SyncPlan) NeedsBaselines() bool {
	return slices.ContainsFunc(plan.Items, func(item SyncPlanItem) bool { return item.Kind == SyncItemRemote })
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
