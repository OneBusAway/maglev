package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

func TestStaticTripRows(t *testing.T) {
	ctx := context.Background()
	matcher, queries := newCountingBlockMatcher(t, fixedNow(matcherMonday))
	rows := staticTripRows{}

	require.NoError(t, rows.load(ctx, matcher.queries, []string{"T_FIRST", "T_MISSING", "T_FIRST"}))
	trip, ok := rows.get("T_FIRST")
	require.True(t, ok)
	assert.Equal(t, "B5", trip.BlockID.String)
	_, ok = rows.get("T_MISSING")
	assert.False(t, ok, "a trip static data lacks has no row")
	assert.Equal(t, int64(1), queries.Load())

	require.NoError(t, rows.load(ctx, matcher.queries, []string{"T_FIRST", "T_MISSING"}))
	assert.Equal(t, int64(1), queries.Load(), "loaded and known-missing trips are not read again")

	require.NoError(t, rows.load(ctx, matcher.queries, []string{"T_FIRST", "T_SECOND"}))
	assert.Equal(t, int64(2), queries.Load(), "only the new trip is read")
}

func TestStaticTripRowsRecordNothingOnFailure(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	queries := gtfsdb.New(newBlockMatcherTestDB(t).DB)
	rows := staticTripRows{}

	require.Error(t, rows.load(canceled, queries, []string{"T_FIRST"}))

	assert.Equal(t, []string{"T_FIRST"}, rows.unloaded([]string{"T_FIRST"}), "a failed read is not recorded as missing")
}
