package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"slices"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestInsertInBatchesBoundaries inserts row counts around a batch size of 3, so
// full batches (through the shared prepared statement), a final partial
// batch, and an exact multiple with nothing left over are all covered.
func TestInsertInBatchesBoundaries(t *testing.T) {
	const batchSize = 3
	for _, rowCount := range []int{0, 1, 3, 4, 6, 7} {
		t.Run(fmt.Sprintf("%d rows", rowCount), func(t *testing.T) {
			client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			_, err = client.DB.Exec(`PRAGMA foreign_keys = OFF`)
			require.NoError(t, err)

			rows := make([]CreateStopTimeParams, rowCount)
			for i := range rows {
				rows[i] = CreateStopTimeParams{TripID: "trip", StopID: "stop", StopSequence: int64(i), ArrivalTime: int64(i), DepartureTime: int64(i)}
			}

			var inserted int
			err = client.withTransaction(context.Background(), nil, "test", func(tx *sql.Tx) error {
				inserted, err = insertInBatches(context.Background(), tx, batchSize, stopTimeInsert, slices.Values(rows))
				return err
			})
			require.NoError(t, err)
			assert.Equal(t, rowCount, inserted)

			var sequences []int64
			result, err := client.DB.Query(`SELECT stop_sequence FROM stop_times ORDER BY rowid`)
			require.NoError(t, err)
			defer func() { _ = result.Close() }()
			for result.Next() {
				var sequence int64
				require.NoError(t, result.Scan(&sequence))
				sequences = append(sequences, sequence)
			}
			require.NoError(t, result.Err())
			for i, sequence := range sequences {
				assert.Equal(t, int64(i), sequence, "rows must be inserted in the order they arrive")
			}
			assert.Len(t, sequences, rowCount)
		})
	}
}

func TestInsertInBatchesRejectsBatchSizeBelowOne(t *testing.T) {
	for _, batchSize := range []int{-1, 0} {
		t.Run(fmt.Sprintf("batch size %d", batchSize), func(t *testing.T) {
			_, err := insertInBatches(context.Background(), nil, batchSize, stopTimeInsert, slices.Values([]CreateStopTimeParams{{}}))
			assert.ErrorContains(t, err, fmt.Sprintf("must be at least 1, got %d", batchSize))
		})
	}
}

// TestInsertInBatchesReportsStatementErrors points a descriptor at a missing
// table, so the full-batch prepare and the partial-batch exec both fail.
func TestInsertInBatchesReportsStatementErrors(t *testing.T) {
	missingTable := shapeInsert
	missingTable.table = "no_such_table"
	tests := []struct {
		name    string
		rows    []CreateShapeParams
		wantErr string
	}{
		{name: "prepare fails on a full batch", rows: []CreateShapeParams{shapeRow("A", 0), shapeRow("A", 1)}, wantErr: "failed to prepare no_such_table batch"},
		{name: "exec fails on a partial batch", rows: []CreateShapeParams{shapeRow("A", 0)}, wantErr: "failed to insert no_such_table batch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inserted, err := insertShapesInBatches(t, context.Background(), missingTable, slices.Values(tt.rows))
			assert.ErrorContains(t, err, tt.wantErr)
			assert.Zero(t, inserted)
		})
	}
}

// TestInsertInBatchesStopsAtFailedBatch makes the second batch break
// UNIQUE(shape_id, shape_pt_sequence) and checks that no later rows are read.
func TestInsertInBatchesStopsAtFailedBatch(t *testing.T) {
	rows := []CreateShapeParams{
		shapeRow("A", 0), shapeRow("A", 1),
		shapeRow("B", 0), shapeRow("B", 0),
		shapeRow("C", 0), shapeRow("C", 1),
	}
	var yields int
	inserted, err := insertShapesInBatches(t, context.Background(), shapeInsert, countYields(slices.Values(rows), &yields))
	assert.ErrorContains(t, err, "failed to insert shapes batch")
	assert.Equal(t, testBatchSize, inserted, "only the first batch is inserted")
	assert.Equal(t, 2*testBatchSize, yields, "rows after the failed batch must not be read")
}

