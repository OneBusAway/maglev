package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
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
	_, err := insertInBatches(context.Background(), nil, 0, stopTimeInsert, slices.Values([]CreateStopTimeParams{{}}))
	assert.Error(t, err)
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
		inserted, err = insertInBatches(context.Background(), tx, 2, shapeInsert, shapeRows(shapes))
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

func TestShapeRowsStopsWhenAsked(t *testing.T) {
	shapes := []gtfs.Shape{{ID: "A", Points: make([]gtfs.ShapePoint, 3)}, {ID: "B", Points: make([]gtfs.ShapePoint, 3)}}
	var seen []string
	for row := range shapeRows(shapes) {
		seen = append(seen, fmt.Sprintf("%s/%d", row.ShapeID, row.ShapePtSequence))
		if len(seen) == 4 {
			break
		}
	}
	assert.Equal(t, []string{"A/0", "A/1", "A/2", "B/0"}, seen)
}
