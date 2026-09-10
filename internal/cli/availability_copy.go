package cli

import (
	"fmt"
	"strings"

	"github.com/akunzai/skills-manager/internal/engine"
)

// Availability applied by copying is one fact a user meets in two places —
// at the end of a Sync that just did it, and from Doctor counting the copies
// it finds later — so the whole sentence is assembled here rather than half in
// each command. Doctor adds a per-Agent breakdown; nothing else differs.
//
// Add is deliberately not a third. It applies Availability too, and the engine
// emits the same event for it, but Add's result is about what was declared;
// the Skills it just Materialized are the ones the next Sync reports on
// anyway, and Doctor answers the question at any time in between.
//
// The engine reports only that a path is a copy. It needs no reason field:
// the fallback fires on exactly one condition, the operating system refusing
// this process the privilege to create a symbolic link, so naming that reason
// is the frontend's job.
func copiedAvailabilityNotice(paths int, breakdown, scopeFlag string) string {
	subject := countOf(paths, "path")
	if breakdown != "" {
		subject += " (" + breakdown + ")"
	}
	// The reason is stated about the link rather than about "them", so the
	// sentence reads the same for one path as for thirty, and is as true when
	// Doctor reports copies someone else made as when Sync has just made them.
	return fmt.Sprintf(
		"Availability applied by copying: %s. A symbolic link needs a privilege this machine did not grant. Enable Developer Mode on Windows, then run 'skills sync%s' to switch to links.",
		subject, scopeFlag)
}

// describeCopiedAvailability renders the copies Doctor found as a total and a
// per-Agent breakdown. The breakdown is not decoration: a bare total against a
// list of Agent names reads as if each held all of them.
func describeCopiedAvailability(agents []engine.AgentHealth) (total int, breakdown string) {
	parts := make([]string, 0, len(agents))
	for _, agent := range agents {
		if n := len(agent.Copies); n > 0 {
			total += n
			parts = append(parts, fmt.Sprintf("%s %d", agent.Name, n))
		}
	}
	return total, strings.Join(parts, ", ")
}
