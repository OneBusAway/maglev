package restapi

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// A vehicleId whose prefix matches the trip's agency is still 404 when the
// vehicle was attributed to a different agency at ingestion.
func TestTripDetailsHandler_VehicleAttributedAgency(t *testing.T) {
	api := createTestApi(t)
	t.Cleanup(api.Shutdown)
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	const vehicleID = "ATTRIBUTED_VEHICLE"
	trip := mustGetTrip(t, api)
	vehicles := []gtfs.Vehicle{{
		ID:   &gtfs.VehicleID{ID: vehicleID},
		Trip: &gtfs.Trip{ID: gtfs.TripID{ID: trip.ID, RouteID: trip.RouteID}},
	}}

	tests := []struct {
		name         string
		attributedTo string
		wantStatus   int
	}{
		{"attributed to the requested agency", testdata.Raba.ID, http.StatusOK},
		{"attributed to another agency", "OTHER", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api.GtfsManager.MockSetAgencyVehicles("_test", tt.attributedTo, vehicles)

			reqURL := "/api/where/trip-details/" + utils.FormCombinedID(testdata.Raba.ID, trip.ID) + ".json?" +
				url.Values{
					"key":       {"TEST"},
					"vehicleId": {utils.FormCombinedID(testdata.Raba.ID, vehicleID)},
				}.Encode()

			resp, _ := callAPIHandler[TripDetailsResponse](t, api, reqURL)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}
