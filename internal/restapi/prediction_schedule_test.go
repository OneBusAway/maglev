package restapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// predictionScheduleDB counts schedule reads using the existing DBTX test seam.
type predictionScheduleDB struct {
	gtfsdb.DBTX
	single, batch int
	failBatch     bool
	byCaller      map[string]int
}

func (db *predictionScheduleDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	single := strings.HasPrefix(query, "-- name: GetStopTimesForTrip :many")
	batch := strings.HasPrefix(query, "-- name: GetStopTimesForTripIDs :many")
	if single || batch {
		db.byCaller[scheduleQueryCaller()]++
	}
	if single {
		db.single++
	}
	if batch {
		db.batch++
		if db.failBatch {
			return nil, fmt.Errorf("forced schedule batch failure")
		}
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

// Attribute reads at the DBTX seam without adding production test hooks.
// Prediction takes precedence even if it is called from another known path.
func scheduleQueryCaller() string {
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	caller := "unexpected"
	for {
		frame, more := frames.Next()
		switch {
		case strings.HasSuffix(frame.Function, ".getPredictedTimes"):
			return "prediction"
		case strings.HasSuffix(frame.Function, ".tripStatusForArrival"):
			caller = "trip-status"
		case strings.HasSuffix(frame.Function, ".loadTripSchedules") && caller == "unexpected":
			caller = "schedule-loader"
		}
		if !more {
			return caller
		}
	}
}

func TestArrivalsReusePredictionSchedules(t *testing.T) {
	day := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithFeed(t, "../../testdata/raba.zip")
	api.Clock = clock.NewMockClock(day.Add(8*time.Hour + 2*time.Minute))
	stop, combinedStop, trip, _ := setupDelayPropTestData(t, api, 3)
	q := api.GtfsManager.GtfsDB.Queries
	_, err := q.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
		TripID: trip, StopID: stop, StopSequence: 1,
		ArrivalTime: int64(7*time.Hour + 40*time.Minute), DepartureTime: int64(7*time.Hour + 42*time.Minute),
	})
	require.NoError(t, err)
	sequence := uint32(1)
	actualDeparture := day.Add(7*time.Hour + 47*time.Minute)
	api.GtfsManager.MockAddTripUpdate(trip, nil, []gtfs.StopTimeUpdate{{
		StopSequence: &sequence, StopID: &stop, Departure: &gtfs.StopTimeEvent{Time: &actualDeparture},
	}})
	rebuildStopAgencyIndex(t, api)
	schedule := loadPredictionSchedule(t, api, trip)
	db := &predictionScheduleDB{DBTX: api.GtfsManager.GtfsDB.DB, byCaller: make(map[string]int)}
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(db)

	t.Run("prediction-does-not-query", func(t *testing.T) {
		for range 10 {
			arrival, departure, predicted := api.getPredictedTimes(t.Context(), trip, stop, 3,
				day.Add(8*time.Hour), day.Add(8*time.Hour+5*time.Minute), schedule)
			require.True(t, predicted)
			assert.Equal(t, day.Add(8*time.Hour+5*time.Minute), arrival)
			assert.Equal(t, day.Add(8*time.Hour+10*time.Minute), departure)
		}
		assert.Zero(t, db.single)
		assert.Zero(t, db.batch)
		assert.Empty(t, db.byCaller)
	})

	t.Run("one-batch-for-schedules-and-counts", func(t *testing.T) {
		db.single, db.batch = 0, 0
		clear(db.byCaller)
		schedules := api.loadTripSchedules(t.Context(), []string{trip})
		require.Len(t, schedules[trip].stopTimes, 2)
		assert.Equal(t, schedule.stopIDs, schedules[trip].stopIDs)
		assert.Equal(t, 1, db.batch)
		assert.Zero(t, db.single)
		assert.Equal(t, map[string]int{"schedule-loader": 1}, db.byCaller)
	})

	type response struct {
		Data struct {
			Entry struct {
				Arrivals []models.ArrivalAndDeparture `json:"arrivalsAndDepartures"`
			} `json:"entry"`
		} `json:"data"`
	}
	endpoints := []struct {
		name, url string
	}{
		{"stop", "/api/where/arrivals-and-departures-for-stop/" + combinedStop + ".json?key=TEST&minutesBefore=10&minutesAfter=30"},
		{"location", "/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=47&lon=-122&radius=100&minutesBefore=10&minutesAfter=30"},
	}
	for _, rows := range []int{1, 3} {
		if rows == 3 {
			for i := range 2 {
				offset := 8*time.Hour + time.Duration(i+1)*10*time.Minute
				_, err := q.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
					TripID: trip, StopID: stop, StopSequence: int64(4 + i),
					ArrivalTime: int64(offset), DepartureTime: int64(offset + 5*time.Minute),
				})
				require.NoError(t, err)
			}
		}
		for _, endpoint := range endpoints {
			t.Run(fmt.Sprintf("%s/%d-arrivals", endpoint.name, rows), func(t *testing.T) {
				db.single, db.batch = 0, 0
				clear(db.byCaller)
				resp, body := callAPIHandler[response](t, api, endpoint.url)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				arrivals := body.Data.Entry.Arrivals
				require.Len(t, arrivals, rows)
				for _, arrival := range arrivals {
					assert.True(t, arrival.Predicted)
					assert.Equal(t, arrival.ScheduledArrivalTime.UnixMilli()+300000, arrival.PredictedArrivalTime.UnixMilli())
					assert.Equal(t, arrival.ScheduledDepartureTime.UnixMilli()+300000, arrival.PredictedDepartureTime.UnixMilli())
					assert.Equal(t, rows+1, arrival.TotalStopsInTrip)
				}
				// Trip status currently adds one single-trip and one batch read per
				// arrival, plus one fixed batch. Attribute reads independently so
				// reductions in status work cannot hide new prediction queries.
				assert.Equal(t, 1, db.byCaller["schedule-loader"])
				assert.Zero(t, db.byCaller["prediction"])
				assert.Zero(t, db.byCaller["unexpected"])
				assert.LessOrEqual(t, db.single, rows)
				assert.LessOrEqual(t, db.batch, rows+2)
				t.Logf("arrivals=%d batch=%d single=%d callers=%v", rows, db.batch, db.single, db.byCaller)
			})
		}
	}

	t.Run("failed-batch-does-not-invent-time-only-predictions", func(t *testing.T) {
		db.failBatch = true
		resp, body := callAPIHandler[response](t, api, endpoints[0].url)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, body.Data.Entry.Arrivals, 3)
		for _, arrival := range body.Data.Entry.Arrivals {
			assert.True(t, arrival.Predicted, "the prior update still tracks this trip")
			assert.True(t, arrival.PredictedArrivalTime.IsZero())
			assert.True(t, arrival.PredictedDepartureTime.IsZero())
			assert.Zero(t, arrival.TotalStopsInTrip)
		}
	})
}

func TestArrivalsRABAAllSkippedTrip(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	const tripID = "25_03969589-98dc-4fcd-a1c2-ce084b4ca5d2"
	for _, stopID := range []string{"25_3027", "25_9038"} {
		t.Run(stopID, func(t *testing.T) {
			endpoint := "/api/where/arrivals-and-departures-for-stop/" + stopID + ".json?key=TEST&minutesAfter=30&minutesBefore=10&time=1749828720000"
			resp, body := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			for _, arrival := range body.Data.Entry.ArrivalsAndDepartures {
				if arrival.TripID != tripID {
					continue
				}
				assert.Equal(t, stopID, arrival.StopID)
				assert.Equal(t, int64(1749828780000), arrival.ScheduledArrivalTime.UnixMilli())
				assert.Equal(t, int64(1749828780000), arrival.ScheduledDepartureTime.UnixMilli())
				assert.True(t, arrival.Predicted)
				assert.True(t, arrival.PredictedArrivalTime.IsZero())
				assert.True(t, arrival.PredictedDepartureTime.IsZero())
				return
			}
			t.Fatalf("missing RABA arrival for %s", tripID)
		})
	}
}
