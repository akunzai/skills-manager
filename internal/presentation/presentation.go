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
	style := Style{Plain: !isInteractive(w), Rule: "─", Branch: "├─", LastBranch: "└─"}
	if style.Plain {
		style.Rule = "-"
		style.Branch = "+-"
		style.LastBranch = "`-"
	}
	if !supportsColor(w) {
		return style
	}
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

func supportsColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func isInteractive(w io.Writer) bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
