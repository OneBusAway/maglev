package models

// MetricsModel reports agency coverage, scheduled trip counts, and GTFS-RT
// matching health, keyed by agency ID. Field names mirror the upstream Java
// OneBusAway `metrics.json` response.
type MetricsModel struct {
	AgenciesWithCoverageCount   int                 `json:"agenciesWithCoverageCount"`
	AgencyIDs                   []string            `json:"agencyIDs"`
	ScheduledTripsCount         map[string]int      `json:"scheduledTripsCount"`
	RealtimeRecordsTotal        map[string]int      `json:"realtimeRecordsTotal"`
	RealtimeTripCountsMatched   map[string]int      `json:"realtimeTripCountsMatched"`
	RealtimeTripCountsUnmatched map[string]int      `json:"realtimeTripCountsUnmatched"`
	RealtimeTripIDsUnmatched    map[string][]string `json:"realtimeTripIDsUnmatched"`
	StopIDsMatchedCount         map[string]int      `json:"stopIDsMatchedCount"`
	StopIDsUnmatchedCount       map[string]int      `json:"stopIDsUnmatchedCount"`
	StopIDsUnmatched            map[string][]string `json:"stopIDsUnmatched"`
	TimeSinceLastRealtimeUpdate map[string]int64    `json:"timeSinceLastRealtimeUpdate"`
}
