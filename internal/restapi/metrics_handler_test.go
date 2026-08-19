package restapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// metricsHandlerTestClock is pinned to 2025-06-13 14:00 Pacific (Friday
// afternoon), verified against testdata/raba.zip to fall within RABA's
// weekday service hours (its Mon-Fri calendar, service_id
// c_1658_b_18260_d_31, has 300+ stop_times between 13:30-14:30 local).
// scheduledTripsCount reflects trips active right now, not a static total,
// so an arbitrary or off-hours timestamp would make this flaky.
var metricsHandlerTestClock = clock.NewMockClock(time.Date(2025, 6, 13, 21, 0, 0, 0, time.UTC))

func TestMetricsHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/metrics.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestMetricsHandlerStaticDataOnly(t *testing.T) {
	api := createTestApiWithClock(t, metricsHandlerTestClock)
	defer api.Shutdown()

	resp, model := callAPIHandler[MetricsEntryResponse](t, api, "/api/where/metrics.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	// createTestApi shares its underlying DB across the whole test binary run,
	// so other tests' Mock* helpers can leave extra agencies behind. Assert
	// RABA is present and the counts are internally consistent rather than
	// assuming an exact, isolated agency set.
	assert.Len(t, entry.AgencyIDs, entry.AgenciesWithCoverageCount)
	assert.Contains(t, entry.AgencyIDs, testdata.Raba.ID)
	assert.Positive(t, entry.ScheduledTripsCount[testdata.Raba.ID],
		"RABA static schedule should have scheduled trips")

	// No real-time feeds are configured, so the realtime maps are present but
	// zero-valued for the known agency rather than missing.
	assert.Equal(t, 0, entry.RealtimeRecordsTotal[testdata.Raba.ID])
	assert.Equal(t, 0, entry.RealtimeTripCountsMatched[testdata.Raba.ID])
	assert.Equal(t, 0, entry.RealtimeTripCountsUnmatched[testdata.Raba.ID])
	assert.Empty(t, entry.RealtimeTripIDsUnmatched[testdata.Raba.ID])
	assert.Equal(t, 0, entry.StopIDsMatchedCount[testdata.Raba.ID])
	assert.Equal(t, 0, entry.StopIDsUnmatchedCount[testdata.Raba.ID])
	assert.Empty(t, entry.StopIDsUnmatched[testdata.Raba.ID])
	assert.Equal(t, int64(0), entry.TimeSinceLastRealtimeUpdate[testdata.Raba.ID])

	assert.Empty(t, model.Data.References.Agencies, "metrics.json does not populate references")
}

func TestMetricsHandlerWithRealTimeData(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(vehiclesRealTimeDataClock))
	defer cleanup()

	resp, model := callAPIHandler[MetricsEntryResponse](t, api, "/api/where/metrics.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	agencyID := testdata.Raba.ID

	require.Positive(t, entry.RealtimeRecordsTotal[agencyID],
		"expected real-time trip records once the .pb fixtures are loaded")
	// Matched counting requires each block's representative trip to have a
	// currently active prediction window (see isCombinedRecordActive), checked
	// against the real wall clock, not the mock clock this test injects. The
	// .pb fixtures' predictions are timestamped in the past, so every record
	// is permanently outside that window: matched is deterministically 0.
	// (Unmatched counting isn't time-gated, and happens to be 0 here too,
	// since these fixtures' trip IDs all resolve against RABA's static data.)
	assert.Equal(t, 0, entry.RealtimeTripCountsMatched[agencyID])
	assert.Equal(t, 0, entry.RealtimeTripCountsUnmatched[agencyID])
	assert.Len(t, entry.RealtimeTripIDsUnmatched[agencyID], entry.RealtimeTripCountsUnmatched[agencyID])

	assert.Len(t, entry.StopIDsUnmatched[agencyID], entry.StopIDsUnmatchedCount[agencyID])

	assert.GreaterOrEqual(t, entry.TimeSinceLastRealtimeUpdate[agencyID], int64(0))
}
