package engine

import "slices"

// DoctorFindingKind is how Doctor classifies one diagnosed fact. Remaining
// counts kinds whose CountsAsIssue is true. CLI leftover grouping asks the
// leftover-dangling and leftover-live kinds rather than inspecting Dangling;
// other sentences stay on DoctorReport fields. Presentation chrome (healthy
// Agent rows, section headers, Availability copies) is not a kind.
type DoctorFindingKind string

const (
	DoctorFindingLeftoverDangling DoctorFindingKind = "leftover-dangling"
	DoctorFindingLeftoverLive     DoctorFindingKind = "leftover-live"

	// The warning kinds (DoctorWarningKinds). The CLI words each one in
	// doctor's summary line, so they are exported.
	DoctorFindingUntracked          DoctorFindingKind = "untracked"
	DoctorFindingUntrackedLink      DoctorFindingKind = "untracked-link"
	DoctorFindingUnmanagedDirectory DoctorFindingKind = "unmanaged-directory"
	DoctorFindingReservedName       DoctorFindingKind = "reserved-name"

	findingMasterMissing        DoctorFindingKind = "master-missing"
	findingAgentUnusable        DoctorFindingKind = "agent-unusable"
	findingAgentUnmanagedBroken DoctorFindingKind = "agent-unmanaged-broken"
	findingLeftoverEmpty        DoctorFindingKind = "leftover-empty"
	findingDriftMissing         DoctorFindingKind = "drift-missing"
	findingDriftUnexpected      DoctorFindingKind = "drift-unexpected"
	findingDriftBroken          DoctorFindingKind = "drift-broken"
	findingDriftForeign         DoctorFindingKind = "drift-foreign"
	findingDriftUnobservable    DoctorFindingKind = "drift-unobservable"
	findingMissingSkill         DoctorFindingKind = "missing-skill"
	findingIllegalLocal         DoctorFindingKind = "illegal-local"
	findingInvalid              DoctorFindingKind = "invalid"
	findingStub                 DoctorFindingKind = "stub"
	findingUnknownAgent         DoctorFindingKind = "unknown-agent"
	findingStateError           DoctorFindingKind = "state-error"
	findingGitError             DoctorFindingKind = "git-error"
	findingStaleState           DoctorFindingKind = "stale-state"
	findingLegacyCache          DoctorFindingKind = "legacy-cache"
	findingCacheRecovery        DoctorFindingKind = "cache-recovery"
	findingStaleScope           DoctorFindingKind = "stale-scope"
)

// CountsAsIssue is whether this kind contributes to Remaining. Untracked
// occupancy on the skills directory, and an unmanaged directory this tool did
// not create and Config does not declare on an Agent directory, are not
// Drift (ADR-0002): the tool leaves both alone and reports them as warnings.
// A Skill whose name an Agent reserves is not Drift either: no repair can make
// it available there, so it is a warning about the declaration. Availability
// Copies never appear as a kind.
func (k DoctorFindingKind) CountsAsIssue() bool {
	return k != "" && !slices.Contains(DoctorWarningKinds(), k)
}

// DoctorWarningKinds is every kind that does not count as an issue, in the
// order doctor's summary line names them. It is the one definition of a
// warning: DoctorOutcome.Warnings counts these, and the CLI must word each.
func DoctorWarningKinds() []DoctorFindingKind {
	return []DoctorFindingKind{
		DoctorFindingUntracked,
		DoctorFindingUntrackedLink,
		DoctorFindingUnmanagedDirectory,
		DoctorFindingReservedName,
	}
}

// DoctorWarning is how many findings of one warning kind a diagnosis holds.
type DoctorWarning struct {
	Kind  DoctorFindingKind
	Count int
}

// warnings counts findings() by warning kind, in DoctorWarningKinds order,
// leaving out kinds with none.
func (p DoctorReport) warnings() []DoctorWarning {
	counts := make(map[DoctorFindingKind]int)
	for _, kind := range p.findings() {
		counts[kind]++
	}
	var out []DoctorWarning
	for _, kind := range DoctorWarningKinds() {
		if n := counts[kind]; n > 0 {
			out = append(out, DoctorWarning{Kind: kind, Count: n})
		}
	}
	return out
}

// FindingKind is leftover dangling vs live occupancy. CLI chooses Error vs
// Warning from this kind rather than inspecting Dangling.
func (p LeftoverPath) FindingKind() DoctorFindingKind {
	if p.Dangling {
		return DoctorFindingLeftoverDangling
	}
	return DoctorFindingLeftoverLive
}

// findings is the single classification of a DoctorReport. issueCount counts
// those whose kind CountsAsIssue. A new diagnose field that skips this list
// disappears from Remaining.
func (p DoctorReport) findings() []DoctorFindingKind {
	var out []DoctorFindingKind
	add := func(kind DoctorFindingKind, n int) {
		for range n {
			out = append(out, kind)
		}
	}
	if p.MasterMissing {
		add(findingMasterMissing, 1)
	}
	for _, agent := range p.Agents {
		if agent.Unusable != "" {
			add(findingAgentUnusable, 1)
		}
		add(findingAgentUnmanagedBroken, len(agent.UnmanagedBroken))
		add(DoctorFindingUnmanagedDirectory, len(agent.Physical))
	}
	for _, path := range p.Leftover.Paths {
		add(path.FindingKind(), 1)
	}
	add(findingLeftoverEmpty, len(p.Leftover.Empty))
	for _, drift := range p.Drift {
		add(findingDriftMissing, len(drift.Missing))
		add(findingDriftUnexpected, len(drift.Unexpected))
		add(findingDriftBroken, len(drift.Broken))
		add(findingDriftForeign, len(drift.Foreign))
		add(findingDriftUnobservable, len(drift.Unobservable))
		// Copies are omitted: Availability applied by copying is a working
		// Scope by another mechanism, not Drift to reconcile (ADR-0002).
	}
	add(findingMissingSkill, len(p.Missing))
	add(DoctorFindingUntracked, len(p.Untracked))
	add(DoctorFindingUntrackedLink, len(p.UntrackedLinks))
	add(findingIllegalLocal, len(p.IllegalLocal))
	add(findingInvalid, len(p.Invalid))
	add(findingStub, len(p.Stubs))
	add(findingUnknownAgent, len(p.UnknownAgents))
	add(DoctorFindingReservedName, len(p.ReservedNames))
	if p.StateError != "" {
		add(findingStateError, 1)
	}
	if p.GitError != "" {
		add(findingGitError, 1)
	}
	add(findingStaleState, len(p.StaleState))
	add(findingLegacyCache, len(p.legacyCache))
	add(findingCacheRecovery, len(p.CacheRecovery))
	add(findingStaleScope, len(p.StaleScopes))
	return out
}
