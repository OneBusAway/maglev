package restapi

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
)

// TestBlockFallbackTimingBoundaries checks the interaction between trip tracking,
// direct update boundaries and the block-deviation fallback on every arrival API.
func TestBlockFallbackTimingBoundaries(t *testing.T) {
	seq := uint32(1)
	delay := 174 * time.Second
	tests := []struct {
		name       string
		update     *gtfs.StopTimeUpdate
		repeatStop bool
		canceled   bool
		emptyTrip  bool
		wantTimes  bool
		rejected   bool
		tripDelay  *time.Duration
		noVehicle  bool
	}{
		{name: "block-fallback", wantTimes: true},
		{name: "trip-delay-over-hour", tripDelay: durationPointer(time.Hour + time.Second), rejected: true},
		{name: "trip-delay-over-hour-no-vehicle", tripDelay: durationPointer(2 * time.Hour), rejected: true, noVehicle: true},
		{name: "trip-delay-under-negative-hour", tripDelay: durationPointer(-time.Hour - time.Second), rejected: true},
		{name: "trip-delay-one-hour", tripDelay: durationPointer(time.Hour), wantTimes: true},
		{name: "trip-delay-negative-hour", tripDelay: durationPointer(-time.Hour), wantTimes: true},
		{name: "empty-trip-update", emptyTrip: true, wantTimes: true},
		{name: "later-stop-update", repeatStop: true, wantTimes: true, update: &gtfs.StopTimeUpdate{StopSequence: uint32Pointer(3), Arrival: &gtfs.StopTimeEvent{Delay: &delay}}},
		{name: "rejected-downstream-update", repeatStop: true, rejected: true, update: &gtfs.StopTimeUpdate{StopSequence: uint32Pointer(3), Arrival: &gtfs.StopTimeEvent{Delay: durationPointer(2 * time.Hour)}}},
		{name: "empty-scheduled", update: &gtfs.StopTimeUpdate{StopSequence: &seq}},
		{name: "no-data", update: &gtfs.StopTimeUpdate{StopSequence: &seq, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}},
		{name: "skipped", update: &gtfs.StopTimeUpdate{StopSequence: &seq, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}},
		{name: "ambiguous-visit", repeatStop: true, update: &gtfs.StopTimeUpdate{Arrival: &gtfs.StopTimeEvent{Delay: &delay}}},
		{name: "canceled", canceled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC)
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			stop, combinedStop, trip, _ := setupDelayPropTestData(t, api, 1)
			// Rejected cases have only the outlier source, matching the reported regression.
			if !tt.rejected {
				addEarlierBlockTripSTU(t, api, delay)
			}
			if tt.repeatStop {
				_, err := api.GtfsManager.GtfsDB.Queries.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
					TripID: trip, StopID: stop, StopSequence: 3, ArrivalTime: int64(8*time.Hour + 20*time.Minute), DepartureTime: int64(8*time.Hour + 25*time.Minute),
				})
				require.NoError(t, err)
			}
			if tt.tripDelay != nil {
				api.GtfsManager.MockAddTripUpdate(trip, tt.tripDelay, nil)
			}
			if tt.emptyTrip {
				api.GtfsManager.MockAddTripUpdate(trip, nil, nil)
			}
			if tt.update != nil {
				update := *tt.update
				update.StopID = &stop
				api.GtfsManager.MockAddTripUpdate(trip, nil, []gtfs.StopTimeUpdate{update})
			}
			relationship := gtfsrt.TripDescriptor_SCHEDULED
			if tt.canceled {
				relationship = gtfsrt.TripDescriptor_CANCELED
			}
			if !tt.noVehicle {
				api.GtfsManager.MockAddVehicleWithOptions("block-fallback-vehicle", trip, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now, ScheduleRelationship: relationship})
			}
			rebuildStopAgencyIndex(t, api)
			params := url.Values{"key": {"TEST"}, "tripId": {"dp-agency_" + trip}, "serviceDate": {fmt.Sprint(time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli())}, "time": {fmt.Sprint(now.UnixMilli())}}
			endpoints := []string{
				"/api/where/arrival-and-departure-for-stop/" + combinedStop + ".json?" + params.Encode(),
				arrivalsAndDeparturesURL(combinedStop, url.Values{"key": {"TEST"}}),
				"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=47&lon=-122&radius=100",
			}
			for i, endpoint := range endpoints {
				t.Run(fmt.Sprint(i), func(t *testing.T) {
					type response struct {
						Data struct {
							Entry struct {
								models.ArrivalAndDeparture
								Arrivals []models.ArrivalAndDeparture `json:"arrivalsAndDepartures"`
							} `json:"entry"`
						} `json:"data"`
					}
					resp, body := callAPIHandler[response](t, api, endpoint)
					require.Equal(t, http.StatusOK, resp.StatusCode)
					arrival := body.Data.Entry.ArrivalAndDeparture
					if i != 0 {
						found := false
						for _, candidate := range body.Data.Entry.Arrivals {
							if candidate.TripID == "dp-agency_"+trip && candidate.StopSequence == 0 {
								arrival = candidate
								found = true
								break
							}
						}
						require.True(t, found, "target visit must remain in the response")
					}
					assert.Equal(t, !tt.canceled && !tt.rejected, arrival.Predicted)
					if !tt.wantTimes {
						assert.True(t, arrival.PredictedArrivalTime.IsZero())
						assert.True(t, arrival.PredictedDepartureTime.IsZero())
					} else if tt.tripDelay != nil {
						assert.Equal(t, arrival.ScheduledArrivalTime.Add(*tt.tripDelay).UnixMilli(), arrival.PredictedArrivalTime.UnixMilli())
						assert.Equal(t, arrival.ScheduledDepartureTime.Add(*tt.tripDelay).UnixMilli(), arrival.PredictedDepartureTime.UnixMilli())
					} else {
						assert.Equal(t, arrival.ScheduledArrivalTime.Add(delay).UnixMilli(), arrival.PredictedArrivalTime.UnixMilli())
						assert.Equal(t, arrival.ScheduledDepartureTime.UnixMilli(), arrival.PredictedDepartureTime.UnixMilli())
					}
				})
			}
		})
	}
}

func uint32Pointer(value uint32) *uint32                 { return &value }
func durationPointer(value time.Duration) *time.Duration { return &value }
