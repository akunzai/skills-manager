package presentation

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var progressFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
