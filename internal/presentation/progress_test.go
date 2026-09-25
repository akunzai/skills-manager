package presentation

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func liveRegion(out *bytes.Buffer, clock *fakeClock, title string, total, width int) *Region {
	return newRegion(out, title, total, regionOptions{live: true, width: func() int { return width }, now: clock.now, interval: 200 * time.Millisecond})
}

// lastFrame is what the terminal shows after the most recent redraw: the
// text written after the last clear of the region.
func lastFrame(out string) string {
	if i := strings.LastIndex(out, "\033[J"); i >= 0 {
		return out[i+len("\033[J"):]
	}
	return out
}

func TestRegionDrawsHeaderWithBarAndOneRowPerRunningJob(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Refreshing 5 Sources", 5, 80)
	r.Start(Job{Name: "owner/a", Phase: "fetching"})
	r.Start(Job{Name: "owner/b", Phase: "fetching"})
	r.Done("owner/a")
	clock.advance(3 * time.Second)
	r.redraw()

	want := "Refreshing 5 Sources  ██░░░░░░░░░░  1/5 · 3.0s\n" +
		"  owner/b  fetching  3.0s ⠴\n"
	if got := lastFrame(out.String()); got != want {
		t.Fatalf("frame = %q\nwant    %q", got, want)
	}
}

func TestRegionRedrawsOverThePreviousFrame(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Syncing 2 Skills", 2, 80)
	r.Start(Job{Name: "alpha", Phase: "linking"})
	r.redraw()
	out.Reset()
	clock.advance(time.Second)
	r.redraw()

	if !strings.HasPrefix(out.String(), "\033[2A\r\033[J") {
		t.Fatalf("redraw did not move up over the two drawn lines: %q", out.String())
	}
}

func TestRegionSkipsAnUnchangedFrame(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Syncing 1 Skill", 1, 80)
	r.Start(Job{Name: "alpha"})
	r.redraw()
	before := out.Len()
	r.redraw()

	if out.Len() != before {
		t.Fatalf("unchanged frame was written again: %q", out.String()[before:])
	}
}

func TestRegionTruncatesLinesToTheTerminalWidth(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := newRegion(&out, "Adding 1 Skill", 1, regionOptions{live: true, color: true, width: func() int { return 20 }, now: clock.now, interval: 200 * time.Millisecond})
	r.Start(Job{Name: "a-very-long-skill-name-that-would-wrap", Phase: "materializing"})
	r.redraw()

	frame := lastFrame(out.String())
	if strings.Count(frame, "\n") != 2 {
		t.Fatalf("frame is not two lines: %q", frame)
	}
	for line := range strings.Lines(frame) {
		visible := stripANSI(strings.TrimSuffix(line, "\n"))
		if n := runewidth.StringWidth(visible); n >= 20 {
			t.Fatalf("line %q is %d columns wide; want under 20", visible, n)
		}
		if strings.Contains(line, "\033[") && !strings.HasSuffix(strings.TrimSuffix(line, "\n"), "\033[0m") {
			t.Fatalf("truncated styled line %q does not reset its style", line)
		}
	}
}

func TestRegionPrintsAPermanentLineAboveItself(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Syncing 2 Skills", 2, 80)
	r.Start(Job{Name: "alpha", Phase: "materializing"})
	r.redraw()
	out.Reset()
	r.Fail("alpha")
	r.Above(func() { out.WriteString("  Failed to copy alpha: denied\n") })

	want := "\033[2A\r\033[J" +
		"  Failed to copy alpha: denied\n" +
		"Syncing 2 Skills  ██████░░░░░░  1/2 · 0.0s\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q\nwant     %q", got, want)
	}
}

