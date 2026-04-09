package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestQueryStopsInBounds_WithRABA(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	// Query a bounding box around the RABA service area (Redding, CA)
	bounds := utils.CoordinateBounds{
		MinLat: 40.50, MaxLat: 40.70,
		MinLon: -122.50, MaxLon: -122.30,
	}

	manager.RLock()
	defer manager.RUnlock()
	results, err := manager.queryStopsInBounds(t.Context(), bounds)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "Should find stops in the RABA service area")

	for _, stop := range results {
		assert.NotEmpty(t, stop.ID, "Stop ID should not be empty")
		assert.LessOrEqual(t, bounds.MinLat, stop.Lat)
		assert.LessOrEqual(t, stop.Lat, bounds.MaxLat)
		assert.LessOrEqual(t, bounds.MinLon, stop.Lon)
		assert.LessOrEqual(t, stop.Lon, bounds.MaxLon)
	}
}

func TestQueryStopsInBounds_SwappedLat(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)
	swappedLat := utils.CoordinateBounds{MinLat: 40.70, MaxLat: 40.50, MinLon: -122.50, MaxLon: -122.30}

	manager.RLock()
	defer manager.RUnlock()
	_, err := manager.queryStopsInBounds(t.Context(), swappedLat)

	require.ErrorContains(t, err, "lat")
}

func TestQueryStopsInBounds_SwappedLon(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)
	swappedLon := utils.CoordinateBounds{MinLat: 40.50, MaxLat: 40.70, MinLon: -122.30, MaxLon: -122.50}

	manager.RLock()
	defer manager.RUnlock()
	_, err := manager.queryStopsInBounds(t.Context(), swappedLon)

	require.ErrorContains(t, err, "lon")
}

func TestQueryStopsInBounds_NoStops(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	// Bounding box in the middle of the ocean
	bounds := utils.CoordinateBounds{
		MinLat: 50.00, MaxLat: 51.00,
		MinLon: -80.00, MaxLon: -79.00,
	}

	manager.RLock()
	defer manager.RUnlock()
	results, err := manager.queryStopsInBounds(t.Context(), bounds)
	require.NoError(t, err)
	assert.Empty(t, results, "Should find no stops outside service area")
}
