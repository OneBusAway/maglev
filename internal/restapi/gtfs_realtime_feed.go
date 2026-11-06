package restapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

const (
	gtfsRealtimeContentType = "application/x-protobuf"
	gtfsDateLayout          = "20060102"
	// etagHashBytes is how much of the SHA-256 of a feed goes into its ETag.
	etagHashBytes = 16
)

// newVehiclePositionsFeed republishes an agency's vehicles as a GTFS-RT
// feed, one entity per vehicle, keyed by vehicle ID. incrementality is left
// unset, which means FULL_DATASET.
func newVehiclePositionsFeed(positions internalgtfs.AgencyVehiclePositions) *gtfsrt.FeedMessage {
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String(internalgtfs.GtfsRealtimeVersion),
			Timestamp:           proto.Uint64(uint64(positions.Timestamp.Unix())),
		},
		Entity: make([]*gtfsrt.FeedEntity, 0, len(positions.Vehicles)),
	}
	for _, vehicle := range positions.Vehicles {
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:      proto.String(vehicle.ID.ID),
			Vehicle: exportVehiclePosition(vehicle),
		})
	}
	return feed
}

func exportVehiclePosition(vehicle gtfs.Vehicle) *gtfsrt.VehiclePosition {
	position := &gtfsrt.VehiclePosition{
		Vehicle:             exportVehicleDescriptor(*vehicle.ID),
		Position:            exportPosition(vehicle.Position),
		CurrentStopSequence: vehicle.CurrentStopSequence,
		StopId:              vehicle.StopID,
		CurrentStatus:       vehicle.CurrentStatus,
		OccupancyStatus:     vehicle.OccupancyStatus,
		OccupancyPercentage: vehicle.OccupancyPercentage,
	}
	if vehicle.Trip != nil {
		position.Trip = exportTripDescriptor(vehicle.Trip.ID)
	}
	if vehicle.Timestamp != nil {
		position.Timestamp = proto.Uint64(uint64(vehicle.Timestamp.Unix()))
	}
	// go-gtfs stores congestion as a plain value, so UNKNOWN can't be told
	// apart from absent; leave it out.
	if vehicle.CongestionLevel != gtfsrt.VehiclePosition_UNKNOWN_CONGESTION_LEVEL {
		position.CongestionLevel = vehicle.CongestionLevel.Enum()
	}
	return position
}

func exportVehicleDescriptor(id gtfs.VehicleID) *gtfsrt.VehicleDescriptor {
	descriptor := &gtfsrt.VehicleDescriptor{Id: proto.String(id.ID)}
	if id.Label != "" {
		descriptor.Label = proto.String(id.Label)
	}
	if id.LicensePlate != "" {
		descriptor.LicensePlate = proto.String(id.LicensePlate)
	}
	return descriptor
}

func exportPosition(position *gtfs.Position) *gtfsrt.Position {
	return &gtfsrt.Position{
		Latitude:  position.Latitude,
		Longitude: position.Longitude,
		Bearing:   position.Bearing,
		Odometer:  position.Odometer,
		Speed:     position.Speed,
	}
}

func exportTripDescriptor(trip gtfs.TripID) *gtfsrt.TripDescriptor {
	descriptor := &gtfsrt.TripDescriptor{DirectionId: exportDirectionID(trip.DirectionID)}
	if trip.ID != "" {
		descriptor.TripId = proto.String(trip.ID)
	}
	if trip.RouteID != "" {
		descriptor.RouteId = proto.String(trip.RouteID)
	}
	if trip.HasStartDate {
		descriptor.StartDate = proto.String(trip.StartDate.Format(gtfsDateLayout))
	}
	if trip.HasStartTime {
		descriptor.StartTime = proto.String(formatGtfsServiceTime(trip.StartTime))
	}
	// Like congestion, go-gtfs stores the relationship as a plain value, so
	// the SCHEDULED default can't be told apart from absent.
	if trip.ScheduleRelationship != gtfsrt.TripDescriptor_SCHEDULED {
		descriptor.ScheduleRelationship = trip.ScheduleRelationship.Enum()
	}
	return descriptor
}

// exportDirectionID maps a direction to its GTFS-RT value, leaving it out
// when unspecified.
func exportDirectionID(direction gtfs.DirectionID) *uint32 {
	switch direction {
	case gtfs.DirectionID_True:
		return proto.Uint32(1)
	case gtfs.DirectionID_False:
		return proto.Uint32(0)
	}
	return nil
}

// formatGtfsServiceTime renders a duration since the service day's start as
// GTFS HH:MM:SS, allowing hours past 24.
func formatGtfsServiceTime(sinceServiceDayStart time.Duration) string {
	seconds := int64(sinceServiceDayStart / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// encodeGtfsRealtimeFeed serializes a feed deterministically, so the same
// content always gives the same bytes and ETag.
func encodeGtfsRealtimeFeed(feed *gtfsrt.FeedMessage) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(feed)
}

// weakETag derives a weak ETag from a serialized feed. It is weak because
// the compression middleware serves the same ETag for gzip and identity
// encodings.
func weakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:etagHashBytes]) + `"`
}