// TestInsertInBatchesHonorsCancellation cancels the context before the import
// or while a row is read, and checks that the next flush stops the import.
func TestInsertInBatchesHonorsCancellation(t *testing.T) {
	const rowCount = 6
	tests := []struct {
		name         string
		cancelAtRow  int // 1-based row whose read cancels the context; 0 cancels before the first row
		wantInserted int
		wantYields   int
	}{
		{name: "cancelled before the first row", cancelAtRow: 0, wantInserted: 0, wantYields: testBatchSize},
		{name: "cancelled while reading the third row", cancelAtRow: 3, wantInserted: testBatchSize, wantYields: 2 * testBatchSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelAtRow == 0 {
				cancel()
			}
			var yields int
			rows := func(yield func(CreateShapeParams) bool) {
				for i := range rowCount {
					yields++
					if yields == tt.cancelAtRow {
						cancel()
					}
					if !yield(shapeRow("A", int64(i))) {
						return
					}
				}
			}

			inserted, err := insertShapesInBatches(t, ctx, shapeInsert, rows)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, tt.wantInserted, inserted)
			assert.Equal(t, tt.wantYields, yields, "the import must stop at the first flush after cancellation")
		})
	}
}

// testBatchSize is the batch size the shape error tests insert with.
const testBatchSize = 2

// insertShapesInBatches runs insertInBatches with testBatchSize in a
// transaction on a new in-memory database, which is rolled back on error.
// rows is an iter.Seq.
// The transaction starts with a background context, so a cancelled ctx is
// seen by insertInBatches and not by BeginTx.
func insertShapesInBatches(t *testing.T, ctx context.Context, insert batchInsert[CreateShapeParams], rows func(yield func(CreateShapeParams) bool)) (int, error) {
	t.Helper()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	var inserted int
	err = client.withTransaction(context.Background(), nil, "test", func(tx *sql.Tx) error {
		var insertErr error
		inserted, insertErr = insertInBatches(ctx, tx, testBatchSize, insert, rows)
		return insertErr
	})
	return inserted, err
}

// shapeRow returns a shape point row with the given shape ID and sequence.
func shapeRow(shapeID string, sequence int64) CreateShapeParams {
	return CreateShapeParams{ShapeID: shapeID, ShapePtSequence: sequence}
}

// countYields wraps the rows sequence and adds one to *count for each row it
// yields.
func countYields[T any](rows func(yield func(T) bool), count *int) func(yield func(T) bool) {
	return func(yield func(T) bool) {
		for row := range rows {
			*count++
			if !yield(row) {
				return
			}
		}
	}
}

// TestInsertInBatchesShapes runs shapes through full batches (via the shared
// prepared statement) and a partial one.
func TestInsertInBatchesShapes(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	shapes := []gtfs.Shape{
		{ID: "A", Points: []gtfs.ShapePoint{{Latitude: 1}, {Latitude: 2}, {Latitude: 3}}},
		{ID: "B", Points: []gtfs.ShapePoint{{Latitude: 4}, {Latitude: 5}}},
	}
	var inserted int
	err = client.withTransaction(context.Background(), nil, "test", func(tx *sql.Tx) error {
		inserted, err = insertInBatches(context.Background(), tx, 2, shapeInsert, shapePoints(shapes))
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, 5, inserted)

	points, err := client.Queries.GetShapePointsWithDistance(context.Background(), "B")
	require.NoError(t, err)
	require.Len(t, points, 2)
	assert.Equal(t, int64(0), points[0].ShapePtSequence, "numbering restarts for each shape")
	assert.Equal(t, 5.0, points[1].Lat)
}

// shapePoints converts shapes to rows, numbering each shape's points from 0.
func shapePoints(shapes []gtfs.Shape) iter.Seq[CreateShapeParams] {
	return func(yield func(CreateShapeParams) bool) {
		for _, shape := range shapes {
			for idx, point := range shape.Points {
				if !yield(shapePointParams(shape.ID, idx, point)) {
					return
				}
			}
		}
	}
}
