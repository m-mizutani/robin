package async

import (
	"context"
	"sync"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/utils/errutil"
)

// inflight counts running handlers. idle is closed while the count is zero and
// replaced when it rises again. A sync.WaitGroup cannot be used: Drain may
// stop waiting at its deadline, and a WaitGroup whose Wait is still pending
// panics when a new handler is added.
var (
	mu       sync.Mutex
	inflight int
	idle     = closedChannel()
)

func closedChannel() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func begin() {
	mu.Lock()
	defer mu.Unlock()
	if inflight == 0 {
		idle = make(chan struct{})
	}
	inflight++
}

func end() {
	mu.Lock()
	defer mu.Unlock()
	inflight--
	if inflight == 0 {
		close(idle)
	}
}

func idleChannel() <-chan struct{} {
	mu.Lock()
	defer mu.Unlock()
	return idle
}

// Dispatch runs handler in a new goroutine. The context keeps every value of
// the caller's ctx but not its cancellation, because the caller (an HTTP
// request that already answered Slack) returns before the handler finishes.
// Handler errors and panics are recorded through errutil.Handle.
func Dispatch(ctx context.Context, handler func(ctx context.Context) error) {
	bgCtx := context.WithoutCancel(ctx)

	begin()
	go func() {
		defer end()
		run(bgCtx, handler)
	}()
}

// run calls handler and records its error or panic.
func run(ctx context.Context, handler func(ctx context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			errutil.Handle(ctx, goerr.New("panic in async handler", goerr.V("panic", r)), "async handler panicked")
		}
	}()

	if err := handler(ctx); err != nil {
		errutil.Handle(ctx, err, "async handler failed")
	}
}

// Drain waits until no handler started by Dispatch is running, or until ctx
// is done. The server calls it on shutdown, after it stopped accepting
// requests and before it closes the clients the handlers use.
func Drain(ctx context.Context) error {
	select {
	case <-idleChannel():
		return nil
	case <-ctx.Done():
		return goerr.Wrap(ctx.Err(), "background handlers did not finish before the deadline")
	}
}

// Wait blocks until no handler started by Dispatch is running. It exists for
// tests that assert on side effects of the background work; production code
// uses Drain.
func Wait() {
	<-idleChannel()
}
