package restapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

type strictValidationResponse struct {
	Code int `json:"code"`
	Data struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	} `json:"data"`
}

func TestStrictIncludeReferencesAcrossHandlers(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	endpoints := []string{
		"route/1_any", "trips-for-location", "schedule-for-stop/1_any", "vehicles-for-agency/unknown",
		"stops-for-location", "routes-for-agency/unknown", "search/route", "agencies-with-coverage",
		"search/stop", "trip-details/1_any", "trip/1_any", "stops-for-route/1_any", "stop/1_any",
		"routes-for-location", "trip-for-vehicle/1_any", "trips-for-route/1_any",
	}
	for _, endpoint := range endpoints {
		for _, value := range []string{"1", "0", "t", "f", "garbage", " true "} {
			t.Run(endpoint+"/"+value, func(t *testing.T) {
				resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/"+endpoint+".json?key=TEST&lat=0&lon=0&includeReferences="+url.QueryEscape(value))
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				require.NotEmpty(t, model.Data.FieldErrors["includeReferences"])
			})
		}
	}
	for _, value := range []string{"", "true", "false", "TrUe", "FaLsE"} {
		req := httptest.NewRequest("GET", "/?includeReferences="+value, nil)
		got, errors := ShouldIncludeReferences(req, map[string][]string{"existing": {"error"}})
		assert.Equal(t, value != "false" && value != "FaLsE", got)
		assert.Equal(t, map[string][]string{"existing": {"error"}}, errors)
	}
}

func TestStrictProblemReports(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, kind := range []string{"stop", "trip"} {
		table := "problem_reports_" + kind
		filter := " WHERE " + kind + "_id = 'strict-validation'"
		t.Cleanup(func() {
			_, err := api.GtfsManager.GtfsDB.DB.Exec("DELETE FROM " + table + filter)
			assert.NoError(t, err)
		})
		endpoint := "/api/where/report-problem-with-" + kind + "/1_strict-validation.json?key=TEST"
		for _, field := range []string{"userLat", "userLon", "userLocationAccuracy"} {
			for _, raw := range []string{"garbage", "12x", "NaN", "Inf", "-Inf", "1e999"} {
				t.Run(kind+"/"+field+"/"+raw, func(t *testing.T) {
					resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&"+field+"="+url.QueryEscape(raw))
					require.Equal(t, 400, resp.StatusCode)
					assert.NotEmpty(t, model.Data.FieldErrors[field])
				})
			}
		}
		if kind == "trip" {
			for _, raw := range []string{"1", "0", "t", "f", "bad"} {
				resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&userOnVehicle="+raw)
				require.Equal(t, 400, resp.StatusCode)
				assert.NotEmpty(t, model.Data.FieldErrors["userOnVehicle"])
			}
		}
		var count int
		require.NoError(t, api.GtfsManager.GtfsDB.DB.QueryRow("SELECT count(*) FROM "+table+filter).Scan(&count))
		require.Zero(t, count, "rejected reports must not be stored")
		for _, params := range []string{"", "&userLat=&userLon=&userLocationAccuracy=&userOnVehicle=", "&userLat=0&userLon=0&userLocationAccuracy=0&userOnVehicle=FaLsE"} {
			resp, _ := callAPIHandler[EmptyResponse](t, api, endpoint+params)
			require.Equal(t, 200, resp.StatusCode)
		}
		columns := "user_lat, user_lon, user_location_accuracy"
		if kind == "trip" {
			columns += ", user_on_vehicle"
		}
		rows, err := api.GtfsManager.GtfsDB.DB.Query("SELECT " + columns + " FROM " + table + filter + " ORDER BY id")
		require.NoError(t, err)
		index := 0
		for rows.Next() {
			var lat, lon, accuracy sql.NullFloat64
			var onVehicle sql.NullInt64
			dest := []any{&lat, &lon, &accuracy}
			if kind == "trip" {
				dest = append(dest, &onVehicle)
			}
			require.NoError(t, rows.Scan(dest...))
			for _, value := range []sql.NullFloat64{lat, lon, accuracy} {
				assert.Equal(t, index == 2, value.Valid)
				assert.Zero(t, value.Float64)
			}
			if kind == "trip" {
				assert.Equal(t, index == 2, onVehicle.Valid)
				assert.Zero(t, onVehicle.Int64)
			}
			index++
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.Equal(t, 3, index)
	}
	resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/report-problem-with-trip/1_any.json?key=TEST&userLat=NaN&userLon=bad&userLocationAccuracy=Inf&userOnVehicle=1")
	require.Equal(t, 400, resp.StatusCode)
	assert.Len(t, model.Data.FieldErrors, 4)
}

func TestStrictEndpointParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	tests := []struct{ endpoint, field, value string }{
		{"stops-for-location", "routeType", "3junk"}, {"stops-for-location", "routeType", "9223372036854775808"},
		{"stops-for-route/1_any", "includePolylines", "1"},
	}
	for _, field := range []string{"offset", "maxCount", "limit"} {
		for _, value := range []string{"bad", "12x", "9223372036854775808", "-1"} {
			tests = append(tests, struct{ endpoint, field, value string }{"agencies-with-coverage", field, value})
		}
	}
	for _, value := range []string{"bad", "12x", "-1", "9223372036854775808", "9223372037"} {
		tests = append(tests, struct{ endpoint, field, value string }{"vehicles-for-agency/unknown", "ageInSeconds", value})
	}
	for _, endpoint := range []string{"trips-for-location", "trips-for-route/1_any"} {
		for _, field := range []string{"includeTrip", "includeSchedule", "includeStatus"} {
			tests = append(tests, struct{ endpoint, field, value string }{endpoint, field, "t"})
		}
	}
	for _, endpoint := range []string{"stops-for-location", "trips-for-location", "trips-for-route/1_any", "vehicles-for-agency/unknown", "stops-for-route/1_any", "trip-details/1_any", "arrivals-and-departures-for-stop/1_any", "arrival-and-departure-for-stop/1_any"} {
		for _, value := range []string{"bad", "-1", "9223372036854775808"} {
			tests = append(tests, struct{ endpoint, field, value string }{endpoint, "time", value})
		}
	}
	for _, tt := range tests {
		t.Run(tt.endpoint+"/"+tt.field+"/"+tt.value, func(t *testing.T) {
			resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/"+tt.endpoint+".json?key=TEST&lat=0&lon=0&"+tt.field+"="+url.QueryEscape(tt.value))
			require.Equal(t, 400, resp.StatusCode)
			assert.NotEmpty(t, model.Data.FieldErrors[tt.field])
		})
	}
	resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&maxCount=1&limit=bad&offset=-1&includeReferences=t")
	require.Equal(t, 400, resp.StatusCode)
	assert.Len(t, model.Data.FieldErrors, 3)
	resp, model = callAPIHandler[strictValidationResponse](t, api, "/api/where/trips-for-location.json?key=TEST&lat=0&lon=0&includeTrip=t&includeSchedule=1&includeStatus=bad&includeReferences=f&time=-1")
	require.Equal(t, 400, resp.StatusCode)
	assert.Len(t, model.Data.FieldErrors, 5)
	for _, endpoint := range []string{"search/route", "search/stop", "routes-for-location"} {
		resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/"+endpoint+".json?key=TEST&lat=0&lon=0&input=test&maxCount=bad&includeReferences=t")
		require.Equal(t, 400, resp.StatusCode)
		assert.Len(t, model.Data.FieldErrors, 2)
	}

}

