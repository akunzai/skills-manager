package tui

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/akunzai/skills-manager/internal/presentation"
	"golang.org/x/term"
)

type loopResult int

const (
	loopContinue loopResult = iota
	loopRedraw
	loopDone
)

type listKeyHandler func(k keyType, cursor, visible int, scrollable bool) (newCursor int, result loopResult, err error)

type promptSize struct {
	width, maxVisible, frame int
	ok                       bool
	session                  *listSession
}

func sizeFromViewport(width, maxVisible, frame int, ok bool) promptSize {
	return promptSize{width: width, maxVisible: maxVisible, frame: frame, ok: ok}
}

type listLoop struct {
	title        string
	style        presentation.Style
	out          io.Writer
	readKey      func() keyType
	contentWidth int
	maxVisible   int
	frameLines   int
	cursorIdx    int
	session      *listSession
	click        func(row, column int) keyType
	total        func() int
	instructions func(scrollable bool) [2]string
	extraHeader  func(windowStart int, scrollable bool) string
	rowLine      func(i, cursorIdx, width int) string
	footer       func(windowStart, windowEnd, total int, scrollable bool) string
	handle       listKeyHandler
}

func selectFooter(windowStart, windowEnd, total int, _ bool) string {
	remaining := total - windowEnd
	if remaining > 0 {
		return fmt.Sprintf("  ▼ (%d more below)", remaining)
	}
	if windowStart > 0 {
		return fmt.Sprintf("  ▲ (%d more above)", windowStart)
	}
	return ""
}

func groupedFooter(_, windowEnd, total int, scrollable bool) string {
	if !scrollable {
		return ""
	}
	remaining := total - windowEnd
	if remaining > 0 {
		return fmt.Sprintf("  ▼ (%d more below)", remaining)
	}
	return ""
}

func blankHeader(_ int, _ bool) string { return "" }

func (l listLoop) run() error {
	cursorIdx := l.cursorIdx
	windowStart := 0
	visibleCount := 0
	isScrollable := false
	rendered := false
	suspended := false
	s := l.style
	if l.session != nil && l.session.mouse {
		fmt.Fprint(l.out, "\x1b[?1000h\x1b[?1006h")
		defer fmt.Fprint(l.out, "\x1b[?1000l\x1b[?1006l")
	}

	render := func() {
		fmt.Fprint(l.out, redrawPrefix(rendered, l.frameLines))
		total := l.total()
		var windowEnd int
		windowStart, visibleCount, windowEnd, isScrollable = viewportWindow(cursorIdx, windowStart, total, l.maxVisible)
		instructions := l.instructions(isScrollable)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", s.Bold, s.Cyan, clipLine(l.title, l.contentWidth), s.Reset)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", clearLine, s.Dim, clipLine(instructions[0], l.contentWidth), s.Reset)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", clearLine, s.Dim, clipLine(instructions[1], l.contentWidth), s.Reset)
		header := l.extraHeader(windowStart, isScrollable)
		if l.session != nil && l.session.origin > 0 {
			header = "Click a row; scroll the wheel. " + header
		}
		fmt.Fprintf(l.out, "%s%s\r\n", clearLine, clipLine(header, l.contentWidth))
		for i := windowStart; i < windowEnd; i++ {
			fmt.Fprintf(l.out, "%s%s\r\n", clearLine, clipLine(l.rowLine(i, cursorIdx, l.contentWidth), l.contentWidth))
		}
		for i := visibleCount; i < l.maxVisible; i++ {
			fmt.Fprintf(l.out, "%s\r\n", clearLine)
		}
		fmt.Fprintf(l.out, "%s%s\r\n", clearLine, clipLine(l.footer(windowStart, windowEnd, total, isScrollable), l.contentWidth))
		rendered = true
	}

	fmt.Fprint(l.out, hideCursor)
	render()
	if l.session != nil {
		l.session.query()
	}
	for {
		k := keyUnknown
		if l.session == nil {
			k = l.readKey()
		} else {
			e := l.session.next()
			if e.err != nil {
				return e.err
			}
			width, height, sizeErr := term.GetSize(int(os.Stdout.Fd()))
			if sizeErr == nil && (width != l.session.width || height != l.session.height) {
				l.session.width, l.session.height = width, height
				l.session.origin = 0
				l.session.queryUntil = time.Time{}
				size := sizeFromViewport(promptViewport(width, height))
				// Reflow can move the old frame. Start afresh rather than erasing
				// unrelated output using an obsolete relative cursor anchor.
				rendered = false
				fmt.Fprint(l.out, "\r\n")
				suspended = !size.ok
				if suspended {
					fmt.Fprintf(l.out, "%s\r\n", clipLine("Enlarge the terminal to continue; Esc to cancel.", max(width-1, 0)))
				} else {
					l.contentWidth, l.maxVisible, l.frameLines = size.width, size.maxVisible, size.frame
					render()
					l.session.query()
				}
				if e.mouse || e.position {
					continue
				}
			}
			if suspended {
				if e.key != keyEscape && e.key != keyInterrupt {
					continue
				}
			}
			if l.session.origin == 0 && !l.session.queryUntil.IsZero() && time.Now().After(l.session.queryUntil) {
				fmt.Fprint(l.out, "\x1b[?1000l\x1b[?1006l")
				l.session.mouse = false
				l.session.queryUntil = time.Time{}
			}
			if e.position {
				if l.session.origin == 0 && time.Now().Before(l.session.queryUntil) && e.x == 1 && e.y > l.frameLines && e.y <= l.session.height {
					l.session.origin = e.y - l.frameLines
					l.session.queryUntil = time.Time{}
					render()
				}
				continue
			}
			if e.mouse {
				row := e.y - l.session.origin - 4
				if l.session.origin == 0 || e.x < 1 || e.x > l.contentWidth || row < 0 || row >= visibleCount {
					continue
				}
				switch e.button {
				case 0:
					cursorIdx = windowStart + row
					if l.click != nil {
						k = l.click(cursorIdx, e.x)
					}
					render()
				case 64, 65:
					delta := 3
					if e.button == 64 {
						delta = -3
					}
					windowStart = max(0, min(windowStart+delta, l.total()-visibleCount))
					cursorIdx = max(windowStart, min(cursorIdx, windowStart+visibleCount-1))
					render()
					continue
				default:
					continue
				}
			} else {
				k = e.key
			}
		}
		newCursor, result, err := l.handle(k, cursorIdx, visibleCount, isScrollable)
		cursorIdx = newCursor
		switch result {
		case loopDone:
			fmt.Fprint(l.out, "\r\n")
			return err
		case loopRedraw:
			render()
		}
	}
}

func rawListSession(run func() error) error {
	fd := int(os.Stdin.Fd())
	restoreOutput, err := enableVTOutput()
	if err != nil {
		return err
	}
	defer restoreOutput()
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Fprint(os.Stdout, showCursor)
	}()
	return run()
}
