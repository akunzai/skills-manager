package engine

import (
	"errors"
	"os"

	"github.com/akunzai/skills-manager/internal/config"
)

type AgentHealth struct {
	Name            string
	Dir             string
	UnmanagedBroken []string
	Physical        []string
	// Unusable is why the directory could not be scanned, when it exists but
	// is not a directory. DiagnoseHealth reads it with ReadDir, which fails
	// with ENOTDIR and returns nothing — indistinguishable from an empty,
	// healthy directory unless it is recorded here.
	Unusable string
}

// SkillDrift is one declared Skill's Availability Drift as Doctor diagnosed
// it, with what --fix did about it.
type SkillDrift struct {
	AvailabilityDrift
	Repair ItemRepair
	// absentUnexpected holds the Unexpected paths of a declared Skill that is
	// not present on the skills directory. --fix removes exactly these rather
	// than Apply, which would link the absent master on the desired Agents.
	absentUnexpected []ManagedAgentPath
}

// RepairStatus is what --fix did to one diagnosed item.
type RepairStatus uint8

const (
	RepairNotAttempted RepairStatus = iota
	RepairSucceeded
	RepairFailed
	RepairSkipped
)

// ItemRepair is the outcome of attempting to repair one diagnosed item.
type ItemRepair struct {
	Status RepairStatus
	Err    error
}

// InvalidSkill is a declared Skill whose folder is on the skills directory but
// has no SKILL.md. The way out depends on how it was declared — a remote Skill
// can be re-Materialized, a symlinked one is only as valid as its Source — so
// the finding carries the declaration, not just the name.
type InvalidSkill struct {
	Name       string
	SourceType string
	Source     string
}

// IllegalLocalSource is a declared local symlink whose Source resolves inside
// the skills directory. Materialize must not replace that destination.
type IllegalLocalSource struct {
	Name   string
	Source string
}

type DoctorReport struct {
	SkillsDir      string
	MasterMissing  bool
	Agents         []AgentHealth
	Leftover       LeftoverOccupancy
	Drift          []SkillDrift
	Missing        []string
	Untracked      []string
	UntrackedLinks []string
	IllegalLocal   []IllegalLocalSource
	Invalid        []InvalidSkill
	// Stubs are declared Skills that arrived on the skills directory as text
	// files instead of directories — what a git client that cannot create
	// symbolic links leaves behind when a committed Project skills directory
	// is checked out.
	Stubs         []string
	UnknownAgents []UnknownAgentReference
	// ReservedNames are declared Skills whose name an Agent they are
	// available to keeps for its own content. Availability skips that pair.
	ReservedNames   []ReservedAvailability
	StateError      string
	StaleState      []string
	StateRepair     ItemRepair
	CacheRecovery   []string
	CacheMigrations []CacheMigrationOutcome
	// legacyCache holds the migration plans repair executes. Only their
	// roots are reportable (LegacyCacheRoots); the migration machinery is
	// an engine concern and stays unexported.
	legacyCache []legacyCacheMigrationPlan
	baselines   *Baselines
	StaleScopes []ScopeStateArtifact
	// GitError is why the git on PATH cannot maintain the sparse Cache
	// (missing, or older than CheckGitVersion accepts). Only diagnosed when
	// the Scope declares a remote Source.
	GitError string
}

// Doctor diagnoses and optionally repairs one Scope's Skill, Agent directory,
// and Availability health.
type Doctor struct {
	cfg            *config.Config
	skillsDir      string
	availability   *Availability
	cacheDir       string
	cacheMigration *legacyCacheMigrator
}

