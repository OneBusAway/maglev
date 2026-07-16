package restapi

import (
	"context"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

func TestGetVehicleStatusAndPhase_NilVehicle(t *testing.T) {
	status, phase := GetVehicleStatusAndPhase(nil)
	assert.Equal(t, "default", status)
	assert.Equal(t, "scheduled", phase)
}

func TestGetVehicleStatusAndPhase_ScheduledTrip(t *testing.T) {
	sr := gtfsrt.TripDescriptor_SCHEDULED
	vehicle := &gtfs.Vehicle{
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{ScheduleRelationship: sr},
		},
	}
	status, phase := GetVehicleStatusAndPhase(vehicle)
	assert.Equal(t, "SCHEDULED", status)
	assert.Equal(t, "in_progress", phase)
}

func TestGetVehicleStatusAndPhase_CanceledTrip(t *testing.T) {
	sr := gtfsrt.TripDescriptor_CANCELED
	vehicle := &gtfs.Vehicle{
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{ScheduleRelationship: sr},
		},
	}
	status, phase := GetVehicleStatusAndPhase(vehicle)
	assert.Equal(t, "CANCELED", status)
	assert.Equal(t, "", phase, "canceled trip should have empty phase")
}

func TestGetVehicleStatusAndPhase_AddedTrip(t *testing.T) {
	sr := gtfsrt.TripDescriptor_ADDED
	vehicle := &gtfs.Vehicle{
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{ScheduleRelationship: sr},
		},
	}
	status, phase := GetVehicleStatusAndPhase(vehicle)
	assert.Equal(t, "ADDED", status)
	assert.Equal(t, "in_progress", phase)
}

func TestGetVehicleStatusAndPhase_DuplicatedTrip(t *testing.T) {
	sr := gtfsrt.TripDescriptor_DUPLICATED
	vehicle := &gtfs.Vehicle{
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{ScheduleRelationship: sr},
		},
	}
	status, phase := GetVehicleStatusAndPhase(vehicle)
	assert.Equal(t, "DUPLICATED", status)
	assert.Equal(t, "in_progress", phase)
}

func TestGetVehicleStatusAndPhase_NoTripInfo(t *testing.T) {
	// Vehicle present but no Trip field — should default to SCHEDULED
	vehicle := &gtfs.Vehicle{}
	status, phase := GetVehicleStatusAndPhase(vehicle)
	assert.Equal(t, "SCHEDULED", status)
	assert.Equal(t, "in_progress", phase)
}

func TestStaleDetector_NilVehicle(t *testing.T) {
	d := NewStaleDetector()
	assert.True(t, d.Check(nil, time.Now()), "nil vehicle should be considered stale")
}

func TestStaleDetector_NilTimestamp_NoPosition(t *testing.T) {
	d := NewStaleDetector()
	vehicle := &gtfs.Vehicle{}
	assert.True(t, d.Check(vehicle, time.Now()), "vehicle with nil timestamp and no position should be considered stale")
}

func TestStaleDetector_NilTimestamp_WithPosition(t *testing.T) {
	d := NewStaleDetector()
	lat := float32(37.7749)
	lon := float32(-122.4194)
	vehicle := &gtfs.Vehicle{
		Position: &gtfs.Position{
			Latitude:  &lat,
			Longitude: &lon,
		},
	}
	assert.False(t, d.Check(vehicle, time.Now()), "vehicle with nil timestamp but valid position should not be stale")
}

func TestStaleDetector_FreshVehicle(t *testing.T) {
	d := NewStaleDetector()
	now := time.Now()
	recent := now.Add(-5 * time.Minute)
	vehicle := &gtfs.Vehicle{Timestamp: &recent}
	assert.False(t, d.Check(vehicle, now), "vehicle updated 5 minutes ago should not be stale with 15-minute threshold")
}

func TestStaleDetector_StaleVehicle(t *testing.T) {
	d := NewStaleDetector()
	now := time.Now()
	old := now.Add(-20 * time.Minute)
	vehicle := &gtfs.Vehicle{Timestamp: &old}
	assert.True(t, d.Check(vehicle, now), "vehicle updated 20 minutes ago should be stale with 15-minute threshold")
}

func TestStaleDetector_ExactThreshold(t *testing.T) {
	d := NewStaleDetector()
	now := time.Now()
	exactly := now.Add(-15 * time.Minute)
	vehicle := &gtfs.Vehicle{Timestamp: &exactly}
	// Exactly at threshold is NOT stale (threshold is strict >)
	assert.False(t, d.Check(vehicle, now), "vehicle at exactly 15 minutes should not be stale")
}

func TestBuildVehicleStatus_NilVehicleSetsDefaultStatus(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	now := time.Now()
	status := models.NewTripStatus()
	api.BuildVehicleStatus(ctx, nil, "any-trip", "any-agency", status, now)

	assert.Equal(t, "default", status.Status)
	assert.Equal(t, "scheduled", status.Phase)
	assert.False(t, status.Predicted, "BuildVehicleStatus must not set Predicted")
}

func TestBuildVehicleStatus_StaleVehicleSetsDefaultStatus(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	now := time.Now()
	old := now.Add(-20 * time.Minute)
	vehicle := &gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: "v1"},
		Timestamp: &old,
	}

	status := models.NewTripStatus()
	api.BuildVehicleStatus(ctx, vehicle, "any-trip", "any-agency", status, now)

	assert.Equal(t, "default", status.Status)
	assert.Equal(t, "scheduled", status.Phase)
}

