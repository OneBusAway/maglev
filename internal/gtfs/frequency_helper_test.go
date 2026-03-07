package gtfs

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

func TestGetActiveHeadway_MatchesWindow(t *testing.T) {
	freqs := []gtfsdb.Frequency{
		{TripID: "t1", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "t1", StartTime: int64(9 * time.Hour), EndTime: int64(12 * time.Hour), HeadwaySecs: 300, ExactTimes: 0},
	}

	// 7:00 AM — falls in first window
	result := GetActiveHeadway(freqs, int64(7*time.Hour))
	require.NotNil(t, result)
	assert.Equal(t, int64(600), result.HeadwaySecs)

	// 10:00 AM — falls in second window
	result = GetActiveHeadway(freqs, int64(10*time.Hour))
	require.NotNil(t, result)
	assert.Equal(t, int64(300), result.HeadwaySecs)
}

func TestGetActiveHeadway_ExactBoundary(t *testing.T) {
	freqs := []gtfsdb.Frequency{
		{TripID: "t1", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600},
	}

	// Exactly at start time — should match (inclusive)
	result := GetActiveHeadway(freqs, int64(6*time.Hour))
	require.NotNil(t, result)

	// Exactly at end time — should NOT match (exclusive)
	result = GetActiveHeadway(freqs, int64(9*time.Hour))
	assert.Nil(t, result)
}

func TestGetActiveHeadway_NoMatch(t *testing.T) {
	freqs := []gtfsdb.Frequency{
		{TripID: "t1", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600},
	}

	// 5:00 AM — before any window
	result := GetActiveHeadway(freqs, int64(5*time.Hour))
	assert.Nil(t, result)

	// 10:00 AM — after all windows
	result = GetActiveHeadway(freqs, int64(10*time.Hour))
	assert.Nil(t, result)
}

func TestGetActiveHeadway_EmptySlice(t *testing.T) {
	result := GetActiveHeadway(nil, int64(7*time.Hour))
	assert.Nil(t, result)

	result = GetActiveHeadway([]gtfsdb.Frequency{}, int64(7*time.Hour))
	assert.Nil(t, result)
}

func TestGetActiveHeadwayForTime(t *testing.T) {
	// Use a non-UTC timezone to ensure the timezone logic works correctly
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	serviceDate := time.Date(2024, 1, 15, 0, 0, 0, 0, loc)

	freqs := []gtfsdb.Frequency{
		{TripID: "t1", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600},
	}

	// 7:30 AM on Jan 15 (Local Time) — within window
	now := time.Date(2024, 1, 15, 7, 30, 0, 0, loc)
	result := GetActiveHeadwayForTime(freqs, serviceDate, now)
	require.NotNil(t, result)
	assert.Equal(t, int64(600), result.HeadwaySecs)

	// 10:00 AM (Local Time) — outside window
	now = time.Date(2024, 1, 15, 10, 0, 0, 0, loc)
	result = GetActiveHeadwayForTime(freqs, serviceDate, now)
	assert.Nil(t, result)
}

func TestExpandFrequencyTrips_Basic(t *testing.T) {
	// Template: trip departs stop A at 06:00, arrives stop B at 06:10
	baseStopTimes := []gtfsdb.StopTime{
		{TripID: "t1", ArrivalTime: int64(6 * time.Hour), DepartureTime: int64(6*time.Hour + 1*time.Minute), StopID: "A", StopSequence: 1},
		{TripID: "t1", ArrivalTime: int64(6*time.Hour + 10*time.Minute), DepartureTime: int64(6*time.Hour + 11*time.Minute), StopID: "B", StopSequence: 2},
	}

	// Frequency window: 06:00 - 07:00, headway 20 min
	freq := gtfsdb.Frequency{
		TripID:      "t1",
		StartTime:   int64(6 * time.Hour),
		EndTime:     int64(7 * time.Hour),
		HeadwaySecs: 1200, // 20 minutes
		ExactTimes:  1,
	}

	expanded := ExpandFrequencyTrips(baseStopTimes, freq)

	// 3 departures: 06:00, 06:20, 06:40
	assert.Equal(t, 6, len(expanded)) // 3 departures * 2 stops

	// First departure: 06:00 (no shift since base arrival == window start)
	assert.Equal(t, int64(6*time.Hour), expanded[0].ArrivalTime)
	assert.Equal(t, "A", expanded[0].StopID)
	assert.Equal(t, int64(6*time.Hour+10*time.Minute), expanded[1].ArrivalTime)
	assert.Equal(t, "B", expanded[1].StopID)

	// Second departure: 06:20
	assert.Equal(t, int64(6*time.Hour+20*time.Minute), expanded[2].ArrivalTime)
	assert.Equal(t, "A", expanded[2].StopID)
	assert.Equal(t, int64(6*time.Hour+30*time.Minute), expanded[3].ArrivalTime)
	assert.Equal(t, "B", expanded[3].StopID)

	// Third departure: 06:40
	assert.Equal(t, int64(6*time.Hour+40*time.Minute), expanded[4].ArrivalTime)
	assert.Equal(t, "A", expanded[4].StopID)
}