// DoctorOutcome is what Doctor diagnosed and repaired, plus the issues that
// remain after Run. Remaining is unavailable when Run returns an execution
// error.
//
// It carries facts, never sentences. Assembling those — wording, indentation,
// suggested commands, shell quoting — is the CLI's job (docs/agents/design.md:
// machine-readable output carries no presentation formatting). Exporting the
// seven diagnosis types this costs buys a Doctor that a second frontend, or a
// --json flag, can report without re-deriving anything; a Message string here
// would be cheaper today and unusable for either.
type DoctorOutcome struct {
	// Report is the pre-fix diagnosis. When AttemptedFix is set, repaired
	// items carry their ItemRepair (or CacheMigrationOutcome) on the value.
	Report         DoctorReport
	AttemptedFix   bool
	Remaining      int
	RecoveryNeeded bool
	// Failed is how many repair actions --fix attempted and could not
	// complete. It is kept apart from Remaining because ADR-0002 keeps the
	// two apart: a finding is a state to act on, a failed repair is work
	// that broke.
	Failed int
	// Untracked is how many Untracked real directories sit on the skills
	// directory. They are not counted in Remaining (see issueCount): they are
	// occupancy the tool does not manage, not Drift to reconcile.
	Untracked int
	// UntrackedLinks is how many leftover symlinks sit on the skills
	// directory, counted from the same diagnosis as Untracked.
	UntrackedLinks int
}

// DoctorEvent reports a legacy Cache rebuild: once when a Source starts, then
// with Finished set when the Cache it shares with its siblings is done, and
// Failed set when that rebuild did not succeed.
type DoctorEvent struct {
	Source           string
	Index, Total     int
	Finished, Failed bool
}

type DoctorProgress func(DoctorEvent)

type DoctorReplaceForeign func([]ForeignAvailabilityPath) (bool, error)

func NewDoctor(cfg *config.Config, skillsDir string) *Doctor {
	return NewDoctorWithCache(cfg, skillsDir, "")
}

func NewDoctorWithCache(cfg *config.Config, skillsDir, cacheDir string) *Doctor {
	// Doctor reads cfg and skillsDir back off the Availability it just built
	// rather than off its own arguments: NewAvailability is what defaults a nil
	// Config and an empty skills directory, and a Doctor diagnosing a different
	// pair from the one its Availability applies would report Drift that is not
	// there. Deliberate reuse, not a Config carried around inside Availability.
	availability := NewAvailability(cfg, skillsDir)
	doctor := &Doctor{
		cfg:          availability.cfg,
		skillsDir:    availability.skillsDir,
		availability: availability,
		cacheDir:     cacheDirOrDefault(cacheDir),
	}
	doctor.cacheMigration = newLegacyCacheMigrator(doctor.cfg, doctor.cacheDir)
	return doctor
}

// Run diagnoses the Scope. With fix, it repairs independent findings, keeps
// their action report, then diagnoses again to count the actual remaining
// state. progress and approve are optional: a nil progress reports nothing, a
// nil approve declines every foreign-path replacement.
//
// One entry point on purpose. Run/RunWithProgress/RunWithRepairApproval used
// to layer over this one, which made the interface read as three shapes when
// only this signature ever ran — the two wrappers had no caller outside tests.
func (d *Doctor) Run(fix bool, progress DoctorProgress, approve DoctorReplaceForeign) (DoctorOutcome, error) {
	plan, err := d.diagnose()
	if err != nil {
		return DoctorOutcome{}, err
	}
	if !fix {
		return DoctorOutcome{Report: plan, Remaining: plan.issueCount(), RecoveryNeeded: len(plan.CacheRecovery) > 0, Untracked: len(plan.Untracked), UntrackedLinks: len(plan.UntrackedLinks)}, nil
	}

	replaceForeign := false
	if foreign := plan.foreignAvailabilityPaths(); len(foreign) > 0 && approve != nil {
		replaceForeign, err = approve(foreign)
		if err != nil {
			return DoctorOutcome{Report: plan, Remaining: plan.issueCount(), Untracked: len(plan.Untracked), UntrackedLinks: len(plan.UntrackedLinks)}, err
		}
	}
	d.repair(&plan, progress, replaceForeign)
	outcome := DoctorOutcome{Report: plan, AttemptedFix: true, RecoveryNeeded: plan.cacheRecoveryNeeded(), Failed: plan.repairFailures()}
	after, err := d.diagnose()
	if err != nil {
		return outcome, err
	}
	outcome.Remaining = after.issueCount()
	outcome.Untracked = len(after.Untracked)
	outcome.UntrackedLinks = len(after.UntrackedLinks)
	outcome.RecoveryNeeded = outcome.RecoveryNeeded || len(after.CacheRecovery) > 0
	return outcome, nil
}

