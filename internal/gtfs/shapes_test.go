package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

func newTestGtfsDB(t *testing.T) *gtfsdb.Client {
	t.Helper()
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func insertTestStop(t *testing.T, ctx context.Context, client *gtfsdb.Client, id string, lat, lon float64) {
	t.Helper()
	_, err := client.Queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:  id,
		Lat: lat,
		Lon: lon,
	})
	require.NoError(t, err)
}

// insertAgencyRouteTripsStops sets up the full chain: agency → route → trip → stop_times → stops
func insertAgencyRouteTripsStops(t *testing.T, ctx context.Context, client *gtfsdb.Client, agencyID string, stops []stopPoint) {
	t.Helper()
	_, err := client.Queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: agencyID, Url: "http://example.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	// Create calendar entry for service_id FK
	_, _ = client.Queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "svc1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		StartDate: "20250101", EndDate: "20251231",
	})

	routeID := agencyID + "_route1"
	_, err = client.Queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, Type: 3,
	})
	require.NoError(t, err)

	tripID := agencyID + "_trip1"
	_, err = client.Queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: "svc1",
	})
	require.NoError(t, err)

	for i, s := range stops {
		insertTestStop(t, ctx, client, s.id, s.lat, s.lon)
		_, err = client.Queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: s.id, StopSequence: int64(i + 1),
			ArrivalTime: 28800000000000, DepartureTime: 28800000000000,
		})
		require.NoError(t, err)
	}
}

type stopPoint struct {
	id  string
	lat float64
	lon float64
}

func TestComputeRegionBounds(t *testing.T) {
	tests := []struct {
		name     string
		agencies map[string][]stopPoint // agencyID -> stops
		expected map[string]RegionBounds
	}{
		{
			name:     "No Data",
			expected: nil,
		},
		{
			name: "Single Agency",
			agencies: map[string][]stopPoint{
				"agency1": {
					{"stop1", 47.0, -122.0},
					{"stop2", 48.0, -121.0},
				},
			},
			expected: map[string]RegionBounds{
				"agency1": {Lat: 47.5, Lon: -121.5, LatSpan: 1.0, LonSpan: 1.0},
			},
		},
		{
			name: "Multiple Agencies",
			agencies: map[string][]stopPoint{
				"agency1": {
					{"stop1", 47.0, -122.0},
					{"stop2", 48.0, -121.0},
				},
				"agency2": {
					{"stop3", 10.0, 20.0},
					{"stop4", 12.0, 22.0},
				},
			},
			expected: map[string]RegionBounds{
				"agency1": {Lat: 47.5, Lon: -121.5, LatSpan: 1.0, LonSpan: 1.0},
				"agency2": {Lat: 11.0, Lon: 21.0, LatSpan: 2.0, LonSpan: 2.0},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newTestGtfsDB(t)

			for agencyID, stops := range tc.agencies {
				insertAgencyRouteTripsStops(t, ctx, client, agencyID, stops)
			}

			bounds := computeRegionBounds(ctx, client)

			if tc.expected == nil {
				assert.Nil(t, bounds)
				return
			}

			require.NotNil(t, bounds)
			assert.Equal(t, len(tc.expected), len(bounds))

			for agencyID, expectedBounds := range tc.expected {
				actual, ok := bounds[agencyID]
				require.True(t, ok, "missing bounds for agency %s", agencyID)
				assert.InDelta(t, expectedBounds.Lat, actual.Lat, 0.00000001, "Latitude for %s", agencyID)
				assert.InDelta(t, expectedBounds.Lon, actual.Lon, 0.00000001, "Longitude for %s", agencyID)
				assert.InDelta(t, expectedBounds.LatSpan, actual.LatSpan, 0.00000001, "LatSpan for %s", agencyID)
				assert.InDelta(t, expectedBounds.LonSpan, actual.LonSpan, 0.00000001, "LonSpan for %s", agencyID)
			}
		})
	}
}

func TestGetRegionBoundsNil(t *testing.T) {
	manager := &Manager{}
	assert.Nil(t, manager.GetRegionBounds())
}

func TestGetRegionBoundsSet(t *testing.T) {
	manager := &Manager{
		regionBounds: map[string]*RegionBounds{
			"agency1": {Lat: 1.0, Lon: 2.0, LatSpan: 3.0, LonSpan: 4.0},
		},
	}
	result := manager.GetRegionBounds()
	assert.Equal(t, RegionBounds{Lat: 1.0, Lon: 2.0, LatSpan: 3.0, LonSpan: 4.0}, result["agency1"])
}
