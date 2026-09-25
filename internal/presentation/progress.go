package presentation

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

var progressFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	regionInterval = 200 * time.Millisecond
	barWidth       = 12
	clearRegion    = "\r\033[J"
)

// Job is one unit of work a Region shows while it runs. Name identifies it and
// is what a finished job prints without a terminal; Label, when set, replaces
// Name on its row.
type Job struct {
	Name  string
	Label string
	Phase string
}

type runningJob struct {
	Job
	started time.Time
}

type regionOptions struct {
	live     bool
	color    bool
	width    func() int
	now      func() time.Time
	interval time.Duration // one spinner frame, and one redraw when ticking
	ticking  bool          // redraw from a background goroutine
}

// Region is the live progress of one slow operation: a header with how many
// of its jobs have finished, and a row per job still running, redrawn in
// place. Without a live terminal it draws nothing and prints one "ok" line per
// job that succeeds. Failures are the caller's to word, through Above.
//
// A nil *Region is valid and shows nothing, so callers need not guard.
type Region struct {
	mu      sync.Mutex
	w       io.Writer
	opts    regionOptions
	style   Style
	title   string
	total   int
	done    int
	started time.Time
	jobs    []runningJob
	drawn   int // lines currently on screen
	last    string
	stopped bool
	halt    chan struct{}
	wg      sync.WaitGroup
}

// StartRegion shows progress for total jobs on w, under title. A title of ""
// draws rows only; a total of 0 leaves out the bar and the count.
func StartRegion(w io.Writer, title string, total int) *Region {
	tty := isTerminal(w)
	opts := regionOptionsFor(tty)
	opts.width = func() int { return terminalWidth(w) }
	opts.now = time.Now
	opts.interval = regionInterval
	opts.ticking = true
	return newRegion(w, title, total, opts)
}

func regionOptionsFor(tty bool) regionOptions {
	live := animated(tty)
	return regionOptions{live: live, color: live && colorful(tty)}
}

func newRegion(w io.Writer, title string, total int, opts regionOptions) *Region {
	r := &Region{w: w, opts: opts, title: sanitize(title), total: total, started: opts.now()}
	if opts.color {
		r.style = withColor(Style{})
	}
	if !opts.live {
		return r
	}
	r.redraw()
	if opts.ticking && opts.interval > 0 {
		r.halt = make(chan struct{})
		r.wg.Go(func() {
			ticker := time.NewTicker(opts.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					r.redraw()
				case <-r.halt:
					return
				}
			}
		})
	}
	return r
}

// Start adds a running job's row.
func (r *Region) Start(job Job) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = append(r.jobs, runningJob{Job: job, started: r.opts.now()})
}

// SetPhase changes what a running job's row says it is doing.
func (r *Region) SetPhase(name, phase string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := r.index(name); i >= 0 {
		r.jobs[i].Phase = phase
	}
}

// Done finishes a job that succeeded. Its row goes away; without a live
// terminal it prints "ok  <name>".
func (r *Region) Done(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finish(name)
	if !r.opts.live {
		fmt.Fprintf(r.w, "ok  %s\n", sanitize(name))
	}
}

// DoneWith finishes a job that succeeded with a permanent line of the
// caller's own, which print writes in place of "ok  <name>", above the region.
// print must not call the Region.
func (r *Region) DoneWith(name string, print func()) {
	if r == nil {
		print()
		return
	}
	r.mu.Lock()
	r.finish(name)
	r.mu.Unlock()
	r.Above(print)
}

// Fail finishes a job that failed or was blocked. Its row goes away and
// nothing is printed: the caller prints the reason through Above.
func (r *Region) Fail(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finish(name)
}

// Above runs print, which writes permanent lines, with the region cleared, and
// then draws the region again below them. print must not call the Region.
func (r *Region) Above(print func()) {
	if r == nil {
		print()
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opts.live || r.stopped {
		print()
		return
	}
	r.clear()
	print()
	r.draw()
}

// Stop clears the region for good. It is safe to call more than once.
func (r *Region) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	// Clear while still holding the lock, so a line printed through Above
	// after this point is never erased. The goroutine sees stopped and draws
	// nothing more.
	r.stopped = true
	if r.opts.live {
		r.clear()
	}
	r.mu.Unlock()
	if r.halt != nil {
		close(r.halt)
		r.wg.Wait()
	}
}

func (r *Region) index(name string) int {
	return slices.IndexFunc(r.jobs, func(job runningJob) bool { return job.Name == name })
}

