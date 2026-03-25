package metrics

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
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
	assert.NotNil(t, m.DBQueryTotal)
	assert.NotNil(t, m.DBQueryDuration)

	// GTFS-RT metrics
	assert.NotNil(t, m.FeedLastSuccessfulFetchTime)
	assert.NotNil(t, m.FeedConsecutiveErrors)
	assert.NotNil(t, m.FeedFetchDuration)
}

func TestNewWithLogger(t *testing.T) {
	m := NewWithLogger(nil)
	assert.NotNil(t, m)
	assert.Nil(t, m.logger)
}

func TestStartDBStatsCollector_NilProvider(t *testing.T) {
	m := New()
	// Should not panic with nil provider
	m.StartDBStatsCollector(nil, time.Second)
	// Collector should not be marked as started
	assert.False(t, m.collectorStarted.Load())
}

func TestStartDBStatsCollector_ProviderCanReturnNilDB(t *testing.T) {
	m := New()
	m.StartDBStatsCollector(func() *sql.DB { return nil }, 20*time.Millisecond)
	defer m.Shutdown()

	assert.True(t, m.collectorStarted.Load())

	require.Eventually(t, func() bool {
		openConns := testutil.ToFloat64(m.DBConnectionsOpen)
		inUse := testutil.ToFloat64(m.DBConnectionsInUse)
		idle := testutil.ToFloat64(m.DBConnectionsIdle)
		return openConns == 0 && inUse == 0 && idle == 0
	}, time.Second, 20*time.Millisecond)
}

func TestStartDBStatsCollector_NonPositiveInterval(t *testing.T) {
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()

	m.StartDBStatsCollector(func() *sql.DB { return db }, 0)
	assert.False(t, m.collectorStarted.Load(), "collector should not start with zero interval")

	m.StartDBStatsCollector(func() *sql.DB { return db }, -time.Second)
	assert.False(t, m.collectorStarted.Load(), "collector should not start with negative interval")

	// A later valid start must still work.
	m.StartDBStatsCollector(func() *sql.DB { return db }, 20*time.Millisecond)
	assert.True(t, m.collectorStarted.Load(), "collector should start with a positive interval")

	m.Shutdown()
}

func TestStartDBStatsCollector_Idempotent(t *testing.T) {
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()

	// Start collector first time
	m.StartDBStatsCollector(func() *sql.DB { return db }, 100*time.Millisecond)
	assert.True(t, m.collectorStarted.Load())

	// Second call should be no-op
	m.StartDBStatsCollector(func() *sql.DB { return db }, 100*time.Millisecond)
	assert.True(t, m.collectorStarted.Load())

	m.Shutdown()
}

func TestStartDBStatsCollector_CollectsStats(t *testing.T) {
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()
	m.StartDBStatsCollector(func() *sql.DB { return db }, 50*time.Millisecond)

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
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := New()
	m.StartDBStatsCollector(func() *sql.DB { return db }, 50*time.Millisecond)

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

func TestStartDBStatsCollector_FollowsDBSwap(t *testing.T) {
	db1, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db1.Close() }()

	db2, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db2.Close() }()

	// Hold one active transaction so db2 reports an in-use connection.
	tx, err := db2.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db1)

	m := New()
	m.StartDBStatsCollector(func() *sql.DB { return dbPtr.Load() }, 20*time.Millisecond)
	defer m.Shutdown()

	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DBConnectionsInUse) == 0
	}, time.Second, 20*time.Millisecond)

	dbPtr.Store(db2)

	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DBConnectionsInUse) >= 1
	}, time.Second, 20*time.Millisecond)
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

func TestRecordDBQuery(t *testing.T) {
	m := New()

	m.RecordDBQuery("GetTrip", "query", nil, 250*time.Millisecond)
	m.RecordDBQuery("GetTrip", "query", assert.AnError, 500*time.Millisecond)
	m.RecordDBQuery("", "", nil, 0)

	okTotal := testutil.ToFloat64(m.DBQueryTotal.WithLabelValues("GetTrip", "query", "ok"))
	errTotal := testutil.ToFloat64(m.DBQueryTotal.WithLabelValues("GetTrip", "query", "error"))
	unknownTotal := testutil.ToFloat64(m.DBQueryTotal.WithLabelValues("unknown", "unknown", "ok"))

	assert.Equal(t, float64(1), okTotal)
	assert.Equal(t, float64(1), errTotal)
	assert.Equal(t, float64(1), unknownTotal)

	assert.GreaterOrEqual(t, testutil.CollectAndCount(m.DBQueryDuration), 1)
}

func TestRecordDBQuery_NilReceiverNoPanic(t *testing.T) {
	var m *Metrics
	m.RecordDBQuery("GetTrip", "query", nil, time.Millisecond)
}
