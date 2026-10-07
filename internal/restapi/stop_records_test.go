package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

func TestStopsForRouteStopRecords(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	seedStopRecordFixture(t, api)

	_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/s1547_r1.json?key=TEST")
	stops := stopsByID(model.Data.References.Stops)

	nullStop := stops["s1547_nullstop"]
	assert.Equal(t, "nullstop", nullStop.Code)
	assert.Equal(t, "s1547_servedstation", nullStop.Parent)

	emptyStop := stops["s1547_emptystop"]
	assert.Equal(t, "emptystop", emptyStop.Code)
	assert.Equal(t, "s1547_emptystation", emptyStop.Parent)

	keptStop := stops["s1547_keptstop"]
	assert.Equal(t, "PLAT", keptStop.Code)
	assert.Empty(t, keptStop.Parent)

	served := stops["s1547_servedstation"]
	assert.Equal(t, "servedstation", served.Code)
	assert.Equal(t, 1, served.LocationType)
	assert.Equal(t, []string{"s1547_stationroute"}, served.RouteIDs)
	assert.Equal(t, served.RouteIDs, served.StaticRouteIDs)

	emptyStation := stops["s1547_emptystation"]
	assert.Equal(t, "emptystation", emptyStation.Code)
	assert.Empty(t, emptyStation.RouteIDs)
	assert.NotNil(t, emptyStation.RouteIDs)
	assert.NotNil(t, emptyStation.StaticRouteIDs)

	_, suppressed := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/s1547_r1.json?key=TEST&includeReferences=false")
	assert.Empty(t, suppressed.Data.References.Stops)
	assert.NotEmpty(t, suppressed.Data.Entry.StopIds)
}

func TestScheduleForStopStopRecords(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	seedStopRecordFixture(t, api)

	tests := []struct {
		name       string
		stopID     string
		wantCode   string
		wantParent string
		parentID   string
	}{
		{
			name:       "null code falls back to the raw stop id",
			stopID:     "nullstop",
			wantCode:   "nullstop",
			wantParent: "s1547_servedstation",
			parentID:   "s1547_servedstation",
		},
		{
			name:       "empty code falls back to the raw stop id",
			stopID:     "emptystop",
			wantCode:   "emptystop",
			wantParent: "s1547_emptystation",
			parentID:   "s1547_emptystation",
		},
		{
			name:     "non-empty code is preserved",
			stopID:   "keptstop",
			wantCode: "PLAT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, model := callAPIHandler[scheduleStopResponse](t, api,
				"/api/where/schedule-for-stop/s1547_"+tt.stopID+".json?key=TEST")
			require.Equal(t, http.StatusOK, model.Code)
			stops := stopsByID(model.Data.References.Stops)
			got := stops["s1547_"+tt.stopID]
			assert.Equal(t, tt.wantCode, got.Code)
			assert.Equal(t, tt.wantParent, got.Parent)
			if tt.parentID == "" {
				return
			}
			parent := stops[tt.parentID]
			assert.Equal(t, parent.ID, got.Parent)
			assert.Equal(t, parent.RouteIDs, parent.StaticRouteIDs)
			if tt.parentID == "s1547_servedstation" {
				assert.Equal(t, []string{"s1547_stationroute"}, parent.RouteIDs)
				assert.Equal(t, "servedstation", parent.Code)
			} else {
				assert.Empty(t, parent.RouteIDs)
				assert.NotNil(t, parent.RouteIDs)
			}
		})
	}

	_, suppressed := callAPIHandler[scheduleStopResponse](t, api,
		"/api/where/schedule-for-stop/s1547_nullstop.json?key=TEST&includeReferences=false")
	assert.Empty(t, suppressed.Data.References.Stops)
}

type scheduleStopResponse struct {
	Code int `json:"code"`
	Data struct {
		References models.ReferencesModel `json:"references"`
	} `json:"data"`
}

func stopsByID(stops []models.Stop) map[string]models.Stop {
	byID := make(map[string]models.Stop, len(stops))
	for _, stop := range stops {
		byID[stop.ID] = stop
	}
	return byID
}

func seedStopRecordFixture(t *testing.T, api *RestAPI) {
	t.Helper()
	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: "s1547", Name: "Stop Record Transit", Url: "http://example.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: "servedstation", Name: nulls.String("Served Station"), Lat: 47.6, Lon: -122.3,
		LocationType: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: "emptystation", Name: nulls.String("Empty Station"), Lat: 47.61, Lon: -122.31,
		LocationType: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: "nullstop", Name: nulls.String("Null Code"), Lat: 47.62, Lon: -122.32,
		ParentStation: nulls.String("servedstation"),
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: "emptystop", Name: nulls.String("Empty Code"), Lat: 47.63, Lon: -122.33,
		Code:          sql.NullString{String: "", Valid: true},
		ParentStation: nulls.String("emptystation"),
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: "keptstop", Name: nulls.String("Kept Code"), Lat: 47.64, Lon: -122.34,
		Code: nulls.String("PLAT"),
	})
	require.NoError(t, err)

	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: "r1", AgencyID: "s1547", ShortName: nulls.String("R1"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: "stationroute", AgencyID: "s1547", ShortName: nulls.String("ST"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "svc1547", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)

	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{ID: "trip1547", RouteID: "r1", ServiceID: "svc1547"})
	require.NoError(t, err)
	for i, stopID := range []string{"nullstop", "emptystop", "keptstop"} {
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: "trip1547", StopID: stopID, StopSequence: int64(i + 1),
			ArrivalTime: int64(36000 + i*60), DepartureTime: int64(36030 + i*60),
		})
		require.NoError(t, err)
	}

	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{ID: "stationtrip", RouteID: "stationroute", ServiceID: "svc1547"})
	require.NoError(t, err)
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: "stationtrip", StopID: "servedstation", StopSequence: 1,
		ArrivalTime: 37000, DepartureTime: 37030,
	})
	require.NoError(t, err)
}
