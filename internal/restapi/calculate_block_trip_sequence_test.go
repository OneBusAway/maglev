package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

func TestCalculateBlockTripSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips, err := api.GtfsManager.GetTrips(ctx, 100)
	require.NoError(t, err)
	require.NotEmpty(t, trips, "Should have test trips")

	// Monday within the RABA dataset's active service period (calendar range covers this date)
	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC)
	// Find a block that has multiple active trips so we can verify sequencing
	type blockInfo struct {
		tripIDs []string
	}

	var multiTripBlock *blockInfo
	seenBlocks := make(map[string]bool)
	for _, trip := range trips {
		tripRow, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
		if err != nil || !tripRow.BlockID.Valid || tripRow.BlockID.String == "" {
			continue
		}
		bid := tripRow.BlockID.String
		if seenBlocks[bid] {
			continue
		}
		seenBlocks[bid] = true

		blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, nulls.String(bid))
		if err != nil {
			continue
		}

		var activeTripIDs []string
		for _, bt := range blockTrips {
			isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, bt.ServiceID, serviceDate)
			if err != nil || isActive == 0 {
				continue
			}
			activeTripIDs = append(activeTripIDs, bt.ID)
		}

		if len(activeTripIDs) >= 2 {
			multiTripBlock = &blockInfo{tripIDs: activeTripIDs}
			break
		}
	}

	require.NotNil(t, multiTripBlock, "Need a block with multiple active trips in test data")
	t.Logf("Selected block with %d trips: %v", len(multiTripBlock.tripIDs), multiTripBlock.tripIDs)

	t.Run("trips in same block get different sequences", func(t *testing.T) {
		sequences := make(map[int]bool)
		for _, tripID := range multiTripBlock.tripIDs {
			seq := api.calculateBlockTripSequence(ctx, tripID, serviceDate)
			sequences[seq] = true
		}
		assert.Equal(t, len(multiTripBlock.tripIDs), len(sequences),
			"Each trip in the block should have a unique sequence index")
	})

	t.Run("sequence values are consecutive from zero", func(t *testing.T) {
		for _, tripID := range multiTripBlock.tripIDs {
			seq := api.calculateBlockTripSequence(ctx, tripID, serviceDate)
			assert.GreaterOrEqual(t, seq, 0)
			assert.Less(t, seq, len(multiTripBlock.tripIDs),
				"Sequence for trip %s should be within [0, %d), got %d", tripID, len(multiTripBlock.tripIDs), seq)
		}
	})

	t.Run("invalid trip ID", func(t *testing.T) {
		sequence := api.calculateBlockTripSequence(ctx, "invalid-trip-id", serviceDate)
		assert.Equal(t, 0, sequence)
	})

	t.Run("date outside service range", func(t *testing.T) {
		futureDate := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		sequence := api.calculateBlockTripSequence(ctx, multiTripBlock.tripIDs[0], futureDate)
		assert.Equal(t, 0, sequence)
	})

	t.Run("sequence order matches chronological departure time", func(t *testing.T) {
		type tripSeq struct {
			sequence       int
			earliestDepart int64
		}
		var results []tripSeq
		for _, tripID := range multiTripBlock.tripIDs {
			seq := api.calculateBlockTripSequence(ctx, tripID, serviceDate)
			stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
			require.NoError(t, err)
			var minDepart int64 = math.MaxInt64
			for _, st := range stopTimes {
				if st.DepartureTime > 0 && st.DepartureTime < minDepart {
					minDepart = st.DepartureTime
				}
			}
			results = append(results, tripSeq{sequence: seq, earliestDepart: minDepart})
		}
		slices.SortFunc(results, func(a, b tripSeq) int {
			return cmp.Compare(a.sequence, b.sequence)
		})
		for i := 1; i < len(results); i++ {
			assert.LessOrEqual(t, results[i-1].earliestDepart, results[i].earliestDepart)
		}
	})
}