func TestStrictArrivalParsers(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(time.UnixMilli(123)))
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, raw := range []string{"bad", "12x", "9223372036854775808", "-1"} {
		req := httptest.NewRequest("GET", "/?minutesAfter="+raw+"&minutesBefore="+raw+"&time=-1&serviceDate=-1&stopSequence=12x", nil)
		_, errors := parseArrivalAndDepartureParams(req)
		assert.Len(t, errors, 5)
		_, errors = api.parseArrivalsAndDeparturesParams(req)
		assert.Len(t, errors, 3)
	}
	loc := time.FixedZone("east", 9*3600)
	req := httptest.NewRequest("GET", "/?minutesAfter=9223372036854775807&minutesBefore=9223372036854775807&time=1749855600123&serviceDate=1749855600123&stopSequence=0", nil)
	singular, errors := parseArrivalAndDepartureParams(req, loc)
	require.Empty(t, errors)
	assert.Equal(t, 1440, singular.MinutesAfter)
	assert.Equal(t, 1440, singular.MinutesBefore)
	assert.Equal(t, int64(1749855600123), singular.Time.UnixMilli())
	assert.Equal(t, loc, singular.ServiceDate.Location())
	assert.Equal(t, 0, *singular.StopSequence)
	plural, errors := api.parseArrivalsAndDeparturesParams(req)
	require.Empty(t, errors)
	assert.Equal(t, 24*time.Hour, plural.After)
	assert.Equal(t, 24*time.Hour, plural.Before)
	assert.Equal(t, int64(1749855600123), plural.Time.UnixMilli())
}

func TestTripsForLocationStrictDefaults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, suffix := range []string{"", "&includeTrip=&includeSchedule=&includeStatus=", "&includeTrip=TrUe&includeSchedule=FaLsE&includeStatus=FaLsE"} {
		req := httptest.NewRequest("GET", "/?lat=0&lon=0"+suffix, nil)
		params, errors, err := api.parseAndValidateRequest(req)
		require.NoError(t, err)
		require.Empty(t, errors)
		assert.True(t, params.IncludeTrip)
		assert.False(t, params.IncludeSchedule)
		assert.False(t, params.IncludeStatus)
	}
}

func TestStrictTripDates(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, endpoint := range []string{"trip-details/1_any", "arrival-and-departure-for-stop/1_any"} {
		for _, raw := range []string{"-1", "bad", "12x", "9223372036854775808"} {
			resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/"+endpoint+".json?key=TEST&serviceDate="+raw)
			require.Equal(t, 400, resp.StatusCode)
			assert.NotEmpty(t, model.Data.FieldErrors["serviceDate"])
		}
	}
	loc := time.FixedZone("east", 9*3600)
	for _, query := range []string{"serviceDate=2025-06-14&time=2025-06-14_00-00-00", "serviceDate=1749826800123&time=1749826800123"} {
		params, errors := api.parseTripParams(httptest.NewRequest("GET", "/?"+query, nil), TripParamDefaults{}, loc)
		require.Empty(t, errors)
		assert.Equal(t, "20250614", params.ServiceDate.Format("20060102"))
		assert.Equal(t, loc, params.Time.Location())
		if strings.HasPrefix(query, "serviceDate=1749826800123") {
			assert.Equal(t, int64(1749826800123), params.Time.UnixMilli())
		}
	}
}
