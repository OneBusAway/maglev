package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStopCreation(t *testing.T) {
	code := "22005"
	direction := UnknownValue
	id := "unitrans_22005"
	name := "2nd St & B St (EB)"
	parent := ""
	wheelchairBoarding := UnknownValue
	lat := 38.542661
	lon := -121.743914
	locationType := 0
	routeIDs := []string{"unitrans_FMS"}
	staticRouteIDs := []string{"unitrans_FMS"}

	stop := NewStop(code, direction, id, name, parent, wheelchairBoarding, lat, lon, locationType, routeIDs, staticRouteIDs)

	assert.Equal(t, code, stop.Code)
	assert.Equal(t, direction, stop.Direction)
	assert.Equal(t, id, stop.ID)
	assert.Equal(t, name, stop.Name)
	assert.Equal(t, parent, stop.Parent)
	assert.Equal(t, wheelchairBoarding, stop.WheelchairBoarding)
	assert.Equal(t, lat, stop.Lat)
	assert.Equal(t, lon, stop.Lon)
	assert.Equal(t, locationType, stop.LocationType)
	assert.Equal(t, routeIDs, stop.RouteIDs)
	assert.Equal(t, staticRouteIDs, stop.StaticRouteIDs)
}

func TestStopJSON(t *testing.T) {
	stop := Stop{
		Code:               "22005",
		Direction:          UnknownValue,
		ID:                 "unitrans_22005",
		Name:               "2nd St & B St (EB)",
		Parent:             "",
		WheelchairBoarding: UnknownValue,
		Lat:                38.542661,
		Lon:                -121.743914,
		LocationType:       0,
		RouteIDs:           []string{"unitrans_FMS"},
		StaticRouteIDs:     []string{"unitrans_FMS"},
	}

	jsonData, err := json.Marshal(stop)
	assert.NoError(t, err)

	var unmarshaledStop Stop
	err = json.Unmarshal(jsonData, &unmarshaledStop)
	assert.NoError(t, err)

	assert.Equal(t, stop.Code, unmarshaledStop.Code)
	assert.Equal(t, stop.Direction, unmarshaledStop.Direction)
	assert.Equal(t, stop.ID, unmarshaledStop.ID)
	assert.Equal(t, stop.Name, unmarshaledStop.Name)
	assert.Equal(t, stop.Parent, unmarshaledStop.Parent)
	assert.Equal(t, stop.WheelchairBoarding, unmarshaledStop.WheelchairBoarding)
	assert.Equal(t, stop.Lat, unmarshaledStop.Lat)
	assert.Equal(t, stop.Lon, unmarshaledStop.Lon)
	assert.Equal(t, stop.LocationType, unmarshaledStop.LocationType)
	assert.Equal(t, stop.RouteIDs, unmarshaledStop.RouteIDs)
	assert.Equal(t, stop.StaticRouteIDs, unmarshaledStop.StaticRouteIDs)
}

func TestStopWithEmptyValues(t *testing.T) {
	stop := NewStop("", "", "", "", "", "", 0, 0, 0, nil, nil)

	assert.Equal(t, "", stop.Code)
	assert.Equal(t, "", stop.Direction)
	assert.Equal(t, "", stop.ID)
	assert.Equal(t, "", stop.Name)
	assert.Equal(t, "", stop.Parent)
	assert.Equal(t, "", stop.WheelchairBoarding)
	assert.Equal(t, 0.0, stop.Lat)
	assert.Equal(t, 0.0, stop.Lon)
	assert.Equal(t, 0, stop.LocationType)
	assert.Nil(t, stop.RouteIDs)
	assert.Nil(t, stop.StaticRouteIDs)
}

func TestStopsResponseJSON(t *testing.T) {
	stop1 := NewStop("22005", UnknownValue, "unitrans_22005", "2nd St & B St (EB)", "", UnknownValue,
		38.542661, -121.743914, 0, []string{"unitrans_FMS"}, []string{"unitrans_FMS"})
	stop2 := NewStop("22002", UnknownValue, "unitrans_22002", "1st St & C St / Downtown (EB)", "", UnknownValue,
		38.541523, -121.742543, 0, []string{"unitrans_M", "unitrans_W"}, []string{"unitrans_M", "unitrans_W"})

	response := StopsResponse{
		List:       []Stop{stop1, stop2},
		OutOfRange: false,
	}

	jsonData, err := json.Marshal(response)
	assert.NoError(t, err)

	var unmarshaledResponse StopsResponse
	err = json.Unmarshal(jsonData, &unmarshaledResponse)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(unmarshaledResponse.List))
	assert.Equal(t, response.OutOfRange, unmarshaledResponse.OutOfRange)
	assert.Equal(t, response.List[0].ID, unmarshaledResponse.List[0].ID)
	assert.Equal(t, response.List[1].ID, unmarshaledResponse.List[1].ID)
}

func TestStopsResponseWithEmptyList(t *testing.T) {
	response := StopsResponse{
		List:       []Stop{},
		OutOfRange: true,
	}

	jsonData, err := json.Marshal(response)
	assert.NoError(t, err)

	var unmarshaledResponse StopsResponse
	err = json.Unmarshal(jsonData, &unmarshaledResponse)
	assert.NoError(t, err)

	assert.Empty(t, unmarshaledResponse.List)
	assert.True(t, unmarshaledResponse.OutOfRange)
}
