package tui

// viewportWindow recomputes a scrollable window over a list of length total,
// given the previous windowStart, so cursorIdx stays visible. Safe to call on
// every render, including after total has shrunk since the previous call
// (e.g. a grouped prompt collapsing a group) — a stale windowStart is
// reclamped rather than left pointing past the end of the list.
func viewportWindow(cursorIdx, windowStart, total, maxVisible int) (newWindowStart, visibleCount, windowEnd int, isScrollable bool) {
	visibleCount = min(total, maxVisible)
	isScrollable = total > maxVisible
	if isScrollable {
		if cursorIdx < windowStart {
			windowStart = cursorIdx
		} else if cursorIdx >= windowStart+visibleCount {
			windowStart = cursorIdx - visibleCount + 1
		}
	}
	if windowStart >= total {
		windowStart = total - visibleCount
	}
	windowEnd = min(windowStart+visibleCount, total)
	return windowStart, visibleCount, windowEnd, isScrollable
}

// viewportMoveUp moves the cursor up by one row, wrapping to the last row.
func viewportMoveUp(cursorIdx, total int) int {
	return (cursorIdx - 1 + total) % total
}

// viewportMoveDown moves the cursor down by one row, wrapping to the first row.
func viewportMoveDown(cursorIdx, total int) int {
	return (cursorIdx + 1) % total
}

// viewportPageDown jumps the cursor forward by a full page, clamped to the last row.
func viewportPageDown(cursorIdx, visibleCount, total int) int {
	return min(cursorIdx+visibleCount-1, total-1)
}

// viewportPageUp jumps the cursor backward by a full page, clamped to the first row.
func viewportPageUp(cursorIdx, visibleCount int) int {
	return max(cursorIdx-visibleCount+1, 0)
}
