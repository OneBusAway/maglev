package gtfs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A worker that never returns must not be able to block Shutdown forever.
// Before the context was honoured, wg.Wait() held the caller indefinitely.
func TestShutdownReturnsWhenContextExpires(t *testing.T) {
	manager := &Manager{shutdownChan: make(chan struct{})}

	blocked := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		<-blocked // never closed until the assertions are done
	}()
	t.Cleanup(func() { close(blocked) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := manager.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Shutdown returned nil, want a context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("Shutdown took %v, want it bounded by the 50ms context", elapsed)
	}
}

func TestShutdownReturnsNilWhenWorkersFinish(t *testing.T) {
	manager := &Manager{shutdownChan: make(chan struct{})}

	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		<-manager.shutdownChan
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown returned %v, want nil when workers exit in time", err)
	}
}

// shutdownOnce means only the first call does the work; later calls must
// still be safe to make.
func TestShutdownIsIdempotent(t *testing.T) {
	manager := &Manager{shutdownChan: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown returned %v, want nil", err)
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown returned %v, want nil", err)
	}
}
