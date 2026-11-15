package restapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// TestArrivalsStopIndexQueryFailure checks both pipelines that use the batched
// stop index, including successful nonzero positions and empty windows.
func TestArrivalsStopIndexQueryFailure(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		count    int
	}{
		{
			name: "for-stop",
			endpoint: arrivalsAndDeparturesURL(utils.FormCombinedID(loopAgencyID, loopStopHubID),
				url.Values{"minutesBefore": {"60"}, "minutesAfter": {"240"}}),
			count: 2,
		},
		{
			name: "for-location",
			endpoint: arrivalsForLocationURL(loopFixtureCenter,
				url.Values{"minutesBefore": {"60"}, "minutesAfter": {"240"}}),
			count: loopTripStopCount(),
		},
	}
	// These are the shared entry fields asserted on both endpoint responses.
	type arrivalEntry struct {
		ArrivalsAndDepartures []models.ArrivalAndDeparture `json:"arrivalsAndDepartures"`
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithLoopTripData(t)
			healthyQueries := api.GtfsManager.GtfsDB.Queries
			resp, healthy := callAPIHandler[EntryResponse[arrivalEntry]](t, api, tt.endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Len(t, healthy.Data.Entry.ArrivalsAndDepartures, tt.count)
			positions := assertLoopTripPositions(t, healthy.Data.Entry.ArrivalsAndDepartures)
			require.True(t, positions[1], "the healthy response must include a non-first stop")
			require.True(t, positions[3], "the loop's second hub visit must remain distinct")

			for _, failure := range []struct {
				name string
				err  error
				code int
				text string
			}{
				{"database-error", errors.New("injected arrivals stop-index query failure"), http.StatusInternalServerError, "internal server error"},
				{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "gateway timeout"},
			} {
				t.Run(failure.name, func(t *testing.T) {
					failingDB := &stopSearchFailureDB{
						DBTX:       api.GtfsManager.GtfsDB.DB,
						queryMatch: "-- name: GetStopTimesForTripIDs :many",
						failWith:   failure.err,
					}
					api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)
					t.Cleanup(func() { api.GtfsManager.GtfsDB.Queries = healthyQueries })

					resp, failed := callAPIHandler[EntryResponse[arrivalEntry]](t, api, tt.endpoint)
					require.Positive(t, failingDB.failures, "the intended query must actually fail")
					assert.Equal(t, failure.code, resp.StatusCode)
					assert.Equal(t, failure.code, failed.Code)
					assert.Equal(t, failure.text, failed.Text)
					assert.Empty(t, failed.Data.Entry.ArrivalsAndDepartures,
						"a failed lookup must not publish fabricated stop positions")

					failingDB.failures = 0
					emptyURL, err := url.Parse(tt.endpoint)
					require.NoError(t, err)
					query := emptyURL.Query()
					query.Set("time", "2025-06-12_02-00-00")
					query.Set("minutesBefore", "0")
					query.Set("minutesAfter", "1")
					emptyURL.RawQuery = query.Encode()
					resp, empty := callAPIHandler[EntryResponse[arrivalEntry]](t, api, emptyURL.String())
					assert.Equal(t, http.StatusOK, resp.StatusCode)
					assert.Empty(t, empty.Data.Entry.ArrivalsAndDepartures)
					assert.Zero(t, failingDB.failures, "an empty window needs no stop index")
				})
			}

			resp, recovered := callAPIHandler[EntryResponse[arrivalEntry]](t, api, tt.endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, healthy.Data.Entry.ArrivalsAndDepartures, recovered.Data.Entry.ArrivalsAndDepartures)
		})
	}
}

// TestBatchArrivalEntitiesEmpty checks that an empty batch needs no stop index.
func TestBatchArrivalEntitiesEmpty(t *testing.T) {
	api := createTestApiWithLoopTripData(t)
	healthyQueries := api.GtfsManager.GtfsDB.Queries
	t.Cleanup(func() { api.GtfsManager.GtfsDB.Queries = healthyQueries })
	failingDB := &stopSearchFailureDB{
		DBTX:       api.GtfsManager.GtfsDB.DB,
		queryMatch: "-- name: GetStopTimesForTripIDs :many",
		failWith:   errors.New("unexpected stop-index lookup for empty batch"),
	}
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)
	_, _, idx, _, err := api.batchArrivalEntities(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, idx.stopCounts)
	assert.Empty(t, idx.positions)
	assert.Zero(t, failingDB.failures)
}