// repairFailures counts the repair actions that broke. Every category doctor
// attempts is listed here, so a new one that forgets to report itself shows up
// as a Scope that reports a clean-ish 1 while a repair silently failed.
func (p DoctorReport) repairFailures() int {
	failures := 0
	if p.StateRepair.Status == RepairFailed {
		failures++
	}
	for _, path := range p.Leftover.Paths {
		if path.Repair.Status == RepairFailed {
			failures++
		}
	}
	for _, empty := range p.Leftover.Empty {
		if empty.Repair.Status == RepairFailed {
			failures++
		}
	}
	for _, drift := range p.Drift {
		if drift.Repair.Status == RepairFailed {
			failures++
		}
	}
	for _, artifact := range p.StaleScopes {
		if artifact.Repair.Status == RepairFailed {
			failures++
		}
	}
	for _, migration := range p.CacheMigrations {
		if migration.Status == CacheMigrationFailed {
			failures++
		}
	}
	return failures
}

func (p DoctorReport) cacheRecoveryNeeded() bool {
	for _, migration := range p.CacheMigrations {
		if migration.Status == CacheMigrationRecoveryNeeded {
			return true
		}
	}
	return false
}

func (p DoctorReport) foreignAvailabilityPaths() []ForeignAvailabilityPath {
	var paths []ForeignAvailabilityPath
	for _, drift := range p.Drift {
		paths = append(paths, drift.Foreign...)
	}
	return paths
}

// diagnose records untracked Skills as warnings. Missing Skills and invalid
// folders are issues but are not repaired.
func (d *Doctor) diagnose() (DoctorReport, error) {
	plan := DoctorReport{SkillsDir: d.skillsDir}
	plan.baselines = OpenBaselines(d.skillsDir)
	if err := plan.baselines.Err(); err != nil {
		plan.StateError = err.Error()
	} else {
		plan.StaleState = plan.baselines.Stale(d.cfg)
	}
	artifacts, artifactErr := ListScopeStateArtifacts()
	if artifactErr != nil {
		return DoctorReport{}, artifactErr
	}
	for _, artifact := range artifacts {
		if artifact.Err == nil {
			if _, err := os.Stat(artifact.ScopePath); os.IsNotExist(err) {
				plan.StaleScopes = append(plan.StaleScopes, artifact)
			}
		}
	}
	if len(d.cfg.Remote) > 0 {
		if err := checkGitVersion(); err != nil {
			plan.GitError = err.Error()
		}
	}
	legacyCache, cacheRecovery, err := d.cacheMigration.detect()
	if err != nil {
		return DoctorReport{}, err
	}
	plan.legacyCache = legacyCache
	plan.CacheRecovery = cacheRecovery
	if _, err := os.Stat(d.skillsDir); os.IsNotExist(err) {
		plan.MasterMissing = true
	}

	occupancy := d.availability.ObserveOccupancy()
	plan.Agents = occupancy.Agents
	plan.Leftover = occupancy.Leftover
	unexpected := make(map[string][]ManagedAgentPath)
	for _, path := range occupancy.Unexpected {
		unexpected[path.Skill] = append(unexpected[path.Skill], path)
	}
	plan.UnknownAgents = d.availability.UnknownAgentReferences()
	plan.ReservedNames = d.availability.ReservedAvailability()

	inv, err := LoadInventory(d.cfg, d.skillsDir)
	if err != nil {
		return DoctorReport{}, err
	}
	plan.Missing = inv.Missing()
	plan.Untracked = inv.Untracked()
	plan.UntrackedLinks = inv.UntrackedLinks()
	plan.IllegalLocal = inv.IllegalLocal()
	plan.Invalid = inv.Invalid()
	plan.Stubs = inv.Stubs()
	for _, s := range inv.declaredPresent() {
		drift := occupancy.Drift(s.Name)
		delete(unexpected, s.Name)
		if drift.Empty() && len(drift.Copies) == 0 {
			continue
		}
		plan.Drift = append(plan.Drift, SkillDrift{AvailabilityDrift: drift})
	}
	// A declared Skill that is not present has no desired paths to observe,
	// only Unexpected ones, which --fix removes rather than Apply.
	for _, item := range inv.SkillItems() {
		paths, ok := unexpected[item.Name]
		if !ok {
			continue
		}
		plan.Drift = append(plan.Drift, SkillDrift{
			AvailabilityDrift: AvailabilityDrift{Skill: item.Name, Unexpected: agentsOf(paths)},
			absentUnexpected:  paths,
		})
	}
	return plan, nil
}

