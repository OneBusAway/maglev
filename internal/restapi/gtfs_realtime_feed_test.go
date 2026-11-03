package restapi

import (
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

// fullVehicle has every field go-gtfs keeps for a vehicle position.
func fullVehicle(reportedAt time.Time) gtfs.Vehicle {
	stopSequence, occupancyPercentage := uint32(7), uint32(40)
	latitude, longitude, bearing, speed := float32(47.6), float32(-122.3), float32(90), float32(8.5)
	odometer := 1234.5
	stopID := "S1"
	status := gtfs.CurrentStatus(gtfsrt.VehiclePosition_STOPPED_AT)
	occupancy := gtfs.OccupancyStatus(gtfsrt.VehiclePosition_FEW_SEATS_AVAILABLE)
	return gtfs.Vehicle{
		ID: &gtfs.VehicleID{ID: "V1", Label: "Bus 1", LicensePlate: "ABC123"},
		Trip: &gtfs.Trip{ID: gtfs.TripID{
			ID:                   "T1",
			RouteID:              "R1",
			DirectionID:          gtfs.DirectionID_True,
			HasStartTime:         true,
			StartTime:            25*time.Hour + 30*time.Minute,
			HasStartDate:         true,
			StartDate:            time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
			ScheduleRelationship: gtfsrt.TripDescriptor_ADDED,
		}},
		Position: &gtfs.Position{
			Latitude:  &latitude,
			Longitude: &longitude,
			Bearing:   &bearing,
			Odometer:  &odometer,
			Speed:     &speed,
		},
		CurrentStopSequence: &stopSequence,
		StopID:              &stopID,
		CurrentStatus:       &status,
		Timestamp:           &reportedAt,
		CongestionLevel:     gtfsrt.VehiclePosition_STOP_AND_GO,
		OccupancyStatus:     &occupancy,
		OccupancyPercentage: &occupancyPercentage,
	}
}

func TestNewVehiclePositionsFeed_Header(t *testing.T) {
	timestamp := time.Unix(1792287624, 0)

	feed := newVehiclePositionsFeed(internalgtfs.AgencyVehiclePositions{Timestamp: timestamp})

	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.Equal(t, uint64(1792287624), feed.GetHeader().GetTimestamp())
	assert.Nil(t, feed.GetHeader().Incrementality, "unset means FULL_DATASET")
	assert.Empty(t, feed.GetEntity())
}

func TestNewVehiclePositionsFeed_AllFields(t *testing.T) {
	reportedAt := time.Unix(1792287600, 0)
	feed := newVehiclePositionsFeed(internalgtfs.AgencyVehiclePositions{
		Vehicles:  []gtfs.Vehicle{fullVehicle(reportedAt)},
		Timestamp: reportedAt,
	})

	require.Len(t, feed.GetEntity(), 1)
	entity := feed.GetEntity()[0]
	assert.Equal(t, "V1", entity.GetId())
	position := entity.GetVehicle()
	require.NotNil(t, position)

	trip := position.GetTrip()
	assert.Equal(t, "T1", trip.GetTripId())
	assert.Equal(t, "R1", trip.GetRouteId())
	assert.Equal(t, uint32(1), trip.GetDirectionId())
	assert.Equal(t, "25:30:00", trip.GetStartTime())
	assert.Equal(t, "20261018", trip.GetStartDate())
	assert.Equal(t, gtfsrt.TripDescriptor_ADDED, trip.GetScheduleRelationship())

	assert.Equal(t, "V1", position.GetVehicle().GetId())
	assert.Equal(t, "Bus 1", position.GetVehicle().GetLabel())
	assert.Equal(t, "ABC123", position.GetVehicle().GetLicensePlate())

	assert.InDelta(t, 47.6, position.GetPosition().GetLatitude(), 0.0001)
	assert.InDelta(t, -122.3, position.GetPosition().GetLongitude(), 0.0001)
	assert.InDelta(t, 90, position.GetPosition().GetBearing(), 0.0001)
	assert.InDelta(t, 1234.5, position.GetPosition().GetOdometer(), 0.0001)
	assert.InDelta(t, 8.5, position.GetPosition().GetSpeed(), 0.0001)

	assert.Equal(t, uint32(7), position.GetCurrentStopSequence())
	assert.Equal(t, "S1", position.GetStopId())
	assert.Equal(t, gtfsrt.VehiclePosition_STOPPED_AT, position.GetCurrentStatus())
	assert.Equal(t, uint64(1792287600), position.GetTimestamp())
	assert.Equal(t, gtfsrt.VehiclePosition_STOP_AND_GO, position.GetCongestionLevel())
	assert.Equal(t, gtfsrt.VehiclePosition_FEW_SEATS_AVAILABLE, position.GetOccupancyStatus())
	assert.Equal(t, uint32(40), position.GetOccupancyPercentage())
}

func TestNewVehiclePositionsFeed_UnsetFieldsStayUnset(t *testing.T) {
	latitude, longitude := float32(47.6), float32(-122.3)
	vehicle := gtfs.Vehicle{
		ID:       &gtfs.VehicleID{ID: "V1"},
		Position: &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
	}

	position := newVehiclePositionsFeed(internalgtfs.AgencyVehiclePositions{Vehicles: []gtfs.Vehicle{vehicle}}).GetEntity()[0].GetVehicle()

	assert.Nil(t, position.Trip)
	assert.Nil(t, position.GetVehicle().Label)
	assert.Nil(t, position.GetVehicle().LicensePlate)
	assert.Nil(t, position.GetPosition().Bearing)
	assert.Nil(t, position.GetPosition().Speed)
	assert.Nil(t, position.CurrentStopSequence)
	assert.Nil(t, position.StopId)
	assert.Nil(t, position.CurrentStatus)
	assert.Nil(t, position.Timestamp)
	assert.Nil(t, position.CongestionLevel)
	assert.Nil(t, position.OccupancyStatus)
	assert.Nil(t, position.OccupancyPercentage)
}

func TestExportTripDescriptor(t *testing.T) {
	tests := []struct {
		name string
		trip gtfs.TripID
		want *gtfsrt.TripDescriptor
	}{
		{
			name: "route only, scheduled, no direction",
			trip: gtfs.TripID{RouteID: "R1"},
			want: &gtfsrt.TripDescriptor{RouteId: proto.String("R1")},
		},
		{
			name: "direction false",
			trip: gtfs.TripID{ID: "T1", DirectionID: gtfs.DirectionID_False},
			want: &gtfsrt.TripDescriptor{TripId: proto.String("T1"), DirectionId: proto.Uint32(0)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, proto.Equal(tt.want, exportTripDescriptor(tt.trip)), "got %v", exportTripDescriptor(tt.trip))
		})
	}
}

func TestEncodeGtfsRealtimeFeed_Deterministic(t *testing.T) {
	reportedAt := time.Unix(1792287600, 0)
	positions := internalgtfs.AgencyVehiclePositions{Vehicles: []gtfs.Vehicle{fullVehicle(reportedAt)}, Timestamp: reportedAt}

	first, err := encodeGtfsRealtimeFeed(newVehiclePositionsFeed(positions))
	require.NoError(t, err)
	second, err := encodeGtfsRealtimeFeed(newVehiclePositionsFeed(positions))
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, weakETag(first), weakETag(second))
	assert.Regexp(t, `^W/"[0-9a-f]{32}"$`, weakETag(first))
	assert.NotEqual(t, weakETag(first), weakETag(append(first, 0)))
}
