package async

import (
	"context"
	"sync"
)

// Group runs handlers in new goroutines, as Dispatch does, and lets the
// caller wait for those handlers only. Its handlers also count for Drain, so
// a server shutdown waits for them.
type Group struct {
	wg sync.WaitGroup
}

// Go runs handler with ctx's values but not its cancellation. Errors and
// panics are recorded through errutil.Handle. Go must not be called after
// Wait has started.
func (g *Group) Go(ctx context.Context, handler func(ctx context.Context) error) {
	bgCtx := context.WithoutCancel(ctx)

	begin()
	g.wg.Add(1)
	go func() {
		defer end()
		defer g.wg.Done()
		run(bgCtx, handler)
	}()
}

// Wait blocks until every handler started by g has returned.
func (g *Group) Wait() {
	g.wg.Wait()
}
