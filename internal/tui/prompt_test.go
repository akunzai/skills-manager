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

func TestDumbTerminalDisablesInteractivePrompt(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if IsTerminal() {
		t.Fatal("TERM=dumb must not enter the full-screen prompt")
	}
}

func TestChoiceIndex(t *testing.T) {
	if got, ok := choiceIndex("", 2, 0); !ok || got != 0 {
		t.Fatalf("default choice = %d, %v", got, ok)
	}
	if got, ok := choiceIndex("2", 2, 0); !ok || got != 1 {
		t.Fatalf("explicit choice = %d, %v", got, ok)
	}
	for _, input := range []string{"0", "3", "no"} {
		if _, ok := choiceIndex(input, 2, 0); ok {
			t.Fatalf("invalid choice %q accepted", input)
		}
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

func TestGroupedRedrawOnlyMovesWithinItsFrame(t *testing.T) {
	if got := redrawPrefix(false, 20); got != "" {
		t.Fatalf("initial redraw prefix = %q; want empty", got)
	}
	if got := redrawPrefix(true, 20); got != "\033[20A\r" {
		t.Fatalf("subsequent redraw prefix = %q", got)
	}
}

func TestPromptViewportFitsTerminal(t *testing.T) {
	width, visible, frame, ok := promptViewport(40, 8)
	if !ok || width != 39 || visible != 2 || frame != 7 {
		t.Fatalf("viewport = %d, %d, %d, %v", width, visible, frame, ok)
	}
	for _, size := range [][2]int{{19, 24}, {80, 6}} {
		if _, _, _, ok := promptViewport(size[0], size[1]); ok {
			t.Fatalf("unsafe terminal size %v accepted", size)
		}
	}
}

func TestClipLinePreventsWrapping(t *testing.T) {
	if got := clipLine("abcdefghijkl", 8); got != "abcdefgh" {
		t.Fatalf("clipped line = %q", got)
	}
	if got := clipLine("技能管理器", 8); got != "技能管理" {
		t.Fatalf("wide clipped line = %q", got)
	}
}

func TestGroupedTopIndicatorUsesOneRowWhenGroupExpandsAtTop(t *testing.T) {
	for _, isScrollable := range []bool{false, true} {
		if got := groupedTopIndicator(0, isScrollable); got != "" {
			t.Fatalf("top indicator for scrollable=%t = %q; want blank", isScrollable, got)
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
