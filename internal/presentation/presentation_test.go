package presentation

import (
	"bytes"
	"testing"
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
	style := Style{Plain: true}
	if got := style.SourceIcon("github"); got != "[remote]" {
		t.Fatalf("icon = %q, want [remote]", got)
	}
	style.Plain = false
	if got := style.SourceIcon("symlink"); got != "→" {
		t.Fatalf("icon = %q, want arrow", got)
	}
}
