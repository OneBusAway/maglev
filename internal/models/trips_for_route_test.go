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

func TestTripsForRouteDataJSON(t *testing.T) {
	frequency := int64(600)
	entry1 := TripsForRouteListEntry{
		TripId:       "trip_1",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		Status:       nil,
		SituationIds: []string{},
	}

	entry2 := TripsForRouteListEntry{
		TripId:       "trip_2",
		ServiceDate:  1609459200000,
		Frequency:    nil,
		Schedule:     nil,
		Status:       nil,
		SituationIds: []string{"situation_1"},
	}

	data := TripsForRouteData{
		LimitExceeded: true,
		List:          []TripsForRouteListEntry{entry1, entry2},
	}

	jsonData, err := json.Marshal(data)
	assert.NoError(t, err)

	var unmarshaledData TripsForRouteData
	err = json.Unmarshal(jsonData, &unmarshaledData)
	assert.NoError(t, err)

	assert.Equal(t, data.LimitExceeded, unmarshaledData.LimitExceeded)
	assert.Equal(t, 2, len(unmarshaledData.List))
	assert.Equal(t, entry1.TripId, unmarshaledData.List[0].TripId)
	assert.Equal(t, entry2.TripId, unmarshaledData.List[1].TripId)
}

func TestTripsForRouteResponseJSON(t *testing.T) {
	frequency := int64(600)
	entry := TripsForRouteListEntry{
		TripId:       "trip_1",
		ServiceDate:  1609459200000,
		Frequency:    &frequency,
		Schedule:     nil,
		Status:       nil,
		SituationIds: []string{},
	}

	response := TripsForRouteResponse{
		Code:        200,
		CurrentTime: 1609459200000,
		Data: TripsForRouteData{
			LimitExceeded: false,
			List:          []TripsForRouteListEntry{entry},
		},
	}

	jsonData, err := json.Marshal(response)
	assert.NoError(t, err)

	var unmarshaledResponse TripsForRouteResponse
	err = json.Unmarshal(jsonData, &unmarshaledResponse)
	assert.NoError(t, err)

	assert.Equal(t, response.Code, unmarshaledResponse.Code)
	assert.Equal(t, response.CurrentTime, unmarshaledResponse.CurrentTime)
	assert.Equal(t, response.Data.LimitExceeded, unmarshaledResponse.Data.LimitExceeded)
	assert.Equal(t, 1, len(unmarshaledResponse.Data.List))
}

func TestTripsForRouteWithEmptyList(t *testing.T) {
	data := TripsForRouteData{
		LimitExceeded: false,
		List:          []TripsForRouteListEntry{},
	}

	assert.False(t, data.LimitExceeded)
	assert.Empty(t, data.List)
}
