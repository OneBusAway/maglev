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

var vehicleExportNow = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

func positionedVehicle(id string, updated time.Time) gtfs.Vehicle {
	return gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: id},
		Timestamp: &updated,
		Position:  &gtfs.Position{Latitude: proto.Float32(40.5), Longitude: proto.Float32(-122.4)},
	}
}

func exportVehicleOnRoute(id, tripID, routeID string) internalgtfs.ExportVehicle {
	return internalgtfs.ExportVehicle{
		Vehicle:       positionedVehicle(id, vehicleExportNow.Add(-time.Minute)),
		ActiveTripID:  tripID,
		ActiveRouteID: routeID,
	}
}

func vehicleExportRequest(agencyID string) gtfsRealtimeExportRequest {
	return gtfsRealtimeExportRequest{AgencyID: agencyID, Time: vehicleExportNow, RemoveAgencyIDs: true}
}

func TestResolveVehicleOwner(t *testing.T) {
	block40 := &internalgtfs.BlockMatch{AgencyID: "40"}
	tests := []struct {
		name        string
		vehicleID   string
		block       *internalgtfs.BlockMatch
		wantAgency  string
		wantVehicle string
		wantOK      bool
	}{
		{"prefixed ID", "40_bus_A", nil, "40", "bus_A", true},
		{"prefix beats block agency", "A_bus_1", &internalgtfs.BlockMatch{AgencyID: "B"}, "A", "bus_1", true},
		{"raw underscore read as prefix", "bus_A", nil, "bus", "A", true},
		{"no underscore uses block", "1234", block40, "40", "1234", true},
		{"no underscore and no block", "1234", nil, "", "", false},
		{"empty prefix", "_123", block40, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vehicle := internalgtfs.ExportVehicle{Vehicle: gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.vehicleID}}, Block: tt.block}
			agencyID, vehicleID, ok := resolveVehicleOwner(vehicle)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantAgency, agencyID)
			assert.Equal(t, tt.wantVehicle, vehicleID)
		})
	}
}

func TestSelectExportVehiclesEligibility(t *testing.T) {
	latitudeOnly := positionedVehicle("40_lat_only", vehicleExportNow)
	latitudeOnly.Position = &gtfs.Position{Latitude: proto.Float32(1)}
	noTimestamp := positionedVehicle("40_no_time", vehicleExportNow)
	noTimestamp.Timestamp = nil
	noPosition := positionedVehicle("40_no_position", vehicleExportNow)
	noPosition.Position = nil

	vehicles := []internalgtfs.ExportVehicle{
		{Vehicle: positionedVehicle("40_age599", vehicleExportNow.Add(-599*time.Second))},
		{Vehicle: positionedVehicle("40_age600", vehicleExportNow.Add(-600*time.Second))},
		{Vehicle: positionedVehicle("40_future", vehicleExportNow.Add(time.Minute))},
		{Vehicle: positionedVehicle("20_other_agency", vehicleExportNow)},
		{Vehicle: positionedVehicle("1234", vehicleExportNow)},
		{Vehicle: latitudeOnly},
		{Vehicle: noTimestamp},
		{Vehicle: noPosition},
	}
	var selected []string
	for _, candidate := range selectExportVehicles(vehicles, vehicleExportRequest("40")) {
		selected = append(selected, candidate.vehicle.Vehicle.ID.ID)
	}
	assert.ElementsMatch(t, []string{"40_age599", "40_future"}, selected)
}

func TestSelectExportVehiclesRouteFilter(t *testing.T) {
	vehicles := []internalgtfs.ExportVehicle{
		exportVehicleOnRoute("40_on_A", "tA", "A"),
		exportVehicleOnRoute("40_on_B", "tB", "B"),
		exportVehicleOnRoute("40_tripless", "", ""),
	}
	unfiltered := selectExportVehicles(vehicles, vehicleExportRequest("40"))
	assert.Len(t, unfiltered, 3, "tripless vehicles appear agency-wide")

	req := vehicleExportRequest("40")
	req.RouteFilterID = "A"
	filtered := selectExportVehicles(vehicles, req)
	require.Len(t, filtered, 1)
	assert.Equal(t, "40_on_A", filtered[0].vehicle.Vehicle.ID.ID)

	req.RouteFilterID = "40_A"
	assert.Empty(t, selectExportVehicles(vehicles, req), "the filter matches raw route IDs only")
}

