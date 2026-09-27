package cli

import (
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// prompter is every terminal question the CLI can ask, over tui's
// primitives. rm, trust, doctor, sync, prune, adopt and add each ask through
// one of these instead of calling tui directly, so a test can script the
// answers. Cancellation is tui's own: Select and Input return "", MultiSelect
// and GroupedMultiSelect return nil.
type prompter interface {
	// Interactive reports whether questions can be asked at all.
	Interactive() bool
	Confirm(prompt string, defaultYes bool) (bool, error)
	Select(title string, options []tui.SelectOption, defaultIndex int) (string, error)
	MultiSelect(title string, options []tui.SelectOption) ([]string, error)
	// GroupedMultiSelect shows groups from order first, then any remaining
	// groups alphabetically; a nil order shows every group alphabetically.
	GroupedMultiSelect(title string, groups tui.GroupedItems, order []string) ([]string, error)
	Input(prompt string) (string, error)
}

// newPrompter is a package var so a test can inject a scripted prompter. cmd
// is unused by the production adapter, but it keeps this seam's shape the
// same as newAddPrompter's.
var newPrompter = func(cmd *cobra.Command) prompter {
	return terminalPrompter{}
}

// terminalPrompter is prompter over the real terminal, through tui.
type terminalPrompter struct{}

func (terminalPrompter) Interactive() bool { return tui.IsTerminal() }

func (terminalPrompter) Confirm(prompt string, defaultYes bool) (bool, error) {
	return tui.PromptConfirm(prompt, defaultYes)
}

func (terminalPrompter) Select(title string, options []tui.SelectOption, defaultIndex int) (string, error) {
	return tui.PromptSelect(title, options, defaultIndex)
}

func (terminalPrompter) MultiSelect(title string, options []tui.SelectOption) ([]string, error) {
	return tui.PromptMultiSelect(title, options)
}

func (terminalPrompter) GroupedMultiSelect(title string, groups tui.GroupedItems, order []string) ([]string, error) {
	return tui.PromptOrderedGroupedMultiSelect(title, groups, order)
}

func (terminalPrompter) Input(prompt string) (string, error) {
	return tui.PromptInput(prompt)
}