func (r *Region) finish(name string) {
	if i := r.index(name); i >= 0 {
		r.jobs = slices.Delete(r.jobs, i, i+1)
	}
	r.done++
}

// redraw draws the current frame over the previous one, unless nothing
// visible has changed.
func (r *Region) redraw() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opts.live || r.stopped {
		return
	}
	if r.render() == r.last {
		return
	}
	r.clear()
	r.draw()
}

func (r *Region) clear() {
	if r.drawn > 0 {
		fmt.Fprintf(r.w, "\033[%dA%s", r.drawn, clearRegion)
	}
	r.drawn = 0
	r.last = ""
}

func (r *Region) draw() {
	frame := r.render()
	io.WriteString(r.w, frame)
	r.drawn = strings.Count(frame, "\n")
	r.last = frame
}

func (r *Region) render() string {
	now := r.opts.now()
	s := r.style
	frame := 0
	if r.opts.interval > 0 {
		frame = int(now.Sub(r.started)/r.opts.interval) % len(progressFrames)
	}
	spinner := s.Dim + progressFrames[frame] + s.Reset
	width := 0
	if r.opts.width != nil {
		width = r.opts.width()
	}
	var lines []string
	indent := ""
	if r.title != "" {
		header := s.Bold + s.Cyan + r.title + s.Reset + "  "
		if r.total > 0 {
			filled := min(r.done*barWidth/r.total, barWidth)
			bar := s.Green + strings.Repeat("█", filled) + s.Reset + s.Dim + strings.Repeat("░", barWidth-filled) + s.Reset
			header += fmt.Sprintf("%s  %d/%d · %s", bar, r.done, r.total, elapsed(now.Sub(r.started)))
		} else {
			header += elapsed(now.Sub(r.started)) + " " + spinner
		}
		lines = append(lines, header)
		indent = "  "
	}
	for _, job := range r.jobs {
		row := indent + sanitize(job.Name)
		if job.Label != "" {
			row = indent + sanitize(job.Label)
		}
		if job.Phase != "" {
			row += "  " + sanitize(job.Phase)
		}
		row += "  " + elapsed(now.Sub(job.started)) + " " + spinner
		lines = append(lines, row)
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(truncate(line, width-1))
		b.WriteByte('\n')
	}
	return b.String()
}

func elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// sanitize replaces every control character with a space. Job text can come
// from a repository's directory names, and an escape or line break in it would
// restyle the terminal or break the cursor arithmetic of the next redraw.
func sanitize(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

// truncate cuts line to at most max display columns, so a row never wraps and
// the cursor arithmetic of the next redraw stays right. Escape sequences take
// no columns; a cut styled line ends with a reset.
func truncate(line string, max int) string {
	if max <= 0 || runewidth.StringWidth(stripANSI(line)) <= max {
		return line
	}
	var b strings.Builder
	columns := 0
	for i := 0; i < len(line); {
		if n := escapeLen(line[i:]); n > 0 {
			b.WriteString(line[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		width := runewidth.RuneWidth(r)
		if columns+width > max {
			break
		}
		b.WriteString(line[i : i+size])
		i += size
		columns += width
	}
	if strings.Contains(line, "\033[") {
		b.WriteString("\033[0m")
	}
	return b.String()
}

// escapeLen is the length of the CSI sequence s starts with, 0 when it starts
// with none. An unterminated sequence runs to the end of s.
func escapeLen(s string) int {
	if !strings.HasPrefix(s, "\033[") {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func terminalWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil {
			return width
		}
	}
	return 80
}

// Progress renders one transient operation on an interactive writer. Plain
// writers remain silent and receive no terminal control sequences.
type Progress struct {
	w           io.Writer
	interactive bool
	done        chan struct{}
	wg          sync.WaitGroup
	once        sync.Once
}

func StartProgress(w io.Writer, message string) *Progress {
	return startProgress(w, message, !For(w).Plain)
}

func startProgress(w io.Writer, message string, interactive bool) *Progress {
	p := &Progress{w: w, interactive: interactive}
	if !interactive {
		return p
	}

	p.done = make(chan struct{})
	fmt.Fprintf(w, "\r%s %s", progressFrames[0], message)
	p.wg.Go(func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		frame := 1
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(w, "\r%s %s", progressFrames[frame], message)
				frame = (frame + 1) % len(progressFrames)
			case <-p.done:
				return
			}
		}
	})
	return p
}

func (p *Progress) Stop() {
	if p == nil || !p.interactive {
		return
	}
	p.once.Do(func() {
		close(p.done)
		p.wg.Wait()
		fmt.Fprint(p.w, "\r\033[K")
	})
}
