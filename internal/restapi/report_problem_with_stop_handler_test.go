package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

func reportProblemWithStopURL(stopID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/report-problem-with-stop/" + stopID + ".json?" + q.Encode()
}

func TestReportProblemWithStopRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/12345.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithStopEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_75403", url.Values{
		"code":                 {"stop_name_wrong"},
		"userComment":          {"Test comment"},
		"userLat":              {"47.6097"},
		"userLon":              {"-122.3331"},
		"userLocationAccuracy": {"10"},
	}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	nullResp, nullModel := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("", url.Values{
		"code": {"stop_name_wrong"},
	}))

	assert.Equal(t, http.StatusBadRequest, nullResp.StatusCode, "Should return 400 when ID is missing")
	assert.Equal(t, http.StatusBadRequest, nullModel.Code)
	assert.Equal(t, "id cannot be empty", nullModel.Text)
}

func TestReportProblemWithStopMinimalParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_75403"))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
}

func TestReportProblemWithStopSanitization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_75403", url.Values{
		"code":    {"stop_name_wrong"},
		"userLat": {"invalid"},
		"userLon": {"not_a_number"},
	}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)

	longComment := strings.Repeat("a", 1000)
	respLong, modelLong := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_75403", url.Values{
		"code":        {"stop_name_wrong"},
		"userComment": {longComment},
	}))

	assert.Equal(t, http.StatusOK, respLong.StatusCode, "Should handle massive user comments gracefully")
	assert.Equal(t, http.StatusOK, modelLong.Code)
}

// TestReportProblemWithStopCoordinateRangeValidation verifies that out-of-range userLat
// and userLon parameters return HTTP 400 with validation errors instead of database 500s.
func TestReportProblemWithStopCoordinateRangeValidation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)

	tests := []struct {
		name           string
		params         url.Values
		expectedErrors map[string]string
	}{
		{name: "userLat too high", params: url.Values{"userLat": {"100"}, "userLon": {"-122.3331"}}, expectedErrors: map[string]string{"userLat": "latitude must be between -90 and 90"}},
		{name: "userLat too low", params: url.Values{"userLat": {"-95"}, "userLon": {"-122.3331"}}, expectedErrors: map[string]string{"userLat": "latitude must be between -90 and 90"}},
		{name: "userLon too high", params: url.Values{"userLat": {"47.6097"}, "userLon": {"200"}}, expectedErrors: map[string]string{"userLon": "longitude must be between -180 and 180"}},
		{name: "userLon too low", params: url.Values{"userLat": {"47.6097"}, "userLon": {"-200"}}, expectedErrors: map[string]string{"userLon": "longitude must be between -180 and 180"}},
		{name: "both coordinates out of range", params: url.Values{"userLat": {"95"}, "userLon": {"190"}}, expectedErrors: map[string]string{
			"userLat": "latitude must be between -90 and 90",
			"userLon": "longitude must be between -180 and 180",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[strictValidationResponse](t, api, reportProblemWithStopURL("1_75403", tt.params))
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			for field, msg := range tt.expectedErrors {
				assert.Contains(t, model.Data.FieldErrors[field], msg)
			}
		})
	}

	for _, bt := range []struct {
		name, lat, lon string
	}{
		{name: "max valid coordinates", lat: "90", lon: "180"},
		{name: "min valid coordinates", lat: "-90", lon: "-180"},
	} {
		t.Run(bt.name, func(t *testing.T) {
			resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_boundary_stop", url.Values{
				"userLat": {bt.lat},
				"userLon": {bt.lon},
			}))
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
		})
	}
}

func TestReportProblemWithStopDatabaseFailure(t *testing.T) {
	api := apiWithClosedDB(t)

	resp, model := callAPIHandler[models.ResponseModel](t, api, reportProblemWithStopURL("1_75403"))
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, http.StatusInternalServerError, model.Code)
	assert.Equal(t, "internal server error", model.Text)
	assert.Equal(t, models.APIVersion, model.Version)
}

func TestReportProblemWithStopNullStorage(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithStopURL("1_75403"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)

	reports, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByStop(t.Context(), "75403")
	require.NoError(t, err)
	require.NotEmpty(t, reports)

	latest := reports[0]
	assert.False(t, latest.Code.Valid, "omitted code should be stored as SQL NULL, not empty string")
	assert.False(t, latest.UserComment.Valid, "omitted userComment should be stored as SQL NULL, not empty string")
}

