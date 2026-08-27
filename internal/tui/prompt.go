package tui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

const (
	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"
	clearLine  = "\033[2K"
)

type SelectOption struct {
	Key       string
	Title     string
	Extra     string
	Installed bool
	Selected  bool
	DependsOn string
}

type GroupedItems map[string][]SelectOption

func singleSelectInstructions(isScrollable bool) [2]string {
	if isScrollable {
		return [2]string{
			"Use ↑/↓ (or k/j) to navigate, Ctrl+f/b to page.",
			"Enter to confirm, Esc/q to cancel.",
		}
	}
	return [2]string{
		"Use ↑/↓ (or k/j) to navigate.",
		"Enter to confirm, Esc/q to cancel.",
	}
}

// PromptSelect displays a locally redrawn single-select list navigated with
// arrow keys. The cursor starts at defaultIndex.
// Returns (selectedKey, nil) or ("", nil) on Esc/q cancel.
func PromptSelect(title string, options []SelectOption, defaultIndex int) (string, error) {
	if !IsTerminal() {
		return "", fmt.Errorf("interactive prompt requires a terminal")
	}
	var selected string
	err := rawListSession(func() error {
		choice, err := runSelect(title, options, defaultIndex, presentation.For(os.Stdout), os.Stdout, func() keyType {
			return readKey(int(os.Stdin.Fd()))
		}, sizeFromViewport(terminalPromptViewport()))
		selected = choice
		return err
	})
	return selected, err
}

func runSelect(
	title string,
	options []SelectOption,
	defaultIndex int,
	style presentation.Style,
	out io.Writer,
	readKeyFn func() keyType,
	size promptSize,
) (string, error) {
	numItems := len(options)
	if numItems == 0 {
		return "", fmt.Errorf("no options to select")
	}
	if !size.ok {
		return "", fmt.Errorf("terminal is too small for interactive selection")
	}
	cursorIdx := defaultIndex
	if cursorIdx < 0 || cursorIdx >= numItems {
		cursorIdx = 0
	}
	var chosen string
	err := listLoop{
		title:        title,
		style:        style,
		out:          out,
		readKey:      readKeyFn,
		contentWidth: size.width,
		maxVisible:   size.maxVisible,
		frameLines:   size.frame,
		cursorIdx:    cursorIdx,
		total:        func() int { return numItems },
		instructions: singleSelectInstructions,
		extraHeader:  blankHeader,
		rowLine: func(i, cursor, width int) string {
			option := options[i]
			name := option.Title
			if name == "" {
				name = option.Key
			}
			badge := ""
			if i == defaultIndex {
				badge = " (default)"
			}
			if option.Extra != "" {
				badge += " " + option.Extra
			}
			return fmt.Sprintf("%s %s%s", cursorMark(i, cursor), name, badge)
		},
		footer: selectFooter,
		handle: func(k keyType, cursor, visible int, scrollable bool) (int, loopResult, error) {
			switch k {
			case keyInterrupt:
				return cursor, loopDone, fmt.Errorf("interrupted")
			case keyEscape:
				chosen = ""
				return cursor, loopDone, nil
			case keyEnter:
				chosen = options[cursor].Key
				return cursor, loopDone, nil
			case keyUp:
				return viewportMoveUp(cursor, numItems), loopRedraw, nil
			case keyDown:
				return viewportMoveDown(cursor, numItems), loopRedraw, nil
			case keyPageDown:
				if scrollable {
					return viewportPageDown(cursor, visible, numItems), loopRedraw, nil
				}
			case keyPageUp:
				if scrollable {
					return viewportPageUp(cursor, visible), loopRedraw, nil
				}
			}
			return cursor, loopContinue, nil
		},
	}.run()
	return chosen, err
}

func cursorMark(i, cursor int) string {
	if i == cursor {
		return "❯"
	}
	return " "
}