// issueCount is Remaining: how many classified findings still stand in the
// way of a matching Scope. It counts findings(), not a second field walk.
func (p DoctorReport) issueCount() int {
	n := 0
	for _, kind := range p.findings() {
		if kind.CountsAsIssue() {
			n++
		}
	}
	return n
}

// repair leaves physical dirs, unmanaged broken links, and missing/untracked/
// invalid Skills unchanged. Independent repair failures do not stop the run.
func (d *Doctor) repair(plan *DoctorReport, progress DoctorProgress, replaceForeign bool) {
	if plan.StateError != "" {
		plan.StateRepair = itemRepairFromErr(plan.baselines.Reset())
	} else if len(plan.StaleState) > 0 {
		plan.StateRepair = itemRepairFromErr(plan.baselines.ForgetStale(d.cfg))
	}
	total := 0
	for _, migration := range plan.legacyCache {
		total += len(migration.Sources)
	}
	index := 0
	var staged []string
	migrations := d.cacheMigration.apply(plan.legacyCache, func(event legacyCacheMigrationEvent) {
		if progress == nil {
			return
		}
		switch event.Phase {
		case legacyCacheMigrationStaging:
			index++
			staged = append(staged, event.Source)
			progress(DoctorEvent{Source: event.Source, Index: index, Total: total})
		case legacyCacheMigrationFinished:
			for _, source := range staged {
				progress(DoctorEvent{Source: source, Index: index, Total: total, Finished: true, Failed: event.Status != legacyCacheRebuilt})
			}
			staged = nil
		}
	})
	for _, migration := range migrations {
		plan.CacheMigrations = append(plan.CacheMigrations, migration.outcome())
	}
	for i, artifact := range plan.StaleScopes {
		if err := os.Remove(artifact.Path); err != nil && !os.IsNotExist(err) {
			plan.StaleScopes[i].Repair = itemRepairFromErr(err)
		} else {
			plan.StaleScopes[i].Repair = ItemRepair{Status: RepairSucceeded}
		}
	}
	plan.Leftover = d.availability.RemoveLeftover(plan.Leftover)
	for i, drift := range plan.Drift {
		var err error
		if len(drift.absentUnexpected) > 0 {
			var errs []error
			for _, repair := range removeManagedPaths(d.skillsDir, drift.absentUnexpected) {
				errs = append(errs, repair.Err)
			}
			err = errors.Join(errs...)
		} else if len(drift.Foreign) > 0 && replaceForeign {
			err = d.availability.ReplaceForeign(drift.Skill, drift.Foreign)
		} else {
			_, err = d.availability.Apply(drift.Skill)
		}
		plan.Drift[i].Repair = itemRepairFromErr(err)
	}
}

// agentsOf is the Agent of each path, in order.
func agentsOf(paths []ManagedAgentPath) []string {
	var agents []string
	for _, path := range paths {
		agents = append(agents, path.Agent)
	}
	return agents
}

func itemRepairFromErr(err error) ItemRepair {
	if err != nil {
		return ItemRepair{Status: RepairFailed, Err: err}
	}
	return ItemRepair{Status: RepairSucceeded}
}

// LegacyCacheRoots names the legacy branchless Cache roots doctor found, for
// callers that report them. The migration plans themselves stay internal.
func (p DoctorReport) LegacyCacheRoots() []string {
	roots := make([]string, 0, len(p.legacyCache))
	for _, migration := range p.legacyCache {
		roots = append(roots, migration.Root)
	}
	return roots
}
