package engine

import (
	"fmt"
	"os"
)

const (
	SyncRepoStart          = "repo_start"
	SyncFetchFailed        = "fetch_failed"
	SyncPathMissing        = "path_missing"
	SyncCopyFailed         = "copy_failed"
	SyncAvailabilityFailed = "availability_failed"
	SyncStateFailed        = "state_failed"
	SyncMaterialized       = "materialized"
	SyncSourceMissing      = "source_missing"
	SyncSymlinkFailed      = "symlink_failed"
	SyncSymlinked          = "symlinked"
	SyncCheckFailed        = "check_failed"
	SyncCommandStart       = "command_start"
	SyncCommandFailed      = "command_failed"
	SyncSkipped            = "skipped"
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
}

// SyncReport is the observable outcome of applying a SyncPlan.
type SyncReport struct {
	Configured []string
	Events     []SyncEvent
	Blocked    int
	Failed     int
	Unknown    []SkillFreshness
}

func (r *SyncReport) add(ev SyncEvent) {
	r.Events = append(r.Events, ev)
}

func (r *SyncReport) tally(outcome SyncOutcome) {
	switch outcome {
	case SyncBlocked:
		r.Blocked++
	case SyncFailed:
		r.Failed++
	}
}

// Converged reports whether every declared Skill reached its declared state.
func (r *SyncReport) Converged() bool {
	return r.Blocked == 0 && r.Failed == 0
}

// Apply materializes the planned Skills and applies Availability. The plan is
// taken as observed: decision lifts blocks, and nothing is re-observed.
// Availability failures fail closed for that Skill; fetch, copy, and symlink
// failures are events and continue. A failed command installer is an event;
// Availability is still applied.
func (plan *SyncPlan) Apply(decision SyncDecision, onProgress func(SyncEvent)) (*SyncReport, error) {
	report := &SyncReport{}
	emit := func(ev SyncEvent) {
		report.add(ev)
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
	if plan.StateError != "" {
		emit(SyncEvent{Kind: SyncStateFailed, Err: plan.StateError})
		report.tally(SyncFailed)
	}
	state, stateStore := plan.openState()
	report.Configured = plan.Names()

	for _, source := range plan.Sources {
		items := plan.SourceItems(source)
		emit(SyncEvent{Kind: SyncRepoStart, Source: source, Skills: itemNames(items)})
		for _, item := range items {
			if item.Block == SyncBlockUnknownBaseline {
				report.Unknown = append(report.Unknown, item.Freshness)
			}
			report.tally(plan.applyRemoteItem(item, decision, state, stateStore, emit))
		}
	}

	for _, item := range plan.LocalItems() {
		report.tally(applyLocalItem(plan.availability, plan.skillsDir, item, emit))
	}
	return report, nil
}

// applyRemoteItem Materializes one remote Skill, applies its Availability, and
// records the baseline it was applied from.
func (plan *SyncPlan) applyRemoteItem(item SyncPlanItem, decision SyncDecision, state ScopeState, stateStore *ScopeStateStore, emit func(SyncEvent)) SyncOutcome {
	if item.Block == SyncBlockCacheMissing {
		emit(SyncEvent{Kind: SyncFetchFailed, Source: item.Source, Skill: item.Name, Err: item.BlockReason})
		return SyncBlocked
	}
	if item.Err != "" {
		emit(SyncEvent{Kind: SyncFetchFailed, Source: item.Source, Skill: item.Name, Err: item.Err})
		return SyncFailed
	}
	action, block := item.Resolve(decision)
	if action == SyncActionSkip {
		emit(SyncEvent{Kind: SyncSkipped, Source: item.Source, Skill: item.Name, Err: string(block)})
		return SyncBlocked
	}
	if action == SyncActionMaterialize {
		if err := MaterializeRemoteSkill(item.Name, item.Freshness.Subpath, item.CachePath, plan.skillsDir); err != nil {
			emit(SyncEvent{Kind: SyncCopyFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
			return SyncFailed
		}
		emit(SyncEvent{Kind: SyncMaterialized, Source: item.Source, Skill: item.Name, Path: item.Freshness.Subpath})
	}
	if err := plan.availability.Apply(item.Name); err != nil {
		emit(SyncEvent{Kind: SyncAvailabilityFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
		return SyncFailed
	}
	if stateStore == nil {
		return SyncDone
	}
	skill := item.Freshness
	digests, err := DigestSkillContent(skill.ScopePath)
	if err != nil {
		emit(SyncEvent{Kind: SyncStateFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
		return SyncFailed
	}
	skill.CacheDigests = digests
	state.Skills[item.Name] = skill.appliedState(item.CachePath, item.LocalSHA)
	if err := stateStore.Save(state); err != nil {
		emit(SyncEvent{Kind: SyncStateFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
		return SyncFailed
	}
	return SyncDone
}

// openState prepares the Scope state for recording applied baselines. A Scope
// state that could not be read is reported by the plan and disables recording
// rather than stopping the Sync.
func (plan *SyncPlan) openState() (ScopeState, *ScopeStateStore) {
	if plan.StateError != "" {
		return ScopeState{}, nil
	}
	store, err := NewScopeStateStore(plan.skillsDir)
	if err != nil {
		return ScopeState{}, nil
	}
	state, err := store.Load()
	if err != nil {
		return ScopeState{}, nil
	}
	if state.Skills == nil {
		state.Skills = make(map[string]AppliedSkillState)
	}
	return state, store
}

// applyLocalItem Materializes one local Skill and applies its Availability. A
// missing Source or a check that does not pass blocks the Skill; a failed
// installer is a failure, and Availability is still applied.
func applyLocalItem(availability *Availability, skillsDir string, item SyncPlanItem, emit func(SyncEvent)) SyncOutcome {
	if item.Block == SyncBlockSourceMissing {
		emit(SyncEvent{Kind: SyncSourceMissing, Skill: item.Name, Path: item.SourcePath})
		return SyncBlocked
	}
	outcome := SyncDone
	if item.Kind == SyncItemCommand {
		if item.Check != "" {
			if _, _, err := RunCmd(item.Check, ""); err != nil {
				emit(SyncEvent{Kind: SyncCheckFailed, Skill: item.Name, Path: item.Check})
				return SyncBlocked
			}
		}
		emit(SyncEvent{Kind: SyncCommandStart, Skill: item.Name})
		if err := MaterializeCommand(item.Command); err != nil {
			emit(SyncEvent{Kind: SyncCommandFailed, Skill: item.Name, Err: err.Error()})
			outcome = SyncFailed
		}
	} else {
		if err := MaterializeLocalSymlink(item.Name, item.LinkTarget, skillsDir); err != nil {
			emit(SyncEvent{Kind: SyncSymlinkFailed, Skill: item.Name, Err: err.Error()})
			return SyncFailed
		}
		emit(SyncEvent{Kind: SyncSymlinked, Skill: item.Name, Target: item.SourcePath})
	}
	if err := availability.Apply(item.Name); err != nil {
		emit(SyncEvent{Kind: SyncAvailabilityFailed, Skill: item.Name, Err: err.Error()})
		return SyncFailed
	}
	return outcome
}

func itemNames(items []SyncPlanItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}
