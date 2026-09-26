package presentation

import (
	"io"
	"os"

	"golang.org/x/term"
)

type Style struct {
	Cyan       string
	Green      string
	Yellow     string
	Red        string
	Bold       string
	Dim        string
	Reset      string
	Plain      bool
	Rule       string
	Branch     string
	LastBranch string
}

func For(w io.Writer) Style {
	tty := isTerminal(w)
	style := Style{Plain: !interactive(tty), Rule: "─", Branch: "├─", LastBranch: "└─"}
	if style.Plain {
		style.Rule = "-"
		style.Branch = "+-"
		style.LastBranch = "`-"
	}
	if !colorful(tty) {
		return style
	}
	return withColor(style)
}

func withColor(style Style) Style {
	style.Cyan = "\033[96m"
	style.Green = "\033[92m"
	style.Yellow = "\033[93m"
	style.Red = "\033[91m"
	style.Bold = "\033[1m"
	style.Dim = "\033[2m"
	style.Reset = "\033[0m"
	return style
}

func (s Style) SourceIcon(sourceType string) string {
	kind := "remote"
	switch {
	case sourceType == "symlink" || sourceType == "local_symlink":
		kind = "link"
	case sourceType == "local_command" || sourceType == "command":
		kind = "command"
	case sourceType == "untracked":
		kind = "untracked"
	}

	if !s.Plain {
		return map[string]string{
			"link":      "→",
			"command":   "›",
			"untracked": "○",
			"remote":    "●",
		}[kind]
	}
	return map[string]string{
		"link":      "[link]",
		"command":   "[command]",
		"untracked": "[untracked]",
		"remote":    "[remote]",
	}[kind]
}

// LineWidth is how many columns one line on w may take without wrapping, or
// 0 when w is no interactive terminal and a line has no width to fit.
func LineWidth(w io.Writer) int {
	if !interactive(isTerminal(w)) {
		return 0
	}
	// The last column is left free: a line that fills it wraps on some
	// terminals.
	return terminalWidth(w) - 1
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// interactive is whether a terminal writer takes terminal control: TERM=dumb
// asks for plain output even on a terminal.
func interactive(tty bool) bool {
	return tty && os.Getenv("TERM") != "dumb"
}

func colorful(tty bool) bool {
	return interactive(tty) && os.Getenv("NO_COLOR") == ""
}

// animated is whether progress may redraw itself in place. A CI log that
// allocates a terminal still keeps every frame, so CI gets plain lines.
func animated(tty bool) bool {
	return interactive(tty) && os.Getenv("CI") == ""
}