func TestExpandFrequencyTrips_PreservesFields(t *testing.T) {
	baseStopTimes := []gtfsdb.StopTime{
		{
			TripID:            "t1",
			ArrivalTime:       int64(8 * time.Hour),
			DepartureTime:     int64(8*time.Hour + 30*time.Second),
			StopID:            "stop_42",
			StopSequence:      5,
			StopHeadsign:      sql.NullString{String: "Downtown", Valid: true},
			PickupType:        sql.NullInt64{Int64: 0, Valid: true},
			DropOffType:       sql.NullInt64{Int64: 1, Valid: true},
			ShapeDistTraveled: sql.NullFloat64{Float64: 1234.5, Valid: true},
			Timepoint:         sql.NullInt64{Int64: 1, Valid: true},
		},
	}

	freq := gtfsdb.Frequency{
		TripID:      "t1",
		StartTime:   int64(8 * time.Hour),
		EndTime:     int64(9 * time.Hour),
		HeadwaySecs: 3600, // exactly one departure
		ExactTimes:  1,
	}

	expanded := ExpandFrequencyTrips(baseStopTimes, freq)
	require.Equal(t, 1, len(expanded))

	st := expanded[0]
	assert.Equal(t, "t1", st.TripID)
	assert.Equal(t, "stop_42", st.StopID)
	assert.Equal(t, int64(5), st.StopSequence)
	assert.Equal(t, "Downtown", st.StopHeadsign.String)
	assert.True(t, st.StopHeadsign.Valid)
	assert.Equal(t, int64(0), st.PickupType.Int64)
	assert.Equal(t, int64(1), st.DropOffType.Int64)
	assert.Equal(t, 1234.5, st.ShapeDistTraveled.Float64)
	assert.Equal(t, int64(1), st.Timepoint.Int64)
}

func TestExpandFrequencyTrips_EmptyStopTimes(t *testing.T) {
	freq := gtfsdb.Frequency{
		TripID:      "t1",
		StartTime:   int64(6 * time.Hour),
		EndTime:     int64(9 * time.Hour),
		HeadwaySecs: 600,
		ExactTimes:  1,
	}

	result := ExpandFrequencyTrips(nil, freq)
	assert.Nil(t, result)

	result = ExpandFrequencyTrips([]gtfsdb.StopTime{}, freq)
	assert.Nil(t, result)
}

func TestExpandFrequencyTrips_ZeroHeadway(t *testing.T) {
	baseStopTimes := []gtfsdb.StopTime{
		{TripID: "t1", ArrivalTime: int64(6 * time.Hour), DepartureTime: int64(6 * time.Hour), StopID: "A", StopSequence: 1},
	}

	freq := gtfsdb.Frequency{
		TripID:      "t1",
		StartTime:   int64(6 * time.Hour),
		EndTime:     int64(9 * time.Hour),
		HeadwaySecs: 0, // invalid
		ExactTimes:  1,
	}

	result := ExpandFrequencyTrips(baseStopTimes, freq)
	assert.Nil(t, result)
}

