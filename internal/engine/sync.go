package engine

import (
	"fmt"
	"os"
)

const (
	SyncRepoStart     = "repo_start"
	SyncRefreshStart  = "refresh_start"
	SyncRefreshDone   = "refresh_done"
	SyncFetchFailed   = "fetch_failed"
	SyncPathMissing   = "path_missing"
	SyncCopyFailed    = "copy_failed"
	SyncMaterialized  = "materialized"
	SyncWouldSync     = "would_sync"
	SyncWouldDrift    = "would_drift"
	SyncSourceMissing = "source_missing"
	SyncWouldSymlink  = "would_symlink"
	SyncSymlinkFailed = "symlink_failed"
	SyncSymlinked     = "symlinked"
	SyncCheckFailed   = "check_failed"
	SyncWouldCommand  = "would_command"
	SyncCommandStart  = "command_start"
	SyncCommandFailed = "command_failed"
	SyncSkipped       = "skipped"
	SyncInSync        = "in_sync"
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
	Changed    bool
	Failures   int
	Unknown    []SkillFreshness
}

func (r *SyncReport) add(ev SyncEvent) {
	r.Events = append(r.Events, ev)
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
		switch ev.Kind {
		case SyncSourceMissing, SyncSymlinkFailed, SyncCheckFailed, SyncCommandFailed:
			report.Failures++
		}
		if onProgress != nil {
			onProgress(ev)
		}
	}
	if plan.StateError != "" {
		emit(SyncEvent{Kind: SyncSkipped, Err: plan.StateError})
		report.Failures++
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
			if item.Err != "" {
				emit(SyncEvent{Kind: SyncFetchFailed, Source: item.Source, Skill: item.Name, Err: item.Err})
				report.Failures++
				continue
			}
			action, block := item.Resolve(decision)
			if action == SyncActionSkip {
				emit(SyncEvent{Kind: SyncSkipped, Source: item.Source, Skill: item.Name, Err: string(block)})
				report.Failures++
				continue
			}
			if err := os.MkdirAll(plan.skillsDir, 0o755); err != nil {
				emit(SyncEvent{Kind: SyncCopyFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
				report.Failures++
				continue
			}
			if action == SyncActionMaterialize {
				if err := MaterializeRemoteSkill(item.Name, item.Freshness.Subpath, item.CachePath, plan.skillsDir); err != nil {
					emit(SyncEvent{Kind: SyncCopyFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
					report.Failures++
					continue
				}
				emit(SyncEvent{Kind: SyncMaterialized, Source: item.Source, Skill: item.Name, Path: item.Freshness.Subpath})
				report.Changed = true
			} else {
				emit(SyncEvent{Kind: SyncInSync, Source: item.Source, Skill: item.Name})
			}
			if err := plan.availability.applyDeclared(item.Name); err != nil {
				emit(SyncEvent{Kind: SyncCopyFailed, Source: item.Source, Skill: item.Name, Err: err.Error()})
				report.Failures++
				continue
			}
			if stateStore == nil {
				continue
			}
			skill := item.Freshness
			digests, digestErr := DigestSkillContent(skill.ScopePath)
			if digestErr != nil {
				report.Failures++
				continue
			}
			skill.CacheDigests = digests
			state.Skills[item.Name] = skill.appliedState(item.CachePath, item.LocalSHA)
			if saveErr := stateStore.Save(state); saveErr != nil {
				report.Failures++
			}
		}
	}

	local := plan.LocalItems()
	if len(local) > 0 {
		if err := os.MkdirAll(plan.skillsDir, 0o755); err != nil {
			return report, fmt.Errorf("failed to create skills dir: %w", err)
		}
	}
	for _, item := range local {
		if err := applyLocalItem(plan.availability, item, emit); err != nil {
			report.Failures++
		}
	}

	if report.Failures > 0 {
		return report, fmt.Errorf("Sync did not converge: %d failure(s)", report.Failures)
	}
	return report, nil
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
// missing Source is an event and leaves the Skill alone; a failed installer is
// an event and Availability is still applied.
func applyLocalItem(availability *Availability, item SyncPlanItem, emit func(SyncEvent)) error {
	if item.Block == SyncBlockSourceMissing {
		emit(SyncEvent{Kind: SyncSourceMissing, Skill: item.Name, Path: item.SourcePath})
		return nil
	}
	if item.Kind == SyncItemCommand {
		if item.Check != "" {
			if _, _, err := RunCmd(item.Check, ""); err != nil {
				emit(SyncEvent{Kind: SyncCheckFailed, Skill: item.Name, Path: item.Check})
				return nil
			}
		}
		emit(SyncEvent{Kind: SyncCommandStart, Skill: item.Name})
		if err := MaterializeCommand(item.Command); err != nil {
			emit(SyncEvent{Kind: SyncCommandFailed, Skill: item.Name, Err: err.Error()})
		}
		return availability.applyDeclared(item.Name)
	}
	if err := MaterializeLocalSymlink(item.Name, item.LinkTarget, availability.skillsDir); err != nil {
		emit(SyncEvent{Kind: SyncSymlinkFailed, Skill: item.Name, Err: err.Error()})
		return nil
	}
	emit(SyncEvent{Kind: SyncSymlinked, Skill: item.Name, Target: item.SourcePath})
	return availability.applyDeclared(item.Name)
}

func itemNames(items []SyncPlanItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}
