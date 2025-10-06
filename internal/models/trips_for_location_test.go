package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	frequency := int64(300)
	entry := TripsForLocationListEntry{
		TripId:       "unitrans_trip_123",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		SituationIds: []string{"situation_1", "situation_2"},
	}

	jsonData, err := json.Marshal(entry)
	assert.NoError(t, err)

	var unmarshaledEntry TripsForLocationListEntry
	err = json.Unmarshal(jsonData, &unmarshaledEntry)
	assert.NoError(t, err)

	assert.Equal(t, entry.TripId, unmarshaledEntry.TripId)
	assert.Equal(t, entry.ServiceDate, unmarshaledEntry.ServiceDate)
	assert.NotNil(t, unmarshaledEntry.Frequency)
	assert.Equal(t, *entry.Frequency, *unmarshaledEntry.Frequency)
	assert.Equal(t, entry.SituationIds, unmarshaledEntry.SituationIds)
}

func TestTripsForLocationDataJSON(t *testing.T) {
	frequency := int64(300)
	entry1 := TripsForLocationListEntry{
		TripId:       "trip_1",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		SituationIds: []string{},
	}

	entry2 := TripsForLocationListEntry{
		TripId:       "trip_2",
		ServiceDate:  1609459200000,
		Frequency:    nil,
		Schedule:     nil,
		SituationIds: []string{"situation_1"},
	}

	data := TripsForLocationData{
		LimitExceeded: false,
		List:          []TripsForLocationListEntry{entry1, entry2},
	}

	jsonData, err := json.Marshal(data)
	assert.NoError(t, err)

	var unmarshaledData TripsForLocationData
	err = json.Unmarshal(jsonData, &unmarshaledData)
	assert.NoError(t, err)

	assert.Equal(t, data.LimitExceeded, unmarshaledData.LimitExceeded)
	assert.Equal(t, 2, len(unmarshaledData.List))
	assert.Equal(t, entry1.TripId, unmarshaledData.List[0].TripId)
	assert.Equal(t, entry2.TripId, unmarshaledData.List[1].TripId)
}

func TestTripsForLocationResponseJSON(t *testing.T) {
	frequency := int64(300)
	entry := TripsForLocationListEntry{
		TripId:       "trip_1",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		SituationIds: []string{},
	}

	response := TripsForLocationResponse{
		Code:        200,
		CurrentTime: 1609459200000,
		Data: TripsForLocationData{
			LimitExceeded: false,
			List:          []TripsForLocationListEntry{entry},
		},
	}

	jsonData, err := json.Marshal(response)
	assert.NoError(t, err)

	var unmarshaledResponse TripsForLocationResponse
	err = json.Unmarshal(jsonData, &unmarshaledResponse)
	assert.NoError(t, err)

	assert.Equal(t, response.Code, unmarshaledResponse.Code)
	assert.Equal(t, response.CurrentTime, unmarshaledResponse.CurrentTime)
	assert.Equal(t, response.Data.LimitExceeded, unmarshaledResponse.Data.LimitExceeded)
	assert.Equal(t, 1, len(unmarshaledResponse.Data.List))
}

func TestTripsForLocationWithEmptyList(t *testing.T) {
	data := TripsForLocationData{
		LimitExceeded: true,
		List:          []TripsForLocationListEntry{},
	}

	assert.True(t, data.LimitExceeded)
	assert.Empty(t, data.List)
}
