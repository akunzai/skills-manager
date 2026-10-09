package engine

import "testing"

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
