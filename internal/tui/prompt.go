package tui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

var (
	colorCyan   string
	colorGreen  string
	colorYellow string
	colorBold   string
	colorDim    string
	colorReset  string
)

func applyPromptStyle() {
	style := presentation.For(os.Stdout)
	colorCyan = style.Cyan
	colorGreen = style.Green
	colorYellow = style.Yellow
	colorBold = style.Bold
	colorDim = style.Dim
	colorReset = style.Reset
}

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

func singleSelectInstructions() [2]string {
	return [2]string{
		"Use ↑/↓ (or k/j) to navigate, Ctrl+f/b to page.",
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
	numItems := len(options)
	if numItems == 0 {
		return "", fmt.Errorf("no options to select")
	}
	applyPromptStyle()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Print(showCursor)
	}()

	cursorIdx := defaultIndex
	if cursorIdx < 0 || cursorIdx >= numItems {
		cursorIdx = 0
	}
	windowStart := 0
	contentWidth, maxVisible, frameLines, ok := terminalPromptViewport()
	if !ok {
		return "", fmt.Errorf("terminal is too small for interactive selection")
	}
	visibleCount := min(numItems, maxVisible)
	rendered := false
	render := func() {
		fmt.Print(redrawPrefix(rendered, frameLines))
		newWindowStart, _, windowEnd, _ := viewportWindow(cursorIdx, windowStart, numItems, maxVisible)
		windowStart = newWindowStart
		instructions := singleSelectInstructions()
		fmt.Printf("%s%s%s%s\r\n", colorBold, colorCyan, clipLine(title, contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine(instructions[0], contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine(instructions[1], contentWidth), colorReset)
		fmt.Printf("%s\r\n", clearLine)
		for i := windowStart; i < windowEnd; i++ {
			option := options[i]
			cursor := " "
			if i == cursorIdx {
				cursor = "❯"
			}
			name := option.Title
			if name == "" {
				name = option.Key
			}
			badge := ""
			if i == defaultIndex {
				// Marks the option Enter would pick before any navigation, replacing PromptChoice's "*" marker.
				badge = " (default)"
			}
			if option.Extra != "" {
				badge += " " + option.Extra
			}
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("%s %s%s", cursor, name, badge), contentWidth))
		}
		for i := visibleCount; i < maxVisible; i++ {
			fmt.Printf("%s\r\n", clearLine)
		}
		remaining := numItems - windowEnd
		if remaining > 0 {
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("  ▼ (%d more below)", remaining), contentWidth))
		} else if windowStart > 0 {
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("  ▲ (%d more above)", windowStart), contentWidth))
		} else {
			fmt.Printf("%s\r\n", clearLine)
		}
		rendered = true
	}

	fmt.Print(hideCursor)
	render()
	for {
		switch readKey(fd) {
		case keyInterrupt:
			fmt.Print("\r\n")
			return "", fmt.Errorf("interrupted")
		case keyEscape:
			fmt.Print("\r\n")
			return "", nil
		case keyEnter:
			fmt.Print("\r\n")
			return options[cursorIdx].Key, nil
		case keyUp:
			cursorIdx = viewportMoveUp(cursorIdx, numItems)
			render()
		case keyDown:
			cursorIdx = viewportMoveDown(cursorIdx, numItems)
			render()
		case keyPageDown:
			cursorIdx = viewportPageDown(cursorIdx, visibleCount, numItems)
			render()
		case keyPageUp:
			cursorIdx = viewportPageUp(cursorIdx, visibleCount)
			render()
		}
	}
}