func TestBuildVehiclePositionsFeedPayload(t *testing.T) {
	rich := exportVehicleOnRoute("1234", "tripA", "A")
	rich.Block = &internalgtfs.BlockMatch{TripID: "tripA", RouteID: "A", AgencyID: "40",
		ServiceDate: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), TripStart: 25 * time.Hour}
	rich.Vehicle.Trip = &gtfs.Trip{ID: gtfs.TripID{ID: "tripA", RouteID: "A",
		HasStartDate: true, StartDate: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}}
	rich.Vehicle.Position.Bearing = proto.Float32(90)
	rich.Vehicle.Position.Speed = proto.Float32(12.5)
	rich.Vehicle.Position.Odometer = proto.Float64(1000)
	rich.Vehicle.CurrentStopSequence = proto.Uint32(4)
	rich.Vehicle.StopID = proto.String("stop_9")
	status := gtfsrt.VehiclePosition_STOPPED_AT
	rich.Vehicle.CurrentStatus = &status
	rich.Vehicle.CongestionLevel = gtfsrt.VehiclePosition_RUNNING_SMOOTHLY
	occupancy := gtfsrt.VehiclePosition_FEW_SEATS_AVAILABLE
	rich.Vehicle.OccupancyStatus = &occupancy
	rich.Vehicle.OccupancyPercentage = proto.Uint32(40)

	tripless := internalgtfs.ExportVehicle{Vehicle: positionedVehicle("40_bus_B", vehicleExportNow)}

	req := vehicleExportRequest("40")
	candidates := selectExportVehicles([]internalgtfs.ExportVehicle{rich, tripless}, req)
	feed := buildVehiclePositionsFeed(candidates, req, nil)

	assert.Equal(t, uint64(vehicleExportNow.Unix()), feed.GetHeader().GetTimestamp())
	require.Len(t, feed.Entity, 2)
	assert.Equal(t, "1", feed.Entity[0].GetId())
	assert.Equal(t, "2", feed.Entity[1].GetId())

	first := feed.Entity[0].GetVehicle()
	assert.Equal(t, "1234", first.GetVehicle().GetId())
	assert.Equal(t, "tripA", first.GetTrip().GetTripId())
	assert.Equal(t, "A", first.GetTrip().GetRouteId())
	assert.Equal(t, "20261005", first.GetTrip().GetStartDate(), "resolved Monday instance beats the Tuesday hint")
	assert.Equal(t, "25:00:00", first.GetTrip().GetStartTime())
	assert.InDelta(t, 90, first.GetPosition().GetBearing(), 0.001)
	assert.InDelta(t, 12.5, first.GetPosition().GetSpeed(), 0.001)
	assert.InDelta(t, 1000, first.GetPosition().GetOdometer(), 0.001)
	assert.Equal(t, uint32(4), first.GetCurrentStopSequence())
	assert.Equal(t, "stop_9", first.GetStopId())
	assert.Equal(t, gtfsrt.VehiclePosition_STOPPED_AT, first.GetCurrentStatus())
	assert.Equal(t, gtfsrt.VehiclePosition_RUNNING_SMOOTHLY, first.GetCongestionLevel())
	assert.Equal(t, gtfsrt.VehiclePosition_FEW_SEATS_AVAILABLE, first.GetOccupancyStatus())
	assert.Equal(t, uint32(40), first.GetOccupancyPercentage())
	assert.Equal(t, uint64(vehicleExportNow.Add(-time.Minute).Unix()), first.GetTimestamp())
	assert.Nil(t, first.GetTrip().ScheduleRelationship, "the SCHEDULED default is not emitted")

	second := feed.Entity[1].GetVehicle()
	assert.Equal(t, "bus_B", second.GetVehicle().GetId())
	assert.Nil(t, second.GetTrip(), "a tripless vehicle has no trip descriptor")
	assert.Nil(t, second.CongestionLevel, "an unknown congestion default is not emitted")
	assert.Nil(t, second.GetPosition().Bearing)
}

func TestBuildVehiclePositionsFeedKeepsAgencyPrefixes(t *testing.T) {
	onRoute := exportVehicleOnRoute("1234", "tripA", "A")
	onRoute.Block = &internalgtfs.BlockMatch{TripID: "tripA", RouteID: "A", AgencyID: "40"}
	prefixed := exportVehicleOnRoute("40_bus_A", "tripB", "B")

	req := vehicleExportRequest("40")
	req.RemoveAgencyIDs = false
	routeAgencies := map[string]string{"B": "40"}
	feed := buildVehiclePositionsFeed(selectExportVehicles([]internalgtfs.ExportVehicle{onRoute, prefixed}, req), req, routeAgencies)

	byVehicle := map[string]*gtfsrt.VehiclePosition{}
	for _, entity := range feed.Entity {
		byVehicle[entity.GetVehicle().GetVehicle().GetId()] = entity.GetVehicle()
	}
	require.Contains(t, byVehicle, "40_1234")
	require.Contains(t, byVehicle, "40_bus_A", "an already prefixed ID is not prefixed twice")
	assert.Equal(t, "40_tripA", byVehicle["40_1234"].GetTrip().GetTripId())
	assert.Equal(t, "40_A", byVehicle["40_1234"].GetTrip().GetRouteId())
	assert.Equal(t, "40_tripB", byVehicle["40_bus_A"].GetTrip().GetTripId())
}
