package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// errAddCancelled is how every addPrompter question reports that the user
// backed out of it.
var errAddCancelled = errors.New("add cancelled")

// addPrompter is every question Add can ask. The Add conversation decides
// which to ask and when; a prompter only asks. User cancellation at any of
// them is errAddCancelled.
type addPrompter interface {
	// Interactive reports whether questions can be asked at all.
	Interactive() bool
	// SelectSkills offers the discovered Skills, grouped when groups is
	// non-nil and as flat otherwise. Choosing none is an empty, non-nil
	// slice, not a cancellation.
	SelectSkills(title string, groups tui.GroupedItems, flat []tui.SelectOption) ([]string, error)
	SelectSourcePath(skill string, paths []string) (string, error)
	SelectScope() (project bool, err error)
	SelectAvailability() (customize bool, err error)
	SelectAgents(options []tui.SelectOption) ([]string, error)
	// ConfirmOverwrite asks before Add replaces existing Skills. Declining
	// is a cancellation.
	ConfirmOverwrite(conflicts []engine.AddConflict) error
}

var newAddPrompter = func(cmd *cobra.Command) addPrompter {
	return terminalAddPrompter{out: cmd.OutOrStdout()}
}

// terminalAddPrompter asks through the tui package, translating each
// prompt's own cancel signal (nil, "") into errAddCancelled.
type terminalAddPrompter struct {
	out io.Writer
}

func (terminalAddPrompter) Interactive() bool { return tui.IsTerminal() }

func (terminalAddPrompter) SelectSkills(title string, groups tui.GroupedItems, flat []tui.SelectOption) ([]string, error) {
	var chosen []string
	var err error
	if groups != nil {
		chosen, err = tui.PromptGroupedMultiSelect(title, groups)
	} else {
		chosen, err = tui.PromptMultiSelect(title, flat)
	}
	if err != nil {
		return nil, err
	}
	if chosen == nil {
		return nil, errAddCancelled
	}
	return chosen, nil
}

func (terminalAddPrompter) SelectSourcePath(skill string, paths []string) (string, error) {
	options := make([]tui.SelectOption, 0, len(paths)+1)
	options = append(options, tui.SelectOption{Title: "Select a Source path"})
	for _, candidate := range paths {
		options = append(options, tui.SelectOption{Key: candidate, Title: candidate})
	}
	chosen, err := tui.PromptSelect(fmt.Sprintf("Select a Source path for %s:", skill), options, -1)
	if err != nil {
		return "", err
	}
	if chosen == "" {
		return "", errAddCancelled
	}
	return chosen, nil
}

func (terminalAddPrompter) SelectScope() (bool, error) {
	choice, err := tui.PromptSelect("Choose a scope:", []tui.SelectOption{
		{Key: "global", Title: "Global"},
		{Key: "project", Title: "Project"},
	}, 0)
	if err != nil {
		return false, err
	}
	if choice == "" {
		return false, errAddCancelled
	}
	return choice == "project", nil
}

func (terminalAddPrompter) SelectAvailability() (bool, error) {
	choice, err := tui.PromptSelect("Agent availability:", []tui.SelectOption{
		{Key: "defaults", Title: "Follow defaults (recommended)"},
		{Key: "custom", Title: "Customize"},
	}, 0)
	if err != nil {
		return false, err
	}
	if choice == "" {
		return false, errAddCancelled
	}
	return choice == "custom", nil
}

func (terminalAddPrompter) SelectAgents(options []tui.SelectOption) ([]string, error) {
	selected, err := tui.PromptMultiSelect("Select agents where these skills should be available:", options)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, errAddCancelled
	}
	return selected, nil
}

func (p terminalAddPrompter) ConfirmOverwrite(conflicts []engine.AddConflict) error {
	fmt.Fprintf(p.out, "\n%sWarning: The following %d skill(s) already exist and will be overwritten:%s\n", colorYellow, len(conflicts), colorReset)
	for _, c := range conflicts {
		fmt.Fprintf(p.out, "  • %s%s%s: %s -> %s\n", colorBold, c.Skill, colorReset, c.CurrentSrc, c.ProposedSrc)
	}
	fmt.Fprintln(p.out)
	confirmed, err := tui.PromptConfirm("Do you want to proceed with overwriting these skills?", false)
	if err != nil {
		return err
	}
	if !confirmed {
		return errAddCancelled
	}
	return nil
}
