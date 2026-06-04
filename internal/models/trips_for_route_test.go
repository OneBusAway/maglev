package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTripsForRouteListEntry_GetTripId(t *testing.T) {
	tripID := "unitrans_trip_456"
	entry := TripsForRouteListEntry{
		TripId:       tripID,
		ServiceDate:  1609459200000,
		Frequency:    nil,
		Schedule:     nil,
		Status:       nil,
		SituationIds: []string{},
	}

	assert.Equal(t, tripID, entry.GetTripId())
}

func TestTripsForRouteListEntryJSON(t *testing.T) {
	frequency := int64(600)
	entry := TripsForRouteListEntry{
		TripId:       "unitrans_trip_456",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		Status:       nil,
		SituationIds: []string{"situation_3"},
	}

	jsonData, err := json.Marshal(entry)
	assert.NoError(t, err)

	var unmarshaledEntry TripsForRouteListEntry
	err = json.Unmarshal(jsonData, &unmarshaledEntry)
	assert.NoError(t, err)

	assert.Equal(t, entry.TripId, unmarshaledEntry.TripId)
	assert.Equal(t, entry.ServiceDate, unmarshaledEntry.ServiceDate)
	assert.NotNil(t, unmarshaledEntry.Frequency)
	assert.Equal(t, *entry.Frequency, *unmarshaledEntry.Frequency)
	assert.Equal(t, entry.SituationIds, unmarshaledEntry.SituationIds)
}
