package engine

// Convergence is what a command's result says about its Scope (ADR-0002):
// it matches its Config, it does not and the user has a next action, or the
// work could not be completed. Each result decides it once, from its Skill
// outcomes and its Scope state verdict; the CLI only words it and maps it to
// an exit code.
type Convergence int

const (
	// Converged is a Scope that matches its Config.
	Converged Convergence = iota
	// Unreconciled is a Scope that does not match its Config, with what
	// stands in the way reported for the user to act on.
	Unreconciled
	// Incomplete is work that could not be completed. It wins over
	// Unreconciled, so a real failure is never masked.
	Incomplete
)

func (c Convergence) String() string {
	return [...]string{"converged", "unreconciled", "incomplete"}[c]
}

// stateConvergence is the least a Scope state verdict allows: a failure when
// a Baseline could not be recorded, compared, or forgotten, otherwise
// nothing, because a warning keeps the code the command's other work earns.
func stateConvergence(verdict StateVerdict) Convergence {
	if verdict == StateFail {
		return Incomplete
	}
	return Converged
}
