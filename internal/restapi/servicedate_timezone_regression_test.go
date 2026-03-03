package restapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tzTestData holds IDs and config for a timezone regression test scenario.
type tzTestData struct {
	AgencyID  string
	RouteID   string
	TripID1   string // earlier trip in block
	TripID2   string // later trip (target)
	StopID    string
	ServiceID string
	BlockID   string
	Timezone  string
}

// setupTzTestGTFS creates a minimal GTFS dataset in the test DB:
// one agency, one route, one stop, one calendar, two trips in a block, and stop times.
// The calendar activeDays is a [7]int (Mon-Sun) so the test can control which day is active.
func setupTzTestGTFS(t *testing.T, queries *gtfsdb.Queries, td tzTestData, activeDays [7]int) {
	t.Helper()
	ctx := context.Background()

	_, err := queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: td.AgencyID, Name: td.AgencyID, Url: "http://test.example.com", Timezone: td.Timezone,
	})
	require.NoError(t, err)

	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: td.RouteID, AgencyID: td.AgencyID,
		ShortName: sql.NullString{String: "TZ", Valid: true},
		LongName:  sql.NullString{String: "TZ Route", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: td.StopID, Name: sql.NullString{String: "TZ Stop", Valid: true},
		Lat: -36.8485, Lon: 174.7633,
	})
	require.NoError(t, err)

	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:        td.ServiceID,
		Monday:    int64(activeDays[0]),
		Tuesday:   int64(activeDays[1]),
		Wednesday: int64(activeDays[2]),
		Thursday:  int64(activeDays[3]),
		Friday:    int64(activeDays[4]),
		Saturday:  int64(activeDays[5]),
		Sunday:    int64(activeDays[6]),
		StartDate: "20240101",
		EndDate:   "20241231",
	})
	require.NoError(t, err)

	// Trip 1: departs 06:00 (earlier in block)
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: td.TripID1, RouteID: td.RouteID, ServiceID: td.ServiceID,
		TripHeadsign: sql.NullString{String: "Early", Valid: true},
		BlockID:      sql.NullString{String: td.BlockID, Valid: true},
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: td.TripID1, StopID: td.StopID, StopSequence: 1,
		ArrivalTime: 6 * 3600 * int64(time.Second), DepartureTime: 6 * 3600 * int64(time.Second),
	})
	require.NoError(t, err)

	// Trip 2: departs 09:00 (later in block, our target)
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: td.TripID2, RouteID: td.RouteID, ServiceID: td.ServiceID,
		TripHeadsign: sql.NullString{String: "Late", Valid: true},
		BlockID:      sql.NullString{String: td.BlockID, Valid: true},
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: td.TripID2, StopID: td.StopID, StopSequence: 1,
		ArrivalTime: 9 * 3600 * int64(time.Second), DepartureTime: 9 * 3600 * int64(time.Second),
	})
	require.NoError(t, err)
}

// serveAndGet starts the API server and GETs the endpoint, returning the decoded response.
func serveAndGet(t *testing.T, api *RestAPI, endpoint string) (int, models.ResponseModel) {
	t.Helper()
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&model))
	return resp.StatusCode, model
}

