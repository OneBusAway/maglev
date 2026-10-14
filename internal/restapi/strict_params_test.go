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
		assert.Equal(t, value == "true" || value == "TrUe", got)
		assert.Equal(t, map[string][]string{"existing": {"error"}}, errors)
	}
}

func TestStrictProblemReportsWithStop(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	const filter = " WHERE stop_id = 'strict-validation'"
	const endpoint = "/api/where/report-problem-with-stop/1_strict-validation.json?key=TEST"
	t.Cleanup(func() {
		_, err := api.GtfsManager.GtfsDB.DB.Exec("DELETE FROM problem_reports_stop" + filter)
		assert.NoError(t, err)
	})

	for _, field := range []string{"userLat", "userLon", "userLocationAccuracy"} {
		for _, raw := range []string{"garbage", "12x", "NaN", "Inf", "-Inf", "1e999"} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&"+field+"="+url.QueryEscape(raw))
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.NotEmpty(t, model.Data.FieldErrors[field])
			})
		}
	}
	var count int
	require.NoError(t, api.GtfsManager.GtfsDB.DB.QueryRow("SELECT count(*) FROM problem_reports_stop"+filter).Scan(&count))
	require.Zero(t, count, "rejected reports must not be stored")

	tests := []struct {
		name         string
		params       string
		wantLat      sql.NullFloat64
		wantLon      sql.NullFloat64
		wantAccuracy sql.NullFloat64
	}{
		{name: "omitted optional fields"},
		{name: "empty optional fields", params: "&userLat=&userLon=&userLocationAccuracy="},
		{
			name:         "explicit zero values",
			params:       "&userLat=0&userLon=0&userLocationAccuracy=0",
			wantLat:      sql.NullFloat64{Float64: 0, Valid: true},
			wantLon:      sql.NullFloat64{Float64: 0, Valid: true},
			wantAccuracy: sql.NullFloat64{Float64: 0, Valid: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := callAPIHandler[EmptyResponse](t, api, endpoint+tt.params)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var lat, lon, accuracy sql.NullFloat64
			err := api.GtfsManager.GtfsDB.DB.QueryRow("SELECT user_lat, user_lon, user_location_accuracy FROM problem_reports_stop"+filter+" ORDER BY id DESC LIMIT 1").Scan(&lat, &lon, &accuracy)
			require.NoError(t, err)
			assert.Equal(t, tt.wantLat, lat)
			assert.Equal(t, tt.wantLon, lon)
			assert.Equal(t, tt.wantAccuracy, accuracy)
		})
	}
	require.NoError(t, api.GtfsManager.GtfsDB.DB.QueryRow("SELECT count(*) FROM problem_reports_stop"+filter).Scan(&count))
	assert.Equal(t, len(tests), count)
}

func TestStrictProblemReportsWithTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	const filter = " WHERE trip_id = 'strict-validation'"
	const endpoint = "/api/where/report-problem-with-trip/1_strict-validation.json?key=TEST"
	t.Cleanup(func() {
		_, err := api.GtfsManager.GtfsDB.DB.Exec("DELETE FROM problem_reports_trip" + filter)
		assert.NoError(t, err)
	})

	for _, field := range []string{"userLat", "userLon", "userLocationAccuracy"} {
		for _, raw := range []string{"garbage", "12x", "NaN", "Inf", "-Inf", "1e999"} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&"+field+"="+url.QueryEscape(raw))
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.NotEmpty(t, model.Data.FieldErrors[field])
			})
		}
	}
	for _, raw := range []string{"1", "0", "t", "f", "bad"} {
		t.Run("userOnVehicle/"+raw, func(t *testing.T) {
			resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&userOnVehicle="+raw)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.NotEmpty(t, model.Data.FieldErrors["userOnVehicle"])
		})
	}
	resp, model := callAPIHandler[strictValidationResponse](t, api, endpoint+"&userLat=NaN&userLon=bad&userLocationAccuracy=Inf&userOnVehicle=1")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Len(t, model.Data.FieldErrors, 4)
	var count int
	require.NoError(t, api.GtfsManager.GtfsDB.DB.QueryRow("SELECT count(*) FROM problem_reports_trip"+filter).Scan(&count))
	require.Zero(t, count, "rejected reports must not be stored")

	tests := []struct {
		name          string
		params        string
		wantLat       sql.NullFloat64
		wantLon       sql.NullFloat64
		wantAccuracy  sql.NullFloat64
		wantOnVehicle sql.NullInt64
	}{
		{name: "omitted optional fields"},
		{name: "empty optional fields", params: "&userLat=&userLon=&userLocationAccuracy=&userOnVehicle="},
		{
			name:          "explicit zero/false values",
			params:        "&userLat=0&userLon=0&userLocationAccuracy=0&userOnVehicle=FaLsE",
			wantLat:       sql.NullFloat64{Float64: 0, Valid: true},
			wantLon:       sql.NullFloat64{Float64: 0, Valid: true},
			wantAccuracy:  sql.NullFloat64{Float64: 0, Valid: true},
			wantOnVehicle: sql.NullInt64{Int64: 0, Valid: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := callAPIHandler[EmptyResponse](t, api, endpoint+tt.params)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var lat, lon, accuracy sql.NullFloat64
			var onVehicle sql.NullInt64
			err := api.GtfsManager.GtfsDB.DB.QueryRow("SELECT user_lat, user_lon, user_location_accuracy, user_on_vehicle FROM problem_reports_trip"+filter+" ORDER BY id DESC LIMIT 1").Scan(&lat, &lon, &accuracy, &onVehicle)
			require.NoError(t, err)
			assert.Equal(t, tt.wantLat, lat)
			assert.Equal(t, tt.wantLon, lon)
			assert.Equal(t, tt.wantAccuracy, accuracy)
			assert.Equal(t, tt.wantOnVehicle, onVehicle)
		})
	}
	require.NoError(t, api.GtfsManager.GtfsDB.DB.QueryRow("SELECT count(*) FROM problem_reports_trip"+filter).Scan(&count))
	assert.Equal(t, len(tests), count)
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
