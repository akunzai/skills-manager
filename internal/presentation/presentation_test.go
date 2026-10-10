package presentation

import (
	"bytes"
	"testing"

	"github.com/akunzai/skills-manager/internal/models"
)

func TestForDisablesPresentationForBufferedOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	style := For(&bytes.Buffer{})
	if style.Bold != "" || style.Reset != "" {
		t.Fatal("buffered output must not contain ANSI styling")
	}
	if !style.Plain {
		t.Fatal("buffered output must use plain presentation")
	}
	if style.Rule != "-" || style.Branch != "+-" || style.LastBranch != "`-" {
		t.Fatalf("plain table marks = %q %q %q", style.Rule, style.Branch, style.LastBranch)
	}
}

func TestSourceIconHasNoEmojiFallback(t *testing.T) {
	for _, tc := range []struct {
		kind        models.InventoryKind
		icon, plain string
	}{
		{models.InventoryRemote, "●", "[remote]"},
		{models.InventorySymlink, "→", "[link]"},
		{models.InventoryUntrackedLink, "→", "[link]"},
		{models.InventoryCommand, "›", "[command]"},
		{models.InventoryUntracked, "○", "[untracked]"},
	} {
		if got := (Style{}).SourceIcon(tc.kind); got != tc.icon {
			t.Errorf("icon for %d = %q, want %q", tc.kind, got, tc.icon)
		}
		if got := (Style{Plain: true}).SourceIcon(tc.kind); got != tc.plain {
			t.Errorf("plain icon for %d = %q, want %q", tc.kind, got, tc.plain)
		}
	}
}

func TestSignedMarkFallsBackToText(t *testing.T) {
	if got := (Style{Plain: true}).SignedMark(); got != "[signed]" {
		t.Fatalf("plain mark = %q, want [signed]", got)
	}
	if got := (Style{}).SignedMark(); got != "✓" {
		t.Fatalf("mark = %q, want ✓", got)
	}
}

func TestUnverifiedMarkFallsBackToText(t *testing.T) {
	if got := (Style{Plain: true}).UnverifiedMark(); got != "[unverified]" {
		t.Fatalf("plain mark = %q, want [unverified]", got)
	}
	if got := (Style{}).UnverifiedMark(); got != "!" {
		t.Fatalf("mark = %q, want !", got)
	}
}
