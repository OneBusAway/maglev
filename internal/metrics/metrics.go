// Package metrics provides Prometheus metrics for the maglev application.
package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all Prometheus metrics for the application.
type Metrics struct {
	// Registry is the Prometheus registry for this metrics instance
	Registry *prometheus.Registry

	// HTTP metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// Database metrics
	DBConnectionsOpen  prometheus.Gauge
	DBConnectionsInUse prometheus.Gauge
	DBConnectionsIdle  prometheus.Gauge
	DBWaitSecondsTotal prometheus.Counter

	// logger for error reporting
	logger *slog.Logger

	// collectorStarted prevents spawning multiple collector goroutines
	collectorStarted atomic.Bool

	// cancel stops the DB stats collector goroutine
	cancel context.CancelFunc

	// wg tracks the DB stats collector goroutine for graceful shutdown
	wg sync.WaitGroup
}

// New creates and registers all application metrics with a new registry.
func New() *Metrics {
	return NewWithLogger(nil)
}

// NewWithLogger creates metrics with a logger for error reporting.
func NewWithLogger(logger *slog.Logger) *Metrics {
	registry := prometheus.NewRegistry()

	httpRequestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "maglev_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "maglev_http_request_duration_seconds",
			Help:    "HTTP request latency distribution",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	dbConnectionsOpen := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "maglev_db_connections_open",
		Help: "Number of open database connections",
	})

	dbConnectionsInUse := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "maglev_db_connections_in_use",
		Help: "Number of database connections currently in use",
	})

	dbConnectionsIdle := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "maglev_db_connections_idle",
		Help: "Number of idle database connections",
	})

	dbWaitSecondsTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "maglev_db_wait_seconds_total",
		Help: "Total time blocked waiting for a database connection",
	})

	// Register all metrics with the custom registry
	registry.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		dbConnectionsOpen,
		dbConnectionsInUse,
		dbConnectionsIdle,
		dbWaitSecondsTotal,
	)

	return &Metrics{
		Registry:            registry,
		HTTPRequestsTotal:   httpRequestsTotal,
		HTTPRequestDuration: httpRequestDuration,
		DBConnectionsOpen:   dbConnectionsOpen,
		DBConnectionsInUse:  dbConnectionsInUse,
		DBConnectionsIdle:   dbConnectionsIdle,
		DBWaitSecondsTotal:  dbWaitSecondsTotal,
		logger:              logger,
	}
}

// StartDBStatsCollector starts a goroutine that periodically collects database
// connection pool statistics and updates the corresponding metrics.
// The interval specifies how often to collect stats.
// This method is idempotent - calling it multiple times has no effect after the first call.
// Call Shutdown() to stop the collector.
func (m *Metrics) StartDBStatsCollector(db *sql.DB, interval time.Duration) {
	if db == nil {
		return
	}

	// Prevent spawning multiple collectors
	if !m.collectorStarted.CompareAndSwap(false, true) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	var lastWaitDuration time.Duration

	// Add to WaitGroup BEFORE exposing cancel to avoid race with Shutdown
	m.wg.Add(1)
	m.cancel = cancel

	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				if m.logger != nil {
					m.logger.Error("panic in DB stats collector", "error", r)
				}
			}
		}()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				stats := db.Stats()
				m.DBConnectionsOpen.Set(float64(stats.OpenConnections))
				m.DBConnectionsInUse.Set(float64(stats.InUse))
				m.DBConnectionsIdle.Set(float64(stats.Idle))

				// Add the delta of wait duration since last check
				waitDelta := stats.WaitDuration - lastWaitDuration
				if waitDelta > 0 {
					m.DBWaitSecondsTotal.Add(waitDelta.Seconds())
				}
				lastWaitDuration = stats.WaitDuration

			case <-ctx.Done():
				return
			}
		}
	}()
}

// Shutdown stops the DB stats collector goroutine and waits for it to exit.
// This method is safe to call multiple times.
func (m *Metrics) Shutdown() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}
