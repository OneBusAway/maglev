package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

func TestNewFrequencyFromDB(t *testing.T) {
	// Use a non-UTC timezone to properly verify the Local timezone fix
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Service date: 2024-01-15 midnight in New York
	serviceDate := time.Date(2024, 1, 15, 0, 0, 0, 0, loc)

	// DB stores times as nanoseconds since midnight (time.Duration)
	// 06:00:00 = 6h * 3600s * 1e9 ns
	startNanos := int64(6 * time.Hour)
	// 09:00:00 = 9h * 3600s * 1e9 ns
	endNanos := int64(9 * time.Hour)

	dbFreq := gtfsdb.Frequency{
		TripID:      "trip_1",
		StartTime:   startNanos,
		EndTime:     endNanos,
		HeadwaySecs: 600, // 10 minutes in seconds
		ExactTimes:  1,
	}

	freq := NewFrequencyFromDB(dbFreq, serviceDate)

	expectedStart := time.Date(2024, 1, 15, 6, 0, 0, 0, loc)
	expectedEnd := time.Date(2024, 1, 15, 9, 0, 0, 0, loc)

	assert.Equal(t, expectedStart, freq.StartTime.Time)
	assert.Equal(t, expectedEnd, freq.EndTime.Time)
	assert.Equal(t, 600*time.Second, freq.Headway.Duration)
	assert.Equal(t, 1, freq.ExactTimes)
}

func TestNewFrequencyFromDB_FrequencyBased(t *testing.T) {
	serviceDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	dbFreq := gtfsdb.Frequency{
		TripID:      "trip_2",
		StartTime:   int64(7 * time.Hour),
		EndTime:     int64(22 * time.Hour),
		HeadwaySecs: 300, // 5 minutes
		ExactTimes:  0,   // frequency-based
	}

	freq := NewFrequencyFromDB(dbFreq, serviceDate)

	assert.Equal(t, 300*time.Second, freq.Headway.Duration)
	assert.Equal(t, 0, freq.ExactTimes)
	assert.Greater(t, freq.EndTime.Time, freq.StartTime.Time)
}

func TestNewFrequencyFromDB_OverMidnight(t *testing.T) {
	// GTFS supports times > 24h for trips that span past midnight
	serviceDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	// 25:00:00 = 1:00 AM next day
	startNanos := int64(25 * time.Hour)
	// 27:00:00 = 3:00 AM next day
	endNanos := int64(27 * time.Hour)

	dbFreq := gtfsdb.Frequency{
		TripID:      "trip_late",
		StartTime:   startNanos,
		EndTime:     endNanos,
		HeadwaySecs: 1800,
		ExactTimes:  0,
	}

	freq := NewFrequencyFromDB(dbFreq, serviceDate)

	// Should resolve to Jan 16 at 1:00 AM and 3:00 AM
	expectedStart := time.Date(2024, 1, 16, 1, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2024, 1, 16, 3, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, freq.StartTime.Time)
	assert.Equal(t, expectedEnd, freq.EndTime.Time)
}

func TestFrequencyJSON(t *testing.T) {
	freq := Frequency{
		FrequencyWindow: FrequencyWindow{
			StartTime: NewModelTime(time.UnixMilli(1705305600000)),
			EndTime:   NewModelTime(time.UnixMilli(1705316400000)),
			Headway:   NewModelDuration(600 * time.Second),
		},
		ExactTimes: 1,
	}

	jsonData, err := json.Marshal(freq)
	require.NoError(t, err)

	var unmarshaled Frequency
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, freq, unmarshaled)

	// Verify JSON field names
	var raw map[string]any
	err = json.Unmarshal(jsonData, &raw)
	require.NoError(t, err)
	assert.Contains(t, raw, "startTime")
	assert.Contains(t, raw, "endTime")
	assert.Contains(t, raw, "headway")
	assert.Contains(t, raw, "exactTimes")

	// Verify schedule-for-stop fields are NOT present on the generic Frequency
	assert.NotContains(t, raw, "serviceDate")
	assert.NotContains(t, raw, "serviceId")
	assert.NotContains(t, raw, "tripId")
}

