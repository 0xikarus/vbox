package cli

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// progress reports an operation immediately and then emits periodic heartbeats
// until the returned function is called. It intentionally writes to stderr so
// JSON and table output on stdout remain script-friendly.
func (a *App) progress(ctx context.Context, label string) func() {
	started := time.Now()
	fmt.Fprintf(a.Err, "vmbox: %s...\n", label)
	interval := a.ProgressInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(a.Err, "vmbox: still %s (%s elapsed)\n", label, elapsedLabel(time.Since(started)))
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}
