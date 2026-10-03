package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/engine"
)

// nextCommands walks joined and wrapped causes without losing later remedies.
func nextCommands(errs ...error) []engine.NextCommand {
	var commands []engine.NextCommand
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if next, ok := err.(engine.NextCommand); ok && next.Command != "" {
			commands = append(commands, next)
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, cause := range wrapped.Unwrap() {
				visit(cause)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	for _, err := range errs {
		visit(err)
	}
	return commands
}

// errorReasons leaves commands for the footer while preserving wrapper context
// and every individual reason, including different reasons for the same command.
func errorReasons(err error) string {
	if err == nil {
		return ""
	}
	reason := err.Error()
	for _, next := range nextCommands(err) {
		reason = strings.ReplaceAll(reason, next.Error(), next.Reason)
	}
	return reason
}

// printNextCommands renders each distinct command once in the active Scope.
func printNextCommands(out io.Writer, scopeFlags string, errs ...error) bool {
	seen := make(map[string]bool)
	for _, next := range nextCommands(errs...) {
		if seen[next.Command] {
			continue
		}
		seen[next.Command] = true
		fmt.Fprintf(out, "Next: %s.\n", strings.TrimPrefix(next.Render(scopeFlags), next.Reason+"; "))
	}
	return len(seen) > 0
}

func syncErrors(events []engine.SyncEvent) []error {
	errs := make([]error, 0, len(events))
	for _, event := range events {
		if event.Err != nil {
			errs = append(errs, event.Err)
		}
	}
	return errs
}
