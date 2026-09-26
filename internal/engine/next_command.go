package engine

import "strings"

// NextCommand is a reason that ends in a skills command for the user to run.
// The engine words the reason and names the command; only the CLI knows the
// Scope flags the command needs to reach the same Scope, so it renders them.
type NextCommand struct {
	Reason string
	// Command is the subcommand, then its arguments: "rm sample".
	Command string
}

func (n NextCommand) Error() string { return n.Render("") }

// Render words n with scopeFlags right after the subcommand.
func (n NextCommand) Render(scopeFlags string) string {
	subcommand, args, _ := strings.Cut(n.Command, " ")
	command := "skills " + subcommand + scopeFlags
	if args != "" {
		command += " " + args
	}
	return n.Reason + "; run '" + command + "'"
}
