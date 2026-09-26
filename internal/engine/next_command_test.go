package engine

import "testing"

// The Scope flags go right after the subcommand, so a command with
// arguments stays one that parses.
func TestNextCommandRendersScopeFlagsAfterTheSubcommand(t *testing.T) {
	for _, tc := range []struct{ command, flags, want string }{
		{"update", "", "Cache missing; run 'skills update'"},
		{"update", " -p", "Cache missing; run 'skills update -p'"},
		{"rm a b", " -p --config 'x.json'", "Cache missing; run 'skills rm -p --config 'x.json' a b'"},
	} {
		if got := (NextCommand{Reason: "Cache missing", Command: tc.command}).Render(tc.flags); got != tc.want {
			t.Errorf("Render(%q, %q) = %q; want %q", tc.command, tc.flags, got, tc.want)
		}
	}
}