func TestRegionDropsTheRowOfAFinishedJob(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Syncing 2 Skills", 2, 80)
	r.Start(Job{Name: "alpha"})
	r.Start(Job{Name: "beta"})
	r.redraw()
	r.Done("alpha")
	r.redraw()

	frame := lastFrame(out.String())
	if strings.Contains(frame, "alpha") || !strings.Contains(frame, "beta") {
		t.Fatalf("frame after alpha finished = %q", frame)
	}
	if strings.Contains(out.String(), "ok") {
		t.Fatalf("a terminal region printed a permanent success line: %q", out.String())
	}
}

func TestRegionStopClearsItselfCompletely(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Syncing 1 Skill", 1, 80)
	r.Start(Job{Name: "alpha"})
	r.redraw()
	r.Stop()
	r.Stop()

	if !strings.HasSuffix(out.String(), "\033[2A\r\033[J") {
		t.Fatalf("output did not end by clearing the region: %q", out.String())
	}
	out.Reset()
	r.redraw()
	r.Above(func() { out.WriteString("after\n") })
	if got := out.String(); got != "after\n" {
		t.Fatalf("a stopped region still drew: %q", got)
	}
}

func TestRegionHeaderWithoutATotalShowsOnlyElapsedTime(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Checking 3 Sources", 0, 80)
	clock.advance(1200 * time.Millisecond)
	r.redraw()

	if got, want := lastFrame(out.String()), "Checking 3 Sources  1.2s ⠦\n"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestRegionWithoutATitleShowsOnlyItsRows(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "", 0, 80)
	r.Start(Job{Name: "owner/repo", Label: "Fetching owner/repo"})
	clock.advance(500 * time.Millisecond)
	r.redraw()

	if got, want := lastFrame(out.String()), "Fetching owner/repo  0.5s ⠹\n"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestRegionStylesHeaderAndSpinnerWhenColored(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := newRegion(&out, "Syncing 1 Skill", 1, regionOptions{live: true, color: true, width: func() int { return 80 }, now: clock.now, interval: 200 * time.Millisecond})
	r.Start(Job{Name: "alpha"})
	r.redraw()

	frame := lastFrame(out.String())
	for _, want := range []string{"\033[1m\033[96mSyncing 1 Skill\033[0m", "\033[2m⠋\033[0m"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame %q does not contain %q", frame, want)
		}
	}
}

func TestRegionWithoutATerminalPrintsOnlyOneLinePerFinishedJob(t *testing.T) {
	var out bytes.Buffer
	r := StartRegion(&out, "Syncing 2 Skills", 2)
	r.Start(Job{Name: "alpha", Phase: "linking"})
	r.SetPhase("alpha", "running installer")
	r.Done("alpha")
	r.Start(Job{Name: "beta"})
	r.Fail("beta")
	r.Above(func() { out.WriteString("  Failed to copy beta: denied\n") })
	r.Stop()

	if got, want := out.String(), "ok  alpha\n  Failed to copy beta: denied\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNilRegionStillPrintsPermanentLines(t *testing.T) {
	var r *Region
	printed := false
	r.Start(Job{Name: "alpha"})
	r.Done("alpha")
	r.Above(func() { printed = true })
	r.Stop()
	if !printed {
		t.Fatal("a nil region dropped a permanent line")
	}
}

func TestLiveProgressNeedsAnInteractiveTerminalOutsideCI(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CI", "")
	if !animated(true) {
		t.Fatal("an interactive terminal should animate")
	}
	if animated(false) {
		t.Fatal("a non-terminal writer must not animate")
	}
	t.Setenv("CI", "true")
	if animated(true) {
		t.Fatal("CI must not animate, even on a terminal")
	}
	t.Setenv("CI", "")
	t.Setenv("TERM", "dumb")
	if animated(true) {
		t.Fatal("TERM=dumb must not animate")
	}
}

