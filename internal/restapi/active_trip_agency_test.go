package restapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

// TestActiveTripAgencyID guards the helper for #1987: the active trip's own
// agency comes from its static route, with a fallback for unknown trips.
func TestActiveTripAgencyID(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "two-agency-block.zip"))

	assert.Equal(t, "X", api.activeTripAgencyID(context.Background(), "tA", "FALLBACK"))
	assert.Equal(t, "Y", api.activeTripAgencyID(context.Background(), "tB", "FALLBACK"))
	assert.Equal(t, "FALLBACK", api.activeTripAgencyID(context.Background(), "nope", "FALLBACK"))
}

// TestActiveTripIDUsesActiveTripOwnAgency guards #1987 end to end: when a block
// mixes trips from two agencies, tripStatus.activeTripId must be built from
// the active trip's own agency — the agency references.trips uses — not from
// the agency of the trip the arrival is for. On main the ID dangles (Y_tA
// while references.trips holds X_tA), which crashes iOS's force-unwrap in
// TripStatus.loadReferences.
func TestActiveTripIDUsesActiveTripOwnAgency(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "two-agency-block.zip"))

	// Monday 2026-01-05 08:25 UTC: block B1's active trip is tA (agency X,
	// started 08:20) while the queried trip tB (agency Y) starts at 08:40.
	serviceDate := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).UnixMilli()
	current := time.Date(2026, 1, 5, 8, 25, 0, 0, time.UTC).UnixMilli()
	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/Y_s3.json?key=TEST&tripId=Y_tB&serviceDate=%d&time=%d", serviceDate, current)

	_, resp := callAPIHandler[struct {
		Code int `json:"code"`
		Data struct {
			Entry struct {
				TripStatus *struct {
					ActiveTripID string `json:"activeTripId"`
				} `json:"tripStatus"`
			} `json:"entry"`
			References struct {
				Trips []struct {
					ID string `json:"id"`
				} `json:"trips"`
			} `json:"references"`
		} `json:"data"`
	}](t, api, endpoint)

	require.Equal(t, 200, resp.Code)
	require.NotNil(t, resp.Data.Entry.TripStatus, "the block snapshot must produce a trip status")
	assert.Equal(t, "X_tA", resp.Data.Entry.TripStatus.ActiveTripID, "activeTripId must use the active trip's own agency")

	ids := make([]string, 0, len(resp.Data.References.Trips))
	for _, trip := range resp.Data.References.Trips {
		ids = append(ids, trip.ID)
	}
	assert.Contains(t, ids, "X_tA", "references.trips must hold the active trip under its own agency")
}