func TestExpandFrequencyTrips_BaseOffsetDiffersFromWindowStart(t *testing.T) {
	// Base template starts at 05:00 but frequency window starts at 06:00
	// The shift should align the first stop to the window start
	baseStopTimes := []gtfsdb.StopTime{
		{TripID: "t1", ArrivalTime: int64(5 * time.Hour), DepartureTime: int64(5*time.Hour + 1*time.Minute), StopID: "A", StopSequence: 1},
		{TripID: "t1", ArrivalTime: int64(5*time.Hour + 15*time.Minute), DepartureTime: int64(5*time.Hour + 16*time.Minute), StopID: "B", StopSequence: 2},
	}

	freq := gtfsdb.Frequency{
		TripID:      "t1",
		StartTime:   int64(6 * time.Hour),
		EndTime:     int64(7 * time.Hour),
		HeadwaySecs: 1800, // 30 min headway => 2 departures (06:00, 06:30)
		ExactTimes:  1,
	}

	expanded := ExpandFrequencyTrips(baseStopTimes, freq)
	assert.Equal(t, 4, len(expanded)) // 2 departures * 2 stops

	// First departure aligned to window start: stop A at 06:00, stop B at 06:15
	assert.Equal(t, int64(6*time.Hour), expanded[0].ArrivalTime)
	assert.Equal(t, int64(6*time.Hour+15*time.Minute), expanded[1].ArrivalTime)

	// Second departure at 06:30: stop A at 06:30, stop B at 06:45
	assert.Equal(t, int64(6*time.Hour+30*time.Minute), expanded[2].ArrivalTime)
	assert.Equal(t, int64(6*time.Hour+45*time.Minute), expanded[3].ArrivalTime)
}

func TestGroupFrequenciesByTrip(t *testing.T) {
	freqs := []gtfsdb.Frequency{
		{TripID: "t1", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600},
		{TripID: "t1", StartTime: int64(9 * time.Hour), EndTime: int64(12 * time.Hour), HeadwaySecs: 300},
		{TripID: "t2", StartTime: int64(7 * time.Hour), EndTime: int64(10 * time.Hour), HeadwaySecs: 900},
	}

	grouped := GroupFrequenciesByTrip(freqs)

	assert.Equal(t, 2, len(grouped))
	assert.Equal(t, 2, len(grouped["t1"]))
	assert.Equal(t, 1, len(grouped["t2"]))
	assert.Equal(t, int64(600), grouped["t1"][0].HeadwaySecs)
	assert.Equal(t, int64(300), grouped["t1"][1].HeadwaySecs)
	assert.Equal(t, int64(900), grouped["t2"][0].HeadwaySecs)
}

func TestGroupFrequenciesByTrip_Empty(t *testing.T) {
	grouped := GroupFrequenciesByTrip(nil)
	assert.Empty(t, grouped)

	grouped = GroupFrequenciesByTrip([]gtfsdb.Frequency{})
	assert.Empty(t, grouped)
}

func TestIsFrequencyBasedTrip(t *testing.T) {
	manager := &Manager{
		frequencyTripIDs: make(map[string]struct{}),
	}

	manager.frequencyTripIDs["trip_freq_1"] = struct{}{}
	manager.frequencyTripIDs["trip_freq_2"] = struct{}{}

	assert.True(t, manager.IsFrequencyBasedTrip("trip_freq_1"))
	assert.True(t, manager.IsFrequencyBasedTrip("trip_freq_2"))

	assert.False(t, manager.IsFrequencyBasedTrip("trip_normal_1"))
	assert.False(t, manager.IsFrequencyBasedTrip("trip_normal_2"))
	assert.False(t, manager.IsFrequencyBasedTrip(""))
}

func TestGetActiveHeadwayForTime_PostMidnight(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	now := time.Date(2024, 1, 2, 2, 0, 0, 0, time.UTC)

	frequencies := []gtfsdb.Frequency{
		{
			StartTime:   int64(25 * time.Hour),
			EndTime:     int64(27 * time.Hour),
			HeadwaySecs: 1800,
		},
	}

	result := GetActiveHeadwayForTime(frequencies, serviceDate, now)

	require.NotNil(t, result, "Should find matching frequency for overnight trip")
	assert.Equal(t, int64(1800), result.HeadwaySecs, "Should correctly match overnight frequencies > 24h")
}

func TestGetFrequenciesForTrips_EmptyInput(t *testing.T) {
	manager := &Manager{}
	ctx := context.Background()

	res, err := manager.GetFrequenciesForTrips(ctx, nil)
	assert.NoError(t, err)
	assert.Nil(t, res)

	res, err = manager.GetFrequenciesForTrips(ctx, []string{})
	assert.NoError(t, err)
	assert.Nil(t, res)
}

func TestSetStaticGTFS_RetainsFrequencyCache(t *testing.T) {
	manager := &Manager{
		frequencyTripIDs: map[string]struct{}{
			"existing_freq_trip": {},
		},
		staticMutex: sync.RWMutex{},
	}

	manager.setStaticGTFS(&gtfs.Static{})

	assert.True(t, manager.IsFrequencyBasedTrip("existing_freq_trip"), "Frequency cache should be retained and not wiped by variable shadowing")
}
