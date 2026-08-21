package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"golang.org/x/term"
)

const (
	colorCyan   = "\033[96m"
	colorGreen  = "\033[92m"
	colorYellow = "\033[93m"
	colorRed    = "\033[91m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"

	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"
	clearLine  = "\033[2K"

	enterAlternateScreen = "\033[?1049h"
	leaveAlternateScreen = "\033[?1049l"
	groupedPromptRedraw  = "\033[H\033[2J"
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

func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
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

func getTermHeight() int {
	_, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || height <= 0 {
		return 24
	}
	return height
}

// PromptMultiSelect displays interactive list with Esc/q to cancel, 'a' to toggle all, Space to toggle.
// Returns (selectedKeys, nil) or (nil, nil) on cancel.
func PromptMultiSelect(title string, items []SelectOption) ([]string, error) {
	if !IsTerminal() {
		return nil, fmt.Errorf("interactive prompt requires a terminal")
	}

	numItems := len(items)
	if numItems == 0 {
		return []string{}, nil
	}

	selected := make([]bool, numItems)
	for i, it := range items {
		selected[i] = it.Selected
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

	// Handle SIGINT cleanly
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	cursorIdx := 0
	windowStart := 0

	termHeight := getTermHeight()
	maxVisible := termHeight - 6
	if maxVisible < 10 {
		maxVisible = 10
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	visibleCount := numItems
	if visibleCount > maxVisible {
		visibleCount = maxVisible
	}
	isScrollable := numItems > maxVisible

	// title (2 lines) + visible items + (2 scroll indicator lines if scrollable) + instructions (2 lines)
	scrollLines := 0
	if isScrollable {
		scrollLines = 2
	}
	frameLines := 2 + visibleCount + scrollLines + 2

	render := func(first bool) {
		if !first {
			fmt.Printf("\033[%dA\r", frameLines)
		}

		if isScrollable {
			if cursorIdx < windowStart {
				windowStart = cursorIdx
			} else if cursorIdx >= windowStart+visibleCount {
				windowStart = cursorIdx - visibleCount + 1
			}
		}
		windowEnd := windowStart + visibleCount
		if windowEnd > numItems {
			windowEnd = numItems
		}

		fmt.Printf("%s%s%s%s\r\n\r\n", colorBold, colorCyan, title, colorReset)

		if isScrollable {
			if windowStart > 0 {
				fmt.Printf("%s  %s▲ (%d more above)%s\r\n", clearLine, colorDim, windowStart, colorReset)
			} else {
				fmt.Printf("%s\r\n", clearLine)
			}
		}

		for i := windowStart; i < windowEnd; i++ {
			it := items[i]
			isCursor := (i == cursorIdx)
			isChecked := selected[i]

			prefix := " "
			if isCursor {
				prefix = fmt.Sprintf("%s❯%s", colorCyan, colorReset)
			}
			box := fmt.Sprintf("%s[ ]%s", colorDim, colorReset)
			if isChecked {
				box = fmt.Sprintf("%s[✔]%s", colorGreen, colorReset)
			}

			badge := ""
			if it.Installed {
				badge = fmt.Sprintf(" %s(installed)%s", colorDim, colorReset)
			}
			if it.Extra != "" {
				badge += fmt.Sprintf(" %s%s%s", colorDim, it.Extra, colorReset)
			}

			nameDisplay := it.Title
			if nameDisplay == "" {
				nameDisplay = it.Key
			}
			if isCursor {
				nameDisplay = fmt.Sprintf("%s%s%s", colorBold, nameDisplay, colorReset)
			}

			fmt.Printf("%s%s %s %s%s\r\n", clearLine, prefix, box, nameDisplay, badge)
		}

		if isScrollable {
			remaining := numItems - windowEnd
			if remaining > 0 {
				fmt.Printf("%s  %s▼ (%d more below)%s\r\n", clearLine, colorDim, remaining, colorReset)
			} else {
				fmt.Printf("%s\r\n", clearLine)
			}
		}

		instructions := fmt.Sprintf("%sUse ↑/↓ (or k/j) to navigate, Ctrl+f/b to page, Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.%s", colorDim, colorReset)
		fmt.Printf("\r\n%s%s\r\n", clearLine, instructions)
	}

	fmt.Print(hideCursor)
	render(true)

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
			for i, sel := range selected {
				if sel {
					chosen = append(chosen, items[i].Key)
				}
			}
			return chosen, nil
		case keyUp:
			cursorIdx = (cursorIdx - 1 + numItems) % numItems
			render(false)
		case keyDown:
			cursorIdx = (cursorIdx + 1) % numItems
			render(false)
		case keyPageDown:
			cursorIdx += visibleCount - 1
			if cursorIdx >= numItems {
				cursorIdx = numItems - 1
			}
			render(false)
		case keyPageUp:
			cursorIdx -= visibleCount - 1
			if cursorIdx < 0 {
				cursorIdx = 0
			}
			render(false)
		case keySpace:
			selected[cursorIdx] = !selected[cursorIdx]
			render(false)
		case keyToggleAll:
			allChecked := true
			for _, sel := range selected {
				if !sel {
					allChecked = false
					break
				}
			}
			for i := range selected {
				selected[i] = !allChecked
			}
			render(false)
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
		return clearLine + "\r\n"
	}

	return fmt.Sprintf("%s  %s▲ (%d more above)%s\r\n", clearLine, colorDim, windowStart, colorReset)
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
		fmt.Print(leaveAlternateScreen, showCursor)
	}()

	cursorIdx := 0
	windowStart := 0

	termHeight := getTermHeight()
	maxVisible := termHeight - 6
	if maxVisible < 10 {
		maxVisible = 10
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	visibleCount := 0
	isScrollable := false
	isCovered := func(row displayRow) bool {
		return row.dependsOn != "" && selectedMap[row.dependsOn]
	}
	isSelected := func(row displayRow) bool {
		return selectedMap[row.skillKey] || isCovered(row)
	}

	render := func() {
		fmt.Print(groupedPromptRedraw)
		visibleCount = totalRows
		if visibleCount > maxVisible {
			visibleCount = maxVisible
		}
		isScrollable = totalRows > maxVisible
		if isScrollable {
			if cursorIdx < windowStart {
				windowStart = cursorIdx
			} else if cursorIdx >= windowStart+visibleCount {
				windowStart = cursorIdx - visibleCount + 1
			}
		}
		if windowStart >= totalRows {
			windowStart = totalRows - visibleCount
		}
		windowEnd := windowStart + visibleCount
		if windowEnd > totalRows {
			windowEnd = totalRows
		}

		fmt.Printf("%s%s%s%s\r\n", colorBold, colorCyan, title, colorReset)
		fmt.Printf("%s%sUse ↑/↓ (or k/j) to navigate, ←/→ to collapse/expand groups, Ctrl+f/b to page.%s\r\n", clearLine, colorDim, colorReset)
		fmt.Printf("%s%sSpace to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.%s\r\n", clearLine, colorDim, colorReset)
		fmt.Print(groupedTopIndicator(windowStart, isScrollable))

		for i := windowStart; i < windowEnd; i++ {
			row := rows[i]
			isCursor := (i == cursorIdx)
			cursorStr := " "
			if isCursor {
				cursorStr = fmt.Sprintf("%s❯%s", colorCyan, colorReset)
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

				box := fmt.Sprintf("%s[ ]%s", colorDim, colorReset)
				if len(row.groupSkills) > 0 && allSel {
					box = fmt.Sprintf("%s[✔]%s", colorGreen, colorReset)
				} else if selectedCount > 0 {
					box = fmt.Sprintf("%s[-]%s", colorYellow, colorReset)
				}

				countStr := fmt.Sprintf("%d/%d selected", selectedCount, len(row.groupSkills))
				if len(row.groupSkills) == 1 {
					countStr = fmt.Sprintf("%d/1 selected", selectedCount)
				}

				disclosure := "▾"
				if collapsed[row.groupSource] {
					disclosure = "▸"
				}
				line := fmt.Sprintf("%s %s %s%s %s%s %s(%s)%s", cursorStr, box, colorBold, disclosure, row.groupSource, colorReset, colorDim, countStr, colorReset)
				fmt.Printf("%s%s\r\n", clearLine, line)
			} else {
				isChecked := isSelected(row)
				box := fmt.Sprintf("%s[ ]%s", colorDim, colorReset)
				if isChecked {
					box = fmt.Sprintf("%s[✔]%s", colorGreen, colorReset)
				}

				badge := ""
				if row.installed {
					badge = fmt.Sprintf(" %s(installed)%s", colorDim, colorReset)
				}
				if row.extra != "" {
					badge += fmt.Sprintf(" %s%s%s", colorDim, row.extra, colorReset)
				}
				if isCovered(row) {
					badge += fmt.Sprintf(" %s(covered by selected master)%s", colorDim, colorReset)
				}

				display := row.skillTitle
				if display == "" {
					display = row.skillKey
				}
				if isCursor {
					display = fmt.Sprintf("%s%s%s", colorBold, display, colorReset)
				}

				line := fmt.Sprintf("%s    %s %s%s", cursorStr, box, display, badge)
				fmt.Printf("%s%s\r\n", clearLine, line)
			}
		}

		if isScrollable {
			remaining := totalRows - windowEnd
			if remaining > 0 {
				fmt.Printf("%s  %s▼ (%d more below)%s\r\n", clearLine, colorDim, remaining, colorReset)
			} else {
				fmt.Printf("%s\r\n", clearLine)
			}
		}

	}

	fmt.Print(enterAlternateScreen, hideCursor)
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
			cursorIdx = (cursorIdx - 1 + totalRows) % totalRows
			render()
		case keyDown:
			cursorIdx = (cursorIdx + 1) % totalRows
			render()
		case keyPageDown:
			cursorIdx += visibleCount - 1
			if cursorIdx >= totalRows {
				cursorIdx = totalRows - 1
			}
			render()
		case keyPageUp:
			cursorIdx -= visibleCount - 1
			if cursorIdx < 0 {
				cursorIdx = 0
			}
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
