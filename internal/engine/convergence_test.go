package engine

import (
	"errors"
	"testing"
)

var (
	stateWarned = StateOutcome{Verdict: StateWarn, Message: "unreadable"}
	stateFailed = StateOutcome{Verdict: StateFail, Message: "unreadable"}
)

// An unrecorded Baseline is a failure even when every Skill was applied, a
// warning keeps the code the Skills earn, and a failure wins over a block.
func TestAddResultConvergence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result AddResult
		want   Convergence
	}{
		{name: "every Skill applied", result: AddResult{}, want: Converged},
		{name: "unreadable state needed no Baseline", result: AddResult{State: stateWarned}, want: Converged},
		{name: "a blocked Skill", result: AddResult{SyncTally: SyncTally{Blocked: 1}}, want: Unreconciled},
		{name: "a blocked Skill with a warning", result: AddResult{SyncTally: SyncTally{Blocked: 1}, State: stateWarned}, want: Unreconciled},
		{name: "Baselines not recorded", result: AddResult{State: stateFailed}, want: Incomplete},
		{name: "a blocked Skill and Baselines not recorded", result: AddResult{SyncTally: SyncTally{Blocked: 1}, State: stateFailed}, want: Incomplete},
		{name: "a failed Skill beside a blocked one", result: AddResult{SyncTally: SyncTally{Blocked: 1, Failed: 1}}, want: Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Convergence(); got != tc.want {
				t.Fatalf("Convergence() = %s; want %s", got, tc.want)
			}
		})
	}
}

// Adopt's furthest Skill decides, and an unrecorded Baseline makes an
// otherwise adopted Scope incomplete.
func TestAdoptResultConvergence(t *testing.T) {
	skills := func(states ...AdoptState) []AdoptOutcome {
		outcomes := make([]AdoptOutcome, len(states))
		for i, state := range states {
			outcomes[i] = AdoptOutcome{State: state}
		}
		return outcomes
	}
	for _, tc := range []struct {
		name   string
		result AdoptResult
		want   Convergence
	}{
		{name: "every Skill adopted", result: AdoptResult{Skills: skills(AdoptAdopted, AdoptAdopted)}, want: Converged},
		{name: "adopted with a warning", result: AdoptResult{Skills: skills(AdoptAdopted), State: stateWarned}, want: Converged},
		{name: "declared without a Baseline", result: AdoptResult{Skills: skills(AdoptAdopted, AdoptDeclaredWithoutBaseline)}, want: Unreconciled},
		{name: "declared with copies left", result: AdoptResult{Skills: skills(AdoptDeclaredWithCopiesLeft)}, want: Unreconciled},
		{name: "skipped", result: AdoptResult{Skills: skills(AdoptSkipped)}, want: Unreconciled},
		{name: "a failed Skill beside a skipped one", result: AdoptResult{Skills: skills(AdoptSkipped, AdoptFailed)}, want: Incomplete},
		{name: "Baselines not recorded", result: AdoptResult{Skills: skills(AdoptAdopted), State: stateFailed}, want: Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Convergence(); got != tc.want {
				t.Fatalf("Convergence() = %s; want %s", got, tc.want)
			}
		})
	}
}

// A removal is never unreconciled: a Skill not fully removed or a Baseline
// not forgotten is incomplete, and a warning costs nothing.
func TestRemoveResultConvergence(t *testing.T) {
	removed := RemoveSkillResult{RetiredSkill: RetiredSkill{Name: "removed", CopyRemoved: true}}
	broken := RemoveSkillResult{RetiredSkill: RetiredSkill{Name: "broken", CopyErr: errors.New("permission denied")}}
	for _, tc := range []struct {
		name   string
		result RemoveResult
		want   Convergence
	}{
		{name: "every Skill removed", result: RemoveResult{Skills: []RemoveSkillResult{removed}}, want: Converged},
		{name: "removed with a warning", result: RemoveResult{Skills: []RemoveSkillResult{removed}, State: stateWarned}, want: Converged},
		{name: "a Skill not fully removed", result: RemoveResult{Skills: []RemoveSkillResult{removed, broken}}, want: Incomplete},
		{name: "Baselines not forgotten", result: RemoveResult{Skills: []RemoveSkillResult{removed}, State: stateFailed}, want: Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Convergence(); got != tc.want {
				t.Fatalf("Convergence() = %s; want %s", got, tc.want)
			}
		})
	}
}

// Doctor's findings are a state to act on and a failed repair is work that
// broke, so a failed repair is incomplete even when the diagnosis after it
// finds nothing left, and a warning is never an issue.
func TestDoctorOutcomeConvergence(t *testing.T) {
	warned := []DoctorWarning{{Kind: DoctorFindingUntracked, Count: 1}}
	for _, tc := range []struct {
		name    string
		outcome DoctorOutcome
		want    Convergence
	}{
		{name: "no issues", outcome: DoctorOutcome{}, want: Converged},
		{name: "only warnings", outcome: DoctorOutcome{Warnings: warned}, want: Converged},
		{name: "issues remain", outcome: DoctorOutcome{Remaining: 2}, want: Unreconciled},
		{name: "a failed repair beside remaining issues", outcome: DoctorOutcome{AttemptedFix: true, Remaining: 1, Failed: 1}, want: Incomplete},
		{name: "a failed repair the diagnosis after it no longer finds", outcome: DoctorOutcome{AttemptedFix: true, Failed: 1}, want: Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.outcome.Convergence(); got != tc.want {
				t.Fatalf("Convergence() = %s; want %s", got, tc.want)
			}
		})
	}
}
