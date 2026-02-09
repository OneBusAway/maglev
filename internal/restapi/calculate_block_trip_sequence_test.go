package restapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateBlockTripSequence tests the block trip sequence calculation
func TestCalculateBlockTripSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Get a trip with a valid block ID
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "Should have test trips")

	var testTripID string
	var testServiceDate time.Time
	
	// Find a trip with a block ID
	for _, trip := range trips {
		tripRow, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
		if err != nil {
			continue
		}
		
		if tripRow.BlockID.Valid && tripRow.BlockID.String != "" {
			testTripID = trip.ID
			// Use a service date for testing
			testServiceDate = time.Now()
			break
		}
	}
	
	if testTripID == "" {
		t.Skip("No trips with block IDs found in test data")
	}

	t.Run("Valid trip with block ID", func(t *testing.T) {
		sequence := api.calculateBlockTripSequence(ctx, testTripID, testServiceDate)
		
		// Sequence should be non-negative
		assert.GreaterOrEqual(t, sequence, 0, "Sequence should be >= 0")
	})

	t.Run("Invalid trip ID", func(t *testing.T) {
		sequence := api.calculateBlockTripSequence(ctx, "invalid-trip-id", testServiceDate)
		
		// Should return 0 for invalid trip
		assert.Equal(t, 0, sequence, "Should return 0 for invalid trip")
	})
	
	t.Run("Transaction consistency", func(t *testing.T) {
		// Call the function multiple times to ensure transaction handling works
		sequence1 := api.calculateBlockTripSequence(ctx, testTripID, testServiceDate)
		sequence2 := api.calculateBlockTripSequence(ctx, testTripID, testServiceDate)
		
		// Should return the same result consistently
		assert.Equal(t, sequence1, sequence2, "Should return consistent results")
	})
}
