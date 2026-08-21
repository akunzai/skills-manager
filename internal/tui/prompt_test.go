package tui

import (
	"testing"
)

func TestSelectOptionData(t *testing.T) {
	opt := SelectOption{
		Key:       "my-skill",
		Title:     "my-skill",
		Extra:     "(v1.0)",
		Installed: true,
		Selected:  true,
	}

	if opt.Key != "my-skill" || !opt.Installed || !opt.Selected {
		t.Errorf("unexpected option values: %+v", opt)
	}
}

func TestGroupedItemsStructure(t *testing.T) {
	groups := GroupedItems{
		"owner/repo": {
			{Key: "skill-1", Title: "skill-1", Selected: true},
			{Key: "skill-2", Title: "skill-2", Selected: false},
		},
		"owner/other-repo": {
			{Key: "skill-3", Title: "skill-3", Selected: true},
		},
	}

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["owner/repo"]) != 2 {
		t.Fatalf("expected 2 items in owner/repo, got %d", len(groups["owner/repo"]))
	}
}

func TestParseKeyRecognizesPageNavigation(t *testing.T) {
	if got := parseKey([]byte{6}); got != keyPageDown {
		t.Fatalf("Ctrl+f key = %v; want page down", got)
	}
	if got := parseKey([]byte{2}); got != keyPageUp {
		t.Fatalf("Ctrl+b key = %v; want page up", got)
	}
}

func TestParseKeyRecognizesGroupCollapseNavigation(t *testing.T) {
	if got := parseKey([]byte{27, '[', 'D'}); got != keyLeft {
		t.Fatalf("left arrow key = %v; want key left", got)
	}
	if got := parseKey([]byte{27, '[', 'C'}); got != keyRight {
		t.Fatalf("right arrow key = %v; want key right", got)
	}
}

func TestPromptRedrawStartsAtFixedScreenOrigin(t *testing.T) {
	const want = "\033[H\033[2J"

	if got := promptRedraw; got != want {
		t.Fatalf("prompt redraw sequence = %q; want %q", got, want)
	}
}

func TestGroupedTopIndicatorUsesOneRowWhenGroupExpandsAtTop(t *testing.T) {
	for _, isScrollable := range []bool{false, true} {
		if got := groupedTopIndicator(0, isScrollable); got != clearLine+"\r\n" {
			t.Fatalf("top indicator for scrollable=%t = %q; want one blank row", isScrollable, got)
		}
	}
}

func TestMultiSelectInstructionsUseTwoTopLines(t *testing.T) {
	got := multiSelectInstructions()
	if len(got) != 2 {
		t.Fatalf("instruction line count = %d; want 2", len(got))
	}
	if got[0] != "Use ↑/↓ (or k/j) to navigate, Ctrl+f/b to page." {
		t.Fatalf("first instruction = %q", got[0])
	}
	if got[1] != "Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel." {
		t.Fatalf("second instruction = %q", got[1])
	}
}