func PromptInput(prompt string) (string, error) {
	if !IsTerminal() {
		return "", fmt.Errorf("interactive prompt requires a terminal")
	}
	applyPromptStyle()
	fmt.Printf("%s%s%s%s: ", colorBold, colorCyan, prompt, colorReset)
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

func multiSelectInstructions() [2]string {
	return [2]string{
		"Use ↑/↓ (or k/j) to navigate, Ctrl+f/b to page.",
		"Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.",
	}
}

// PromptMultiSelect displays a locally redrawn checkbox list.
// Returns (selectedKeys, nil) or (nil, nil) on cancel.
func PromptMultiSelect(title string, items []SelectOption) ([]string, error) {
	if !IsTerminal() {
		return nil, fmt.Errorf("interactive prompt requires a terminal")
	}
	applyPromptStyle()

	numItems := len(items)
	if numItems == 0 {
		return []string{}, nil
	}

	selected := make([]bool, numItems)
	for i, item := range items {
		selected[i] = item.Selected
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Print(showCursor)
	}()

	cursorIdx := 0
	windowStart := 0
	contentWidth, maxVisible, frameLines, ok := terminalPromptViewport()
	if !ok {
		return nil, fmt.Errorf("terminal is too small for interactive selection")
	}
	visibleCount := min(numItems, maxVisible)
	rendered := false
	render := func() {
		fmt.Print(redrawPrefix(rendered, frameLines))
		newWindowStart, _, windowEnd, _ := viewportWindow(cursorIdx, windowStart, numItems, maxVisible)
		windowStart = newWindowStart
		instructions := multiSelectInstructions()
		fmt.Printf("%s%s%s%s\r\n", colorBold, colorCyan, clipLine(title, contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine(instructions[0], contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine(instructions[1], contentWidth), colorReset)
		fmt.Printf("%s\r\n", clearLine)
		for i := windowStart; i < windowEnd; i++ {
			item := items[i]
			cursor := " "
			if i == cursorIdx {
				cursor = "❯"
			}
			box := "[ ]"
			if selected[i] {
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
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("%s %s %s%s", cursor, box, name, badge), contentWidth))
		}
		for i := visibleCount; i < maxVisible; i++ {
			fmt.Printf("%s\r\n", clearLine)
		}
		remaining := numItems - windowEnd
		if remaining > 0 {
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("  ▼ (%d more below)", remaining), contentWidth))
		} else if windowStart > 0 {
			fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("  ▲ (%d more above)", windowStart), contentWidth))
		} else {
			fmt.Printf("%s\r\n", clearLine)
		}
		rendered = true
	}

	fmt.Print(hideCursor)
	render()
	for {
		switch readKey(fd) {
		case keyInterrupt:
			fmt.Print("\r\n")
			return nil, fmt.Errorf("interrupted")
		case keyEscape:
			fmt.Print("\r\n")
			return nil, nil
		case keyEnter:
			fmt.Print("\r\n")
			chosen := make([]string, 0, numItems)
			for i, isSelected := range selected {
				if isSelected {
					chosen = append(chosen, items[i].Key)
				}
			}
			return chosen, nil
		case keyUp:
			cursorIdx = viewportMoveUp(cursorIdx, numItems)
			render()
		case keyDown:
			cursorIdx = viewportMoveDown(cursorIdx, numItems)
			render()
		case keyPageDown:
			cursorIdx = viewportPageDown(cursorIdx, visibleCount, numItems)
			render()
		case keyPageUp:
			cursorIdx = viewportPageUp(cursorIdx, visibleCount)
			render()
		case keyToggleAll:
			allSelected := true
			for _, isSelected := range selected {
				if !isSelected {
					allSelected = false
					break
				}
			}
			for i := range selected {
				selected[i] = !allSelected
			}
			render()
		case keySpace:
			selected[cursorIdx] = !selected[cursorIdx]
			render()
		}
	}
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
	applyPromptStyle()

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
	sort.Strings(remainingSources)
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

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Print(showCursor)
	}()

	cursorIdx := 0
	windowStart := 0

	contentWidth, maxVisible, frameLines, ok := terminalPromptViewport()
	if !ok {
		return nil, fmt.Errorf("terminal is too small for interactive selection")
	}
	visibleCount := 0
	isScrollable := false
	rendered := false
	isCovered := func(row displayRow) bool {
		return row.dependsOn != "" && selectedMap[row.dependsOn]
	}
	isSelected := func(row displayRow) bool {
		return selectedMap[row.skillKey] || isCovered(row)
	}

	render := func() {
		fmt.Print(redrawPrefix(rendered, frameLines))
		var newWindowStart, windowEnd int
		newWindowStart, visibleCount, windowEnd, isScrollable = viewportWindow(cursorIdx, windowStart, totalRows, maxVisible)
		windowStart = newWindowStart

		fmt.Printf("%s%s%s%s\r\n", colorBold, colorCyan, clipLine(title, contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine("Use ↑/↓ (or k/j) to navigate, ←/→ to collapse/expand groups, Ctrl+f/b to page.", contentWidth), colorReset)
		fmt.Printf("%s%s%s%s\r\n", clearLine, colorDim, clipLine("Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.", contentWidth), colorReset)
		fmt.Printf("%s%s\r\n", clearLine, clipLine(groupedTopIndicator(windowStart, isScrollable), contentWidth))

		for i := windowStart; i < windowEnd; i++ {
			row := rows[i]
			isCursor := (i == cursorIdx)
			cursorStr := " "
			if isCursor {
				cursorStr = "❯"
			}

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
				line := fmt.Sprintf("%s %s %s %s (%s)", cursorStr, box, disclosure, row.groupSource, countStr)
				fmt.Printf("%s%s\r\n", clearLine, clipLine(line, contentWidth))
			} else {
				isChecked := isSelected(row)
				box := "[ ]"
				if isChecked {
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
				line := fmt.Sprintf("%s    %s %s%s", cursorStr, box, display, badge)
				fmt.Printf("%s%s\r\n", clearLine, clipLine(line, contentWidth))
			}
		}
		for i := visibleCount; i < maxVisible; i++ {
			fmt.Printf("%s\r\n", clearLine)
		}

		if isScrollable {
			remaining := totalRows - windowEnd
			if remaining > 0 {
				fmt.Printf("%s%s\r\n", clearLine, clipLine(fmt.Sprintf("  ▼ (%d more below)", remaining), contentWidth))
			} else {
				fmt.Printf("%s\r\n", clearLine)
			}
		} else {
			fmt.Printf("%s\r\n", clearLine)
		}
		rendered = true
	}

	fmt.Print(hideCursor)
	render()

	for {
		k := readKey(fd)
		switch k {
		case keyInterrupt:
			fmt.Print("\r\n")
			return nil, fmt.Errorf("interrupted")
		case keyEscape:
			fmt.Print("\r\n")
			return nil, nil
		case keyEnter:
			fmt.Print("\r\n")
			var chosen []string
			for _, skKey := range allSkillKeys {
				if selectedMap[skKey] {
					chosen = append(chosen, skKey)
				}
			}
			return chosen, nil
		case keyUp:
			cursorIdx = viewportMoveUp(cursorIdx, totalRows)
			render()
		case keyDown:
			cursorIdx = viewportMoveDown(cursorIdx, totalRows)
			render()
		case keyPageDown:
			cursorIdx = viewportPageDown(cursorIdx, visibleCount, totalRows)
			render()
		case keyPageUp:
			cursorIdx = viewportPageUp(cursorIdx, visibleCount)
			render()
		case keyLeft:
			row := rows[cursorIdx]
			if !collapsed[row.groupSource] {
				collapsed[row.groupSource] = true
				rebuildRows()
				for i, candidate := range rows {
					if candidate.rType == rowGroup && candidate.groupSource == row.groupSource {
						cursorIdx = i
						break
					}
				}
				render()
			}
		case keyRight:
			row := rows[cursorIdx]
			if row.rType == rowGroup && collapsed[row.groupSource] {
				collapsed[row.groupSource] = false
				rebuildRows()
				render()
			}
		case keyToggleAll:
			allSel := true
			for _, skKey := range allSkillKeys {
				row := rowByKey[skKey]
				if !isSelected(row) {
					allSel = false
					break
				}
			}
			for _, skKey := range allSkillKeys {
				selectedMap[skKey] = !allSel
			}
			render()
		case keySpace:
			row := rows[cursorIdx]
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
			render()
		}
	}
}

// PromptConfirm prompts the user for a yes/no confirmation.
func PromptConfirm(prompt string, defaultYes bool) (bool, error) {
	if !IsTerminal() {
		return false, fmt.Errorf("interactive prompt requires a terminal")
	}
	applyPromptStyle()
	suffix := " [y/N]: "
	if defaultYes {
		suffix = " [Y/n]: "
	}
	fmt.Printf("%s%s%s%s", colorBold, colorYellow, prompt, colorReset)
	fmt.Print(suffix)
	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "" {
		return defaultYes, nil
	}
	return response == "y" || response == "yes", nil
}
