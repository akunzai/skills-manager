package presentation

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressPlainWriterIsSilent(t *testing.T) {
	var out bytes.Buffer
	p := startProgress(&out, "Fetching Source...", false)
	p.Stop()

	if got := out.String(); got != "" {
		t.Fatalf("output = %q, want no transient progress", got)
	}
}

func TestProgressInteractiveWriterClearsAnimation(t *testing.T) {
	var out bytes.Buffer
	p := startProgress(&out, "Fetching Source...", true)
	p.Stop()

	got := out.String()
	if !strings.HasPrefix(got, "\r⠋ Fetching Source...") {
		t.Fatalf("output did not start with first frame: %q", got)
	}
	if !strings.HasSuffix(got, "\r\033[K") {
		t.Fatalf("output did not clear final frame: %q", got)
	}
}
