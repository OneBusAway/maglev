package restapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

// memoizedTripRows holds every memoized lookup a request can make for one
// trip.
type memoizedTripRows struct {
	trip              gtfsdb.Trip
	route             gtfsdb.Route
	activeServiceIDs  []string
	blockTrips        []gtfsdb.GetTripsByBlockIDRow
	stopTimes         []gtfsdb.StopTime
	shapePoints       []gtfsdb.GetShapePointsByTripIDRow
	frequencies       []gtfsdb.Frequency
	blockTripSequence int64
}

// memoCheckOptions says which per-trip lookups a request's path makes.
type memoCheckOptions struct {
	// frequenciesBatched skips the per-trip frequency lookup, for paths that
	// load every trip's frequencies in one GetFrequenciesForTrips query.
	frequenciesBatched bool
}

func readMemoizedTripRows(t *testing.T, ctx context.Context, reader gtfsdb.MemoReader, tripID, serviceDate string) memoizedTripRows {
	t.Helper()
	return readMemoizedTripRowsWith(t, ctx, reader, tripID, serviceDate, memoCheckOptions{})
}

func readMemoizedTripRowsWith(t *testing.T, ctx context.Context, reader gtfsdb.MemoReader, tripID, serviceDate string, options memoCheckOptions) memoizedTripRows {
	t.Helper()
	var rows memoizedTripRows
	var err error
	rows.trip, err = reader.GetTrip(ctx, tripID)
	require.NoError(t, err)
	rows.route, err = reader.GetRoute(ctx, rows.trip.RouteID)
	require.NoError(t, err)
	rows.activeServiceIDs, err = reader.GetActiveServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err)
	rows.blockTrips, err = reader.GetTripsByBlockID(ctx, rows.trip.BlockID)
	require.NoError(t, err)
	rows.stopTimes, err = reader.GetStopTimesForTrip(ctx, tripID)
	require.NoError(t, err)
	rows.shapePoints, err = reader.GetShapePointsByTripID(ctx, tripID)
	require.NoError(t, err)
	if !options.frequenciesBatched {
		rows.frequencies, err = reader.GetFrequenciesForTrip(ctx, tripID)
		require.NoError(t, err)
	}
	if rows.trip.BlockID.Valid && len(rows.activeServiceIDs) > 0 {
		rows.blockTripSequence, err = reader.GetBlockTripSequence(ctx, gtfsdb.GetBlockTripSequenceParams{
			TripID:     tripID,
			BlockID:    rows.trip.BlockID,
			ServiceIds: rows.activeServiceIDs,
		})
		require.NoError(t, err)
	}
	return rows
}

// memoTrip names one trip a request touched and the service date it ran on,
// formatted as GetActiveServiceIDsForDate takes it (20060102).
type memoTrip struct {
	tripID      string
	serviceDate string
}

// assertMemoMatchesFresh checks the rows a finished request left in its read
// memo. Reading them back through the memo must not reach the database, so
// the request really memoized them, and they must equal a fresh read, so
// nothing on the request's path edited a shared row.
func assertMemoMatchesFresh(t *testing.T, ctx context.Context, counter *queryCountingDB, direct *gtfsdb.Queries, trips []memoTrip, options memoCheckOptions) {
	t.Helper()
	require.NotEmpty(t, trips, "the request should have touched at least one trip")
	memo := gtfsdb.New(counter).Memo(ctx)
	fresh := direct.Memo(context.Background())
	for _, trip := range trips {
		counter.count.Store(0)
		memoized := readMemoizedTripRowsWith(t, ctx, memo, trip.tripID, trip.serviceDate, options)
		require.Zero(t, counter.count.Load(), "trip %s: every row should already be in the request's memo", trip.tripID)
		assert.Equal(t, readMemoizedTripRowsWith(t, context.Background(), fresh, trip.tripID, trip.serviceDate, options), memoized,
			"trip %s: a memoized row was changed during the request", trip.tripID)
	}
}
