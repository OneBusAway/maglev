package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

var increasingDirectionShape = []string{"0", "450", "900", "1400", "2000", "2500", "3000"}

func directionTestClient(t testing.TB, feed []byte) *gtfsdb.Client {
	t.Helper()
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	parsed, err := gtfsdb.ParseGtfsData(feed, "stop-distance-test")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	return client
}

func TestCalculateStopDirection_StopDistanceValidation(t *testing.T) {
	tests := []struct {
		name      string
		distances []string
		expected  []string
	}{
		{"zero placeholders", []string{"0", "0", "0", "0"}, []string{"E", "E", "NE", "N"}},
		{"positive placeholders", []string{"100", "100", "100", "100"}, []string{"E", "E", "NE", "N"}},
		{"decreasing", []string{"0", "900", "800", "3000"}, []string{"E", "E", "NE", "N"}},
		{"repeated", []string{"0", "900", "900", "3000"}, []string{"E", "E", "NE", "N"}},
		{"single supplied", []string{"", "", "", "100"}, []string{"E", "E", "NE", "N"}},
		{"missing", []string{"", "", "", ""}, []string{"E", "E", "NE", "N"}},
		{"valid distances", []string{"0", "900", "2000", "3000"}, []string{"E", "E", "NE", "N"}},
		{"gaps", []string{"0", "", "2000", ""}, []string{"E", "E", "NE", "N"}},
		// Option 1 checks order, not geometric consistency. This control also proves
		// valid supplied distances are still used when geography would select N.
		{"increasing mismatched scale", []string{"0", "1", "2", "3"}, []string{"E", "E", "E", "E"}},
	}
	for _, tt := range tests {
		for _, preloaded := range []bool{false, true} {
			name := "database/"
			if preloaded {
				name = "preloaded/"
			}
			t.Run(name+tt.name, func(t *testing.T) {
				client := directionTestClient(t, buildDirectionFeed(t, tt.distances, increasingDirectionShape))
				calculator := NewAdvancedDirectionCalculator(client.Queries)
				if preloaded {
					cache, err := NewDirectionPrecomputer(client.Queries, client.DB).loadShapeCache(context.Background())
					require.NoError(t, err)
					require.NoError(t, calculator.SetShapeCache(cache))
				}
				var got []string
				for _, stopID := range []string{"S1", "S2", "S3", "S4"} {
					got = append(got, calculator.CalculateStopDirection(context.Background(), stopID))
				}
				assert.Equal(t, tt.expected, got)
			})
		}
	}
}

func TestCalculateStopDirection_ValidZeroUsesDistance(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "900", "2000", "3000"}, increasingDirectionShape))
	_, err := client.DB.Exec("UPDATE stops SET lat = 40.020, lon = -121.980 WHERE id = 'S1'")
	require.NoError(t, err)
	calculator := NewAdvancedDirectionCalculator(client.Queries)
	assert.Equal(t, "E", calculator.CalculateStopDirection(context.Background(), "S1"))
	orientation, err := calculator.calculateOrientationAtStop(context.Background(), "SH1", -1, 40.020, -121.980)
	require.NoError(t, err)
	assert.Equal(t, "N", calculator.getAngleAsDirection(orientation))
}

func TestCalculateStopDirection_TripsSharingShape(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "1", "2", "3"}, increasingDirectionShape))
	_, err := client.DB.Exec("INSERT INTO trips (id, route_id, service_id, shape_id) VALUES ('T2', 'R1', 'WD', 'SH1')")
	require.NoError(t, err)
	_, err = client.DB.Exec(`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence, shape_dist_traveled)
 SELECT 'T2', arrival_time, departure_time, stop_id, stop_sequence, 0 FROM stop_times WHERE trip_id = 'T1'`)
	require.NoError(t, err)
	calculator := NewAdvancedDirectionCalculator(client.Queries)
	calculator.standardDeviationThreshold = 1
	// E from T1 and N from T2 combine into NE. Both use the same shape but must
	// have distinct validation results and orientation-cache keys.
	assert.Equal(t, "NE", calculator.CalculateStopDirection(context.Background(), "S4"))
	valid, ok := calculator.cache.Load().tripDistances.Load("T1")
	require.True(t, ok)
	assert.Equal(t, true, valid)
	valid, ok = calculator.cache.Load().tripDistances.Load("T2")
	require.True(t, ok)
	assert.Equal(t, false, valid)
}

