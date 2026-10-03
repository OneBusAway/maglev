package restapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

func TestTripAndArrivalEpochValidation(t *testing.T) {
	api := &RestAPI{}
	tests := []struct {
		name      string
		value     string
		wantError bool
		wantEpoch int64
	}{
		{name: "zero", value: "0"},
		{name: "negative zero", value: "-0"},
		{name: "positive", value: "1609459200000", wantEpoch: 1609459200000},
		{name: "negative one", value: "-1", wantError: true},
		{name: "negative five", value: "-5", wantError: true},
		{name: "minimum int64", value: "-9223372036854775808", wantError: true},
		{name: "overflow", value: "9223372036854775808", wantError: true},
		{name: "malformed", value: "1609459200000junk", wantError: true},
	}
	for _, field := range []string{"time", "serviceDate"} {
		for _, tt := range tests {
			request := httptest.NewRequest(http.MethodGet, "/?"+url.Values{field: {tt.value}}.Encode(), nil)
			t.Run("trip/"+field+"/"+tt.name, func(t *testing.T) {
				params, fieldErrors := api.parseTripParams(request, TripParamDefaults{})
				if tt.wantError {
					assert.NotEmpty(t, fieldErrors[field])
					return
				}
				require.Empty(t, fieldErrors)
				parsed := params.Time
				if field == "serviceDate" {
					parsed = params.ServiceDate
				}
				require.NotNil(t, parsed)
				assert.Equal(t, tt.wantEpoch, parsed.UnixMilli())
			})
			t.Run("arrival/"+field+"/"+tt.name, func(t *testing.T) {
				params, fieldErrors := parseArrivalAndDepartureParams(request)
				if tt.wantError {
					assert.NotEmpty(t, fieldErrors[field])
					return
				}
				require.Empty(t, fieldErrors)
				parsed := params.Time
				if field == "serviceDate" {
					parsed = params.ServiceDate
				}
				require.NotNil(t, parsed)
				assert.Equal(t, tt.wantEpoch, parsed.UnixMilli())
			})
		}
	}
}

func TestArrivalsEpochValidation(t *testing.T) {
	api := &RestAPI{Application: &app.Application{Clock: clock.NewMockClock(time.UnixMilli(1609459200000))}}
	for _, raw := range []string{"-1", "-5", "-9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?"+url.Values{"time": {raw}}.Encode(), nil)
			_, fieldErrors := api.parseArrivalsAndDeparturesParams(request)
			assert.NotEmpty(t, fieldErrors["time"])
		})
	}
	for _, raw := range []string{"0", "1609459200000"} {
		t.Run(raw, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?time="+raw, nil)
			params, fieldErrors := api.parseArrivalsAndDeparturesParams(request)
			require.Empty(t, fieldErrors)
			assert.Equal(t, raw, strconv.FormatInt(params.Time.UnixMilli(), 10))
		})
	}
}

func TestNegativeEpochsReturnFieldErrors(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	trip := mustGetTrip(t, api)
	const mockVehicleID = "NEGATIVE_EPOCH_VEHICLE"
	api.GtfsManager.MockAddVehicle(mockVehicleID, trip.ID, trip.RouteID)
	vehicleID := utils.FormCombinedID(mustGetAgencies(t, api)[0].ID, mockVehicleID)
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)
	for _, endpoint := range []struct {
		path   string
		fields []string
	}{
		{path: "trip-details/1_unknown", fields: []string{"time", "serviceDate"}},
		{path: "trip-for-vehicle/" + vehicleID, fields: []string{"time", "serviceDate"}},
		{path: "arrival-and-departure-for-stop/1_unknown", fields: []string{"time", "serviceDate"}},
		{path: "arrivals-and-departures-for-stop/1_unknown", fields: []string{"time"}},
	} {
		for _, field := range endpoint.fields {
			t.Run(endpoint.path+"/"+field, func(t *testing.T) {
				resp, model := callAPIHandler[strictValidationResponse](t, api, "/api/where/"+endpoint.path+".json?key=TEST&"+field+"=-5")
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.Equal(t, http.StatusBadRequest, model.Code)
				assert.NotEmpty(t, model.Data.FieldErrors[field])
			})
		}
	}
}
