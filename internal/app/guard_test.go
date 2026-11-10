package app

import (
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
)

func TestPanicGuardRecordsPanic(t *testing.T) {
	guard := &panicGuard{app: tview.NewApplication()}
	done := make(chan struct{})
	guard.Go(func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not run")
	}
	deadline := time.Now().Add(time.Second)
	for guard.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := guard.Err(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Err() = %v, want recorded panic", err)
	}
}
