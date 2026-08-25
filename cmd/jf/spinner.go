package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// spinnerFrames is the animation that `jf login` shows while it waits.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner shows one line that turns while a command waits.
//
// It writes to a terminal only. On a pipe or a file the spinner prints the
// message once and then stays silent, because a redraw needs a carriage return
// that a log file would keep as noise.
type spinner struct {
	out     io.Writer
	style   *theme
	message string

	mutex   sync.Mutex
	stop    chan struct{}
	stopped bool
	running bool
}

// newSpinner returns a spinner for one writer.
func newSpinner(out io.Writer, style *theme, message string) *spinner {
	return &spinner{
		out:     out,
		style:   style,
		message: message,
		stop:    make(chan struct{}),
	}
}

// Start begins the animation, or prints the message once when the output is not
// a terminal.
func (item *spinner) Start() {
	if !item.style.Enabled || !isTerminal(item.out) {
		fmt.Fprintln(item.out, item.message)
		return
	}

	item.mutex.Lock()
	item.running = true
	item.mutex.Unlock()

	go func() {
		frame := 0
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-item.stop:
				return
			case <-ticker.C:
				item.mutex.Lock()
				if !item.stopped {
					fmt.Fprintf(item.out, "\r%s %s",
						item.style.Accent.Render(spinnerFrames[frame%len(spinnerFrames)]),
						item.message)
				}
				item.mutex.Unlock()
				frame++
			}
		}
	}()
}

// Stop ends the animation and clears its line.
//
// Clearing matters: the line that follows the spinner is the real answer, and a
// leftover frame above it reads as a step that never finished.
func (item *spinner) Stop() {
	item.mutex.Lock()
	defer item.mutex.Unlock()
	if item.stopped {
		return
	}
	item.stopped = true
	if item.running {
		close(item.stop)
		// The blanks cover the longest line the spinner drew, and the second
		// carriage return puts the cursor back at the start for the next write.
		fmt.Fprintf(item.out, "\r%s\r", spaces(len(item.message)+2))
	}
}

func spaces(count int) string {
	if count < 0 {
		return ""
	}
	blanks := make([]byte, count)
	for index := range blanks {
		blanks[index] = ' '
	}
	return string(blanks)
}
