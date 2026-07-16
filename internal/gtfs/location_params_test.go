package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
)

func TestBoundsFromParams(t *testing.T) {
	t.Run("Radius exceeding max is clamped to MaxSearchRadiusInMeters when clamp=true", func(t *testing.T) {
		largeParams := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: 50000,
		}
		maxParams := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: models.MaxSearchRadiusInMeters,
		}
		assert.Equal(t, BoundsFromParams(maxParams, true), BoundsFromParams(largeParams, true))
		assert.NotEqual(t, BoundsFromParams(maxParams, false), BoundsFromParams(largeParams, false))
	})

	t.Run("Spans exceeding max circle dimensions are clamped when clamp=true", func(t *testing.T) {
		largeParams := &LocationParams{
			Lat:     47.6,
			Lon:     -122.3,
			LatSpan: 15.0,
			LonSpan: 20.0,
		}
		boundsClamped := BoundsFromParams(largeParams, true)
		boundsUnclamped := BoundsFromParams(largeParams, false)
		assert.NotEqual(t, boundsUnclamped, boundsClamped)

		maxCircleBounds := BoundsFromParams(&LocationParams{Lat: 47.6, Lon: -122.3, Radius: models.MaxSearchRadiusInMeters})
		assert.InDelta(t, maxCircleBounds.MaxLat-maxCircleBounds.MinLat, boundsClamped.MaxLat-boundsClamped.MinLat, 0.0001)
		assert.InDelta(t, maxCircleBounds.MaxLon-maxCircleBounds.MinLon, boundsClamped.MaxLon-boundsClamped.MinLon, 0.0001)
	})

	t.Run("Radius takes precedence when both Radius > 0 and Spans > 0 are provided", func(t *testing.T) {
		paramsWithBoth := &LocationParams{
			Lat:     47.6,
			Lon:     -122.3,
			Radius:  1500,
			LatSpan: 10.0,
			LonSpan: 10.0,
		}
		paramsOnlyRadius := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: 1500,
		}
		assert.Equal(t, BoundsFromParams(paramsOnlyRadius), BoundsFromParams(paramsWithBoth))
	})

	t.Run("Zero radius and zero spans defaults to DefaultSearchRadiusInMeters", func(t *testing.T) {
		zeroParams := &LocationParams{
			Lat: 47.6,
			Lon: -122.3,
		}
		defaultParams := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: models.DefaultSearchRadiusInMeters,
		}
		assert.Equal(t, BoundsFromParams(defaultParams), BoundsFromParams(zeroParams))
	})

	t.Run("Normal radius within limits is used", func(t *testing.T) {
		params := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: 1500,
		}
		bounds := BoundsFromParams(params)
		assert.NotNil(t, bounds)
		assert.Less(t, bounds.MinLat, 47.6)
		assert.Greater(t, bounds.MaxLat, 47.6)
	})

	t.Run("Only LatSpan positive falls back to radius calculation", func(t *testing.T) {
		params := &LocationParams{
			Lat:     47.6,
			Lon:     -122.3,
			LatSpan: 0.1,
			LonSpan: 0,
			Radius:  1000,
		}
		expected := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: 1000,
		}
		assert.Equal(t, BoundsFromParams(expected), BoundsFromParams(params))
	})

	t.Run("Only LonSpan positive falls back to radius calculation", func(t *testing.T) {
		params := &LocationParams{
			Lat:     47.6,
			Lon:     -122.3,
			LatSpan: 0,
			LonSpan: 0.1,
			Radius:  1000,
		}
		expected := &LocationParams{
			Lat:    47.6,
			Lon:    -122.3,
			Radius: 1000,
		}
		assert.Equal(t, BoundsFromParams(expected), BoundsFromParams(params))
	})

	t.Run("Normal positive spans calculate bounding box directly", func(t *testing.T) {
		params := &LocationParams{
			Lat:     47.6,
			Lon:     -122.3,
			LatSpan: 0.2,
			LonSpan: 0.4,
		}
		bounds := BoundsFromParams(params)
		assert.InDelta(t, 47.5, bounds.MinLat, 0.0001)
		assert.InDelta(t, 47.7, bounds.MaxLat, 0.0001)
		assert.InDelta(t, -122.5, bounds.MinLon, 0.0001)
		assert.InDelta(t, -122.1, bounds.MaxLon, 0.0001)
	})
}

func TestCheckIfOutOfBounds(t *testing.T) {
	t.Run("Nil or empty regionBounds returns false", func(t *testing.T) {
		manager := &Manager{}
		params := &LocationParams{Lat: 47.6, Lon: -122.3, Radius: 1000}
		assert.False(t, manager.CheckIfOutOfBounds(params))
	})

	t.Run("Search location completely outside all region bounds returns true", func(t *testing.T) {
		manager := &Manager{
			regionBounds: map[string]*RegionBounds{
				"agency1": {Lat: 40.0, Lon: -70.0, LatSpan: 1.0, LonSpan: 1.0},
			},
		}
		params := &LocationParams{Lat: 47.6, Lon: -122.3, Radius: 1000}
		assert.True(t, manager.CheckIfOutOfBounds(params))
	})

	t.Run("Search location overlapping region bounds returns false", func(t *testing.T) {
		manager := &Manager{
			regionBounds: map[string]*RegionBounds{
				"agency1": {Lat: 47.6, Lon: -122.3, LatSpan: 1.0, LonSpan: 1.0},
			},
		}
		params := &LocationParams{Lat: 47.6, Lon: -122.3, Radius: 1000}
		assert.False(t, manager.CheckIfOutOfBounds(params))
	})
}