func TestRegionHonorsNoColor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CI", "")
	t.Setenv("NO_COLOR", "1")
	if opts := regionOptionsFor(true); !opts.live || opts.color {
		t.Fatalf("options = %+v; want a live region without colour", opts)
	}
	t.Setenv("NO_COLOR", "")
	if opts := regionOptionsFor(true); !opts.live || !opts.color {
		t.Fatalf("options = %+v; want a live, coloured region", opts)
	}
	t.Setenv("CI", "1")
	if opts := regionOptionsFor(true); opts.live || opts.color {
		t.Fatalf("options = %+v; want CI to print plain lines", opts)
	}
}

func TestRegionTruncatesWideCharactersByDisplayWidth(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "", 0, 12)
	r.Start(Job{Name: "技能名稱很長很長"})
	r.redraw()

	line := strings.TrimSuffix(lastFrame(out.String()), "\n")
	if w := runewidth.StringWidth(stripANSI(line)); w >= 12 {
		t.Fatalf("row %q is %d columns wide; want under 12 so it cannot wrap", line, w)
	}
	if line != "技能名稱很" {
		t.Fatalf("row = %q, want it cut before the column limit", line)
	}
}

func TestTruncateSurvivesMalformedEscapes(t *testing.T) {
	for _, line := range []string{
		"name \033[",             // unterminated at the end
		"long enough text \033[", // unterminated past the limit
		"name \033[12;3",         // unterminated with parameters
		"a\033[Kb long enough",   // a final byte other than m
	} {
		done := make(chan string, 1)
		go func() { done <- truncate(line, 6) }()
		select {
		case got := <-done:
			if w := runewidth.StringWidth(stripANSI(got)); w > 6 {
				t.Fatalf("truncate(%q) = %q, %d columns wide", line, got, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("truncate(%q) did not return", line)
		}
	}
	if got := stripANSI("a\033[Kb"); got != "ab" {
		t.Fatalf("stripANSI dropped the wrong bytes: %q", got)
	}
}

// Job and header text can come from a repository's directory names, so none
// of its control characters may reach the terminal.
func TestRegionNeverWritesControlCharactersFromJobText(t *testing.T) {
	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Adding\033[2J 1 Skill", 1, 80)
	r.Start(Job{Name: "evil", Label: "Installing ev\033[il\n\rx\u009b31m (from \033[)", Phase: "ph\ase"})
	r.redraw()
	r.Stop()

	got := out.String()
	for _, bad := range []string{"\033[2J", "\033[il", "\n\r", "\rx", "\u009b", "\a"} {
		if strings.Contains(got, bad) {
			t.Fatalf("output carries %q from job text: %q", bad, got)
		}
	}

	var plain bytes.Buffer
	p := newRegion(&plain, "", 0, regionOptions{now: clock.now})
	p.Start(Job{Name: "ev\033[2Jil\n"})
	p.Done("ev\033[2Jil\n")
	if got, want := plain.String(), "ok  ev [2Jil \n"; got != want {
		t.Fatalf("plain output = %q, want %q", got, want)
	}
}

func TestRegionDoneWithPrintsTheCallersLineInsteadOfOk(t *testing.T) {
	var plain bytes.Buffer
	p := StartRegion(&plain, "Refreshing 1 Source", 1)
	p.Start(Job{Name: "owner/repo"})
	p.DoneWith("owner/repo", func() { plain.WriteString("Updated owner/repo.\n") })
	p.Stop()
	if got, want := plain.String(), "Updated owner/repo.\n"; got != want {
		t.Fatalf("plain output = %q, want %q", got, want)
	}

	var out bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := liveRegion(&out, clock, "Refreshing 2 Sources", 2, 80)
	r.Start(Job{Name: "owner/a"})
	r.redraw()
	out.Reset()
	r.DoneWith("owner/a", func() { out.WriteString("Updated owner/a.\n") })

	want := "\033[2A\r\033[J" +
		"Updated owner/a.\n" +
		"Refreshing 2 Sources  ██████░░░░░░  1/2 · 0.0s\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q\nwant     %q", got, want)
	}
}
