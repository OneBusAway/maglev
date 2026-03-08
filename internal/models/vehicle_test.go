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
		var top map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &top))
		assert.NotContains(t, top, "occupancyCapacity")
		assert.NotContains(t, top, "occupancyCount")
		assert.NotContains(t, top, "occupancyStatus")
		assert.NotContains(t, top, "phase")
		assert.NotContains(t, top, "status")
	})

	t.Run("optional fields appear only when set", func(t *testing.T) {
		capacity := 50
		count := 30
		ts := int64(1000)

		tripStatus := NewTripStatus()
		tripStatus.ActiveTripID = "trip_1"

		vs := VehicleStatus{
			VehicleID:              "40_1234",
			LastUpdateTime:         &ts,
			LastLocationUpdateTime: &ts,
			Location:               &Location{Lat: 47.65, Lon: -122.3},
			TripID:                 "trip_1",
			TripStatus:             tripStatus,
			OccupancyStatus:        "MANY_SEATS_AVAILABLE",
			OccupancyCapacity:      &capacity,
			OccupancyCount:         &count,
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
		vs.OccupancyCapacity = nil
		vs.OccupancyCount = nil
		vs.OccupancyStatus = ""
		vs.Phase = ""
		vs.Status = ""

		data, err = json.Marshal(vs)
		require.NoError(t, err)

		var top map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &top))

		assert.NotContains(t, top, "occupancyCapacity")
		assert.NotContains(t, top, "occupancyCount")
		assert.NotContains(t, top, "occupancyStatus")
		assert.NotContains(t, top, "phase")
		assert.NotContains(t, top, "status")
	})
}
