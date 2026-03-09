package gtfsdb

// The TriMet dataset is used when testdata/perf/trimet.zip is present;
// the RABA dataset is used otherwise.
// Download TriMet data: bash scripts/download-perf-data.sh
//
// Run latency tests:
//
//	make test-latency
//
// Run benchmarks:
//
//	make bench-sqlite-all
//
// Run large-dataset benchmarks:
//
//	make bench-sqlite-perftest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// loadLatencyFixture opens a file-based SQLite client for latency tests,
// using the TriMet dataset if available, otherwise RABA. A file DB enables
// WAL mode and allows the pool to reach MaxOpenConns=25.
func loadLatencyFixture(tb testing.TB) (*Client, string, string) {
	tb.Helper()

	trimetZip, _ := filepath.Abs(filepath.Join("..", "testdata", "perf", "trimet.zip"))
	rabaZip, _ := filepath.Abs(filepath.Join("..", "testdata", "raba.zip"))

	var zipPath, dbPath string
	switch {
	case latencyFileExists(trimetZip):
		zipPath = trimetZip
		dbPath, _ = filepath.Abs(filepath.Join("..", "testdata", "perf", "trimet-perf.db"))
	case latencyFileExists(rabaZip):
		zipPath = rabaZip
		tmpDir := filepath.Join(os.TempDir(), "maglev_query_latency")
		if mkErr := os.MkdirAll(tmpDir, 0755); mkErr != nil {
			tb.Fatalf("cannot create tmp dir: %v", mkErr)
		}
		dbPath = filepath.Join(tmpDir, "raba_latency.db")
	default:
		tb.Skip("no GTFS dataset found – run scripts/download-perf-data.sh or ensure testdata/raba.zip exists")
	}

	cfg := Config{
		DBPath: dbPath,
		Env:    appconf.Development,
	}

	client, clientErr := NewClient(cfg)
	if clientErr != nil {
		tb.Fatalf("NewClient: %v", clientErr)
	}

	ctx := context.Background()

	// GTFS import is skipped when the stops table is non-empty.
	if latencyIsEmpty(ctx, client.DB) {
		data, readErr := os.ReadFile(zipPath)
		if readErr != nil {
			tb.Fatalf("reading %s: %v", filepath.Base(zipPath), readErr)
		}
		if impErr := client.processAndStoreGTFSDataWithSource(data, zipPath); impErr != nil {
			tb.Fatalf("importing GTFS data from %s: %v", filepath.Base(zipPath), impErr)
		}
	}

	stopID := latencyPickFirstStop(ctx, tb, client.DB)
	tripID := latencyPickFirstTrip(ctx, tb, client.DB)
	return client, stopID, tripID
}

func latencyFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func latencyIsEmpty(ctx context.Context, db *sql.DB) bool {
	var n int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM stops").Scan(&n)
	return n == 0
}