func PromptInput(prompt string) (string, error) {
	if !IsTerminal() {
		return "", fmt.Errorf("interactive prompt requires a terminal")
	}
	style := presentation.For(os.Stdout)
	fmt.Printf("%s%s%s%s: ", style.Bold, style.Cyan, prompt, style.Reset)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func IsTerminal() bool {
	return os.Getenv("TERM") != "dumb" && term.IsTerminal(int(os.Stdin.Fd()))
}

type keyType int

const (
	keyUnknown keyType = iota
	keyUp
	keyDown
	keySpace
	keyEnter
	keyEscape
	keyToggleAll // 'a' or 'A'
	keyInterrupt
	keyPageDown // Ctrl+f
	keyPageUp   // Ctrl+b
	keyLeft
	keyRight
)

func readKey(fd int) keyType {
	var buf [16]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return keyUnknown
	}

	return parseKey(buf[:n])
}

func parseKey(b []byte) keyType {

	if len(b) == 1 {
		switch b[0] {
		case 2: // Ctrl+B
			return keyPageUp
		case 3: // Ctrl+C
			return keyInterrupt
		case 6: // Ctrl+F
			return keyPageDown
		case 13, 10: // Enter
			return keyEnter
		case ' ', 'x', 'X':
			return keySpace
		case 27, 'q', 'Q':
			return keyEscape
		case 'k', 'K':
			return keyUp
		case 'j', 'J':
			return keyDown
		case 'a', 'A':
			return keyToggleAll
		}
	}

	// Escape sequences (arrows)
	if bytes.HasPrefix(b, []byte{27, '['}) || bytes.HasPrefix(b, []byte{27, 'O'}) {
		if len(b) >= 3 {
			switch b[2] {
			case 'A':
				return keyUp
			case 'B':
				return keyDown
			case 'C':
				return keyRight
			case 'D':
				return keyLeft
			}
		}
	}

	return keyUnknown
}

func promptViewport(width, height int) (int, int, int, bool) {
	if width < 20 || height < 7 {
		return 0, 0, 0, false
	}
	contentWidth := width - 1
	maxVisible := min(height-6, 20)
	return contentWidth, maxVisible, maxVisible + 5, true
}

func terminalPromptViewport() (int, int, int, bool) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}
	return promptViewport(width, height)
}

func clipLine(text string, width int) string {
	return runewidth.Truncate(text, width, "")
}

func multiSelectInstructions(isScrollable bool) [2]string {
	if isScrollable {
		return [2]string{
			"Use ↑/↓ (or k/j) to navigate, Ctrl+f/b to page.",
			"Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.",
		}
	}
	return [2]string{
		"Use ↑/↓ (or k/j) to navigate.",
		"Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.",
	}
}

// PromptMultiSelect displays a locally redrawn checkbox list.
// Returns (selectedKeys, nil) or (nil, nil) on cancel.
func PromptMultiSelect(title string, items []SelectOption) ([]string, error) {
	if !IsTerminal() {
		return nil, fmt.Errorf("interactive prompt requires a terminal")
	}
	var selected []string
	err := rawListSession(func() error {
		choice, err := runMultiSelect(title, items, presentation.For(os.Stdout), os.Stdout, func() keyType {
			return readKey(int(os.Stdin.Fd()))
		}, sizeFromViewport(terminalPromptViewport()))
		selected = choice
		return err
	})
	return selected, err
}

