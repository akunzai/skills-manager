package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoticeLine(t *testing.T) {
	got := NoticeLine("0.13.0")
	want := "Self-update 0.13.0 is available. Run: skills self-update"
	if got != want {
		t.Fatalf("NoticeLine = %q; want %q", got, want)
	}
}

func TestAllowSelfUpdateNotice(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	never := time.Time{}
	fresh := now.Add(-23 * time.Hour)
	due := now.Add(-24 * time.Hour)

	tests := []struct {
		name       string
		skipEnv    bool
		terminal   bool
		jsonOutput bool
		command    string
		last       time.Time
		want       bool
	}{
		{name: "first TTY command of the day", terminal: true, last: never, want: true},
		{name: "skip env", skipEnv: true, terminal: true, last: never, want: false},
		{name: "non-TTY", terminal: false, last: never, want: false},
		{name: "json", terminal: true, jsonOutput: true, last: never, want: false},
		{name: "self-update", terminal: true, command: "self-update", last: never, want: false},
		{name: "within 24h", terminal: true, last: fresh, want: false},
		{name: "after 24h", terminal: true, last: due, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AllowSelfUpdateNotice(tt.skipEnv, tt.terminal, tt.jsonOutput, tt.command, now, tt.last)
			if got != tt.want {
				t.Fatalf("AllowSelfUpdateNotice = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestCheckStampRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills-manager", "self-update-check")
	at := time.Unix(1789800000, 0)
	if err := WriteCheckStamp(path, at); err != nil {
		t.Fatal(err)
	}
	got := ReadCheckStamp(path)
	if !got.Equal(at) {
		t.Fatalf("ReadCheckStamp = %v; want %v", got, at)
	}
}

func TestReadCheckStampMissingIsZero(t *testing.T) {
	got := ReadCheckStamp(filepath.Join(t.TempDir(), "missing"))
	if !got.IsZero() {
		t.Fatalf("missing stamp = %v; want zero", got)
	}
}

func TestSkipSelfUpdateCheckEnv(t *testing.T) {
	t.Setenv(SkipSelfUpdateCheckEnv, "1")
	if !SkipSelfUpdateCheck() {
		t.Fatal("expected skip when SKILLS_SKIP_SELF_UPDATE_CHECK is set")
	}
	t.Setenv(SkipSelfUpdateCheckEnv, "")
	if SkipSelfUpdateCheck() {
		t.Fatal("expected no skip when env is empty")
	}
}

func TestCheckStampPathHonorsXDGStateHome(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	got := CheckStampPath()
	want := filepath.Join(state, "skills-manager", "self-update-check")
	if got != want {
		t.Fatalf("CheckStampPath = %q; want %q", got, want)
	}
}

func TestReadCheckStampInvalidIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stamp")
	if err := os.WriteFile(path, []byte("not-a-time\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadCheckStamp(path)
	if !got.IsZero() {
		t.Fatalf("invalid stamp = %v; want zero", got)
	}
}
