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
	DBQueryTotal       *prometheus.CounterVec

	// GTFS-RT metrics
	FeedLastSuccessfulFetchTime *prometheus.GaugeVec
	FeedConsecutiveErrors       *prometheus.GaugeVec
	FeedFetchDuration           *prometheus.HistogramVec

	// Static GTFS metrics
	FeedExpiresAt prometheus.Gauge

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

	dbQueryTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "maglev_db_query_total",
			Help: "Total number of database queries by query name, operation, and status",
		},
		[]string{"query_name", "op", "status"},
	)

	feedLastSuccessfulFetchTime := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "maglev_feed_last_successful_fetch_time",
			Help: "Timestamp of the last successful GTFS-RT fetch for a feed",
		},
		[]string{"feed"},
	)

	feedConsecutiveErrors := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "maglev_feed_consecutive_errors",
			Help: "Number of consecutive errors fetching GTFS-RT data",
		},
		[]string{"feed"},
	)

	feedFetchDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "maglev_feed_fetch_duration_seconds",
			Help:    "Duration of GTFS-RT fetches",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"feed"},
	)

	feedExpiresAt := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "maglev_gtfs_feed_expires_at",
			Help: "Unix timestamp when the static GTFS feed expires",
		},
	)

	// Default to -1 so that it doesn't trigger alerts before actual feed expiry is loaded
	feedExpiresAt.Set(-1)

	// Register all metrics with the custom registry
	registry.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		dbConnectionsOpen,
		dbConnectionsInUse,
		dbConnectionsIdle,
		dbWaitSecondsTotal,
		dbQueryTotal,
		feedLastSuccessfulFetchTime,
		feedConsecutiveErrors,
		feedFetchDuration,
		feedExpiresAt,
	)

	return &Metrics{
		Registry:                    registry,
		HTTPRequestsTotal:           httpRequestsTotal,
		HTTPRequestDuration:         httpRequestDuration,
		DBConnectionsOpen:           dbConnectionsOpen,
		DBConnectionsInUse:          dbConnectionsInUse,
		DBConnectionsIdle:           dbConnectionsIdle,
		DBWaitSecondsTotal:          dbWaitSecondsTotal,
		DBQueryTotal:                dbQueryTotal,
		FeedLastSuccessfulFetchTime: feedLastSuccessfulFetchTime,
		FeedConsecutiveErrors:       feedConsecutiveErrors,
		FeedFetchDuration:           feedFetchDuration,
		FeedExpiresAt:               feedExpiresAt,
		logger:                      logger,
	}
}

// RecordDBQuery records per-query DB counters.
func (m *Metrics) RecordDBQuery(queryName, op string, err error) {
	if m == nil || m.DBQueryTotal == nil {
		return
	}

	if queryName == "" {
		queryName = "unknown"
	}
	if op == "" {
		op = "unknown"
	}

	status := "ok"
	if err != nil {
		status = "error"
	}

	m.DBQueryTotal.WithLabelValues(queryName, op, status).Inc()
}

// DBStatsProvider supplies database connection pool statistics.
type DBStatsProvider interface {
	Stats() sql.DBStats
}

// StartDBStatsCollector starts a goroutine that periodically collects database
// connection pool statistics and updates the corresponding metrics.
// The interval specifies how often to collect stats.
// This method is idempotent - calling it multiple times has no effect after the first call.
// Call Shutdown() to stop the collector.
func (m *Metrics) StartDBStatsCollector(provider func() DBStatsProvider, interval time.Duration) {
	if provider == nil {
		return
	}
	if interval <= 0 {
		if m.logger != nil {
			m.logger.Error("invalid DB stats collector interval", "interval", interval)
		}
		return
	}

	// Prevent spawning multiple collectors
	if !m.collectorStarted.CompareAndSwap(false, true) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	var currentProvider DBStatsProvider
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
				m.updateDBStats(provider, &currentProvider, &lastWaitDuration)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// updateDBStats samples database connection pool statistics from the provider
// and updates the corresponding Prometheus gauges and wait duration counters.
func (m *Metrics) updateDBStats(provider func() DBStatsProvider, currentProvider *DBStatsProvider, lastWaitDuration *time.Duration) {
	p := provider()
	if p == nil {
		m.DBConnectionsOpen.Set(0)
		m.DBConnectionsInUse.Set(0)
		m.DBConnectionsIdle.Set(0)
		*currentProvider = nil
		*lastWaitDuration = 0
		return
	}

	stats := p.Stats()
	m.DBConnectionsOpen.Set(float64(stats.OpenConnections))
	m.DBConnectionsInUse.Set(float64(stats.InUse))
	m.DBConnectionsIdle.Set(float64(stats.Idle))

	// WaitDuration is cumulative per connection pool. If hot-swap changed
	// the provider pointer, reset baseline to this pool's current counter.
	if p != *currentProvider {
		*currentProvider = p
		*lastWaitDuration = stats.WaitDuration
		return
	}

	// Add the delta of wait duration since last check
	waitDelta := stats.WaitDuration - *lastWaitDuration
	if waitDelta > 0 {
		m.DBWaitSecondsTotal.Add(waitDelta.Seconds())
	}
	*lastWaitDuration = stats.WaitDuration
}

// Shutdown stops the DB stats collector goroutine and waits for it to exit.
// This method is safe to call multiple times.
func (m *Metrics) Shutdown() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}