func runMultiSelect(
	title string,
	items []SelectOption,
	style presentation.Style,
	out io.Writer,
	readKeyFn func() keyType,
	size promptSize,
) ([]string, error) {
	numItems := len(items)
	if numItems == 0 {
		return []string{}, nil
	}
	if !size.ok {
		return nil, fmt.Errorf("terminal is too small for interactive selection")
	}
	checked := make([]bool, numItems)
	for i, item := range items {
		checked[i] = item.Selected
	}
	var chosen []string
	cancelled := false
	err := listLoop{
		title:        title,
		style:        style,
		out:          out,
		readKey:      readKeyFn,
		contentWidth: size.width,
		maxVisible:   size.maxVisible,
		frameLines:   size.frame,
		cursorIdx:    0,
		total:        func() int { return numItems },
		instructions: multiSelectInstructions,
		extraHeader:  blankHeader,
		rowLine: func(i, cursor, width int) string {
			item := items[i]
			box := "[ ]"
			if checked[i] {
				box = "[✓]"
			}
			name := item.Title
			if name == "" {
				name = item.Key
			}
			badge := ""
			if item.Installed {
				badge = " (installed)"
			}
			if item.Extra != "" {
				badge += " " + item.Extra
			}
			return fmt.Sprintf("%s %s %s%s", cursorMark(i, cursor), box, name, badge)
		},
		footer: selectFooter,
		handle: func(k keyType, cursor, visible int, scrollable bool) (int, loopResult, error) {
			switch k {
			case keyInterrupt:
				return cursor, loopDone, fmt.Errorf("interrupted")
			case keyEscape:
				cancelled = true
				return cursor, loopDone, nil
			case keyEnter:
				chosen = make([]string, 0, numItems)
				for i, on := range checked {
					if on {
						chosen = append(chosen, items[i].Key)
					}
				}
				return cursor, loopDone, nil
			case keyUp:
				return viewportMoveUp(cursor, numItems), loopRedraw, nil
			case keyDown:
				return viewportMoveDown(cursor, numItems), loopRedraw, nil
			case keyPageDown:
				if scrollable {
					return viewportPageDown(cursor, visible, numItems), loopRedraw, nil
				}
			case keyPageUp:
				if scrollable {
					return viewportPageUp(cursor, visible), loopRedraw, nil
				}
			case keyToggleAll:
				allSelected := true
				for _, on := range checked {
					if !on {
						allSelected = false
						break
					}
				}
				for i := range checked {
					checked[i] = !allSelected
				}
				return cursor, loopRedraw, nil
			case keySpace:
				checked[cursor] = !checked[cursor]
				return cursor, loopRedraw, nil
			}
			return cursor, loopContinue, nil
		},
	}.run()
	if cancelled {
		return nil, err
	}
	return chosen, err
}

type rowType int

const (
	rowGroup rowType = iota
	rowSkill
)

type displayRow struct {
	rType       rowType
	groupSource string
	skillKey    string
	skillTitle  string
	installed   bool
	extra       string
	dependsOn   string
	groupSkills []string
}

func groupedMultiSelectInstructions(isScrollable bool) [2]string {
	if isScrollable {
		return [2]string{
			"Use ↑/↓ (or k/j) to navigate, ←/→ to collapse/expand groups, Ctrl+f/b to page.",
			"Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.",
		}
	}
	return [2]string{
		"Use ↑/↓ (or k/j) to navigate, ←/→ to collapse/expand groups.",
		"Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.",
	}
}

func groupedTopIndicator(windowStart int, isScrollable bool) string {
	if !isScrollable || windowStart == 0 {
		return ""
	}

	return fmt.Sprintf("  ▲ (%d more above)", windowStart)
}

func redrawPrefix(rendered bool, frameLines int) string {
	if !rendered {
		return ""
	}
	return fmt.Sprintf("\033[%dA\r", frameLines)
}

// PromptGroupedMultiSelect displays grouped items with collapsible/batch-selectable group headers.
// Pressing Space on a group header batch toggles all skills in that group.
// Returns (selectedSkillKeys, nil) or (nil, nil) on Esc/q cancel.
func PromptGroupedMultiSelect(title string, groupedItems GroupedItems) ([]string, error) {
	return promptGroupedMultiSelect(title, groupedItems, nil)
}

// PromptOrderedGroupedMultiSelect displays groups in groupOrder first, then
// any remaining groups alphabetically.
func PromptOrderedGroupedMultiSelect(title string, groupedItems GroupedItems, groupOrder []string) ([]string, error) {
	return promptGroupedMultiSelect(title, groupedItems, groupOrder)
}

func promptGroupedMultiSelect(title string, groupedItems GroupedItems, groupOrder []string) ([]string, error) {
	if !IsTerminal() {
		return nil, fmt.Errorf("interactive prompt requires a terminal")
	}
	var selected []string
	err := rawListSession(func() error {
		choice, err := runGroupedMultiSelect(title, groupedItems, groupOrder, presentation.For(os.Stdout), os.Stdout, func() keyType {
			return readKey(int(os.Stdin.Fd()))
		}, sizeFromViewport(terminalPromptViewport()))
		selected = choice
		return err
	})
	return selected, err
}

