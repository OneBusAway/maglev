package restapi

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

const (
	gtfsRealtimeContentType = "application/x-protobuf"
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
	descriptor := &gtfsrt.VehicleDescriptor{}
	if id.ID != "" {
		descriptor.Id = proto.String(id.ID)
	}
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
		descriptor.StartDate = proto.String(trip.StartDate.Format(internalgtfs.ServiceDateLayout))
	}
	if trip.HasStartTime {
		descriptor.StartTime = proto.String(internalgtfs.FormatServiceTime(trip.StartTime))
	}
	// Like congestion, go-gtfs stores the relationship as a plain value, so
	// the SCHEDULED default can't be told apart from absent.
	if trip.ScheduleRelationship != gtfsrt.TripDescriptor_SCHEDULED {
		descriptor.ScheduleRelationship = trip.ScheduleRelationship.Enum()
	}
	return descriptor
}

// newTripUpdatesFeed republishes an agency's trip updates as a GTFS-RT
// feed, one entity per trip update, keyed by its entity ID.
// incrementality is left unset, which means FULL_DATASET.
func newTripUpdatesFeed(updates internalgtfs.AgencyTripUpdates) *gtfsrt.FeedMessage {
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String(internalgtfs.GtfsRealtimeVersion),
			Timestamp:           proto.Uint64(uint64(updates.Timestamp.Unix())),
		},
		Entity: make([]*gtfsrt.FeedEntity, 0, len(updates.TripUpdates)),
	}
	for _, update := range updates.TripUpdates {
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:         proto.String(update.EntityID),
			TripUpdate: exportTripUpdate(update),
		})
	}
	return feed
}

func exportTripUpdate(update internalgtfs.TripUpdate) *gtfsrt.TripUpdate {
	tripUpdate := &gtfsrt.TripUpdate{
		Trip:           exportTripDescriptor(update.Trip.ID),
		StopTimeUpdate: make([]*gtfsrt.TripUpdate_StopTimeUpdate, 0, len(update.Trip.StopTimeUpdates)),
	}
	// Trip-updates sources set the relationship on every trip. SCHEDULED is
	// the protocol default, so writing it never misstates an absent value.
	tripUpdate.Trip.ScheduleRelationship = update.Trip.ID.ScheduleRelationship.Enum()
	if !update.Timestamp.IsZero() {
		tripUpdate.Timestamp = proto.Uint64(uint64(update.Timestamp.Unix()))
	}
	if vehicle := update.Trip.Vehicle; vehicle != nil && vehicle.ID != nil {
		tripUpdate.Vehicle = exportVehicleDescriptor(*vehicle.ID)
	}
	if update.Trip.Delay != nil {
		tripUpdate.Delay = proto.Int32(int32(*update.Trip.Delay / time.Second))
	}
	for _, stopTimeUpdate := range update.Trip.StopTimeUpdates {
		tripUpdate.StopTimeUpdate = append(tripUpdate.StopTimeUpdate, exportStopTimeUpdate(stopTimeUpdate))
	}
	return tripUpdate
}

func exportStopTimeUpdate(update gtfs.StopTimeUpdate) *gtfsrt.TripUpdate_StopTimeUpdate {
	exported := &gtfsrt.TripUpdate_StopTimeUpdate{
		StopSequence: update.StopSequence,
		StopId:       update.StopID,
		Arrival:      exportStopTimeEvent(update.Arrival),
		Departure:    exportStopTimeEvent(update.Departure),
	}
	// As for trips, the SCHEDULED default can't be told apart from absent.
	if update.ScheduleRelationship != gtfsrt.TripUpdate_StopTimeUpdate_SCHEDULED {
		exported.ScheduleRelationship = update.ScheduleRelationship.Enum()
	}
	return exported
}

func exportStopTimeEvent(event *gtfs.StopTimeEvent) *gtfsrt.TripUpdate_StopTimeEvent {
	if event == nil {
		return nil
	}
	exported := &gtfsrt.TripUpdate_StopTimeEvent{Uncertainty: event.Uncertainty}
	if event.Time != nil {
		exported.Time = proto.Int64(event.Time.Unix())
	}
	if event.Delay != nil {
		exported.Delay = proto.Int32(int32(*event.Delay / time.Second))
	}
	return exported
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
