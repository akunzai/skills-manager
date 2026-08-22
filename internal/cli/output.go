package cli

import (
	"io"

	"github.com/akunzai/skills-manager/internal/presentation"
)

var (
	colorCyan      string
	colorGreen     string
	colorYellow    string
	colorRed       string
	colorBold      string
	colorDim       string
	colorReset     string
	tableRule      string
	treeBranch     string
	treeLastBranch string

	// errStyle mirrors the vars above but is keyed to the command's error
	// writer rather than its output writer. Stdout and stderr can have
	// different TTY status (e.g. output redirected to a file with an
	// interactive stderr), so a message written to cmd.ErrOrStderr() must not
	// borrow color decisions made for cmd.OutOrStdout().
	errStyle presentation.Style
)

func applyOutputStyle(out io.Writer) {
	style := presentation.For(out)
	colorCyan = style.Cyan
	colorGreen = style.Green
	colorYellow = style.Yellow
	colorRed = style.Red
	colorBold = style.Bold
	colorDim = style.Dim
	colorReset = style.Reset
	tableRule = style.Rule
	treeBranch = style.Branch
	treeLastBranch = style.LastBranch
}

func applyErrorOutputStyle(errOut io.Writer) {
	errStyle = presentation.For(errOut)
}
