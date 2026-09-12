package engine

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
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

type SkillDrift struct {
	Skill        string
	Source       string
	Missing      []string
	Unexpected   []string
	Broken       []string
	Copies       []string
	Foreign      []ForeignAvailabilityPath
	Unobservable []UnobservableAvailabilityPath
}

type HealthFix struct {
	Agent string
	Name  string
	Err   error
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
	LeftoverEmpty  []AgentDir
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
	StateError    string
	StaleState    []string
	CacheRecovery []string
	// legacyCache holds the migration plans repair executes. Only their
	// roots are reportable (LegacyCacheRoots); the migration machinery is
	// an engine concern and stays unexported.
	legacyCache []legacyCacheMigrationPlan
	StaleScopes []ScopeStateArtifact
}

type RepairOutcome struct {
	RemovedLeftoverPaths []LeftoverPath
	FailedLeftoverPaths  []LeftoverFailure
	RemovedLeftover      []AgentDir
	FailedLeftover       []HealthFix
	FixedDrift           []string
	FailedDrift          []HealthFix
	StateRepaired        bool
	StateRepairErr       error
	CacheMigrations      []CacheMigrationOutcome
	RemovedScopes        []string
	FailedScopes         []HealthFix
}

// Doctor diagnoses and optionally repairs one Scope's Skill, Agent directory,
// and Availability health.
type Doctor struct {
	cfg            *config.Config
	skillsDir      string
	availability   *Availability
	cacheDir       string
	stateStore     *ScopeStateStore
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
	// Report is the diagnosis. Repair is nil unless Run was asked to fix.
	Report         DoctorReport
	Repair         *RepairOutcome
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
}

type DoctorEvent struct {
	Source       string
	Index, Total int
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
	stateStore, _ := NewScopeStateStore(availability.skillsDir)
	doctor := &Doctor{
		cfg:          availability.cfg,
		skillsDir:    availability.skillsDir,
		availability: availability,
		cacheDir:     cacheDirOrDefault(cacheDir),
		stateStore:   stateStore,
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
		return DoctorOutcome{Report: plan, Remaining: plan.issueCount(), RecoveryNeeded: len(plan.CacheRecovery) > 0, Untracked: len(plan.Untracked)}, nil
	}

	replaceForeign := false
	if foreign := plan.foreignAvailabilityPaths(); len(foreign) > 0 && approve != nil {
		replaceForeign, err = approve(foreign)
		if err != nil {
			return DoctorOutcome{Report: plan, Remaining: plan.issueCount(), Untracked: len(plan.Untracked)}, err
		}
	}
	result := d.repair(plan, progress, replaceForeign)
	outcome := DoctorOutcome{Report: plan, Repair: &result, RecoveryNeeded: result.cacheRecoveryNeeded(), Failed: result.repairFailures()}
	after, err := d.diagnose()
	if err != nil {
		return outcome, err
	}
	outcome.Remaining = after.issueCount()
	outcome.Untracked = len(after.Untracked)
	outcome.RecoveryNeeded = outcome.RecoveryNeeded || len(after.CacheRecovery) > 0
	return outcome, nil
}

// repairFailures counts the repair actions that broke. Every category doctor
// attempts is listed here, so a new one that forgets to report itself shows up
// as a Scope that reports a clean-ish 1 while a repair silently failed.
func (r RepairOutcome) repairFailures() int {
	failures := len(r.FailedLeftoverPaths) + len(r.FailedLeftover) + len(r.FailedDrift) + len(r.FailedScopes)
	if r.StateRepairErr != nil {
		failures++
	}
	for _, migration := range r.CacheMigrations {
		if migration.Status == CacheMigrationFailed {
			failures++
		}
	}
	return failures
}

