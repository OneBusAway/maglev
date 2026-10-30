package gtfsdb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestGetStopTimesForStopInWindow_IntervalOverlap tests that single-stop and batch
// window queries match all overlapping intervals (spanning, contained, boundary, and partial)
// while excluding intervals strictly outside the query window.
func TestGetStopTimesForStopInWindow_IntervalOverlap(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone) VALUES ('A1', 'Agency 1', 'http://example.com', 'America/Los_Angeles');
		INSERT INTO routes (id, agency_id, short_name, long_name, type) VALUES ('R1', 'A1', '1', 'Route 1', 3);
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
			VALUES ('SRV1', 1, 1, 1, 1, 1, 1, 1, '20260101', '20261231');
		INSERT INTO stops (id, name, lat, lon) VALUES ('S1', 'Stop 1', 40.0, -120.0);
	`)
	require.NoError(t, err)

	// Window: 10:10 to 10:20
	winStart := int64(10*time.Hour + 10*time.Minute)
	winEnd := int64(10*time.Hour + 20*time.Minute)

	testCases := []struct {
		tripID string
		arr    int64
		dep    int64
		match  bool
		desc   string
	}{
		{
			tripID: "T_SPAN",
			arr:    int64(10 * time.Hour),
			dep:    int64(10*time.Hour + 30*time.Minute),
			match:  true,
			desc:   "completely spans query window (10:00 -> 10:30)",
		},
		{
			tripID: "T_INSIDE",
			arr:    int64(10*time.Hour + 12*time.Minute),
			dep:    int64(10*time.Hour + 18*time.Minute),
			match:  true,
			desc:   "completely inside query window (10:12 -> 10:18)",
		},
		{
			tripID: "T_OVERLAP_START",
			arr:    int64(10*time.Hour + 5*time.Minute),
			dep:    int64(10*time.Hour + 15*time.Minute),
			match:  true,
			desc:   "overlaps window start (10:05 -> 10:15)",
		},
		{
			tripID: "T_OVERLAP_END",
			arr:    int64(10*time.Hour + 15*time.Minute),
			dep:    int64(10*time.Hour + 25*time.Minute),
			match:  true,
			desc:   "overlaps window end (10:15 -> 10:25)",
		},
		{
			tripID: "T_EXACT_START",
			arr:    winStart,
			dep:    winStart,
			match:  true,
			desc:   "exact match at window start boundary (10:10)",
		},
		{
			tripID: "T_EXACT_END",
			arr:    winEnd,
			dep:    winEnd,
			match:  true,
			desc:   "exact match at window end boundary (10:20)",
		},
		{
			tripID: "T_BEFORE",
			arr:    int64(10 * time.Hour),
			dep:    int64(10*time.Hour + 9*time.Minute),
			match:  false,
			desc:   "strictly before window (10:00 -> 10:09)",
		},
		{
			tripID: "T_AFTER",
			arr:    int64(10*time.Hour + 21*time.Minute),
			dep:    int64(10*time.Hour + 30*time.Minute),
			match:  false,
			desc:   "strictly after window (10:21 -> 10:30)",
		},
	}

	for _, tc := range testCases {
		_, err = client.DB.ExecContext(ctx,
			"INSERT INTO trips (id, route_id, service_id) VALUES (?, 'R1', 'SRV1')",
			tc.tripID,
		)
		require.NoError(t, err)

		_, err = client.DB.ExecContext(ctx,
			"INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES (?, 'S1', 1, ?, ?)",
			tc.tripID, tc.arr, tc.dep,
		)
		require.NoError(t, err)
	}

	// 1. Single stop query
	rows, err := client.Queries.GetStopTimesForStopInWindow(ctx, GetStopTimesForStopInWindowParams{
		StopID:           "S1",
		WindowStartNanos: winStart,
		WindowEndNanos:   winEnd,
	})
	require.NoError(t, err)

	matchedMap := make(map[string]bool)
	for _, r := range rows {
		matchedMap[r.TripID] = true
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.match, matchedMap[tc.tripID], "GetStopTimesForStopInWindow mismatch for %s (%s)", tc.tripID, tc.desc)
	}

	// 2. Batch stops query
	batchRows, err := client.Queries.GetStopTimesForStopsInWindow(ctx, GetStopTimesForStopsInWindowParams{
		StopIds:          []string{"S1"},
		WindowStartNanos: winStart,
		WindowEndNanos:   winEnd,
	})
	require.NoError(t, err)

	batchMatchedMap := make(map[string]bool)
	for _, r := range batchRows {
		batchMatchedMap[r.TripID] = true
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.match, batchMatchedMap[tc.tripID], "GetStopTimesForStopsInWindow mismatch for %s (%s)", tc.tripID, tc.desc)
	}
}
