package async_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/utils/async"
)

func TestGroup_WaitsForItsOwnHandlersOnly(t *testing.T) {
	// A handler started by Dispatch that does not finish until the end of
	// the test.
	releaseOther := make(chan struct{})
	async.Dispatch(context.Background(), func(_ context.Context) error {
		<-releaseOther
		return nil
	})
	t.Cleanup(func() {
		close(releaseOther)
		async.Wait()
	})

	var g async.Group
	var finished atomic.Int32
	for i := 0; i < 3; i++ {
		g.Go(context.Background(), func(_ context.Context) error {
			time.Sleep(10 * time.Millisecond)
			finished.Add(1)
			return nil
		})
	}

	done := make(chan struct{})
	go func() {
		g.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Group.Wait waited for a handler of Dispatch")
	}
	gt.Number(t, finished.Load()).Equal(3)
}

func TestGroup_DrainWaitsForGroupHandlers(t *testing.T) {
	release := make(chan struct{})
	var g async.Group
	g.Go(context.Background(), func(_ context.Context) error {
		<-release
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	gt.Error(t, async.Drain(ctx)).Is(context.DeadlineExceeded)

	close(release)
	g.Wait()
	gt.NoError(t, async.Drain(context.Background()))
}

func TestGroup_RecordsErrorsAndPanics(t *testing.T) {
	ctx, buf := capturingCtx()
	var g async.Group
	g.Go(ctx, func(_ context.Context) error {
		return goerr.New("synthetic group failure")
	})
	g.Go(ctx, func(_ context.Context) error {
		panic("group boom")
	})
	g.Wait()

	gt.String(t, buf.String()).Contains("synthetic group failure")
	gt.String(t, buf.String()).Contains("async handler panicked")
	gt.String(t, buf.String()).Contains("group boom")
}

type ctxKey struct{}

func TestGroup_CallerCancellationDoesNotCancelHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "kept"))
	cancel()

	var notCancelled, value atomic.Value
	var g async.Group
	g.Go(ctx, func(ctx context.Context) error {
		notCancelled.Store(ctx.Err() == nil)
		value.Store(ctx.Value(ctxKey{}))
		return nil
	})
	g.Wait()
	gt.Value(t, notCancelled.Load()).Equal(true)
	gt.Value(t, value.Load()).Equal("kept")
}