// TestReportProblemAcceptsQueryID covers the path form and the Android form
// that puts the id in the query string (report-problem-with-stop.json?stopId=
// and report-problem-with-trip.json?tripId=).
func TestReportProblemAcceptsQueryID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)

	stopIDs := []string{"path-stop", "query-stop", "path-wins", "query-ignored"}
	tripIDs := []string{"path-trip", "query-trip", "path-loses", "query-wins"}
	t.Cleanup(func() {
		for _, id := range stopIDs {
			_, err := api.GtfsManager.GtfsDB.DB.Exec(`DELETE FROM problem_reports_stop WHERE stop_id = ?`, id)
			assert.NoError(t, err)
		}
		for _, id := range tripIDs {
			_, err := api.GtfsManager.GtfsDB.DB.Exec(`DELETE FROM problem_reports_trip WHERE trip_id = ?`, id)
			assert.NoError(t, err)
		}
	})

	type response struct {
		Code int    `json:"code"`
		Text string `json:"text"`
		Data struct {
			FieldErrors map[string][]string `json:"fieldErrors"`
		} `json:"data"`
	}

	tests := []struct {
		name           string
		url            string
		wantStatus     int
		wantText       string
		wantField      string
		storedStop     string
		storedTrip     string
		storedTripStop string
	}{
		{
			name:       "stop path id",
			url:        "/api/where/report-problem-with-stop/1_path-stop.json?key=TEST&code=stop_name_wrong",
			wantStatus: http.StatusOK,
			storedStop: "path-stop",
		},
		{
			name:       "stop query id",
			url:        "/api/where/report-problem-with-stop.json?key=TEST&stopId=1_query-stop&code=stop_name_wrong",
			wantStatus: http.StatusOK,
			storedStop: "query-stop",
		},
		{
			name:       "stopId wins over the path id",
			url:        "/api/where/report-problem-with-stop/1_path-wins.json?key=TEST&stopId=1_query-ignored&code=other",
			wantStatus: http.StatusOK,
			storedStop: "query-ignored",
		},
		{
			name:       "stop query id missing",
			url:        "/api/where/report-problem-with-stop.json?key=TEST",
			wantStatus: http.StatusBadRequest,
			wantText:   "id cannot be empty",
			wantField:  "stopId",
		},
		{
			name:       "stop query id malformed",
			url:        "/api/where/report-problem-with-stop.json?key=TEST&stopId=not-combined",
			wantStatus: http.StatusBadRequest,
			wantText:   "invalid format: not-combined",
			wantField:  "stopId",
		},
		{
			name:           "trip path id",
			url:            "/api/where/report-problem-with-trip/1_path-trip.json?key=TEST&code=vehicle_never_came&stopId=1_assoc-stop",
			wantStatus:     http.StatusOK,
			storedTrip:     "path-trip",
			storedTripStop: "1_assoc-stop",
		},
		{
			name:           "trip query id",
			url:            "/api/where/report-problem-with-trip.json?key=TEST&tripId=1_query-trip&stopId=1_assoc-stop&code=vehicle_never_came",
			wantStatus:     http.StatusOK,
			storedTrip:     "query-trip",
			storedTripStop: "1_assoc-stop",
		},
		{
			name:           "tripId wins over the path id",
			url:            "/api/where/report-problem-with-trip/1_path-loses.json?key=TEST&tripId=1_query-wins&stopId=1_assoc-stop&code=vehicle_never_came",
			wantStatus:     http.StatusOK,
			storedTrip:     "query-wins",
			storedTripStop: "1_assoc-stop",
		},
		{
			name:       "trip query id missing",
			url:        "/api/where/report-problem-with-trip.json?key=TEST&stopId=1_assoc-stop",
			wantStatus: http.StatusBadRequest,
			wantText:   "id cannot be empty",
			wantField:  "tripId",
		},
		{
			name:       "trip query id malformed",
			url:        "/api/where/report-problem-with-trip.json?key=TEST&tripId=not-combined",
			wantStatus: http.StatusBadRequest,
			wantText:   "invalid format: not-combined",
			wantField:  "tripId",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, model := callAPIHandler[response](t, api, tc.url)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantStatus, model.Code)
			if tc.wantText != "" {
				assert.Equal(t, tc.wantText, model.Text)
			}
			if tc.wantField != "" {
				assert.NotEmpty(t, model.Data.FieldErrors[tc.wantField])
			}
			if tc.storedStop != "" {
				reports, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByStop(t.Context(), tc.storedStop)
				require.NoError(t, err)
				require.NotEmpty(t, reports)
				assert.Equal(t, tc.storedStop, reports[0].StopID)
			}
			if tc.storedTrip != "" {
				reports, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByTrip(t.Context(), tc.storedTrip)
				require.NoError(t, err)
				require.Len(t, reports, 1)
				assert.Equal(t, tc.storedTrip, reports[0].TripID)
				assert.Equal(t, tc.storedTripStop, reports[0].StopID.String)
			}
		})
	}

	pathStop, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByStop(t.Context(), "path-wins")
	require.NoError(t, err)
	assert.Empty(t, pathStop, "stopId must replace the path id")
	pathTrip, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByTrip(t.Context(), "path-loses")
	require.NoError(t, err)
	assert.Empty(t, pathTrip, "tripId must replace the path id")
}
