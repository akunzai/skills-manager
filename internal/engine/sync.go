package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
)

const (
	SyncRepoStart          = "repo_start"
	SyncFetchFailed        = "fetch_failed"
	SyncPathMissing        = "path_missing"
	SyncCopyFailed         = "copy_failed"
	SyncAvailabilityFailed = "availability_failed"
	SyncAvailabilityCopied = "availability_copied"
	SyncStateFailed        = "state_failed"
	// SyncStateUnreadable is an unreadable Scope state in a Scope that needs
	// no Baseline: a warning, not a failure (ADR-0002).
	SyncStateUnreadable = "state_unreadable"
	SyncMaterialized    = "materialized"
	SyncSourceMissing   = "source_missing"
	SyncSymlinkFailed   = "symlink_failed"
	SyncSymlinked       = "symlinked"
	SyncCheckFailed     = "check_failed"
	SyncCommandStart    = "command_start"
	SyncCommandFailed   = "command_failed"
	SyncSkipped         = "skipped"
	SyncRenamed         = "renamed"
	SyncRenameFailed    = "rename_failed"
	// SyncItemStart and SyncItemDone bracket each declared Skill, carrying
	// the Action about to be taken and the Outcome reached. They drive live
	// progress only and are not recorded in the SyncReport.
	SyncItemStart = "item_start"
	SyncItemDone  = "item_done"
)

// SyncOutcome is what became of one declared Skill. Blocked and Failed ask
// different things of the user — decide whether to overwrite, versus find out
// what broke — so Sync counts and reports them apart.
type SyncOutcome string

const (
	SyncDone    SyncOutcome = "done"
	SyncBlocked SyncOutcome = "blocked"
	SyncFailed  SyncOutcome = "failed"
)

// SyncEvent is one step of applying a SyncPlan for the CLI to print.
type SyncEvent struct {
	Kind       string
	Source     string
	Skill      string
	Skills     []string
	Path       string
	Target     string
	Err        string
	Missing    []string
	Unexpected []string
	// Agents carries the Agents named by an Availability event — for
	// SyncAvailabilityCopied, those whose Availability is a copy of the Skill
	// rather than a link to it.
	Agents []string
	// Action is what Apply is about to do with the Skill of a SyncItemStart.
	Action SyncAction
	// Outcome is what became of the Skill of a SyncItemDone.
	Outcome SyncOutcome
}

// SyncTally counts the declared Skills that did not reach their declared
// state, blocked apart from failed (ADR-0002). Add and Sync both count through
// it, so the two cannot disagree about what a Skill's outcome adds up to.
type SyncTally struct {
	Blocked int
	Failed  int
}

func (t *SyncTally) tally(outcome SyncOutcome) {
	switch outcome {
	case SyncBlocked:
		t.Blocked++
	case SyncFailed:
		t.Failed++
	}
}

// SyncReport is the observable outcome of applying a SyncPlan.
type SyncReport struct {
	SyncTally
	Configured []string
	Events     []SyncEvent
	Unknown    []SkillFreshness
	// Updated is the Skills whose Scope copy was replaced with changed
	// content, and Restored those written where the Scope had none. A Skill
	// rewritten unchanged under --force is neither, and a rename is its own
	// event.
	Updated  []string
	Restored []string
	// forceable is whether --force would lift a block left under the
	// decision Apply applied.
	forceable bool
}

func (r *SyncReport) add(ev SyncEvent) {
	r.Events = append(r.Events, ev)
}

// noteMaterialized files a Materialized Skill under what its Scope copy was
// before.
func (r *SyncReport) noteMaterialized(skill string, before SkillFreshnessStatus) {
	switch before {
	case SkillMissing:
		r.Restored = append(r.Restored, skill)
	case SkillCacheUpdateAvailable, SkillUnknownBaseline, SkillLocalDrift:
		r.Updated = append(r.Updated, skill)
	}
}

// Summary is where the Scope stands after Apply. Nothing is pending once
// applied: each Skill was done, blocked, or failed.
func (r *SyncReport) Summary() SyncSummary {
	return SyncSummary{Configured: len(r.Configured), Blocked: r.Blocked, Failed: r.Failed, Forceable: r.forceable}
}

