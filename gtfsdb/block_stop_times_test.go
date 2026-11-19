package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBlockStopTimesForTripIDs_MatchesFullRows(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	blockTrips, err := client.Queries.GetTripsByBlockID(ctx, sql.NullString{String: memoTestBlockID, Valid: true})
	require.NoError(t, err)
	require.Greater(t, len(blockTrips), 1, "fixture block should have several trips")
	tripIDs := make([]string, len(blockTrips))
	for i, trip := range blockTrips {
		tripIDs[i] = trip.ID
	}

	fullRows, err := client.Queries.GetStopTimesForTripIDs(ctx, tripIDs)
	require.NoError(t, err)
	want := make([]GetBlockStopTimesForTripIDsRow, len(fullRows))
	for i, row := range fullRows {
		want[i] = GetBlockStopTimesForTripIDsRow{
			TripID:        row.TripID,
			StopID:        row.StopID,
			StopSequence:  row.StopSequence,
			ArrivalTime:   row.ArrivalTime,
			DepartureTime: row.DepartureTime,
		}
	}

	got, err := client.Queries.GetBlockStopTimesForTripIDs(ctx, tripIDs)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