func runGroupedMultiSelect(
	title string,
	groupedItems GroupedItems,
	groupOrder []string,
	style presentation.Style,
	out io.Writer,
	readKeyFn func() keyType,
	size promptSize,
) ([]string, error) {
	if !size.ok {
		return nil, fmt.Errorf("terminal is too small for interactive selection")
	}

	var rows []displayRow
	var allSkillKeys []string
	selectedMap := make(map[string]bool)
	groupHeaders := make(map[string]displayRow, len(groupedItems))
	groupRows := make(map[string][]displayRow, len(groupedItems))

	sources := make([]string, 0, len(groupedItems))
	seenSources := make(map[string]struct{}, len(groupedItems))
	for _, source := range groupOrder {
		if len(groupedItems[source]) == 0 {
			continue
		}
		sources = append(sources, source)
		seenSources[source] = struct{}{}
	}
	var remainingSources []string
	for source := range groupedItems {
		if _, seen := seenSources[source]; !seen {
			remainingSources = append(remainingSources, source)
		}
	}
	slices.Sort(remainingSources)
	sources = append(sources, remainingSources...)
	for _, source := range sources {
		skills := groupedItems[source]
		if len(skills) == 0 {
			continue
		}
		var groupSkillKeys []string
		for _, sk := range skills {
			groupSkillKeys = append(groupSkillKeys, sk.Key)
			allSkillKeys = append(allSkillKeys, sk.Key)
			if sk.Selected {
				selectedMap[sk.Key] = true
			}
		}

		groupHeaders[source] = displayRow{
			rType:       rowGroup,
			groupSource: source,
			groupSkills: groupSkillKeys,
		}

		for _, sk := range skills {
			groupRows[source] = append(groupRows[source], displayRow{
				rType:       rowSkill,
				groupSource: source,
				skillKey:    sk.Key,
				skillTitle:  sk.Title,
				installed:   sk.Installed,
				extra:       sk.Extra,
				dependsOn:   sk.DependsOn,
			})
		}
	}

	collapsed := make(map[string]bool, len(sources))
	for _, source := range sources {
		collapsed[source] = true
	}
	totalRows := 0
	rebuildRows := func() {
		rows = rows[:0]
		for _, source := range sources {
			rows = append(rows, groupHeaders[source])
			if !collapsed[source] {
				rows = append(rows, groupRows[source]...)
			}
		}
		totalRows = len(rows)
	}
	rebuildRows()
	if totalRows == 0 {
		return []string{}, nil
	}
	rowByKey := make(map[string]displayRow, len(allSkillKeys))
	for _, group := range groupRows {
		for _, row := range group {
			rowByKey[row.skillKey] = row
		}
	}

	isCovered := func(row displayRow) bool {
		return row.dependsOn != "" && selectedMap[row.dependsOn]
	}
	isSelected := func(row displayRow) bool {
		return selectedMap[row.skillKey] || isCovered(row)
	}

	var chosen []string
	cancelled := false
	err := listLoop{
		title:        title,
		style:        style,
		out:          out,
		readKey:      readKeyFn,
		contentWidth: size.width,
		maxVisible:   size.maxVisible,
		frameLines:   size.frame,
		cursorIdx:    0,
		total:        func() int { return totalRows },
		instructions: groupedMultiSelectInstructions,
		extraHeader:  groupedTopIndicator,
		rowLine: func(i, cursor, width int) string {
			row := rows[i]
			if row.rType == rowGroup {
				allSel := true
				selectedCount := 0
				for _, skKey := range row.groupSkills {
					if selectedMap[skKey] || (rowByKey[skKey].dependsOn != "" && selectedMap[rowByKey[skKey].dependsOn]) {
						selectedCount++
					} else {
						allSel = false
					}
				}
				box := "[ ]"
				if len(row.groupSkills) > 0 && allSel {
					box = "[✓]"
				} else if selectedCount > 0 {
					box = "[-]"
				}
				countStr := fmt.Sprintf("%d/%d selected", selectedCount, len(row.groupSkills))
				if len(row.groupSkills) == 1 {
					countStr = fmt.Sprintf("%d/1 selected", selectedCount)
				}
				disclosure := "▾"
				if collapsed[row.groupSource] {
					disclosure = "▸"
				}
				return fmt.Sprintf("%s %s %s %s (%s)", cursorMark(i, cursor), box, disclosure, row.groupSource, countStr)
			}
			box := "[ ]"
			if isSelected(row) {
				box = "[✓]"
			}
			badge := ""
			if row.installed {
				badge = " (installed)"
			}
			if row.extra != "" {
				badge += " " + row.extra
			}
			if isCovered(row) {
				badge += " (covered by selected master)"
			}
			display := row.skillTitle
			if display == "" {
				display = row.skillKey
			}
			return fmt.Sprintf("%s    %s %s%s", cursorMark(i, cursor), box, display, badge)
		},
		footer: groupedFooter,
		handle: func(k keyType, cursor, visible int, scrollable bool) (int, loopResult, error) {
			switch k {
			case keyInterrupt:
				return cursor, loopDone, fmt.Errorf("interrupted")
			case keyEscape:
				cancelled = true
				return cursor, loopDone, nil
			case keyEnter:
				for _, skKey := range allSkillKeys {
					if selectedMap[skKey] {
						chosen = append(chosen, skKey)
					}
				}
				return cursor, loopDone, nil
			case keyUp:
				return viewportMoveUp(cursor, totalRows), loopRedraw, nil
			case keyDown:
				return viewportMoveDown(cursor, totalRows), loopRedraw, nil
			case keyPageDown:
				if scrollable {
					return viewportPageDown(cursor, visible, totalRows), loopRedraw, nil
				}
			case keyPageUp:
				if scrollable {
					return viewportPageUp(cursor, visible), loopRedraw, nil
				}
			case keyLeft:
				row := rows[cursor]
				if !collapsed[row.groupSource] {
					collapsed[row.groupSource] = true
					rebuildRows()
					for i, candidate := range rows {
						if candidate.rType == rowGroup && candidate.groupSource == row.groupSource {
							return i, loopRedraw, nil
						}
					}
					return cursor, loopRedraw, nil
				}
			case keyRight:
				row := rows[cursor]
				if row.rType == rowGroup && collapsed[row.groupSource] {
					collapsed[row.groupSource] = false
					rebuildRows()
					return cursor, loopRedraw, nil
				}
			case keyToggleAll:
				allSel := true
				for _, skKey := range allSkillKeys {
					if !isSelected(rowByKey[skKey]) {
						allSel = false
						break
					}
				}
				for _, skKey := range allSkillKeys {
					selectedMap[skKey] = !allSel
				}
				return cursor, loopRedraw, nil
			case keySpace:
				row := rows[cursor]
				if row.rType == rowGroup {
					allSel := true
					for _, skKey := range row.groupSkills {
						if !isSelected(rowByKey[skKey]) {
							allSel = false
							break
						}
					}
					for _, skKey := range row.groupSkills {
						selectedMap[skKey] = !allSel
					}
				} else if !isCovered(row) {
					selectedMap[row.skillKey] = !selectedMap[row.skillKey]
				}
				return cursor, loopRedraw, nil
			}
			return cursor, loopContinue, nil
		},
	}.run()
	if cancelled {
		return nil, err
	}
	return chosen, err
}

// PromptConfirm prompts the user for a yes/no confirmation.
func PromptConfirm(prompt string, defaultYes bool) (bool, error) {
	if !IsTerminal() {
		return false, fmt.Errorf("interactive prompt requires a terminal")
	}
	style := presentation.For(os.Stdout)
	suffix := " [y/N]: "
	if defaultYes {
		suffix = " [Y/n]: "
	}
	fmt.Printf("%s%s%s%s", style.Bold, style.Yellow, prompt, style.Reset)
	fmt.Print(suffix)
	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "" {
		return defaultYes, nil
	}
	return response == "y" || response == "yes", nil
}
