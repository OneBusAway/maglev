package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestGetActiveRoutesWithinBounds_CenteredOnStop checks that a query centered
// exactly on a stop doesn't fail. Rounding can push the distance formula's acos
// argument just above 1 there, which made acos return NULL and the scan fail.
// Stop 4000 (Canby Transfer Center) is one of the affected stops, so its routes
// are checked too.
func TestGetActiveRoutesWithinBounds_CenteredOnStop(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	stops, err := client.Queries.ListStops(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	const boxSpan = 0.0005
	var stop4000RouteIDs []string
	for _, stop := range stops {
		routes, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
			Lat:      stop.Lat,
			Lon:      stop.Lon,
			MinLat:   stop.Lat - boxSpan,
			MaxLat:   stop.Lat + boxSpan,
			MinLon:   stop.Lon - boxSpan,
			MaxLon:   stop.Lon + boxSpan,
			MaxCount: 100,
		})
		assert.NoError(t, err, "query centered on stop %s", stop.ID)
		if stop.ID == "4000" {
			for _, route := range routes {
				stop4000RouteIDs = append(stop4000RouteIDs, route.ID)
			}
		}
	}

	assert.ElementsMatch(t, []string{"15", "154", "159", "160", "44X"}, stop4000RouteIDs)
}

// TestGetActiveRoutesWithinBounds_EmptyRouteIDsMeansNoFilter pins the invariant that
// GetActiveRoutesWithinBoundsParams.RouteIDs being empty means "no filter" rather than
// "match nothing". Callers that derive RouteIDs from a candidate search (e.g.
// Manager.GetRoutesForLocation) must special-case an empty candidate list themselves
// before calling this, or they will silently get every in-bounds route instead of none.
func TestGetActiveRoutesWithinBounds_EmptyRouteIDsMeansNoFilter(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	const (
		centreLat = 40.583321
		centreLon = -122.362535
		boxSpan   = 0.05
	)

	unfiltered, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
		Lat:      centreLat,
		Lon:      centreLon,
		MinLat:   centreLat - boxSpan,
		MaxLat:   centreLat + boxSpan,
		MinLon:   centreLon - boxSpan,
		MaxLon:   centreLon + boxSpan,
		RouteIDs: nil,
		MaxCount: 100,
	})
	require.NoError(t, err)
	require.Greater(t, len(unfiltered), 1, "fixture box should contain more than one route")

	target := unfiltered[0].ID

	filtered, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
		Lat:      centreLat,
		Lon:      centreLon,
		MinLat:   centreLat - boxSpan,
		MaxLat:   centreLat + boxSpan,
		MinLon:   centreLon - boxSpan,
		MaxLon:   centreLon + boxSpan,
		RouteIDs: []string{target},
		MaxCount: 100,
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, target, filtered[0].ID)
}

