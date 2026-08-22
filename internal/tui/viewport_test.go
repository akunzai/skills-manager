package tui

import "testing"

func TestViewportWindowFitsWithoutScrolling(t *testing.T) {
	windowStart, visibleCount, windowEnd, isScrollable := viewportWindow(2, 0, 5, 10)
	if windowStart != 0 || visibleCount != 5 || windowEnd != 5 || isScrollable {
		t.Fatalf("got (%d, %d, %d, %v); want (0, 5, 5, false)", windowStart, visibleCount, windowEnd, isScrollable)
	}
}

func TestViewportWindowScrollsForwardPastVisibleEnd(t *testing.T) {
	// total=10, maxVisible=3, windowStart=0: moving the cursor to 4 (past
	// windowStart+visibleCount-1=2) must slide the window forward.
	windowStart, visibleCount, windowEnd, isScrollable := viewportWindow(4, 0, 10, 3)
	if !isScrollable || visibleCount != 3 {
		t.Fatalf("visibleCount=%d isScrollable=%v; want 3, true", visibleCount, isScrollable)
	}
	if windowStart != 2 || windowEnd != 5 {
		t.Fatalf("window = [%d,%d); want [2,5)", windowStart, windowEnd)
	}
}

func TestViewportWindowScrollsBackwardBeforeStart(t *testing.T) {
	// windowStart=5 but the cursor moved to 2, above the window: it must jump up.
	windowStart, _, windowEnd, _ := viewportWindow(2, 5, 10, 3)
	if windowStart != 2 || windowEnd != 5 {
		t.Fatalf("window = [%d,%d); want [2,5)", windowStart, windowEnd)
	}
}

func TestViewportWindowReclampsWhenTotalShrinks(t *testing.T) {
	// A group collapsed since the last render: total dropped to 4 while
	// windowStart was still 5, which would point past the list's end. Since
	// everything now fits (total <= maxVisible), the cursor-follow branch
	// never runs, so only the reclamp guards against the stale windowStart.
	windowStart, visibleCount, windowEnd, isScrollable := viewportWindow(2, 5, 4, 10)
	if isScrollable {
		t.Fatalf("isScrollable = true; want false for total(4) <= maxVisible(10)")
	}
	if visibleCount != 4 {
		t.Fatalf("visibleCount = %d; want 4", visibleCount)
	}
	if windowStart != 0 || windowEnd != 4 {
		t.Fatalf("window = [%d,%d); want [0,4) after reclamping a stale windowStart", windowStart, windowEnd)
	}
}

func TestViewportMoveUpAndDownWrap(t *testing.T) {
	if got := viewportMoveUp(0, 5); got != 4 {
		t.Fatalf("viewportMoveUp(0,5) = %d; want 4", got)
	}
	if got := viewportMoveUp(3, 5); got != 2 {
		t.Fatalf("viewportMoveUp(3,5) = %d; want 2", got)
	}
	if got := viewportMoveDown(4, 5); got != 0 {
		t.Fatalf("viewportMoveDown(4,5) = %d; want 0", got)
	}
	if got := viewportMoveDown(1, 5); got != 2 {
		t.Fatalf("viewportMoveDown(1,5) = %d; want 2", got)
	}
}

func TestViewportPageDownAndUpClamp(t *testing.T) {
	if got := viewportPageDown(0, 3, 10); got != 2 {
		t.Fatalf("viewportPageDown(0,3,10) = %d; want 2", got)
	}
	if got := viewportPageDown(8, 3, 10); got != 9 {
		t.Fatalf("viewportPageDown(8,3,10) = %d; want 9 (clamped to last row)", got)
	}
	if got := viewportPageUp(5, 3); got != 3 {
		t.Fatalf("viewportPageUp(5,3) = %d; want 3", got)
	}
	if got := viewportPageUp(1, 3); got != 0 {
		t.Fatalf("viewportPageUp(1,3) = %d; want 0 (clamped to first row)", got)
	}
}