// Apply materializes the planned Skills and applies Availability. The plan is
// taken as observed: decision lifts blocks, and nothing is re-observed.
// Availability failures fail closed for that Skill; fetch, copy, and symlink
// failures are events and continue. A failed command installer is an event;
// Availability is still applied.
func (plan *SyncPlan) Apply(decision SyncDecision, onProgress func(SyncEvent)) (*SyncReport, error) {
	report := &SyncReport{forceable: plan.Forceable(decision)}
	// A renamed Skill's new name is not planned, so it reads as neither
	// Updated nor Restored.
	before := make(map[string]SkillFreshnessStatus)
	for _, item := range plan.Items {
		if item.Kind == SyncItemRemote {
			before[item.Name] = item.Freshness.Status
		}
	}
	emit := func(ev SyncEvent) {
		report.add(ev)
		if ev.Kind == SyncMaterialized {
			report.noteMaterialized(ev.Skill, before[ev.Skill])
		}
		if onProgress != nil {
			onProgress(ev)
		}
	}
	// Scope-level preconditions once, up front: without a skills directory
	// every Skill below would fail for the same reason.
	if len(plan.Items) > 0 {
		if err := os.MkdirAll(plan.skillsDir, 0o755); err != nil {
			return report, fmt.Errorf("failed to create skills dir: %w", err)
		}
	}
	baselines := plan.openBaselines()
	switch baselines.Verdict(plan.needsBaselines()) {
	case StateFail:
		emit(SyncEvent{Kind: SyncStateFailed, Err: baselines.Err().Error()})
		report.tally(SyncFailed)
	case StateWarn:
		emit(SyncEvent{Kind: SyncStateUnreadable, Err: baselines.Err().Error()})
	}
	report.Configured = plan.Names()
	// progress tells onProgress alone where each Skill stands.
	progress := func(ev SyncEvent) {
		if onProgress != nil {
			onProgress(ev)
		}
	}
	finish := func(item SyncPlanItem, outcome SyncOutcome) {
		progress(SyncEvent{Kind: SyncItemDone, Source: item.Source, Skill: item.Name, Outcome: outcome})
		report.tally(outcome)
	}

	for _, source := range plan.Sources {
		items := plan.SourceItems(source)
		emit(SyncEvent{Kind: SyncRepoStart, Source: source, Skills: itemNames(items)})
		for _, item := range items {
			if item.Block == SyncBlockUnknownBaseline {
				report.Unknown = append(report.Unknown, item.Freshness)
			}
			action, _ := item.Resolve(decision)
			progress(SyncEvent{Kind: SyncItemStart, Source: source, Skill: item.Name, Action: action})
			if action == SyncActionRename {
				finish(item, plan.applyRename(item, baselines, emit))
				report.Configured = config.GetConfiguredSkillNames(plan.cfg)
				continue
			}
			finish(item, applyItem(plan.availability, plan.skillsDir, item, decision, baselines, emit))
		}
	}

	for _, item := range plan.LocalItems() {
		action, _ := item.Resolve(decision)
		progress(SyncEvent{Kind: SyncItemStart, Skill: item.Name, Action: action})
		finish(item, applyItem(plan.availability, plan.skillsDir, item, decision, baselines, emit))
	}
	return report, nil
}

func emitSync(emit func(SyncEvent), ev SyncEvent) {
	if emit != nil {
		emit(ev)
	}
}

// applyItem Materializes one planned Skill and applies its Availability. Add
// and Sync apply every Skill through here. Each reason a Skill was not applied
// is emitted as an event, so the outcome is all a caller needs back.
func applyItem(availability *Availability, skillsDir string, item SyncPlanItem, decision SyncDecision, baselines *Baselines, emit func(SyncEvent)) SyncOutcome {
	var outcome SyncOutcome
	if item.Kind == SyncItemRemote {
		outcome, _ = applyRemoteItem(availability, skillsDir, item, decision, baselines, emit)
	} else {
		outcome, _ = applyLocalItem(availability, skillsDir, item, emit)
	}
	return outcome
}

