package engine

import "github.com/akunzai/skills-manager/internal/config"

// UpdateOptions controls Source refresh. Force does not override Sync's
// protection of local edits; DryRun observes remote refs without refreshing.
type UpdateOptions struct {
	Targets []string
	Force   bool
	DryRun  bool
}

// UpdateSyncPath is the route fixed after refreshing and observing the Scope.
type UpdateSyncPath int

const (
	// UpdateConverged leaves a Scope that already matches Config alone.
	UpdateConverged UpdateSyncPath = iota
	// UpdatePreview presents pending Sync work without applying it.
	UpdatePreview
	// UpdateApply applies the frozen plan after the caller supplies a decision.
	UpdateApply
)

// UpdateState is the final Update disposition, before CLI exit-code mapping.
type UpdateState int

const (
	UpdateComplete UpdateState = iota
	UpdatePendingRefresh
	UpdatePendingSync
	UpdateRefreshFailed
	UpdateSyncFailed
)

// PreparedUpdate holds the post-refresh Sync plan across the caller's
// confirmation. Path is fixed before that decision; Finish does not replan.
type PreparedUpdate struct {
	Refresh *UpdateResult
	Sync    *SyncPlan
	Path    UpdateSyncPath
	summary SyncSummary
	dryRun  bool
}

// UpdateOutcome retains both phases' results even when a refresh failure
// determines State. Report is present only when Sync was applied.
type UpdateOutcome struct {
	Refresh *UpdateResult
	Summary SyncSummary
	Report  *SyncReport
	State   UpdateState
}

// PrepareUpdate refreshes the selected Sources, then plans the whole Scope
// from the resulting Cache. Source failures are data and do not prevent Sync
// planning; errors that prevent preparation return immediately.
func PrepareUpdate(cfg *config.Config, configPath, skillsDir, cacheDir string, options UpdateOptions, progress UpdateProgress) (*PreparedUpdate, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	refresh := &UpdateResult{UpdatedRepos: []UpdatedRepoInfo{}, SkippedRepos: []SkippedRepoInfo{}, Renamed: []RenamedSkillInfo{}, Errors: []UpdateErrorInfo{}}
	// Validate named targets even when no remote Sources are declared.
	if len(cfg.Remote) > 0 || len(options.Targets) > 0 {
		var err error
		refresh, err = UpdateRemoteSkills(cfg, options.Targets, options.Force, options.DryRun, cacheDir, progress)
		if err != nil {
			return nil, err
		}
	}
	plan, err := PlanSync(cfg, configPath, skillsDir, cacheDir)
	if err != nil {
		return nil, err
	}
	summary := plan.Summary(SyncDecision{})
	path := UpdateApply
	switch {
	case summary.Converged():
		path = UpdateConverged
	case options.DryRun:
		path = UpdatePreview
	}
	return &PreparedUpdate{Refresh: refresh, Sync: plan, Path: path, summary: summary, dryRun: options.DryRun}, nil
}

// Finish follows the prepared path and combines the two phases. A fatal Apply
// error takes precedence over accumulated Source failures; after successful
// execution, Source failures outrank Sync's failed or pending work (ADR-0002).
func (p *PreparedUpdate) Finish(decision SyncDecision, progress func(SyncEvent)) (*UpdateOutcome, error) {
	outcome := &UpdateOutcome{Refresh: p.Refresh, Summary: p.summary}
	if p.Path == UpdateApply {
		report, err := p.Sync.Apply(decision, progress)
		if err != nil {
			return nil, err
		}
		outcome.Report = report
		outcome.Summary = report.Summary()
	}
	switch {
	case len(p.Refresh.Errors) > 0:
		outcome.State = UpdateRefreshFailed
	case outcome.Summary.Convergence() == Incomplete:
		outcome.State = UpdateSyncFailed
	case !outcome.Summary.Converged():
		outcome.State = UpdatePendingSync
	case p.dryRun && len(p.Refresh.UpdatedRepos) > 0:
		outcome.State = UpdatePendingRefresh
	default:
		outcome.State = UpdateComplete
	}
	return outcome, nil
}
