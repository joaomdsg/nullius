package scout

import (
	"context"
	"testing"
)

// The fast-tier semaphore bounds how many scout subprocesses run at once.
// A nil Sem is unbounded; a buffered Sem caps concurrency at its capacity
// and a release frees exactly one slot.
func TestSemaphoreCap(t *testing.T) {
	s := &Tool{Sem: make(chan struct{}, 2)}
	ctx := context.Background()

	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	// Two slots taken → a third acquire must not be immediately available.
	select {
	case s.Sem <- struct{}{}:
		t.Fatal("acquired a 3rd slot past the cap of 2")
	default:
	}
	// Freeing one slot must make exactly one available again.
	s.release()
	select {
	case s.Sem <- struct{}{}:
		<-s.Sem // undo the probe
	default:
		t.Fatal("slot not freed after release")
	}
}

func TestSemaphoreNilUnbounded(t *testing.T) {
	s := &Tool{} // no Sem
	if err := s.acquire(context.Background()); err != nil {
		t.Fatalf("nil-Sem acquire should never block/err: %v", err)
	}
	s.release() // must not panic
}

// A cancelled context must abort a blocked acquire rather than hang.
func TestSemaphoreAcquireCancel(t *testing.T) {
	s := &Tool{Sem: make(chan struct{}, 1)}
	_ = s.acquire(context.Background()) // fill the one slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.acquire(ctx); err == nil {
		t.Fatal("acquire on a full sem with a cancelled ctx must return ctx.Err()")
	}
}