func TestFrequencyJSON_NilPointer(t *testing.T) {
	// When Frequency is a pointer field and nil, it should serialize as null
	type wrapper struct {
		Freq *Frequency `json:"frequency"`
	}

	w := wrapper{Freq: nil}
	jsonData, err := json.Marshal(w)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"frequency":null`)
}

func TestNewScheduleFrequencyFromDB(t *testing.T) {
	// Use a non-UTC timezone to verify start-of-day is computed in the
	// agency's local timezone.
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	serviceDate := time.Date(2024, 1, 15, 0, 0, 0, 0, loc)

	dbFreq := gtfsdb.Frequency{
		TripID:      "trip_1",
		StartTime:   int64(6 * time.Hour),
		EndTime:     int64(9 * time.Hour),
		HeadwaySecs: 600, // 10 minutes
		ExactTimes:  1,
	}

	sf := NewScheduleFrequencyFromDB(
		dbFreq,
		serviceDate,
		"service_1",         // serviceID
		"15_trip_1",         // tripID (combined form)
		"Downtown Terminal", // stopHeadsign
		true,                // arrivalEnabled
		false,               // departureEnabled
	)

	expectedStart := time.Date(2024, 1, 15, 6, 0, 0, 0, loc)
	expectedEnd := time.Date(2024, 1, 15, 9, 0, 0, 0, loc)

	assert.Equal(t, expectedStart, sf.StartTime.Time)
	assert.Equal(t, expectedEnd, sf.EndTime.Time)
	assert.Equal(t, 600*time.Second, sf.Headway.Duration)
	assert.Equal(t, serviceDate, sf.ServiceDate.Time)
	assert.Equal(t, "service_1", sf.ServiceID)
	assert.Equal(t, "15_trip_1", sf.TripID)
	assert.Equal(t, "Downtown Terminal", sf.StopHeadsign)
	assert.True(t, sf.ArrivalEnabled)
	assert.False(t, sf.DepartureEnabled)
}

func TestNewScheduleFrequencyFromDB_OverMidnight(t *testing.T) {
	serviceDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	dbFreq := gtfsdb.Frequency{
		TripID:      "trip_late",
		StartTime:   int64(25 * time.Hour),
		EndTime:     int64(27 * time.Hour),
		HeadwaySecs: 1800,
		ExactTimes:  0,
	}

	sf := NewScheduleFrequencyFromDB(dbFreq, serviceDate, "service_1", "15_trip_late", "Uptown", true, true)

	// Should resolve to Jan 16 at 1:00 AM and 3:00 AM
	expectedStart := time.Date(2024, 1, 16, 1, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2024, 1, 16, 3, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, sf.StartTime.Time)
	assert.Equal(t, expectedEnd, sf.EndTime.Time)
	assert.Equal(t, serviceDate, sf.ServiceDate.Time)
}

func TestScheduleFrequencyJSON(t *testing.T) {
	sf := ScheduleFrequency{
		FrequencyWindow: FrequencyWindow{
			StartTime: NewModelTime(time.UnixMilli(1705305600000)),
			EndTime:   NewModelTime(time.UnixMilli(1705316400000)),
			Headway:   NewModelDuration(600 * time.Second),
		},
		ServiceDate:      NewModelTime(time.UnixMilli(1705276800000)),
		ServiceID:        "service_123",
		TripID:           "trip_67",
		StopHeadsign:     "Downtown Terminal",
		ArrivalEnabled:   true,
		DepartureEnabled: true,
	}

	jsonData, err := json.Marshal(sf)
	require.NoError(t, err)

	var unmarshaled ScheduleFrequency
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, sf, unmarshaled)

	// Verify JSON field names
	var raw map[string]any
	err = json.Unmarshal(jsonData, &raw)
	require.NoError(t, err)
	assert.Contains(t, raw, "startTime")
	assert.Contains(t, raw, "endTime")
	assert.Contains(t, raw, "headway")
	assert.Contains(t, raw, "serviceDate")
	assert.Contains(t, raw, "serviceId")
	assert.Contains(t, raw, "tripId")
	assert.Contains(t, raw, "stopHeadsign")
	assert.Contains(t, raw, "arrivalEnabled")
	assert.Contains(t, raw, "departureEnabled")

	// Verify exactTimes is NOT present on ScheduleFrequency
	assert.NotContains(t, raw, "exactTimes")
}

func TestScheduleFrequencyJSON_StopHeadsignOmitEmpty(t *testing.T) {
	sf := ScheduleFrequency{
		FrequencyWindow: FrequencyWindow{
			StartTime: NewModelTime(time.UnixMilli(1705305600000)),
			EndTime:   NewModelTime(time.UnixMilli(1705316400000)),
			Headway:   NewModelDuration(600 * time.Second),
		},
		ServiceDate:      NewModelTime(time.UnixMilli(1705276800000)),
		ServiceID:        "service_123",
		TripID:           "trip_67",
		StopHeadsign:     "", // empty — should be omitted
		ArrivalEnabled:   true,
		DepartureEnabled: false,
	}

	jsonData, err := json.Marshal(sf)
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(jsonData, &raw)
	require.NoError(t, err)

	// stopHeadsign should be omitted when empty
	assert.NotContains(t, raw, "stopHeadsign")

	// Other fields should still be present
	assert.Contains(t, raw, "arrivalEnabled")
	assert.Contains(t, raw, "departureEnabled")
}