// findLaterTripInMultiTripShift returns a block shift's first trip and a later
// trip of that shift, both active on serviceDate.
func findLaterTripInMultiTripShift(t *testing.T, api *RestAPI, serviceDate time.Time) (firstTripID, laterTripID string) {
	t.Helper()
	ctx := context.Background()
	trips, err := api.GtfsManager.GetTrips(ctx, 500)
	require.NoError(t, err)
	for _, trip := range trips {
		shift := api.blockShiftTripIDsSortedByStartTime(ctx, trip.ID, serviceDate)
		if len(shift) >= 2 {
			return shift[0], shift[len(shift)-1]
		}
	}
	require.Fail(t, "need a block shift with at least two trips in test data")
	return "", ""
}

func TestArrivalBlockTripSequenceDiffersFromTripStatus(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, losAngeles)
	firstTripID, laterTripID := findLaterTripInMultiTripShift(t, api, serviceDate)

	queries := api.GtfsManager.GtfsDB.Queries
	firstTripStops, err := queries.GetStopTimesForTrip(ctx, firstTripID)
	require.NoError(t, err)
	laterTripStops, err := queries.GetStopTimesForTrip(ctx, laterTripID)
	require.NoError(t, err)
	laterTrip, err := queries.GetTrip(ctx, laterTripID)
	require.NoError(t, err)
	route, err := queries.GetRoute(ctx, laterTrip.RouteID)
	require.NoError(t, err)

	queryTime := serviceDate.Add(time.Duration(firstTripStops[0].DepartureTime))
	minutesUntilLaterTrip := int(time.Duration(laterTripStops[0].ArrivalTime-firstTripStops[0].DepartureTime).Minutes()) + 1
	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&time=%d&minutesBefore=0&minutesAfter=%d",
		utils.FormCombinedID(route.AgencyID, laterTripStops[0].StopID), queryTime.UnixMilli(), minutesUntilLaterTrip)

	_, body := serveApiAndRetrieveEndpoint(t, api, endpoint)

	entry, ok := body.Data.(map[string]any)["entry"].(map[string]any)
	require.True(t, ok)
	wantTripID := utils.FormCombinedID(route.AgencyID, laterTripID)
	var arrival map[string]any
	for _, raw := range entry["arrivalsAndDepartures"].([]any) {
		if candidate := raw.(map[string]any); candidate["tripId"] == wantTripID {
			arrival = candidate
		}
	}
	require.NotNil(t, arrival, "the later trip must appear in the arrival window")

	tripStatus := arrival["tripStatus"].(map[string]any)
	require.Equal(t, utils.FormCombinedID(route.AgencyID, firstTripID), tripStatus["activeTripId"])
	assert.EqualValues(t, api.calculateBlockTripSequence(ctx, laterTripID, serviceDate), arrival["blockTripSequence"],
		"the arrival reports its own trip's position in the block")
	assert.EqualValues(t, api.calculateBlockTripSequence(ctx, firstTripID, serviceDate), tripStatus["blockTripSequence"],
		"the status reports the active trip's position in the block")
}

type rowQueryCountingDB struct {
	gtfsdb.DBTX
	rowQueries int
}

func (db *rowQueryCountingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.rowQueries++
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

func TestActiveTripBlockSequence_QueriesOncePerRequest(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	tripID := mustGetTrip(t, api).ID
	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC)

	originalQueries := api.GtfsManager.GtfsDB.Queries
	t.Cleanup(func() { api.GtfsManager.GtfsDB.Queries = originalQueries })
	countingDB := &rowQueryCountingDB{DBTX: api.GtfsManager.GtfsDB.DB}
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(countingDB)

	tests := []struct {
		name         string
		ctx          context.Context
		secondCallDB bool
	}{
		{name: "a request-scoped cache reuses the first result", ctx: WithSnapshotCache(context.Background(), newSnapshotCache()), secondCallDB: false},
		{name: "without a cache each call queries", ctx: context.Background(), secondCallDB: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := api.activeTripBlockSequence(tt.ctx, tripID, serviceDate)
			afterFirstCall := countingDB.rowQueries
			require.Positive(t, afterFirstCall)

			second := api.activeTripBlockSequence(tt.ctx, tripID, serviceDate)
			assert.Equal(t, first, second)
			assert.Equal(t, tt.secondCallDB, countingDB.rowQueries > afterFirstCall)
		})
	}
}
