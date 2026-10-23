package restapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

// Agency route and stop-id lists must follow routes/stops insertion order
// (rowid), not SQLite's unordered scan order and not alphabetical ID order.
// See https://github.com/OneBusAway/maglev/issues/1661.

func TestAgencyRouteListsPreserveInsertionOrder(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries
	const agencyID = "order-agency"

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Order Agency", Url: "http://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)

	// Non-alphabetical insertion order: zebra, alpha, middle.
	routeIDs := []string{"zebra", "alpha", "middle"}
	for _, id := range routeIDs {
		_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID: id, AgencyID: agencyID, ShortName: nulls.String(id), Type: 3,
		})
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM routes WHERE agency_id = ?`, agencyID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM agencies WHERE id = ?`, agencyID)
	})

	expectedCombined := []string{
		agencyID + "_zebra",
		agencyID + "_alpha",
		agencyID + "_middle",
	}

	resp, idsModel := callAPIHandler[RouteIDsForAgencyResponse](t, api,
		"/api/where/route-ids-for-agency/"+agencyID+".json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, expectedCombined, idsModel.Data.List)

	resp, routesModel := callAPIHandler[RoutesResponse](t, api,
		"/api/where/routes-for-agency/"+agencyID+".json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, routesModel.Data.List, len(expectedCombined))
	gotRouteIDs := make([]string, len(routesModel.Data.List))
	for i, route := range routesModel.Data.List {
		gotRouteIDs[i] = route.ID
	}
	assert.Equal(t, expectedCombined, gotRouteIDs)
}

func TestAgencyStopIDsPreserveStopInsertionOrder(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries
	const (
		agencyID  = "stop-order-agency"
		routeID   = "stop-order-route"
		serviceID = "stop-order-svc"
		tripID    = "stop-order-trip"
	)

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Stop Order Agency", Url: "http://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, ShortName: nulls.String("SO"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: serviceID, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		Saturday: 1, Sunday: 1, StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: serviceID,
	})
	require.NoError(t, err)

	// Non-alphabetical stop insertion order. GetStopIDsForAgency orders by stops.rowid.
	stopIDs := []string{"zzz-stop", "aaa-stop", "mmm-stop"}
	for i, id := range stopIDs {
		_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
			ID: id, Name: nulls.String(id), Lat: 47.0 + float64(i)*0.01, Lon: -122.0,
		})
		require.NoError(t, err)
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: id, StopSequence: int64(i + 1),
			ArrivalTime: int64(i+1) * 3600e9, DepartureTime: int64(i+1)*3600e9 + 60e9,
		})
		require.NoError(t, err)
	}

	rebuildStopAgencyIndex(t, api)

	t.Cleanup(func() {
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM stop_agencies WHERE agency_id = ?`, agencyID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id = ?`, tripID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM trips WHERE id = ?`, tripID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM calendar WHERE id = ?`, serviceID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, routeID)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM stops WHERE id IN ('zzz-stop','aaa-stop','mmm-stop')`)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM agencies WHERE id = ?`, agencyID)
		rebuildStopAgencyIndex(t, api)
	})

	expected := []string{
		agencyID + "_zzz-stop",
		agencyID + "_aaa-stop",
		agencyID + "_mmm-stop",
	}

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(agencyID))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, expected, model.Data.List)
}

func TestAgencyListOrderStableUnderReverseUnorderedSelects(t *testing.T) {
	// Dedicated in-memory DB (MaxOpenConns=1): PRAGMA reverse_unordered_selects
	// is connection-scoped and must not touch the package-shared file DB.
	api := createTestApiWithFeed(t, "../../testdata/raba.zip")

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries
	const (
		agencyID  = "reverse-order-agency"
		routeID   = "reverse-order-route"
		serviceID = "reverse-order-svc"
		tripID    = "reverse-order-trip"
	)

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Reverse Order Agency", Url: "http://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, ShortName: nulls.String("RO"), Type: 3,
	})
	require.NoError(t, err)

	extraRouteIDs := []string{"r-zebra", "r-alpha", "r-middle"}
	for _, id := range extraRouteIDs {
		_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID: id, AgencyID: agencyID, ShortName: nulls.String(id), Type: 3,
		})
		require.NoError(t, err)
	}

	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: serviceID, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		Saturday: 1, Sunday: 1, StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: serviceID,
	})
	require.NoError(t, err)

	stopIDs := []string{"z-stop", "a-stop", "m-stop"}
	for i, id := range stopIDs {
		_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
			ID: id, Name: nulls.String(id), Lat: 48.0 + float64(i)*0.01, Lon: -123.0,
		})
		require.NoError(t, err)
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: id, StopSequence: int64(i + 1),
			ArrivalTime: int64(i+1) * 3600e9, DepartureTime: int64(i+1)*3600e9 + 60e9,
		})
		require.NoError(t, err)
	}

	rebuildStopAgencyIndex(t, api)

	expectedRoutes := []string{
		agencyID + "_" + routeID,
		agencyID + "_r-zebra",
		agencyID + "_r-alpha",
		agencyID + "_r-middle",
	}
	expectedStops := []string{
		agencyID + "_z-stop",
		agencyID + "_a-stop",
		agencyID + "_m-stop",
	}

	assertAgencyLists := func(t *testing.T) {
		t.Helper()
		resp, idsModel := callAPIHandler[RouteIDsForAgencyResponse](t, api,
			"/api/where/route-ids-for-agency/"+agencyID+".json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, expectedRoutes, idsModel.Data.List)

		resp, stopsModel := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(agencyID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, expectedStops, stopsModel.Data.List)
	}

	assertAgencyLists(t)

	_, err = api.GtfsManager.GtfsDB.DB.Exec("PRAGMA reverse_unordered_selects=ON")
	require.NoError(t, err)
	assertAgencyLists(t)
}
