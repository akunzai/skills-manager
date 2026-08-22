package tui

import (
	"fmt"
	"io"
	"os"

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
	s := l.style

	render := func() {
		fmt.Fprint(l.out, redrawPrefix(rendered, l.frameLines))
		total := l.total()
		var windowEnd int
		windowStart, visibleCount, windowEnd, isScrollable = viewportWindow(cursorIdx, windowStart, total, l.maxVisible)
		instructions := l.instructions(isScrollable)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", s.Bold, s.Cyan, clipLine(l.title, l.contentWidth), s.Reset)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", clearLine, s.Dim, clipLine(instructions[0], l.contentWidth), s.Reset)
		fmt.Fprintf(l.out, "%s%s%s%s\r\n", clearLine, s.Dim, clipLine(instructions[1], l.contentWidth), s.Reset)
		fmt.Fprintf(l.out, "%s%s\r\n", clearLine, clipLine(l.extraHeader(windowStart, isScrollable), l.contentWidth))
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
	for {
		newCursor, result, err := l.handle(l.readKey(), cursorIdx, visibleCount, isScrollable)
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