// applyRemoteItem Materializes one remote Skill, applies its Availability, and
// records the baseline it was applied from.
func applyRemoteItem(availability *Availability, skillsDir string, item SyncPlanItem, decision SyncDecision, baselines *Baselines, emit func(SyncEvent)) (SyncOutcome, error) {
	if item.Block == SyncBlockCacheMissing {
		emitSync(emit, SyncEvent{Kind: SyncFetchFailed, Source: item.Source, Skill: item.Name, Err: item.BlockReason})
		return SyncBlocked, fmt.Errorf("%s", item.BlockReason)
	}
	if item.Err != "" {
		emitSync(emit, SyncEvent{Kind: SyncFetchFailed, Source: item.Source, Skill: item.Name, Err: item.Err})
		return SyncFailed, fmt.Errorf("%s", item.Err)
	}
	action, block := item.Resolve(decision)
	if action == SyncActionSkip {
		reason := string(block)
		if item.BlockReason != "" {
			reason += ": " + item.BlockReason
		}
		emitSync(emit, SyncEvent{Kind: SyncSkipped, Source: item.Source, Skill: item.Name, Err: reason})
		return SyncBlocked, fmt.Errorf("%s", block)
	}
	if action == SyncActionMaterialize {
		if err := MaterializeRemoteSkill(item.Name, item.Freshness.Subpath, item.CachePath, skillsDir); err != nil {
			kind := SyncCopyFailed
			if errors.Is(err, errRepoPathMissing) {
				kind = SyncPathMissing
			}
			emitSync(emit, SyncEvent{Kind: kind, Source: item.Source, Skill: item.Name, Path: item.Freshness.Subpath, Err: err.Error()})
			return SyncFailed, err
		}
		emitSync(emit, SyncEvent{Kind: SyncMaterialized, Source: item.Source, Skill: item.Name, Path: item.Freshness.Subpath})
	}
	copied, err := availability.Apply(item.Name)
	if err != nil {
		emitSync(emit, SyncEvent{Kind: SyncAvailabilityFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
		return SyncFailed, err
	}
	if len(copied) > 0 {
		emitSync(emit, SyncEvent{Kind: SyncAvailabilityCopied, Source: item.Source, Skill: item.Name, Agents: copied})
	}
	// An unreadable Scope state is the Scope's verdict, counted once by the
	// caller, not a failure of each Skill.
	if err := baselines.Record(item.Freshness, item.CachePath, item.LocalSHA); err != nil && !errors.Is(err, ErrNotRecorded) {
		emitSync(emit, SyncEvent{Kind: SyncStateFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
		return SyncFailed, err
	}
	return SyncDone, nil
}

// openBaselines opens the Scope's Baselines for recording. A Scope state the
// plan could not read stays unreadable for the whole Sync, even if it has since
// become readable, so Sync records against the state it planned from.
func (plan *SyncPlan) openBaselines() *Baselines {
	if plan.StateError != "" {
		return plan.plannedBaselines()
	}
	return OpenBaselines(plan.skillsDir)
}

// plannedBaselines is the Scope state as far as the plan observed it: only
// whether it could be read.
func (plan *SyncPlan) plannedBaselines() *Baselines {
	if plan.StateError == "" {
		return &Baselines{}
	}
	return &Baselines{err: errors.New(plan.StateError)}
}

// applyLocalItem Materializes one local Skill and applies its Availability. A
// missing Source or a check that does not pass blocks the Skill; a failed
// installer is a failure, and Availability is still applied.
func applyLocalItem(availability *Availability, skillsDir string, item SyncPlanItem, emit func(SyncEvent)) (SyncOutcome, error) {
	if item.Block == SyncBlockSourceMissing {
		emitSync(emit, SyncEvent{Kind: SyncSourceMissing, Skill: item.Name, Path: item.SourcePath})
		return SyncBlocked, fmt.Errorf("local symlink source missing: %s", item.SourcePath)
	}
	outcome := SyncDone
	var applyErr error
	if item.Kind == SyncItemCommand {
		if item.Check != "" {
			if _, _, err := RunCmd(item.Check, ""); err != nil {
				emitSync(emit, SyncEvent{Kind: SyncCheckFailed, Skill: item.Name, Path: item.Check})
				return SyncBlocked, fmt.Errorf("command check %q failed, skipping %s", item.Check, item.Name)
			}
		}
		emitSync(emit, SyncEvent{Kind: SyncCommandStart, Skill: item.Name})
		if err := MaterializeCommand(item.Command); err != nil {
			emitSync(emit, SyncEvent{Kind: SyncCommandFailed, Skill: item.Name, Err: err.Error()})
			outcome = SyncFailed
			applyErr = err
		}
	} else {
		if err := MaterializeLocalSymlink(item.Name, item.LinkTarget, skillsDir); err != nil {
			emitSync(emit, SyncEvent{Kind: SyncSymlinkFailed, Skill: item.Name, Err: err.Error()})
			return SyncFailed, err
		}
		emitSync(emit, SyncEvent{Kind: SyncSymlinked, Skill: item.Name, Target: item.SourcePath})
	}
	copied, err := availability.Apply(item.Name)
	if err != nil {
		emitSync(emit, SyncEvent{Kind: SyncAvailabilityFailed, Skill: item.Name, Err: err.Error()})
		return SyncFailed, err
	}
	if len(copied) > 0 {
		emitSync(emit, SyncEvent{Kind: SyncAvailabilityCopied, Skill: item.Name, Agents: copied})
	}
	return outcome, applyErr
}

func itemNames(items []SyncPlanItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

// applyRename migrates one renamed Skill (ADR 0007). Retire saves Config
// first, so an interruption leaves the new name declared and the next Sync
// Materializes it; the old copy then shows up as Untracked rather than being
// lost. Retire takes the old copy's Availability and Baseline with it, and
// anything it could not remove fails the item; otherwise the new Skill is
// applied exactly as Sync applies any Skill. An unreadable Scope state is not
// this item's failure: Apply already reported it once.
func (plan *SyncPlan) applyRename(item SyncPlanItem, baselines *Baselines, emit func(SyncEvent)) SyncOutcome {
	old, skill := item.Name, item.Freshness
	fail := func(err error) SyncOutcome {
		emitSync(emit, SyncEvent{Kind: SyncRenameFailed, Source: item.Source, Skill: old, Target: skill.RenamedTo, Err: err.Error()})
		return SyncFailed
	}
	if plan.configPath == "" {
		return fail(errors.New("no Config path to record the rename in"))
	}
	repo := plan.cfg.Remote[item.Source]
	delete(repo.Skills, old)
	if !item.RenameTargetDeclared {
		repo.Skills[skill.RenamedTo] = skill.RenamedSubpath
	}
	plan.cfg.Remote[item.Source] = repo
	if override, ok := plan.cfg.Settings.Availability[old]; ok {
		if _, taken := plan.cfg.Settings.Availability[skill.RenamedTo]; !taken && !item.RenameTargetDeclared {
			plan.cfg.Settings.Availability[skill.RenamedTo] = override
		}
		delete(plan.cfg.Settings.Availability, old)
	}
	retired, err := Retire(plan.cfg, plan.configPath, plan.skillsDir, []string{old}, baselines)
	if err != nil {
		return fail(err)
	}
	plan.availability = NewAvailability(plan.cfg, plan.skillsDir)
	if err := retired[0].Err(); err != nil {
		return fail(err)
	}
	emitSync(emit, SyncEvent{Kind: SyncRenamed, Source: item.Source, Skill: old, Target: skill.RenamedTo})
	if item.RenameTargetDeclared {
		return SyncDone
	}
	renamed := planDeclaredRemoteItem(item.Source, item.cache, SkillFreshness{
		Name:      skill.RenamedTo,
		Source:    item.Source,
		Subpath:   skill.RenamedSubpath,
		ScopePath: filepath.Join(plan.skillsDir, skill.RenamedTo),
		CachePath: filepath.Join(item.CachePath, filepath.FromSlash(skill.RenamedSubpath)),
	}, plan.availability.ObserveOccupancy().Drift(skill.RenamedTo))
	return applyItem(plan.availability, plan.skillsDir, renamed, SyncDecision{}, baselines, emit)
}
