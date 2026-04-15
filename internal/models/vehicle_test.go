package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVehicleStatus_JSONFields(t *testing.T) {

	t.Run("required fields always present", func(t *testing.T) {
		vs := VehicleStatus{
			VehicleID: "40_1234",
		}

		data, err := json.Marshal(vs)
		require.NoError(t, err)

		jsonStr := string(data)

		assert.Contains(t, jsonStr, `"vehicleId"`)
		assert.Contains(t, jsonStr, `"lastUpdateTime"`)
		assert.Contains(t, jsonStr, `"lastLocationUpdateTime"`)
		assert.Contains(t, jsonStr, `"location"`)
		assert.Contains(t, jsonStr, `"tripId"`)
		assert.Contains(t, jsonStr, `"tripStatus"`)

		// Optional fields should be absent at the top level.
		// Unmarshal to a map to avoid false positives from nested TripStatus JSON
		// (which has its own phase/status/occupancyStatus without omitempty).
		var top map[string]any
		require.NoError(t, json.Unmarshal(data, &top))
		assert.Equal(t, float64(0), top["occupancyCapacity"])
		assert.Equal(t, float64(0), top["occupancyCount"])
		assert.NotContains(t, top, "occupancyStatus")
		assert.NotContains(t, top, "phase")
		assert.NotContains(t, top, "status")

		assert.Equal(t, float64(0), top["lastUpdateTime"], "lastUpdateTime must serialize as 0 when not set")
		assert.Equal(t, float64(0), top["lastLocationUpdateTime"], "lastLocationUpdateTime must serialize as 0 when not set")
		assert.Nil(t, top["location"], "location must serialize as JSON null when not set")
		assert.Nil(t, top["tripStatus"], "tripStatus must serialize as JSON null when not set")
		assert.Equal(t, "", top["tripId"], "tripId must serialize as empty string when not set")
	})

	t.Run("optional fields appear only when set", func(t *testing.T) {
		capacity := 50
		count := 30
		ts := int64(1000)

		tripStatus := NewTripStatus()
		tripStatus.ActiveTripID = "trip_1"

		vs := VehicleStatus{
			VehicleID:              "40_1234",
			LastUpdateTime:         ts,
			LastLocationUpdateTime: ts,
			Location:               &Location{Lat: 47.65, Lon: -122.3},
			TripID:                 "trip_1",
			TripStatus:             tripStatus,
			OccupancyStatus:        "MANY_SEATS_AVAILABLE",
			OccupancyCapacity:      capacity,
			OccupancyCount:         count,
			Phase:                  "in_progress",
			Status:                 "IN_TRANSIT_TO",
		}

		data, err := json.Marshal(vs)
		require.NoError(t, err)

		jsonStr := string(data)

		assert.Contains(t, jsonStr, `"occupancyStatus":"MANY_SEATS_AVAILABLE"`)
		assert.Contains(t, jsonStr, `"occupancyCapacity":50`)
		assert.Contains(t, jsonStr, `"occupancyCount":30`)
		assert.Contains(t, jsonStr, `"phase":"in_progress"`)
		assert.Contains(t, jsonStr, `"status":"IN_TRANSIT_TO"`)

		// Optional fields should be absent at the top level.
		// Unmarshal to a map to avoid false positives from nested TripStatus JSON
		// (which has its own phase/status/occupancyStatus without omitempty).
		vs.OccupancyCapacity = 0
		vs.OccupancyCount = 0
		vs.OccupancyStatus = ""
		vs.Phase = ""
		vs.Status = ""

		data, err = json.Marshal(vs)
		require.NoError(t, err)

		var top map[string]any
		require.NoError(t, json.Unmarshal(data, &top))

		assert.Equal(t, float64(0), top["occupancyCapacity"])
		assert.Equal(t, float64(0), top["occupancyCount"])
		assert.NotContains(t, top, "occupancyStatus")
		assert.NotContains(t, top, "phase")
		assert.NotContains(t, top, "status")
	})
}