func TestSpatialQueriesWithinBounds_RTreeRounding(t *testing.T) {
	const (
		minLat      = 40.589311
		maxLat      = 40.590311
		minLon      = -122.354
		maxLon      = -122.353
		interiorLat = 40.589800
		interiorLon = -122.353633
		edgeStep    = 0.000001
	)

	// The step is smaller than RTree's coordinate precision here. Exact
	// boundaries must be included; outside stops guard against overlap alone.
	tests := []struct {
		name                 string
		lat, lon             float64
		inwardLat, inwardLon float64
	}{
		{name: "min_lat", lat: minLat, lon: interiorLon, inwardLat: edgeStep},
		{name: "max_lat", lat: maxLat, lon: interiorLon, inwardLat: -edgeStep},
		{name: "min_lon", lat: interiorLat, lon: minLon, inwardLon: edgeStep},
		{name: "max_lon", lat: interiorLat, lon: maxLon, inwardLon: -edgeStep},
		{name: "southwest_corner", lat: minLat, lon: minLon, inwardLat: edgeStep, inwardLon: edgeStep},
		{name: "southeast_corner", lat: minLat, lon: maxLon, inwardLat: edgeStep, inwardLon: -edgeStep},
		{name: "northwest_corner", lat: maxLat, lon: minLon, inwardLat: -edgeStep, inwardLon: edgeStep},
		{name: "northeast_corner", lat: maxLat, lon: maxLon, inwardLat: -edgeStep, inwardLon: -edgeStep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClientWithSpatialStops(t, []spatialTestStop{
				{id: "inside-edge", lat: tt.lat + tt.inwardLat, lon: tt.lon + tt.inwardLon},
				{id: "on-edge", lat: tt.lat, lon: tt.lon},
				{id: "outside-edge", lat: tt.lat - tt.inwardLat, lon: tt.lon - tt.inwardLon},
				{id: "inside-center", lat: interiorLat, lon: interiorLon},
			})
			wantStopIDs := []string{"inside-edge", "on-edge", "inside-center"}

			t.Run("GetActiveStopsWithinBounds", func(t *testing.T) {
				stops, err := client.Queries.GetActiveStopsWithinBounds(t.Context(), GetActiveStopsWithinBoundsParams{
					MinLat: minLat,
					MaxLat: maxLat,
					MinLon: minLon,
					MaxLon: maxLon,
				})
				require.NoError(t, err)

				var stopIDs []string
				for _, stop := range stops {
					stopIDs = append(stopIDs, stop.ID)
				}
				assert.ElementsMatch(t, wantStopIDs, stopIDs)
			})

			t.Run("GetStopIDsWithinBounds", func(t *testing.T) {
				stopIDs, err := client.Queries.GetStopIDsWithinBounds(t.Context(), GetStopIDsWithinBoundsParams{
					MinLat: minLat,
					MaxLat: maxLat,
					MinLon: minLon,
					MaxLon: maxLon,
				})
				require.NoError(t, err)
				assert.ElementsMatch(t, wantStopIDs, stopIDs)
			})

			t.Run("GetActiveRoutesWithinBounds", func(t *testing.T) {
				routeTests := []struct {
					name     string
					routeIDs []string
					maxCount int
					want     []string
				}{
					{
						name:     "unfiltered",
						maxCount: 10,
						want:     []string{"route-inside-edge", "route-on-edge", "route-inside-center"},
					},
					{
						name:     "inside_edge_filter",
						routeIDs: []string{"route-inside-edge"},
						maxCount: 10,
						want:     []string{"route-inside-edge"},
					},
					{
						name:     "boundary_filter",
						routeIDs: []string{"route-on-edge"},
						maxCount: 10,
						want:     []string{"route-on-edge"},
					},
					{
						name:     "outside_filter",
						routeIDs: []string{"route-outside-edge"},
						maxCount: 10,
					},
					{
						name:     "filter_with_limit",
						routeIDs: []string{"route-inside-edge", "route-outside-edge", "route-inside-center"},
						maxCount: 1,
						want:     []string{"route-inside-center"},
					},
				}
				for _, routeTest := range routeTests {
					t.Run(routeTest.name, func(t *testing.T) {
						routes, err := client.Queries.GetActiveRoutesWithinBounds(t.Context(), GetActiveRoutesWithinBoundsParams{
							Lat:      (minLat + maxLat) / 2,
							Lon:      (minLon + maxLon) / 2,
							MinLat:   minLat,
							MaxLat:   maxLat,
							MinLon:   minLon,
							MaxLon:   maxLon,
							RouteIDs: routeTest.routeIDs,
							MaxCount: routeTest.maxCount,
						})
						require.NoError(t, err)

						var routeIDs []string
						for _, route := range routes {
							routeIDs = append(routeIDs, route.ID)
						}
						assert.ElementsMatch(t, routeTest.want, routeIDs)
					})
				}
			})
		})
	}
}

type spatialTestStop struct {
	id       string
	lat, lon float64
}

func newTestClientWithSpatialStops(t *testing.T, stops []spatialTestStop) *Client {
	t.Helper()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := t.Context()

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('agency', 'Test Agency', 'https://example.com', 'UTC');
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('service', 1, 1, 1, 1, 1, 1, 1, '20260101', '20261231');
	`)
	require.NoError(t, err)

	for _, stop := range stops {
		_, err = client.DB.ExecContext(ctx, "INSERT INTO stops (id, name, lat, lon) VALUES (?, ?, ?, ?)", stop.id, stop.id, stop.lat, stop.lon)
		require.NoError(t, err)
		routeID := "route-" + stop.id
		_, err = client.DB.ExecContext(ctx, "INSERT INTO routes (id, agency_id, type) VALUES (?, 'agency', 3)", routeID)
		require.NoError(t, err)
		tripID := "trip-" + stop.id
		_, err = client.DB.ExecContext(ctx, "INSERT INTO trips (id, route_id, service_id) VALUES (?, ?, 'service')", tripID, routeID)
		require.NoError(t, err)
		_, err = client.DB.ExecContext(ctx, `
			INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
			VALUES (?, ?, 0, 0, 1)
		`, tripID, stop.id)
		require.NoError(t, err)
	}
	return client
}
