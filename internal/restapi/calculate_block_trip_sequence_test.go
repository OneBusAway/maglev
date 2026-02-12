package restapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateBlockTripSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "Should have test trips")

	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC)

	// Find a block that has multiple active trips so we can verify sequencing
	type blockInfo struct {
		blockID string
		tripIDs []string
	}
	blocks := make(map[string]*blockInfo)

	for _, trip := range trips {
		tripRow, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
		if err != nil || !tripRow.BlockID.Valid || tripRow.BlockID.String == "" {
			continue
		}

		isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, tripRow.ServiceID, serviceDate)
		if err != nil || isActive == 0 {
			continue
		}

		bid := tripRow.BlockID.String
		if blocks[bid] == nil {
			blocks[bid] = &blockInfo{blockID: bid}
		}
		blocks[bid].tripIDs = append(blocks[bid].tripIDs, trip.ID)
	}

	// Find a block with at least 2 active trips
	var multiTripBlock *blockInfo
	for _, b := range blocks {
		if len(b.tripIDs) >= 2 {
			multiTripBlock = b
			break
		}
	}

	require.NotNil(t, multiTripBlock, "Need a block with multiple active trips in test data")

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
}
