package gtfsdb

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingDB counts the queries an unprepared Queries issues, so a test can
// tell a memo hit from a database round trip.
type countingDB struct {
	DBTX
	count int
}

func (db *countingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.count++
	return db.DBTX.QueryContext(ctx, query, args...)
}

func (db *countingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.count++
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

// RABA rows used below: a trip on route 154 in block 403, scheduled on a
// Friday, with stop times and a shape but no frequencies.
const (
	memoTestTripID  = "6a3ef1d9-13d7-488b-8528-633ce9bbeb6d"
	memoTestRouteID = "154"
	memoTestBlockID = "403"
	memoTestDate    = "20250613"
)

func newMemoTestQueries(t *testing.T) (*Queries, *countingDB) {
	t.Helper()
	client := newTestClientWithRABA(t)
	db := &countingDB{DBTX: client.DB}
	return New(db), db
}

func TestReadMemo_RepeatsHitTheMemo(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	blockID := sql.NullString{String: memoTestBlockID, Valid: true}

	tests := []struct {
		name string
		warm func(ctx context.Context, reader MemoReader) error
		read func(ctx context.Context, reader MemoReader) (any, error)
	}{
		{name: "GetTrip", read: func(ctx context.Context, r MemoReader) (any, error) { return r.GetTrip(ctx, memoTestTripID) }},
		{name: "GetRoute", read: func(ctx context.Context, r MemoReader) (any, error) { return r.GetRoute(ctx, memoTestRouteID) }},
		{name: "GetActiveServiceIDsForDate", read: func(ctx context.Context, r MemoReader) (any, error) {
			return r.GetActiveServiceIDsForDate(ctx, memoTestDate)
		}},
		{name: "GetTripsByBlockID", read: func(ctx context.Context, r MemoReader) (any, error) { return r.GetTripsByBlockID(ctx, blockID) }},
		{name: "GetStopTimesForTrip", read: func(ctx context.Context, r MemoReader) (any, error) {
			return r.GetStopTimesForTrip(ctx, memoTestTripID)
		}},
		{name: "GetFrequenciesForTrip", read: func(ctx context.Context, r MemoReader) (any, error) {
			return r.GetFrequenciesForTrip(ctx, memoTestTripID)
		}},
		{
			name: "GetBlockTripSequence",
			warm: func(ctx context.Context, r MemoReader) error {
				// Pre-warm GetActiveServiceIDsForDate so db.count only measures GetBlockTripSequence itself.
				_, err := r.GetActiveServiceIDsForDate(ctx, memoTestDate)
				return err
			},
			read: func(ctx context.Context, r MemoReader) (any, error) {
				serviceIDs, err := r.GetActiveServiceIDsForDate(ctx, memoTestDate)
				if err != nil {
					return nil, err
				}
				return r.GetBlockTripSequence(ctx, GetBlockTripSequenceParams{
					TripID:     memoTestTripID,
					BlockID:    blockID,
					ServiceIds: serviceIDs,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithReadMemo(context.Background())
			if tt.warm != nil {
				require.NoError(t, tt.warm(ctx, queries.Memo(ctx)))
			}
			db.count = 0

			first, err := tt.read(ctx, queries.Memo(ctx))
			require.NoError(t, err)
			second, err := tt.read(ctx, queries.Memo(ctx))
			require.NoError(t, err)

			assert.Equal(t, 1, db.count)
			assert.Equal(t, first, second)
		})
	}
}

func TestReadMemo_NoMemoQueriesEveryTime(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := context.Background()

	for range 2 {
		_, err := queries.Memo(ctx).GetTrip(ctx, memoTestTripID)
		require.NoError(t, err)
	}

	assert.Equal(t, 2, db.count)
}

func TestReadMemo_KeysArePerArgument(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())

	first, err := queries.Memo(ctx).GetActiveServiceIDsForDate(ctx, memoTestDate)
	require.NoError(t, err)
	sunday, err := queries.Memo(ctx).GetActiveServiceIDsForDate(ctx, "20250615")
	require.NoError(t, err)

	assert.Equal(t, 2, db.count)
	assert.NotEqual(t, first, sunday)
}

func TestReadMemo_ErrorsAreNotCached(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())

	for range 2 {
		_, err := queries.Memo(ctx).GetTrip(ctx, "no-such-trip")
		require.ErrorIs(t, err, sql.ErrNoRows)
	}

	assert.Equal(t, 2, db.count)
}

func TestReadMemo_EmptyResultIsCached(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())

	for range 2 {
		serviceIDs, err := queries.Memo(ctx).GetActiveServiceIDsForDate(ctx, "19990101")
		require.NoError(t, err)
		assert.Empty(t, serviceIDs)
	}

	assert.Equal(t, 1, db.count)
}

// Two callers appending to a shared slice with spare capacity would write
// into the same hidden slot, so cached slices carry no spare capacity.
func TestReadMemo_CachedSlicesHaveNoSpareCapacity(t *testing.T) {
	queries, _ := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())

	stopTimes, err := queries.Memo(ctx).GetStopTimesForTrip(ctx, memoTestTripID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	assert.Equal(t, len(stopTimes), cap(stopTimes))
}

func TestWithReadMemo_KeepsAnInstalledMemo(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())

	_, err := queries.Memo(ctx).GetTrip(ctx, memoTestTripID)
	require.NoError(t, err)
	rewrapped := WithReadMemo(ctx)
	_, err = queries.Memo(rewrapped).GetTrip(rewrapped, memoTestTripID)
	require.NoError(t, err)

	assert.Equal(t, 1, db.count)
}

// TestReadMemo_GetBlockTripSequenceServiceIDSet verifies service ID set canonicalization and error non-caching.
func TestReadMemo_GetBlockTripSequenceServiceIDSet(t *testing.T) {
	queries, db := newMemoTestQueries(t)
	ctx := WithReadMemo(context.Background())
	blockID := sql.NullString{String: memoTestBlockID, Valid: true}

	serviceIDs, err := queries.Memo(ctx).GetActiveServiceIDsForDate(ctx, memoTestDate)
	require.NoError(t, err)
	require.NotEmpty(t, serviceIDs)
	db.count = 0

	first, err := queries.Memo(ctx).GetBlockTripSequence(ctx, GetBlockTripSequenceParams{
		TripID:     memoTestTripID,
		BlockID:    blockID,
		ServiceIds: serviceIDs,
	})
	require.NoError(t, err)

	// Reordered and duplicated service IDs represent the same set and must hit the memo.
	reordered := append([]string{serviceIDs[0]}, serviceIDs...)
	slices.Reverse(reordered)
	second, err := queries.Memo(ctx).GetBlockTripSequence(ctx, GetBlockTripSequenceParams{
		TripID:     memoTestTripID,
		BlockID:    blockID,
		ServiceIds: reordered,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, db.count)
	assert.Equal(t, first, second)

	// Missing rows must not be cached.
	_, err = queries.Memo(ctx).GetBlockTripSequence(ctx, GetBlockTripSequenceParams{
		TripID:     "no-such-trip",
		BlockID:    blockID,
		ServiceIds: serviceIDs,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = queries.Memo(ctx).GetBlockTripSequence(ctx, GetBlockTripSequenceParams{
		TripID:     "no-such-trip",
		BlockID:    blockID,
		ServiceIds: serviceIDs,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	assert.Equal(t, 3, db.count)
}