func TestCalculateStopDirection_RepeatedStopVisits(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "1", "2", "3"}, increasingDirectionShape))
	// A later visit to the same stop repeats the supplied distance. Validation
	// must retain both occurrences rather than reducing the trip to unique stops.
	_, err := client.DB.Exec(`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence, shape_dist_traveled)
 VALUES ('T1', 30000, 30000, 'S4', 5, 3)`)
	require.NoError(t, err)
	calculator := NewAdvancedDirectionCalculator(client.Queries)
	assert.Equal(t, "N", calculator.CalculateStopDirection(context.Background(), "S4"))
}

// Wrap the real DB to count validation reads and inject a recoverable failure.
type tripDistanceTestDB struct {
	gtfsdb.DBTX
	reads   atomic.Int64
	fail    atomic.Bool
	started chan struct{}
	release chan struct{}
}

func (db *tripDistanceTestDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "-- name: GetStopTimesForTrip :many") {
		db.reads.Add(1)
		if db.fail.Load() {
			return nil, errors.New("temporary trip-distance read failure")
		}
		if db.started != nil {
			close(db.started)
			<-db.release
		}
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

func TestTripDistanceReadFailure_Retries(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "0", "0", "0"}, increasingDirectionShape))
	db := &tripDistanceTestDB{DBTX: client.DB}
	db.fail.Store(true)
	calculator := NewAdvancedDirectionCalculator(gtfsdb.New(db))
	assert.Empty(t, calculator.CalculateStopDirection(context.Background(), "S4"))
	_, cached := calculator.cache.Load().directionResults.Load("S4")
	assert.False(t, cached)
	_, cached = calculator.cache.Load().tripDistances.Load("T1")
	assert.False(t, cached)
	db.fail.Store(false)
	assert.Equal(t, "N", calculator.CalculateStopDirection(context.Background(), "S4"))
	assert.EqualValues(t, 2, db.reads.Load())
}

func TestTripDistanceCache_ConcurrentStops(t *testing.T) {
	for _, distances := range [][]string{{"0", "0", "0", "0"}, {"0", "900", "2000", "3000"}} {
		t.Run(strings.Join(distances, ","), func(t *testing.T) {
			client := directionTestClient(t, buildDirectionFeed(t, distances, increasingDirectionShape))
			db := &tripDistanceTestDB{DBTX: client.DB}
			calculator := NewAdvancedDirectionCalculator(gtfsdb.New(db))
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					stopID := []string{"S1", "S2", "S3", "S4"}[i%4]
					expected := []string{"E", "E", "NE", "N"}[i%4]
					assert.Equal(t, expected, calculator.CalculateStopDirection(context.Background(), stopID))
				}(i)
			}
			wg.Wait()
			assert.EqualValues(t, 1, db.reads.Load(), "one trip read across concurrent stop calculations")
		})
	}
}

func TestDirectionCacheReload_RetiresInflightCalculations(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "0", "0", "0"}, increasingDirectionShape))
	db := &tripDistanceTestDB{DBTX: client.DB, started: make(chan struct{}), release: make(chan struct{})}
	calculator := NewAdvancedDirectionCalculator(gtfsdb.New(db))
	oldCache := calculator.cache.Load()
	done := make(chan string, 1)
	go func() { done <- calculator.CalculateStopDirection(context.Background(), "S4") }()
	<-db.started
	calculator.ClearCache()
	newCache := calculator.cache.Load()
	require.NotSame(t, oldCache, newCache)
	close(db.release)
	assert.Equal(t, "N", <-done)
	_, cached := newCache.directionResults.Load("S4")
	assert.False(t, cached, "in-flight direction must stay in the retired cache")
	_, cached = newCache.tripDistances.Load("T1")
	assert.False(t, cached, "in-flight trip validation must stay in the retired cache")
	// Change the feed distances so the next generation must validate again.
	_, err := client.DB.Exec("UPDATE stop_times SET shape_dist_traveled = stop_sequence - 1")
	require.NoError(t, err)
	db.started = nil
	assert.Equal(t, "E", calculator.CalculateStopDirection(context.Background(), "S4"))
	assert.EqualValues(t, 2, db.reads.Load())
}

func TestPrecomputeDirections_StopDistancePlaceholders(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"100", "100", "100", "100"}, increasingDirectionShape))
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	require.NoError(t, precomputer.PrecomputeAllDirections(context.Background()))
	for i, stopID := range []string{"S1", "S2", "S3", "S4"} {
		stop, err := client.Queries.GetStop(context.Background(), stopID)
		require.NoError(t, err)
		assert.Equal(t, sql.NullString{String: []string{"E", "E", "NE", "N"}[i], Valid: true}, stop.Direction)
	}
}
