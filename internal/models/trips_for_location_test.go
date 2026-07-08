package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestFrequency(headwaySecs int) *Frequency {
	return &Frequency{
		StartTime: NewModelTime(time.UnixMilli(1609459200000)),
		EndTime:   NewModelTime(time.UnixMilli(1609462800000)),
		Headway:   NewModelDuration(time.Duration(headwaySecs) * time.Second),
	}
}

func TestTripsForLocationListEntry_GetTripId(t *testing.T) {
	tripID := "unitrans_trip_123"
	entry := TripsForLocationListEntry{
		TripId:       tripID,
		ServiceDate:  1609459200000,
		Frequency:    nil,
		Schedule:     nil,
		SituationIds: []string{},
	}

	assert.Equal(t, tripID, entry.GetTripId())
}

func TestTripsForLocationListEntryJSON(t *testing.T) {
	frequency := newTestFrequency(300)

	status := NewTripStatus()
	status.ActiveTripID = "mock_active_trip_123"
	status.Phase = "in_progress"

	entry := TripsForLocationListEntry{
		TripId:       "unitrans_trip_123",
		ServiceDate:  1609459200000,
		Frequency:    frequency,
		Schedule:     nil,
		SituationIds: []string{"situation_1", "situation_2"},
		Status:       status,
	}

	jsonData, err := json.Marshal(entry)
	assert.NoError(t, err)

	var unmarshaledEntry TripsForLocationListEntry
	err = json.Unmarshal(jsonData, &unmarshaledEntry)
	assert.NoError(t, err)

	assert.Equal(t, entry.TripId, unmarshaledEntry.TripId)
	assert.Equal(t, entry.ServiceDate, unmarshaledEntry.ServiceDate)
	assert.NotNil(t, unmarshaledEntry.Frequency)
	assert.Equal(t, entry.Frequency.Headway, unmarshaledEntry.Frequency.Headway)
	assert.Equal(t, entry.SituationIds, unmarshaledEntry.SituationIds)

	assert.NotNil(t, unmarshaledEntry.Status)
	assert.Equal(t, entry.Status.ActiveTripID, unmarshaledEntry.Status.ActiveTripID)
	assert.Equal(t, entry.Status.Phase, unmarshaledEntry.Status.Phase)
}
