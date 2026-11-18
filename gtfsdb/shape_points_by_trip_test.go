package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetShapePointsByTripID_ReturnsTripShapeInSequence(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	rows, err := client.DB.QueryContext(ctx, `
		SELECT lat, lon FROM shapes
		WHERE shape_id = (SELECT shape_id FROM trips WHERE id = ?)
		ORDER BY shape_pt_sequence
	`, memoTestTripID)
	require.NoError(t, err)
	var want []GetShapePointsByTripIDRow
	for rows.Next() {
		var point GetShapePointsByTripIDRow
		require.NoError(t, rows.Scan(&point.Lat, &point.Lon))
		want = append(want, point)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Greater(t, len(want), 1, "fixture trip should have a shape")

	got, err := client.Queries.GetShapePointsByTripID(ctx, memoTestTripID)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestGetShapePointsByTripID_TripWithoutShape(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	_, err := client.DB.ExecContext(ctx, "UPDATE trips SET shape_id = NULL WHERE id = ?", memoTestTripID)
	require.NoError(t, err)

	got, err := client.Queries.GetShapePointsByTripID(ctx, memoTestTripID)
	require.NoError(t, err)
	assert.Empty(t, got)
}
