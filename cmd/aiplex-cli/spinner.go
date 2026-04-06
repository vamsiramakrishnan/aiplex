package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	msg    string
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
}

// startSpinner begins an animated spinner with a message.
func startSpinner(msg string) *spinner {
	s := &spinner{
		msg:  msg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Printf("  %s...\n", msg)
		close(s.done)
		return s
	}

	go func() {
		defer close(s.done)
		i := 0
		for {
			select {
			case <-s.stop:
				return
			default:
				s.mu.Lock()
				fmt.Printf("\r  %s %s...", spinnerFrames[i%len(spinnerFrames)], s.msg)
				s.mu.Unlock()
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return s
}

func (s *spinner) finish(msg string) {
	select {
	case <-s.stop:
		return
	default:
	}
	close(s.stop)
	<-s.done
	fmt.Printf("\r  [pass] %s\n", msg)
}

func (s *spinner) fail(msg string) {
	select {
	case <-s.stop:
		return
	default:
	}
	close(s.stop)
	<-s.done
	fmt.Printf("\r  [FAIL] %s\n", msg)
}
