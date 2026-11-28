package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// arrivalsForStopWideWindowURL asks for 12 hours either side, so the stop has
// many arrivals and every one of them builds a block snapshot.
func arrivalsForStopWideWindowURL() string {
	return arrivalsForStopMemoURL() + "&minutesBefore=720&minutesAfter=720"
}

func serveArrivals(t *testing.T, mux *http.ServeMux, endpoint string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	return recorder
}

// TestArrivalsForStopReadsShapesOncePerImport checks that block snapshots read
// shapes through the manager's cache: a second request for the same stop
// reads no shape from the database.
func TestArrivalsForStopReadsShapesOncePerImport(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	serveArrivals(t, mux, arrivalsForStopWideWindowURL())
	coldReads := counter.countOf("GetShapePointsByIDs")
	require.Positive(t, coldReads, "the first request should read the block shapes")

	serveArrivals(t, mux, arrivalsForStopWideWindowURL())
	assert.Equal(t, coldReads, counter.countOf("GetShapePointsByIDs"), "the second request should read no shape")
}

// TestArrivalsLeaveCachedShapesUnchanged checks that nothing on the arrivals
// path writes into the shapes it shares through the cache.
func TestArrivalsLeaveCachedShapesUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	direct := api.GtfsManager.GtfsDB.Queries
	swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	ctx := context.Background()

	recorder := serveArrivals(t, mux, arrivalsForStopWideWindowURL())
	arrivals := arrivalsForStopFromBody(t, recorder.Body.Bytes())
	require.NotEmpty(t, arrivals)

	var shapeIDs []string
	for _, arrival := range arrivals {
		tripID, err := utils.ExtractCodeID(arrival.TripID)
		require.NoError(t, err)
		trip, err := direct.GetTrip(ctx, tripID)
		require.NoError(t, err)
		if trip.ShapeID.Valid {
			shapeIDs = append(shapeIDs, trip.ShapeID.String)
		}
	}
	require.NotEmpty(t, shapeIDs)

	cached, err := api.GtfsManager.ShapesByID(ctx, shapeIDs)
	require.NoError(t, err)
	for shapeID, shape := range cached {
		assert.Equal(t, freshShape(t, direct, shapeID), shape, "shape %s changed while it was cached", shapeID)
	}
}

// freshShape reads a shape straight from the database, the way the cache
// builds it.
func freshShape(t *testing.T, queries *gtfsdb.Queries, shapeID string) internalgtfs.CachedShape {
	t.Helper()
	rows, err := queries.GetShapePointsByIDs(context.Background(), []string{shapeID})
	require.NoError(t, err)
	points := make([]gtfs.ShapePoint, len(rows))
	for i, row := range rows {
		points[i] = gtfs.ShapePoint{Latitude: row.Lat, Longitude: row.Lon}
	}
	shape := internalgtfs.CachedShape{Points: points}
	if len(points) >= 2 {
		shape.CumulativeDistances = utils.CumulativeShapeDistances(points)
		shape.TotalDistance = shape.CumulativeDistances[len(points)-1]
	}
	return shape
}

// TestArrivalsForStopSurvivesShapeReadFailure checks that a failed shape read
// still degrades block snapshots to stop-to-stop distances instead of failing
// the request.
func TestArrivalsForStopSurvivesShapeReadFailure(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	counter.failOn("GetShapePointsByIDs")
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	recorder := serveArrivals(t, mux, arrivalsForStopWideWindowURL())
	assert.NotEmpty(t, arrivalsForStopFromBody(t, recorder.Body.Bytes()))
	assert.Positive(t, counter.countOf("GetShapePointsByIDs"), "the shape read should have been attempted")
}

// TestArrivalsTripStatusReadsShapesFromCache checks that trip status takes the
// arriving trip's shape from the manager's cache, the one block snapshots
// fill, instead of reading it again per arrival.
func TestArrivalsTripStatusReadsShapesFromCache(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	recorder := serveArrivals(t, mux, arrivalsForStopWideWindowURL())
	require.NotEmpty(t, arrivalsForStopFromBody(t, recorder.Body.Bytes()))
	assert.Zero(t, counter.countOf("GetShapePointsByTripID"))
}
