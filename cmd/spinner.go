package cmd

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

type spinner struct {
	message atomic.Pointer[string]
	done    chan struct{}
	exited  chan struct{}
	stopped atomic.Bool
	tty     bool
}

const spinnerFrames = `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`

func startSpinner(initial string) *spinner {
	sp := &spinner{done: make(chan struct{}), exited: make(chan struct{})}
	sp.message.Store(&initial)
	sp.tty = term.IsTerminal(int(os.Stdout.Fd()))

	if !sp.tty {
		fmt.Println(initial + "...")
		close(sp.exited)
		return sp
	}

	go sp.run()
	return sp
}

func (s *spinner) SetMessage(msg string) {
	s.message.Store(&msg)
}

func (s *spinner) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}
	if !s.tty {
		return
	}
	close(s.done)
	<-s.exited // wait for goroutine to finish writing
	fmt.Print("\r\033[K")
}

func (s *spinner) run() {
	defer close(s.exited)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	frames := []rune(spinnerFrames)
	i := 0
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			msg := ""
			if p := s.message.Load(); p != nil {
				msg = *p
			}
			fmt.Printf("\r\033[K%s %s", string(frames[i]), msg)
			i = (i + 1) % len(frames)
		}
	}
}