// TestServiceDateTimezoneRegression_ArrivalDeparture verifies that the
// arrival-and-departure-for-stop endpoint uses the agency's local date
// (not UTC) when computing scheduledArrivalTime and serviceDateMillis.
//
// Scenario: Pacific/Auckland (UTC+13). The chosen instant is Friday 00:30 NZDT
// which is still Thursday in UTC. Without timezone localization the handler
// would compute stop times using the wrong calendar day.
func TestServiceDateTimezoneRegression_ArrivalDeparture(t *testing.T) {
	td := tzTestData{
		AgencyID: "TzAD", RouteID: "TzADR", TripID1: "TzADT1", TripID2: "TzADT2",
		StopID: "TzADS", ServiceID: "TzADSvc", BlockID: "TzADB", Timezone: "Pacific/Auckland",
	}

	loc, err := time.LoadLocation(td.Timezone)
	require.NoError(t, err)

	// Friday 2024-03-15 00:30 NZDT = Thursday 2024-03-14 11:30 UTC
	localTime := time.Date(2024, 3, 15, 0, 30, 0, 0, loc)
	require.Equal(t, 14, localTime.UTC().Day(), "precondition: UTC day should be 14")
	require.Equal(t, 15, localTime.Day(), "precondition: local day should be 15")

	mockClock := clock.NewMockClock(localTime)
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	// All days active so the arrival lookup succeeds regardless of date
	allDays := [7]int{1, 1, 1, 1, 1, 1, 1}
	setupTzTestGTFS(t, api.GtfsManager.GtfsDB.Queries, td, allDays)

	// Trip1 has arrival at 06:00 (set by setupTzTestGTFS)
	arrivalNs := int64(6 * 3600 * int64(time.Second))

	combinedStop := utils.FormCombinedID(td.AgencyID, td.StopID)
	combinedTrip := utils.FormCombinedID(td.AgencyID, td.TripID1)
	endpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d",
		combinedStop, combinedTrip, localTime.UnixMilli(),
	)

	code, model := serveAndGet(t, api, endpoint)
	require.Equal(t, http.StatusOK, code)

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})

	// scheduledArrivalTime should use local midnight (March 15 NZDT), not UTC (March 14)
	expectedMidnight := time.Date(2024, 3, 15, 0, 0, 0, 0, loc)
	expectedArrivalMs := expectedMidnight.Add(time.Duration(arrivalNs)).UnixMilli()
	actualArrivalMs := int64(entry["scheduledArrivalTime"].(float64))
	assert.Equal(t, expectedArrivalMs, actualArrivalMs, "arrival should use local date, not UTC")

	// serviceDate in response should be midnight of local date
	responseSdMs := int64(entry["serviceDate"].(float64))
	assert.Equal(t, expectedMidnight.UnixMilli(), responseSdMs, "serviceDate should be local midnight")
}

// TestServiceDateTimezoneRegression_BlockTripSequence uses a table-driven approach
// to test both positive (Auckland UTC+13) and negative (Hawaii UTC-10) offsets.
//
// The calendar is active ONLY on the local day. If the handler uses the UTC date
// instead of the local date, no services match and blockTripSequence returns 0.
func TestServiceDateTimezoneRegression_BlockTripSequence(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		timezone  string
		localTime time.Time // chosen so UTC date differs from local date
		activeDay int       // 0=Mon ... 6=Sun — the LOCAL day of week
	}{
		{
			name:     "positive offset (Auckland UTC+13)",
			prefix:   "TzBTS1",
			timezone: "Pacific/Auckland",
			// Fri 2024-03-15 00:30 NZDT = Thu 2024-03-14 11:30 UTC
			localTime: func() time.Time {
				loc, _ := time.LoadLocation("Pacific/Auckland")
				return time.Date(2024, 3, 15, 0, 30, 0, 0, loc)
			}(),
			activeDay: 4, // Friday
		},
		{
			name:     "negative offset (Hawaii UTC-10)",
			prefix:   "TzBTS2",
			timezone: "US/Hawaii",
			// Thu 2024-03-14 22:30 HST = Fri 2024-03-15 08:30 UTC
			localTime: func() time.Time {
				loc, _ := time.LoadLocation("US/Hawaii")
				return time.Date(2024, 3, 14, 22, 30, 0, 0, loc)
			}(),
			activeDay: 3, // Thursday
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			td := tzTestData{
				AgencyID: tc.prefix + "A", RouteID: tc.prefix + "R",
				TripID1: tc.prefix + "T1", TripID2: tc.prefix + "T2",
				StopID: tc.prefix + "S", ServiceID: tc.prefix + "Svc",
				BlockID: tc.prefix + "B", Timezone: tc.timezone,
			}

			// Calendar active only on the local day
			var days [7]int
			days[tc.activeDay] = 1

			mockClock := clock.NewMockClock(tc.localTime)
			api := createTestApiWithClock(t, mockClock)
			defer api.Shutdown()

			setupTzTestGTFS(t, api.GtfsManager.GtfsDB.Queries, td, days)

			combinedTrip := utils.FormCombinedID(td.AgencyID, td.TripID2)
			endpoint := fmt.Sprintf(
				"/api/where/trip-details/%s.json?key=TEST&serviceDate=%d&includeStatus=true",
				combinedTrip, tc.localTime.UnixMilli(),
			)

			code, model := serveAndGet(t, api, endpoint)
			require.Equal(t, http.StatusOK, code)

			data := model.Data.(map[string]interface{})
			entry := data["entry"].(map[string]interface{})
			status := entry["status"].(map[string]interface{})

			blockTripSeq := int(status["blockTripSequence"].(float64))
			assert.Equal(t, 1, blockTripSeq,
				"blockTripSequence should be 1; got 0 means handler used UTC date instead of local")
		})
	}
}
