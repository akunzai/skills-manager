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