func (r RepairOutcome) cacheRecoveryNeeded() bool {
	for _, migration := range r.CacheMigrations {
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

func availabilitySource(item models.SkillItem) string {
	if strings.HasPrefix(item.SourceType, "local_") {
		return "local"
	}
	return item.Source
}

// diagnose records untracked Skills as warnings. Missing Skills and invalid
// folders are issues but are not repaired.
func (d *Doctor) diagnose() (DoctorReport, error) {
	plan := DoctorReport{SkillsDir: d.skillsDir}
	if d.stateStore != nil {
		state, err := d.stateStore.Load()
		if err != nil {
			plan.StateError = err.Error()
		} else {
			for name := range state.Skills {
				if _, _, declared := config.FindSkillSource(d.cfg, name); !declared {
					plan.StaleState = append(plan.StaleState, name)
				}
			}
			slices.Sort(plan.StaleState)
		}
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
	legacyCache, cacheRecovery, err := d.cacheMigration.detect()
	if err != nil {
		return DoctorReport{}, err
	}
	plan.legacyCache = legacyCache
	plan.CacheRecovery = cacheRecovery
	if _, err := os.Stat(d.skillsDir); os.IsNotExist(err) {
		plan.MasterMissing = true
	}

	configuredAgents := d.availability.ConfiguredAgentDirs()

	for _, agentName := range slices.Sorted(maps.Keys(configuredAgents)) {
		agentDir := configuredAgents[agentName]
		info, err := os.Stat(agentDir)
		if os.IsNotExist(err) {
			continue
		}
		if err == nil && !info.IsDir() {
			plan.Agents = append(plan.Agents, AgentHealth{Name: agentName, Dir: agentDir, Unusable: "not a directory"})
			continue
		}
		health := diagnoseAgentDirHealth(agentDir, d.skillsDir)
		plan.Agents = append(plan.Agents, AgentHealth{
			Name:            agentName,
			Dir:             agentDir,
			UnmanagedBroken: health.UnmanagedBroken,
			Physical:        health.Physical,
		})
	}

	leftover := d.availability.ObserveLeftover()
	plan.Leftover = leftover
	plan.LeftoverEmpty = leftover.Empty
	plan.UnknownAgents = d.availability.UnknownAgentReferences()

	inv, err := Inventory(d.cfg, d.skillsDir)
	if err != nil {
		return DoctorReport{}, err
	}
	for _, s := range inv {
		if !s.IsInstalled {
			plan.Missing = append(plan.Missing, s.Name)
			continue
		}
		if isUntracked(s) {
			if s.SourceType == "symlink" {
				plan.UntrackedLinks = append(plan.UntrackedLinks, s.Name)
			} else {
				plan.Untracked = append(plan.Untracked, s.Name)
			}
			continue
		}
		if s.SourceType == "local_symlink" && models.LocalSourceInsideSkillsDir(models.ResolveLocalSourcePath(s.Source, d.skillsDir), d.skillsDir) && IsRealMasterDir(d.skillsDir, s.Name) {
			plan.IllegalLocal = append(plan.IllegalLocal, IllegalLocalSource{Name: s.Name, Source: s.Source})
			continue
		}
		if !s.IsValidSkill {
			// A stub takes precedence over the invalid folder it also looks
			// like: its way out is a git setting, not a repair of the Source.
			if isSymlinkStub(s, d.skillsDir) {
				plan.Stubs = append(plan.Stubs, s.Name)
			} else {
				plan.Invalid = append(plan.Invalid, InvalidSkill{Name: s.Name, SourceType: s.SourceType, Source: s.Source})
			}
		}
		source := availabilitySource(s)
		drift := d.availability.ObserveAvailability(s.Name)
		if drift.Empty() && len(drift.Copies) == 0 {
			continue
		}
		plan.Drift = append(plan.Drift, SkillDrift{
			Skill:        s.Name,
			Source:       source,
			Missing:      drift.Missing,
			Unexpected:   drift.Unexpected,
			Broken:       drift.Broken,
			Copies:       drift.Copies,
			Foreign:      drift.Foreign,
			Unobservable: drift.Unobservable,
		})
	}
	return plan, nil
}

// isSymlinkStub reports whether a declared Skill arrived on the skills
// directory as a plain file instead of a directory. A git client that cannot
// create symbolic links — the default on Windows — checks a committed symlink
// out as a text file holding its target path, and an Agent then reads that
// path as the Skill's content. Two signals decide it together: the entry is a
// regular file, and Config declares the Skill with a local symlink Source. No
// content sniffing — a file where only directories belong is already
// anomalous, and the declaration is what lets doctor word a precise next
// action.
func isSymlinkStub(item models.SkillItem, skillsDir string) bool {
	if item.SourceType != "local_symlink" {
		return false
	}
	info, err := os.Lstat(filepath.Join(skillsDir, item.Name))
	return err == nil && info.Mode().IsRegular()
}

// IssueCount is the number of issues doctor reports without --fix.
// Untracked Skills are warnings only.
func (p DoctorReport) issueCount() int {
	n := 0
	if p.MasterMissing {
		n++
	}
	for _, a := range p.Agents {
		n += len(a.UnmanagedBroken) + len(a.Physical)
		if a.Unusable != "" {
			n++
		}
	}
	n += len(p.Leftover.Paths) + len(p.Leftover.Empty)
	for _, d := range p.Drift {
		n += len(d.Missing) + len(d.Unexpected) + len(d.Broken) + len(d.Foreign) + len(d.Unobservable)
	}
	// Copies are not counted: Availability applied by copying is a working
	// Scope by another mechanism, not Drift to reconcile (ADR-0002).
	n += len(p.Missing) + len(p.Invalid) + len(p.Stubs) + len(p.IllegalLocal)
	n += len(p.UnknownAgents)
	if p.StateError != "" {
		n++
	}
	n += len(p.StaleState) + len(p.legacyCache) + len(p.CacheRecovery) + len(p.StaleScopes)
	return n
}

// repair leaves physical dirs, unmanaged broken links, and missing/untracked/
// invalid Skills unchanged. Independent repair failures do not stop the run.
func (d *Doctor) repair(plan DoctorReport, progress DoctorProgress, replaceForeign bool) RepairOutcome {
	result := RepairOutcome{}
	if d.stateStore != nil {
		if plan.StateError != "" {
			result.StateRepairErr = d.stateStore.Prune()
			result.StateRepaired = result.StateRepairErr == nil
		} else if len(plan.StaleState) > 0 {
			keep := make(map[string]struct{})
			for source, repo := range d.cfg.Remote {
				_ = source
				for name := range repo.Skills {
					keep[name] = struct{}{}
				}
			}
			result.StateRepairErr = d.stateStore.PruneSkills(keep)
			result.StateRepaired = result.StateRepairErr == nil
		}
	}
	total := 0
	for _, migration := range plan.legacyCache {
		total += len(migration.Sources)
	}
	index := 0
	migrations := d.cacheMigration.apply(plan.legacyCache, func(event legacyCacheMigrationEvent) {
		if event.Phase != legacyCacheMigrationStaging {
			return
		}
		index++
		if progress != nil {
			progress(DoctorEvent{Source: event.Source, Index: index, Total: total})
		}
	})
	for _, migration := range migrations {
		result.CacheMigrations = append(result.CacheMigrations, migration.outcome())
	}
	for _, artifact := range plan.StaleScopes {
		if err := os.Remove(artifact.Path); err != nil && !os.IsNotExist(err) {
			result.FailedScopes = append(result.FailedScopes, HealthFix{Name: artifact.Path, Err: err})
		} else {
			result.RemovedScopes = append(result.RemovedScopes, artifact.ScopePath)
		}
	}
	leftover := d.availability.ApplyLeftover(plan.Leftover)
	result.RemovedLeftoverPaths = leftover.RemovedPaths
	result.FailedLeftoverPaths = leftover.FailedPaths
	result.RemovedLeftover = leftover.RemovedEmpty
	for _, failure := range leftover.FailedEmpty {
		result.FailedLeftover = append(result.FailedLeftover, HealthFix{
			Agent: failure.Dir.Name,
			Name:  failure.Dir.Dir,
			Err:   failure.Err,
		})
	}
	for _, drift := range plan.Drift {
		var err error
		if len(drift.Foreign) > 0 && replaceForeign {
			err = d.availability.ReplaceForeign(drift.Skill, drift.Foreign)
		} else {
			_, err = d.availability.Apply(drift.Skill)
		}
		if err != nil {
			result.FailedDrift = append(result.FailedDrift, HealthFix{Name: drift.Skill, Err: err})
			continue
		}
		result.FixedDrift = append(result.FixedDrift, drift.Skill)
	}
	return result
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
