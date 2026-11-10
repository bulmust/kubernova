package app

import (
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/rivo/tview"
)

// panicGuard runs background goroutines so that a panic stops the
// application, which restores the terminal, instead of crashing the process
// with the screen still in raw mode. The panic is returned from Run.
type panicGuard struct {
	app *tview.Application

	mu  sync.Mutex
	err error
}

// Go runs fn in a new goroutine and converts a panic into a stopped app.
func (g *panicGuard) Go(fn func()) {
	go func() {
		defer func() {
			if value := recover(); value != nil {
				g.mu.Lock()
				if g.err == nil {
					g.err = fmt.Errorf("internal error: %v\n\n%s", value, debug.Stack())
				}
				g.mu.Unlock()
				g.app.Stop()
			}
		}()
		fn()
	}()
}

// Err returns the first recovered panic, if any.
func (g *panicGuard) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}
