package gtfs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
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

// The first call's failure has to survive. shutdownOnce means later calls skip
// the work, so an error held in a per-call local was reported once and then
// silently became nil for every caller after it.
func TestShutdownRepeatsTheFirstError(t *testing.T) {
	manager := &Manager{shutdownChan: make(chan struct{})}

	blocked := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		<-blocked
	}()
	t.Cleanup(func() { close(blocked) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	first := manager.Shutdown(ctx)
	if first == nil {
		t.Fatal("first Shutdown returned nil, want a context error")
	}

	second := manager.Shutdown(context.Background())
	if second == nil {
		t.Fatal("second Shutdown returned nil, want the first call's error")
	}
	if !errors.Is(second, context.DeadlineExceeded) {
		t.Errorf("second Shutdown error = %v, want it to wrap context.DeadlineExceeded", second)
	}
	if first.Error() != second.Error() {
		t.Errorf("second Shutdown error = %q, want the same as the first %q", second, first)
	}
}

// A worker still running when Shutdown times out keeps a usable database, and
// the database closes once that worker exits.
func TestShutdownClosesDatabaseAfterWorkersExit(t *testing.T) {
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	manager := &Manager{shutdownChan: make(chan struct{}), GtfsDB: client}

	release := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		<-release
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := manager.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want it to wrap context.DeadlineExceeded", err)
	}

	for end := time.Now().Add(100 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if err := client.DB.PingContext(context.Background()); err != nil {
			t.Fatalf("database closed while a worker was still running: %v", err)
		}
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for {
		pingErr := client.DB.PingContext(context.Background())
		if pingErr != nil && strings.Contains(pingErr.Error(), "database is closed") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("database still open after the worker exited, ping error = %v", pingErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
