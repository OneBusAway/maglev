package gtfsdb

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestGetNextAndPreviousTripsInBlock_JavaOrder checks that a block's trips
// are ordered by their average departure time, as Java's BlockTripComparator
// does, across the services given, and then by trip ID.
func TestGetNextAndPreviousTripsInBlock_JavaOrder(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	_, err = client.DB.Exec(`PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	// long leaves first (08:00) but averages 09:00; short leaves at 08:30 and
	// averages 08:35; other runs under a second service and averages 10:00;
	// tie-a and tie-b both average 11:00.
	trips := map[string]struct {
		service    string
		departures []time.Duration
	}{
		"long":  {"WEEKDAY", []time.Duration{8 * time.Hour, 10 * time.Hour}},
		"short": {"WEEKDAY", []time.Duration{8*time.Hour + 30*time.Minute, 8*time.Hour + 40*time.Minute}},
		"other": {"SCHOOL", []time.Duration{10 * time.Hour}},
		"tie-b": {"WEEKDAY", []time.Duration{11 * time.Hour}},
		"tie-a": {"WEEKDAY", []time.Duration{11 * time.Hour}},
	}
	for id, trip := range trips {
		_, err = client.DB.Exec(`INSERT INTO trips (id, route_id, service_id, block_id) VALUES (?, 'R', ?, 'B')`, id, trip.service)
		require.NoError(t, err)
		for sequence, departure := range trip.departures {
			_, err = client.DB.Exec(`INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES (?, 'S', ?, ?, ?)`,
				id, sequence, int64(departure), int64(departure))
			require.NoError(t, err)
		}
	}

	neighbours := func(tripID string, serviceIDs ...string) (string, string) {
		row, err := client.Queries.GetNextAndPreviousTripsInBlock(ctx, GetNextAndPreviousTripsInBlockParams{
			TripID: tripID, BlockID: sql.NullString{String: "B", Valid: true}, ServiceIds: serviceIDs,
		})
		require.NoError(t, err)
		text := func(v any) string {
			if v == nil {
				return ""
			}
			return v.(string)
		}
		return text(row.PrevTripID), text(row.NextTripID)
	}

	// short, long, other, tie-a, tie-b
	prev, next := neighbours("long", "WEEKDAY", "SCHOOL")
	assert.Equal(t, []string{"short", "other"}, []string{prev, next})
	prev, next = neighbours("other", "WEEKDAY", "SCHOOL")
	assert.Equal(t, []string{"long", "tie-a"}, []string{prev, next})
	prev, next = neighbours("tie-a", "WEEKDAY", "SCHOOL")
	assert.Equal(t, []string{"other", "tie-b"}, []string{prev, next})

	// Without the second service, long leads straight into the ties.
	prev, next = neighbours("long", "WEEKDAY")
	assert.Equal(t, []string{"short", "tie-a"}, []string{prev, next})
}
