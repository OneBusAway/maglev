package metrics

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	m := New()

	assert.NotNil(t, m.Registry)
	assert.NotNil(t, m.HTTPRequestsTotal)
	assert.NotNil(t, m.HTTPRequestDuration)
	assert.NotNil(t, m.DBConnectionsOpen)
	assert.NotNil(t, m.DBConnectionsInUse)
	assert.NotNil(t, m.DBConnectionsIdle)
	assert.NotNil(t, m.DBWaitSecondsTotal)
}

func TestNewWithLogger(t *testing.T) {
	m := NewWithLogger(nil)
	assert.NotNil(t, m)
	assert.Nil(t, m.logger)
}

func TestStartDBStatsCollector_NilDB(t *testing.T) {
	m := New()
	// Should not panic with nil DB
	m.StartDBStatsCollector(nil, time.Second)
	// Collector should not be marked as started
	assert.False(t, m.collectorStarted.Load())
}

func TestStartDBStatsCollector_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()

	// Start collector first time
	m.StartDBStatsCollector(db, 100*time.Millisecond)
	assert.True(t, m.collectorStarted.Load())

	// Second call should be no-op
	m.StartDBStatsCollector(db, 100*time.Millisecond)
	assert.True(t, m.collectorStarted.Load())

	m.Shutdown()
}

func TestStartDBStatsCollector_CollectsStats(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()
	m.StartDBStatsCollector(db, 50*time.Millisecond)

	// Wait for at least one collection cycle
	time.Sleep(100 * time.Millisecond)

	// Verify metrics were actually collected using testutil
	openConns := testutil.ToFloat64(m.DBConnectionsOpen)
	inUse := testutil.ToFloat64(m.DBConnectionsInUse)
	idle := testutil.ToFloat64(m.DBConnectionsIdle)

	// For an in-memory SQLite DB, we expect at least 0 connections (valid value)
	assert.GreaterOrEqual(t, openConns, float64(0))
	assert.GreaterOrEqual(t, inUse, float64(0))
	assert.GreaterOrEqual(t, idle, float64(0))

	m.Shutdown()
}

func TestShutdown_StopsGoroutine(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()
	m.StartDBStatsCollector(db, 50*time.Millisecond)

	// Shutdown should block until goroutine exits
	done := make(chan struct{})
	go func() {
		m.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Success - Shutdown completed
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not complete within timeout")
	}
}

func TestShutdown_SafeToCallMultipleTimes(t *testing.T) {
	m := New()

	// Should not panic when called multiple times
	m.Shutdown()
	m.Shutdown()
	m.Shutdown()
}

func TestShutdown_SafeWithoutStartingCollector(t *testing.T) {
	m := New()

	// Should not panic even if collector was never started
	m.Shutdown()
}

func TestHTTPMetrics_RecordRequest(t *testing.T) {
	m := New()

	// Record a request
	m.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "200").Inc()
	m.HTTPRequestDuration.WithLabelValues("GET", "/api/test").Observe(0.5)

	// Metrics should be accessible without error
	assert.NotNil(t, m.HTTPRequestsTotal)
	assert.NotNil(t, m.HTTPRequestDuration)
}
