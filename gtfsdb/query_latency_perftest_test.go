//go:build perftest

package gtfsdb

// Large-dataset benchmarks against the TriMet GTFS feed.
//
// Prerequisites:
//
//	bash scripts/download-perf-data.sh
//
// Run:
//
//	make bench-sqlite-perftest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// loadPerfFixture opens the TriMet GTFS database at testdata/perf/trimet-perf.db.
// The database file is created on the first call; subsequent calls within the
// same test binary invocation reuse the existing file.
func loadPerfFixture(b *testing.B) *Client {
	b.Helper()

	zipPath, _ := filepath.Abs(filepath.Join("..", "testdata", "perf", "trimet.zip"))
	if _, err := os.Stat(zipPath); err != nil {
		b.Skipf("TriMet GTFS not found at %s – run scripts/download-perf-data.sh first", zipPath)
	}

	dbPath := filepath.Join(filepath.Dir(zipPath), "trimet-perf.db")

	cfg := Config{
		DBPath: dbPath,
		Env:    appconf.Development,
	}
	client, err := NewClient(cfg)
	require.NoError(b, err)
	b.Cleanup(func() { _ = client.Close() })

	// Import GTFS data; skipped automatically when the hash matches an existing import.
	zipData, err := os.ReadFile(zipPath)
	require.NoError(b, err)
	if importErr := client.processAndStoreGTFSDataWithSource(context.Background(), zipData, zipPath); importErr != nil {
		// A duplicate import fails the hash-match check; the error is non-fatal.
		b.Logf("GTFS import: %v", importErr)
		if latencyIsEmpty(context.Background(), b, client.DB) {
			b.Fatalf("GTFS import failed and database is empty: %v", importErr)
		}
	}

	return client
}

// BenchmarkLargeDatasetGetStopTimesForStopInWindow benchmarks the hottest
// query (arrivals inner loop) against the full TriMet dataset (~3 M stop_times).
func BenchmarkLargeDatasetGetStopTimesForStopInWindow(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	stopID := latencyPickFirstStop(ctx, b, client.DB)
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)

	b.ReportAllocs()
	b.ResetTimer()

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

// BenchmarkLargeDatasetGetScheduleForStopOnDate benchmarks the schedule-for-stop
// query (the heaviest query by plan cost) against TriMet data.
func BenchmarkLargeDatasetGetScheduleForStopOnDate(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	stopID := latencyPickFirstStop(ctx, b, client.DB)
	now := time.Now()
	dateStr := now.Format("20060102")
	weekday := strings.ToLower(now.Weekday().String())
	routeIDs := latencyFetchRouteIDsForStop(ctx, b, client.Queries, stopID)
	if len(routeIDs) == 0 {
		b.Skip("no routes for chosen stop in TriMet data")
	}

	b.ReportAllocs()
	b.ResetTimer()

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

// BenchmarkLargeDatasetSearchStopsByName benchmarks FTS5 search over the full
// TriMet stop corpus (~10 k stops).
func BenchmarkLargeDatasetSearchStopsByName(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

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

// BenchmarkLargeDatasetGetStopTimesForTrip benchmarks the trip-details path.
func BenchmarkLargeDatasetGetStopTimesForTrip(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	tripID := latencyPickFirstTrip(ctx, b, client.DB)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := client.Queries.GetStopTimesForTrip(ctx, tripID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLargeDatasetGetActiveRouteIDsForStopsOnDate benchmarks the
// stops-for-location batch query against TriMet data.
func BenchmarkLargeDatasetGetActiveRouteIDsForStopsOnDate(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	stopID := latencyPickFirstStop(ctx, b, client.DB)
	dateStr := time.Now().Format("20060102")
	svcIDs := latencyFetchActiveServiceIDs(ctx, b, client.Queries, dateStr)
	if len(svcIDs) == 0 {
		b.Skip("no active service IDs for today in TriMet data")
	}

	b.ReportAllocs()
	b.ResetTimer()

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

// BenchmarkLargeDatasetConcurrentMixed runs a realistic mixed workload against
// TriMet data with 25 parallel goroutines, mirroring the k6 scenario ratios.
func BenchmarkLargeDatasetConcurrentMixed(b *testing.B) {
	client := loadPerfFixture(b)
	ctx := context.Background()

	stopID := latencyPickFirstStop(ctx, b, client.DB)
	tripID := latencyPickFirstTrip(ctx, b, client.DB)
	now := time.Now()
	dateStr := now.Format("20060102")
	weekday := strings.ToLower(now.Weekday().String())
	windowStart := int64(5 * time.Hour)
	windowEnd := int64(23 * time.Hour)
	routeIDs := latencyFetchRouteIDsForStop(ctx, b, client.Queries, stopID)
	svcIDs := latencyFetchActiveServiceIDs(ctx, b, client.Queries, dateStr)

	b.ReportAllocs()
	b.ResetTimer()
	b.SetParallelism(25)

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
