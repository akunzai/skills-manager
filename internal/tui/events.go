package tui

import (
	"fmt"
	"golang.org/x/term"
	"os"
	"time"
)

// ListOptions applies to one prompt; keyboard interaction is always available.
type ListOptions struct{ NoMouse bool }

type inputEvent struct {
	key             keyType
	button, x, y    int
	mouse, position bool
	err             error
}

type listSession struct {
	mouse         bool
	pending       []byte
	escapeAt      time.Time
	origin        int
	queryUntil    time.Time
	width, height int
}

func (s *listSession) next() inputEvent {
	for {
		if len(s.pending) > 0 {
			b := s.pending
			if b[0] != 27 {
				s.pending = b[1:]
				return inputEvent{key: parseKey(b[:1])}
			}
			if len(b) > 1 && b[1] != '[' && b[1] != 'O' {
				s.pending = b[1:]
				return inputEvent{key: keyEscape}
			}
			if len(b) > 2 {
				for i := 2; i < len(b); i++ {
					if b[i] >= 0x40 && b[i] <= 0x7e {
						sequence := b[:i+1]
						s.pending = b[i+1:]
						s.escapeAt = time.Time{}
						e := inputEvent{key: parseKey(sequence)}
						if sequence[len(sequence)-1] == 'R' {
							_, err := fmt.Sscanf(string(sequence), "\x1b[%d;%dR", &e.y, &e.x)
							e.position = err == nil
						}
						if sequence[len(sequence)-1] == 'M' {
							_, err := fmt.Sscanf(string(sequence), "\x1b[<%d;%d;%dM", &e.button, &e.x, &e.y)
							e.mouse = err == nil
						}
						return e
					}
				}
			}
			if s.escapeAt.IsZero() {
				s.escapeAt = time.Now()
			}
			if time.Since(s.escapeAt) > 80*time.Millisecond {
				s.pending = nil
				s.escapeAt = time.Time{}
				if len(b) == 1 {
					return inputEvent{key: keyEscape}
				}
				return inputEvent{}
			}
		}
		b, err := readInput(25 * time.Millisecond)
		if err != nil {
			return inputEvent{err: err}
		}
		if len(b) == 0 {
			return inputEvent{}
		}
		s.pending = append(s.pending, b...)
	}
}

func sessionSize(s *listSession) promptSize {
	s.width, s.height, _ = term.GetSize(int(os.Stdout.Fd()))
	size := sizeFromViewport(terminalPromptViewport())
	size.session = s
	return size
}

func newListSession(options []ListOptions) *listSession {
	mouse := len(options) == 0 || !options[0].NoMouse
	return &listSession{mouse: mouse && term.IsTerminal(int(os.Stdout.Fd()))}
}

func (s *listSession) query() {
	s.origin = 0
	s.queryUntil = time.Time{}
	if s.mouse {
		s.queryUntil = time.Now().Add(250 * time.Millisecond)
		fmt.Fprint(os.Stdout, "\x1b[6n")
	}
}
