package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

		blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, sql.NullString{String: bid, Valid: true})
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
