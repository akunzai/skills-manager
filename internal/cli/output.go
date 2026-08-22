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
