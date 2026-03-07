package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouteCreation(t *testing.T) {
	id := "1"
	agencyID := "agency-1"
	shortName := "A"
	longName := "Downtown Express"
	description := "Express service to downtown"
	routeType := RouteType(3)
	url := "https://transit.org/routes/1"
	color := "FF0000"
	textColor := "FFFFFF"

	route := NewRoute(
		id, agencyID, shortName, longName, description,
		routeType, url, color, textColor)

	assert.Equal(t, id, route.ID)
	assert.Equal(t, agencyID, route.AgencyID)
	assert.Equal(t, shortName, route.ShortName)
	assert.Equal(t, longName, route.LongName)
	assert.Equal(t, description, route.Description)
	assert.Equal(t, routeType, route.Type)
	assert.Equal(t, url, route.URL)
	assert.Equal(t, color, route.Color)
	assert.Equal(t, textColor, route.TextColor)
	assert.Equal(t, "A", route.NullSafeShortName)
}

func TestRouteJSON(t *testing.T) {
	route := Route{
		ID:                "2",
		AgencyID:          "agency-2",
		ShortName:         "B",
		LongName:          "Airport Shuttle",
		Description:       "Service to the airport",
		Type:              RouteType(2),
		URL:               "https://transit.org/routes/2",
		Color:             "00FF00",
		TextColor:         "000000",
		NullSafeShortName: "B",
	}

	jsonData, err := json.Marshal(route)
	assert.NoError(t, err)

	var unmarshaledRoute Route
	err = json.Unmarshal(jsonData, &unmarshaledRoute)
	assert.NoError(t, err)

	assert.Equal(t, route.ID, unmarshaledRoute.ID)
	assert.Equal(t, route.AgencyID, unmarshaledRoute.AgencyID)
	assert.Equal(t, route.ShortName, unmarshaledRoute.ShortName)
	assert.Equal(t, route.LongName, unmarshaledRoute.LongName)
	assert.Equal(t, route.Description, unmarshaledRoute.Description)
	assert.Equal(t, route.Type, unmarshaledRoute.Type)
	assert.Equal(t, route.URL, unmarshaledRoute.URL)
	assert.Equal(t, route.Color, unmarshaledRoute.Color)
	assert.Equal(t, route.TextColor, unmarshaledRoute.TextColor)
	assert.Equal(t, route.NullSafeShortName, unmarshaledRoute.NullSafeShortName)
}

func TestRouteWithEmptyValues(t *testing.T) {
	route := NewRoute("", "", "", "", "", 0, "", "", "")

	assert.Equal(t, "", route.ID)
	assert.Equal(t, "", route.AgencyID)
	assert.Equal(t, "", route.ShortName)
	assert.Equal(t, "", route.LongName)
	assert.Equal(t, "", route.Description)
	assert.Equal(t, RouteType(0), route.Type)
	assert.Equal(t, "", route.URL)
	assert.Equal(t, "", route.Color)
	assert.Equal(t, "", route.TextColor)
	assert.Equal(t, "", route.NullSafeShortName)
}

func TestRouteWithNilValuesJSON(t *testing.T) {
	route := Route{
		ID:       "3",
		AgencyID: "agency-3",
	}

	jsonData, err := json.Marshal(route)
	assert.NoError(t, err)

	jsonStr := string(jsonData)
	assert.Contains(t, jsonStr, `"id":"3"`)
	assert.Contains(t, jsonStr, `"agencyId":"agency-3"`)
	assert.Contains(t, jsonStr, `"shortName":""`)
	assert.Contains(t, jsonStr, `"longName":""`)
	assert.Contains(t, jsonStr, `"type":0`)
}

func TestRouteDataJSON(t *testing.T) {
	route1 := NewRoute("1", "agency-1", "A", "Route A", "Description A", 3, "url-a", "FF0000", "FFFFFF")
	route2 := NewRoute("2", "agency-1", "B", "Route B", "Description B", 2, "url-b", "00FF00", "000000")

	routeData := RouteData{
		LimitExceeded: false,
		List:          []Route{route1, route2},
		References:    NewEmptyReferences(),
	}

	jsonData, err := json.Marshal(routeData)
	assert.NoError(t, err)

	var unmarshaledRouteData RouteData
	err = json.Unmarshal(jsonData, &unmarshaledRouteData)
	assert.NoError(t, err)

	assert.Equal(t, routeData.LimitExceeded, unmarshaledRouteData.LimitExceeded)
	assert.Len(t, unmarshaledRouteData.List, 2)
	assert.Equal(t, route1.ID, unmarshaledRouteData.List[0].ID)
	assert.Equal(t, route2.ID, unmarshaledRouteData.List[1].ID)
}

func TestRouteResponseJSON(t *testing.T) {
	route := NewRoute("1", "agency-1", "A", "Route A", "Description A", 3, "url-a", "FF0000", "FFFFFF")

	references := ReferencesModel{
		Agencies: []AgencyReference{
			NewAgencyReference("agency-1", "Agency Name", "http://agency.org", "America/New_York", "en", "555-1234", "info@agency.org", "http://fares.org", "", false),
		},
		Routes:     []Route{},
		Situations: []Situation{},
		StopTimes:  []RouteStopTime{},
		Stops:      []Stop{},
		Trips:      []Trip{},
	}

	routeData := RouteData{
		LimitExceeded: false,
		List:          []Route{route},
		References:    references,
	}

	response := RouteResponse{
		Code:        200,
		CurrentTime: 1633046400000,
		Data:        routeData,
		Text:        "OK",
		Version:     2,
	}

	jsonData, err := json.Marshal(response)
	assert.NoError(t, err)

	var unmarshaledResponse RouteResponse
	err = json.Unmarshal(jsonData, &unmarshaledResponse)
	assert.NoError(t, err)

	assert.Equal(t, response.Code, unmarshaledResponse.Code)
	assert.Equal(t, response.CurrentTime, unmarshaledResponse.CurrentTime)
	assert.Equal(t, response.Text, unmarshaledResponse.Text)
	assert.Equal(t, response.Version, unmarshaledResponse.Version)

	assert.Equal(t, response.Data.LimitExceeded, unmarshaledResponse.Data.LimitExceeded)
	assert.Len(t, unmarshaledResponse.Data.List, 1)
	assert.Equal(t, route.ID, unmarshaledResponse.Data.List[0].ID)

	assert.Len(t, unmarshaledResponse.Data.References.Agencies, 1)
	assert.Equal(t, "agency-1", unmarshaledResponse.Data.References.Agencies[0].ID)
}

func TestRouteNullSafeShortNameFallback(t *testing.T) {
	// When shortName is empty, NullSafeShortName should fall back to longName
	route := NewRoute("1", "agency-1", "", "Downtown Express", "", 3, "", "", "")
	assert.Equal(t, "Downtown Express", route.NullSafeShortName)
	assert.Equal(t, "", route.ShortName) // ShortName itself stays empty

	// When shortName is present, NullSafeShortName should use it
	route2 := NewRoute("2", "agency-1", "DX", "Downtown Express", "", 3, "", "", "")
	assert.Equal(t, "DX", route2.NullSafeShortName)
}