func TestBuildVehicleStatus_FreshVehicleWithPosition_SetsLocationAndPhase(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	now := time.Now()
	lat := float32(37.7749)
	lon := float32(-122.4194)
	vehicle := &gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: "v1"},
		Timestamp: &now,
		Position: &gtfs.Position{
			Latitude:  &lat,
			Longitude: &lon,
		},
	}

	status := models.NewTripStatus()
	api.BuildVehicleStatus(ctx, vehicle, "any-trip", "any-agency", status, now)

	assert.False(t, status.Predicted, "BuildVehicleStatus must not set Predicted")
	assert.Equal(t, "SCHEDULED", status.Status)
	assert.Equal(t, "in_progress", status.Phase)
}

func TestBuildVehicleStatus_FreshVehicleNoPosition_DoesNotSetPredicted(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	now := time.Now()
	vehicle := &gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: "v1"},
		Timestamp: &now,
		// No Position
	}

	status := models.NewTripStatus()
	api.BuildVehicleStatus(ctx, vehicle, "any-trip", "any-agency", status, now)

	assert.False(t, status.Predicted, "BuildVehicleStatus must not set Predicted")
}

func TestBuildVehicleStatus_BearingConversion(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tests := []struct {
		name                string
		bearing             float32
		expectedOrientation float64
	}{
		{
			name:                "North (0°) → 90°",
			bearing:             0,
			expectedOrientation: 90,
		},
		{
			name:                "East (90°) → 0°",
			bearing:             90,
			expectedOrientation: 0,
		},
		{
			name:                "South (180°) → 270°",
			bearing:             180,
			expectedOrientation: 270,
		},
		{
			name:                "West (270°) → 180°",
			bearing:             270,
			expectedOrientation: 180,
		},
		{
			name:                "NW (315°) → 135°",
			bearing:             315,
			expectedOrientation: 135,
		},
		{
			name:                "Bearing > 90 wraps (120°) → 330°",
			bearing:             120,
			expectedOrientation: 330,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			lat := float32(37.7749)
			lon := float32(-122.4194)
			bearing := tt.bearing
			vehicle := &gtfs.Vehicle{
				ID:        &gtfs.VehicleID{ID: "v-bearing"},
				Timestamp: &now,
				Position: &gtfs.Position{
					Latitude:  &lat,
					Longitude: &lon,
					Bearing:   &bearing,
				},
			}

			status := models.NewTripStatus()
			api.BuildVehicleStatus(ctx, vehicle, "any-trip", "any-agency", status, now)

			assert.Equal(t, tt.expectedOrientation, status.Orientation, "Orientation should be (90 - bearing) with wraparound")
			assert.Equal(t, tt.expectedOrientation, status.LastKnownOrientation, "LastKnownOrientation should match Orientation")
		})
	}
}

func TestResolveActiveTripID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Monday within the RABA dataset's active service period.
	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC)
	formattedDate := serviceDate.Format("20060102")
	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	require.NoError(t, err)
	require.NotEmpty(t, serviceIDs)

	trips, err := api.GtfsManager.GetTrips(ctx, 200)
	require.NoError(t, err)

	// Find a block with at least two ordered trips that have scheduled windows.
	var blockTrips []gtfsdb.GetTripsByBlockIDOrderedRow
	seen := make(map[string]bool)
	for _, tr := range trips {
		row, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tr.ID)
		if err != nil || !row.BlockID.Valid || row.BlockID.String == "" || seen[row.BlockID.String] {
			continue
		}
		seen[row.BlockID.String] = true

		ordered, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
			BlockID:    nulls.String(row.BlockID.String),
			ServiceIds: serviceIDs,
		})
		if err != nil {
			continue
		}
		var withWindows []gtfsdb.GetTripsByBlockIDOrderedRow
		for _, bt := range ordered {
			if bt.EarliestTime.Valid && bt.LatestTime.Valid {
				withWindows = append(withWindows, bt)
			}
		}
		if len(withWindows) >= 2 {
			blockTrips = withWindows
			break
		}
	}
	require.GreaterOrEqual(t, len(blockTrips), 2, "need a block with >=2 scheduled trips in test data")

	nominal := blockTrips[0]
	active := blockTrips[1]

	t.Run("returns interlining active trip at its scheduled time", func(t *testing.T) {
		// A reference time inside the second trip's window while the nominal trip is the first.
		midWindowNs := (active.EarliestTime.Int64 + active.LatestTime.Int64) / 2
		refTime := serviceDate.Add(time.Duration(midWindowNs))

		got := api.resolveActiveTripID(ctx, nominal.ID, refTime)
		assert.Equal(t, active.ID, got,
			"expected the trip whose scheduled window contains the reference time")
	})

	t.Run("returns nominal trip within its own window", func(t *testing.T) {
		midWindowNs := (nominal.EarliestTime.Int64 + nominal.LatestTime.Int64) / 2
		refTime := serviceDate.Add(time.Duration(midWindowNs))

		got := api.resolveActiveTripID(ctx, nominal.ID, refTime)
		assert.Equal(t, nominal.ID, got)
	})

	t.Run("falls back to nominal when no window matches", func(t *testing.T) {
		// A time before any block trip starts.
		refTime := serviceDate.Add(-1 * time.Hour)
		got := api.resolveActiveTripID(ctx, nominal.ID, refTime)
		assert.Equal(t, nominal.ID, got)
	})

	t.Run("falls back to nominal for unknown trip", func(t *testing.T) {
		got := api.resolveActiveTripID(ctx, "nonexistent-trip", serviceDate)
		assert.Equal(t, "nonexistent-trip", got)
	})
}
