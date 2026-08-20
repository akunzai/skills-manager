package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
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
)

type SelectOption struct {
	Key       string
	Title     string
	Extra     string
	Installed bool
	Selected  bool
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
)

func readKey(fd int) keyType {
	var buf [16]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return keyUnknown
	}

	b := buf[:n]

	if len(b) == 1 {
		switch b[0] {
		case 3: // Ctrl+C
			return keyInterrupt
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

		instructions := fmt.Sprintf("%sUse ↑/↓ (or k/j) to navigate, Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.%s", colorDim, colorReset)
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
	groupSkills []string
}

// PromptGroupedMultiSelect displays grouped items with collapsible/batch-selectable group headers.
// Pressing Space on a group header batch toggles all skills in that group.
// Returns (selectedSkillKeys, nil) or (nil, nil) on Esc/q cancel.
func PromptGroupedMultiSelect(title string, groupedItems GroupedItems) ([]string, error) {
	if !IsTerminal() {
		return nil, fmt.Errorf("interactive prompt requires a terminal")
	}

	var rows []displayRow
	var allSkillKeys []string
	selectedMap := make(map[string]bool)

	for source, skills := range groupedItems {
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

		rows = append(rows, displayRow{
			rType:       rowGroup,
			groupSource: source,
			groupSkills: groupSkillKeys,
		})

		for _, sk := range skills {
			rows = append(rows, displayRow{
				rType:       rowSkill,
				groupSource: source,
				skillKey:    sk.Key,
				skillTitle:  sk.Title,
				installed:   sk.Installed,
				extra:       sk.Extra,
			})
		}
	}

	totalRows := len(rows)
	if totalRows == 0 {
		return []string{}, nil
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

	termHeight := getTermHeight()
	maxVisible := termHeight - 6
	if maxVisible < 10 {
		maxVisible = 10
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	visibleCount := totalRows
	if visibleCount > maxVisible {
		visibleCount = maxVisible
	}
	isScrollable := totalRows > maxVisible

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
		if windowEnd > totalRows {
			windowEnd = totalRows
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
			row := rows[i]
			isCursor := (i == cursorIdx)
			cursorStr := " "
			if isCursor {
				cursorStr = fmt.Sprintf("%s❯%s", colorCyan, colorReset)
			}

			if row.rType == rowGroup {
				allSel := true
				anySel := false
				for _, skKey := range row.groupSkills {
					if selectedMap[skKey] {
						anySel = true
					} else {
						allSel = false
					}
				}

				box := fmt.Sprintf("%s[ ]%s", colorDim, colorReset)
				if len(row.groupSkills) > 0 && allSel {
					box = fmt.Sprintf("%s[✔]%s", colorGreen, colorReset)
				} else if anySel {
					box = fmt.Sprintf("%s[-]%s", colorYellow, colorReset)
				}

				countStr := fmt.Sprintf("%d skills", len(row.groupSkills))
				if len(row.groupSkills) == 1 {
					countStr = "1 skill"
				}

				line := fmt.Sprintf("%s %s %s📦 %s%s %s(%s)%s", cursorStr, box, colorBold, row.groupSource, colorReset, colorDim, countStr, colorReset)
				fmt.Printf("%s%s\r\n", clearLine, line)
			} else {
				isChecked := selectedMap[row.skillKey]
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

		instructions := fmt.Sprintf("%sUse ↑/↓ (or k/j) to navigate, Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.%s", colorDim, colorReset)
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
			for _, skKey := range allSkillKeys {
				if selectedMap[skKey] {
					chosen = append(chosen, skKey)
				}
			}
			return chosen, nil
		case keyUp:
			cursorIdx = (cursorIdx - 1 + totalRows) % totalRows
			render(false)
		case keyDown:
			cursorIdx = (cursorIdx + 1) % totalRows
			render(false)
		case keyToggleAll:
			allSel := true
			for _, skKey := range allSkillKeys {
				if !selectedMap[skKey] {
					allSel = false
					break
				}
			}
			for _, skKey := range allSkillKeys {
				selectedMap[skKey] = !allSel
			}
			render(false)
		case keySpace:
			row := rows[cursorIdx]
			if row.rType == rowGroup {
				allSel := true
				for _, skKey := range row.groupSkills {
					if !selectedMap[skKey] {
						allSel = false
						break
					}
				}
				for _, skKey := range row.groupSkills {
					selectedMap[skKey] = !allSel
				}
			} else {
				selectedMap[row.skillKey] = !selectedMap[row.skillKey]
			}
			render(false)
		}
	}
}
