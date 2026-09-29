package restapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestStrictBooleanFlags(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, tc := range []struct{ endpoint, flag string }{
		{"trips-for-location", "includeTrip"},
		{"trips-for-location", "includeSchedule"},
		{"trips-for-location", "includeStatus"},
		{"trips-for-route/1_any", "includeTrip"},
		{"trips-for-route/1_any", "includeSchedule"},
		{"trips-for-route/1_any", "includeStatus"},
		{"stops-for-route/1_any", "includePolylines"},
	} {
		for _, raw := range []string{"1", "t", "abc"} {
			t.Run(tc.endpoint+"/"+tc.flag+"/"+raw, func(t *testing.T) {
				endpoint := "/api/where/" + tc.endpoint + ".json?key=TEST&lat=0&lon=0&" + tc.flag + "=" + raw
				resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint)
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.NotEmpty(t, model.Data.FieldErrors[tc.flag])
			})
		}
	}
}

func TestTripsForLocationStrictDefaults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	for _, suffix := range []string{"", "&includeTrip=TrUe&includeSchedule=FaLsE&includeStatus=FaLsE"} {
		req := httptest.NewRequest("GET", "/?lat=0&lon=0"+suffix, nil)
		params, errors, err := api.parseAndValidateRequest(req)
		require.NoError(t, err)
		require.Empty(t, errors)
		assert.True(t, params.IncludeTrip)
		assert.False(t, params.IncludeSchedule)
		assert.False(t, params.IncludeStatus)
	}
	req := httptest.NewRequest("GET", "/?lat=0&lon=0&includeTrip=&includeSchedule=&includeStatus=", nil)
	params, errors, err := api.parseAndValidateRequest(req)
	require.NoError(t, err)
	require.Empty(t, errors)
	assert.False(t, params.IncludeTrip)
	assert.False(t, params.IncludeSchedule)
	assert.False(t, params.IncludeStatus)
}
