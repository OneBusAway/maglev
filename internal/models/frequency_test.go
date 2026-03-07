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

	expectedStart := time.Date(2024, 1, 15, 6, 0, 0, 0, loc).UnixMilli()
	expectedEnd := time.Date(2024, 1, 15, 9, 0, 0, 0, loc).UnixMilli()

	assert.Equal(t, expectedStart, freq.StartTime)
	assert.Equal(t, expectedEnd, freq.EndTime)
	assert.Equal(t, 600, freq.Headway)
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

	assert.Equal(t, 300, freq.Headway)
	assert.Equal(t, 0, freq.ExactTimes)
	assert.Greater(t, freq.EndTime, freq.StartTime)
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
	expectedStart := time.Date(2024, 1, 16, 1, 0, 0, 0, time.UTC).UnixMilli()
	expectedEnd := time.Date(2024, 1, 16, 3, 0, 0, 0, time.UTC).UnixMilli()

	assert.Equal(t, expectedStart, freq.StartTime)
	assert.Equal(t, expectedEnd, freq.EndTime)
}

func TestNewFrequency(t *testing.T) {
	freq := NewFrequency(1705305600000, 1705316400000, 600, 1)

	assert.Equal(t, int64(1705305600000), freq.StartTime)
	assert.Equal(t, int64(1705316400000), freq.EndTime)
	assert.Equal(t, 600, freq.Headway)
	assert.Equal(t, 1, freq.ExactTimes)
}

func TestFrequencyJSON(t *testing.T) {
	freq := Frequency{
		StartTime:  1705305600000,
		EndTime:    1705316400000,
		Headway:    600,
		ExactTimes: 1, // This shouldn't be serialized to API clients
	}

	jsonData, err := json.Marshal(freq)
	require.NoError(t, err)

	var unmarshaled Frequency
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Since ExactTimes is ignored in JSON, it defaults to 0 when unmarshaled
	expected := freq
	expected.ExactTimes = 0
	assert.Equal(t, expected, unmarshaled)

	// Verify JSON field names
	var raw map[string]interface{}
	err = json.Unmarshal(jsonData, &raw)
	require.NoError(t, err)
	assert.Contains(t, raw, "startTime")
	assert.Contains(t, raw, "endTime")
	assert.Contains(t, raw, "headway")

	// IMPORTANT: Verify exactTimes is NOT in the JSON (API Backward Compatibility)
	assert.NotContains(t, raw, "exactTimes")
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

func TestFrequencyJSON_NonNilPointer(t *testing.T) {
	type wrapper struct {
		Freq *Frequency `json:"frequency"`
	}

	freq := NewFrequency(1000, 2000, 300, 0)
	w := wrapper{Freq: &freq}
	jsonData, err := json.Marshal(w)
	require.NoError(t, err)

	var result wrapper
	err = json.Unmarshal(jsonData, &result)
	require.NoError(t, err)
	assert.NotNil(t, result.Freq)
	assert.Equal(t, 300, result.Freq.Headway)
}