func latencyPickFirstStop(ctx context.Context, tb testing.TB, db *sql.DB) string {
	tb.Helper()
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT DISTINCT st.stop_id
		FROM stop_times st
		JOIN trips t ON st.trip_id = t.id
		LIMIT 1`).Scan(&id)
	if err != nil {
		tb.Fatalf("pick stop: %v", err)
	}
	return id
}

func latencyPickFirstTrip(ctx context.Context, tb testing.TB, db *sql.DB) string {
	tb.Helper()
	var id string
	if err := db.QueryRowContext(ctx, "SELECT id FROM trips LIMIT 1").Scan(&id); err != nil {
		tb.Fatalf("pick trip: %v", err)
	}
	return id
}

func latencyFetchRouteIDsForStop(ctx context.Context, tb testing.TB, q *Queries, stopID string) []string {
	tb.Helper()
	routes, err := q.GetRoutesForStop(ctx, stopID)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(routes))
	for _, r := range routes {
		ids = append(ids, r.ID)
	}
	return ids
}

func latencyFetchActiveServiceIDs(ctx context.Context, q *Queries, dateStr string) []string {
	// Use the production query: includes weekday filtering and calendar_dates
	// exception/addition handling — identical to what the API layer calls.
	ids, err := q.GetActiveServiceIDsForDate(ctx, dateStr)
	if err != nil {
		return nil
	}
	return ids
}

func latencyIsWALEnabled(ctx context.Context, t *testing.T, db *sql.DB) bool {
	t.Helper()
	var mode string
	_ = db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	return mode == "wal"
}

// Latency distribution helper
type queryLatencyStat struct {
	name    string
	samples []time.Duration
}

func (s *queryLatencyStat) report(t *testing.T) {
	t.Helper()
	if len(s.samples) == 0 {
		t.Logf("  %s: no samples", s.name)
		return
	}
	sorted := make([]time.Duration, len(s.samples))
	copy(sorted, s.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := func(pct float64) int {
		i := int(math.Round(float64(len(sorted))*pct)) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(sorted) {
			i = len(sorted) - 1
		}
		return i
	}

	p50 := sorted[idx(0.50)]
	p95 := sorted[idx(0.95)]
	p99 := sorted[idx(0.99)]
	maxD := sorted[len(sorted)-1]

	t.Logf("  %-55s  p50=%-9s p95=%-9s p99=%-9s max=%s",
		s.name,
		p50.Round(time.Microsecond),
		p95.Round(time.Microsecond),
		p99.Round(time.Microsecond),
		maxD.Round(time.Microsecond))
}

// TestQueryLatencyUnderConcurrentLoad runs each hot query 200 times across
// 25 concurrent goroutines and reports the p50 / p95 / p99 latency distribution.
func TestQueryLatencyUnderConcurrentLoad(t *testing.T) {
	client, stopID, tripID := loadLatencyFixture(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	const (
		concurrency = 25  // matches MaxOpenConns for file DBs
		iterations  = 200 // per goroutine
	)

	now := time.Now()
	windowStart := int64(5 * time.Hour) // 05:00 as nanoseconds-since-midnight
	windowEnd := int64(23 * time.Hour)  // 23:00 as nanoseconds-since-midnight
	dateStr := now.Format("20060102")
	weekday := strings.ToLower(now.Weekday().String())

	routeIDs := latencyFetchRouteIDsForStop(ctx, t, client.Queries, stopID)
	svcIDs := latencyFetchActiveServiceIDs(ctx, client.Queries, dateStr)

	type querySpec struct {
		name string
		fn   func() error
	}
	querySpecs := []querySpec{
		{
			name: "GetStopTimesForStopInWindow (arrivals inner loop)",
			fn: func() error {
				_, err := client.Queries.GetStopTimesForStopInWindow(ctx, GetStopTimesForStopInWindowParams{
					StopID:           stopID,
					WindowStartNanos: windowStart,
					WindowEndNanos:   windowEnd,
				})
				return err
			},
		},
		{
			name: "GetScheduleForStopOnDate (schedule-for-stop)",
			fn: func() error {
				if len(routeIDs) == 0 {
					return nil
				}
				_, err := client.Queries.GetScheduleForStopOnDate(ctx, GetScheduleForStopOnDateParams{
					StopID:     stopID,
					TargetDate: dateStr,
					Weekday:    weekday,
					RouteIds:   routeIDs,
				})
				return err
			},
		},
		{
			name: "GetStopTimesForTrip (trip-details path)",
			fn: func() error {
				_, err := client.Queries.GetStopTimesForTrip(ctx, tripID)
				return err
			},
		},
		{
			name: "GetActiveRouteIDsForStopsOnDate (stops-for-location batch)",
			fn: func() error {
				if len(svcIDs) == 0 {
					return nil
				}
				_, err := client.Queries.GetActiveRouteIDsForStopsOnDate(ctx, GetActiveRouteIDsForStopsOnDateParams{
					StopIds:    []string{stopID},
					ServiceIds: svcIDs,
				})
				return err
			},
		},
		{
			name: "SearchStopsByName FTS5 (search endpoint)",
			fn: func() error {
				_, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
					SearchQuery: "st*",
					Limit:       20,
				})
				return err
			},
		},
	}

	t.Log("=== Query Latency Distribution (p50 / p95 / p99) ===")
	t.Logf("  concurrency=%d  iterations-per-goroutine=%d  WAL=%v",
		concurrency, iterations, latencyIsWALEnabled(ctx, t, client.DB))

	for _, q := range querySpecs {
		stat := &queryLatencyStat{name: q.name}
		var mu sync.Mutex
		var wg sync.WaitGroup
		errCh := make(chan error, concurrency)

		for g := 0; g < concurrency; g++ {
			wg.Add(1)
			go func(fn func() error) {
				defer wg.Done()
				local := make([]time.Duration, 0, iterations)
				for i := 0; i < iterations; i++ {
					start := time.Now()
					if err := fn(); err != nil {
						errCh <- fmt.Errorf("%s: %w", q.name, err)
						return
					}
					local = append(local, time.Since(start))
				}
				mu.Lock()
				stat.samples = append(stat.samples, local...)
				mu.Unlock()
			}(q.fn)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("query error: %v", err)
		}
		stat.report(t)
	}

	// Report connection-pool stats collected during the load.
	stats := client.DB.Stats()
	t.Log("=== Connection Pool Stats (after load) ===")
	t.Logf("  MaxOpenConnections=%d  OpenConnections=%d  InUse=%d  Idle=%d",
		stats.MaxOpenConnections, stats.OpenConnections, stats.InUse, stats.Idle)
	t.Logf("  WaitCount=%d  WaitDuration=%s  MaxIdleClosed=%d  MaxLifetimeClosed=%d",
		stats.WaitCount, stats.WaitDuration.Round(time.Millisecond),
		stats.MaxIdleClosed, stats.MaxLifetimeClosed)

	if stats.WaitCount > 0 {
		avgWait := time.Duration(int64(stats.WaitDuration) / stats.WaitCount)
		t.Logf("  AvgWaitPerRequest=%s", avgWait.Round(time.Microsecond))
		if avgWait > 5*time.Millisecond {
			t.Logf("  RECOMMENDATION: avg pool wait (%.1fms) > 5ms; "+
				"consider increasing MaxOpenConns beyond %d",
				float64(avgWait)/float64(time.Millisecond), stats.MaxOpenConnections)
		}
	} else {
		t.Log("  No pool contention observed (WaitCount=0)")
	}
}

// TestExplainQueryPlans executes EXPLAIN QUERY PLAN for each hot query and
// logs the output. Query plan rows that indicate a full-table scan without
// an index are flagged with a warning.
func TestExplainQueryPlans(t *testing.T) {
	client, stopID, tripID := loadLatencyFixture(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	now := time.Now()
	dateStr := now.Format("20060102")
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)

	plans := []struct {
		name string
		sql  string
		args []interface{}
	}{
		{
			name: "GetStopTimesForStopInWindow",
			sql: `EXPLAIN QUERY PLAN
SELECT st.trip_id, st.arrival_time, st.departure_time,
       st.stop_id, st.stop_sequence, st.stop_headsign,
       t.route_id, t.service_id, t.trip_headsign, t.block_id
FROM stop_times st
JOIN trips t ON st.trip_id = t.id
WHERE st.stop_id = ?
  AND (
      (st.arrival_time   BETWEEN ? AND ?)
   OR (st.departure_time BETWEEN ? AND ?)
  )
ORDER BY st.arrival_time`,
			args: []interface{}{stopID, windowStart, windowEnd, windowStart, windowEnd},
		},
		{
			name: "GetScheduleForStopOnDate",
			sql: `EXPLAIN QUERY PLAN
SELECT st.trip_id, st.arrival_time, st.departure_time
FROM stop_times st
JOIN trips t ON st.trip_id = t.id
JOIN routes r ON t.route_id = r.id
WHERE st.stop_id = ?
ORDER BY r.id, st.arrival_time`,
			args: []interface{}{stopID},
		},
		{
			name: "GetStopTimesForTrip",
			sql: `EXPLAIN QUERY PLAN
SELECT * FROM stop_times WHERE trip_id = ? ORDER BY stop_sequence`,
			args: []interface{}{tripID},
		},
		{
			name: "GetActiveRouteIDsForStopsOnDate (stops-for-location batch)",
			sql: `EXPLAIN QUERY PLAN
SELECT DISTINCT routes.agency_id || '_' || routes.id AS route_id,
                stop_times.stop_id
FROM stop_times
JOIN trips  ON stop_times.trip_id  = trips.id
JOIN routes ON trips.route_id      = routes.id
WHERE stop_times.stop_id = ?`,
			args: []interface{}{stopID},
		},
		{
			name: "GetActiveServiceIDsForDate (calendar CTE)",
			sql: `EXPLAIN QUERY PLAN
WITH formatted_date AS (
  SELECT STRFTIME('%w',
    SUBSTR(?1,1,4) || '-' || SUBSTR(?1,5,2) || '-' || SUBSTR(?1,7,2)) AS weekday
),
base_services AS (
  SELECT c.id AS service_id
  FROM calendar c, formatted_date fd
  WHERE c.start_date <= ?1 AND c.end_date >= ?1
)
SELECT DISTINCT service_id FROM base_services`,
			args: []interface{}{dateStr},
		},
	}

	t.Log("=== EXPLAIN QUERY PLAN Results ===")
	for _, p := range plans {
		t.Logf("--- %s ---", p.name)
		rows, err := client.DB.QueryContext(ctx, p.sql, p.args...)
		if err != nil {
			t.Errorf("EXPLAIN failed for %s: %v", p.name, err)
			continue
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if scanErr := rows.Scan(ptrs...); scanErr != nil {
				t.Errorf("scan: %v", scanErr)
				break
			}
			t.Logf("    %v", vals)

			// Warn on a full-table scan without any index.
			if len(vals) > 0 {
				detail := fmt.Sprintf("%v", vals[len(vals)-1])
				if strings.Contains(strings.ToUpper(detail), "SCAN") &&
					!strings.Contains(strings.ToUpper(detail), "USING INDEX") &&
					!strings.Contains(strings.ToUpper(detail), "COVERING INDEX") {
					t.Logf("    ⚠ potential full-table scan detected for %s: %s",
						p.name, detail)
				}
			}
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows for %s: %v", p.name, closeErr)
		}
		t.Log("")
	}

	// List all user-defined indexes from sqlite_master.
	t.Log("=== Existing Indexes ===")
	idxRows, err := client.DB.QueryContext(ctx, `
		SELECT tbl_name, name, sql
		FROM sqlite_master
		WHERE type = 'index'
		  AND name NOT LIKE 'sqlite_autoindex_%'
		ORDER BY tbl_name, name`)
	if err == nil {
		defer func() { _ = idxRows.Close() }()
		for idxRows.Next() {
			var tbl, name string
			var idxSQL sql.NullString
			if scanErr := idxRows.Scan(&tbl, &name, &idxSQL); scanErr == nil {
				t.Logf("  %-35s  %s", tbl+"."+name, idxSQL.String)
			}
		}
	}
}

// TestConnectionPoolTuning benchmarks GetStopTimesForStopInWindow with
// MaxOpenConns [5,10,25,50] using 25 goroutines, measuring throughput and latency.
// Uses the RABA dataset to keep runs fast since each pool size requires a fresh import.
func TestConnectionPoolTuning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pool tuning test in -short mode")
	}

	rabaZip, _ := filepath.Abs(filepath.Join("..", "testdata", "raba.zip"))
	if !latencyFileExists(rabaZip) {
		t.Skip("testdata/raba.zip not found")
	}

	tmpDir := filepath.Join(os.TempDir(), "maglev_pool_tuning")
	require.NoError(t, os.MkdirAll(tmpDir, 0755))

	const (
		concurrency = 25
		iterations  = 100
	)
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)

	type poolResult struct {
		maxConns     int
		throughput   float64 // q/s
		avgLatencyMs float64
		waitCount    int64
		avgWaitMs    float64
	}
	var results []poolResult

	for _, maxConns := range []int{5, 10, 25, 50} {
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("raba_pool_%d.db", maxConns))
		_ = os.Remove(dbPath)

		cfg := Config{DBPath: dbPath, Env: appconf.Development}
		client, err := NewClient(cfg)
		require.NoError(t, err)

		data, readErr := os.ReadFile(rabaZip)
		require.NoError(t, readErr)
		require.NoError(t, client.processAndStoreGTFSDataWithSource(data, rabaZip))

		// Apply the pool size under test.
		client.DB.SetMaxOpenConns(maxConns)
		client.DB.SetMaxIdleConns(maxConns / 2)

		ctx := context.Background()
		stopID := latencyPickFirstStop(ctx, t, client.DB)

		var (
			mu      sync.Mutex
			samples []time.Duration
			wg      sync.WaitGroup
		)
		start := time.Now()
		for g := 0; g < concurrency; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				local := make([]time.Duration, 0, iterations)
				for i := 0; i < iterations; i++ {
					t0 := time.Now()
					_, qErr := client.Queries.GetStopTimesForStopInWindow(ctx, GetStopTimesForStopInWindowParams{
						StopID:           stopID,
						WindowStartNanos: windowStart,
						WindowEndNanos:   windowEnd,
					})
					if qErr != nil {
						return
					}
					local = append(local, time.Since(t0))
				}
				mu.Lock()
				samples = append(samples, local...)
				mu.Unlock()
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)

		poolStats := client.DB.Stats()
		_ = client.Close()
		_ = os.Remove(dbPath)

		if len(samples) == 0 {
			continue
		}
		var total time.Duration
		for _, s := range samples {
			total += s
		}
		avg := total / time.Duration(len(samples))

		avgWaitMs := 0.0
		if poolStats.WaitCount > 0 {
			avgWaitMs = float64(poolStats.WaitDuration.Milliseconds()) / float64(poolStats.WaitCount)
		}

		results = append(results, poolResult{
			maxConns:     maxConns,
			throughput:   float64(len(samples)) / elapsed.Seconds(),
			avgLatencyMs: float64(avg.Microseconds()) / 1000.0,
			waitCount:    poolStats.WaitCount,
			avgWaitMs:    avgWaitMs,
		})
	}

	t.Log("=== Connection Pool Tuning Results (GetStopTimesForStopInWindow) ===")
	t.Logf("  %-12s  %-14s  %-16s  %-12s  %s",
		"MaxOpenConns", "Throughput(q/s)", "AvgLatency(ms)", "WaitCount", "AvgWait(ms)")
	for _, r := range results {
		t.Logf("  %-12d  %-14.1f  %-16.3f  %-12d  %.3f",
			r.maxConns, r.throughput, r.avgLatencyMs, r.waitCount, r.avgWaitMs)
	}

	if len(results) < 2 {
		return
	}
	best := results[0]
	for _, r := range results[1:] {
		if r.throughput > best.throughput {
			best = r
		}
	}
	t.Logf("  => Best throughput at MaxOpenConns=%d (%.1f q/s, avg %.3fms)",
		best.maxConns, best.throughput, best.avgLatencyMs)
	if best.maxConns != 25 {
		t.Logf("  RECOMMENDATION: current default MaxOpenConns=25 may not be optimal; "+
			"consider MaxOpenConns=%d", best.maxConns)
	} else {
		t.Log("  Current default MaxOpenConns=25 appears optimal for RABA dataset.")
	}
}

// Benchmarks (normal tags – RABA data)
// BenchmarkQueryGetStopTimesForStopInWindow measures the hot arrivals path.
func BenchmarkQueryGetStopTimesForStopInWindow(b *testing.B) {
	client, stopID, _ := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.Queries.GetStopTimesForStopInWindow(ctx, GetStopTimesForStopInWindowParams{
			StopID:           stopID,
			WindowStartNanos: windowStart,
			WindowEndNanos:   windowEnd,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryGetScheduleForStopOnDate measures the schedule-for-stop query.
func BenchmarkQueryGetScheduleForStopOnDate(b *testing.B) {
	client, stopID, _ := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	now := time.Now()
	dateStr := now.Format("20060102")
	weekday := strings.ToLower(now.Weekday().String())
	routeIDs := latencyFetchRouteIDsForStop(ctx, b, client.Queries, stopID)
	if len(routeIDs) == 0 {
		b.Skip("no routes for chosen stop")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.Queries.GetScheduleForStopOnDate(ctx, GetScheduleForStopOnDateParams{
			StopID:     stopID,
			TargetDate: dateStr,
			Weekday:    weekday,
			RouteIds:   routeIDs,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryGetStopTimesForTrip measures the trip-details path.
func BenchmarkQueryGetStopTimesForTrip(b *testing.B) {
	client, _, tripID := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.Queries.GetStopTimesForTrip(ctx, tripID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryGetActiveRouteIDsForStopsOnDate measures the stops-for-location
// batch query.
func BenchmarkQueryGetActiveRouteIDsForStopsOnDate(b *testing.B) {
	client, stopID, _ := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	dateStr := time.Now().Format("20060102")
	svcIDs := latencyFetchActiveServiceIDs(ctx, client.Queries, dateStr)
	if len(svcIDs) == 0 {
		b.Skip("no active service IDs for today")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.Queries.GetActiveRouteIDsForStopsOnDate(ctx, GetActiveRouteIDsForStopsOnDateParams{
			StopIds:    []string{stopID},
			ServiceIds: svcIDs,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuerySearchStopsByName measures FTS5 stop-name search.
func BenchmarkQuerySearchStopsByName(b *testing.B) {
	client, _, _ := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "st*",
			Limit:       20,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryConcurrentMixed runs the five queries concurrently using the same traffic ratios as the k6 load test.
//
//	40 % GetStopTimesForStopInWindow   (arrivals-and-departures)
//	25 % GetActiveRouteIDsForStopsOnDate (stops-for-location)
//	15 % SearchStopsByName              (proxy for vehicles-for-agency DB work)
//	10 % GetStopTimesForTrip            (trip-details)
//	10 % GetScheduleForStopOnDate       (schedule-for-stop)
func BenchmarkQueryConcurrentMixed(b *testing.B) {
	client, stopID, tripID := loadLatencyFixture(b)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	now := time.Now()
	dateStr := now.Format("20060102")
	weekday := strings.ToLower(now.Weekday().String())
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)
	routeIDs := latencyFetchRouteIDsForStop(ctx, b, client.Queries, stopID)
	svcIDs := latencyFetchActiveServiceIDs(ctx, client.Queries, dateStr)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			r := i % 100
			i++
			var err error
			switch {
			case r < 40:
				_, err = client.Queries.GetStopTimesForStopInWindow(ctx, GetStopTimesForStopInWindowParams{
					StopID:           stopID,
					WindowStartNanos: windowStart,
					WindowEndNanos:   windowEnd,
				})
			case r < 65:
				if len(svcIDs) > 0 {
					_, err = client.Queries.GetActiveRouteIDsForStopsOnDate(ctx, GetActiveRouteIDsForStopsOnDateParams{
						StopIds:    []string{stopID},
						ServiceIds: svcIDs,
					})
				}
			case r < 80:
				_, err = client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
					SearchQuery: "st*",
					Limit:       20,
				})
			case r < 90:
				_, err = client.Queries.GetStopTimesForTrip(ctx, tripID)
			default:
				if len(routeIDs) > 0 {
					_, err = client.Queries.GetScheduleForStopOnDate(ctx, GetScheduleForStopOnDateParams{
						StopID:     stopID,
						TargetDate: dateStr,
						Weekday:    weekday,
						RouteIds:   routeIDs,
					})
				}
			}
			if err != nil {
				b.Error(err)
			}
		}
	})
}
