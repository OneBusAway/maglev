package gtfs

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/gtfsdb"
)

func vehicleWithTrip(vehicleID, tripID, routeID string) gtfs.Vehicle {
	vehicle := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: vehicleID}}
	if tripID != "" {
		vehicle.Trip = &gtfs.Trip{ID: gtfs.TripID{ID: tripID, RouteID: routeID}}
	}
	return vehicle
}

func TestNewExportVehicleActiveTrip(t *testing.T) {
	tripUpdateBlock := &BlockMatch{TripID: "T_TU", RouteID: "R_STATIC", AgencyID: "40"}
	tripByVehicle := map[string]tripUpdateRef{"V1": {TripID: "T_TU", VehicleID: "V1"}}
	tripUpdateBlocks := map[string]*BlockMatch{"T_TU": tripUpdateBlock}

	t.Run("trip update naming the vehicle supplies trip and block", func(t *testing.T) {
		got := newExportVehicle(vehicleWithTrip("V1", "T_VP", "R_VP"), tripByVehicle, tripUpdateBlocks)
		assert.Equal(t, "T_TU", got.ActiveTripID)
		assert.Equal(t, "R_STATIC", got.ActiveRouteID, "missing realtime route falls back to the matched static route")
		assert.Same(t, tripUpdateBlock, got.Block)
	})
	t.Run("position-only vehicle keeps its trip but gets no block", func(t *testing.T) {
		got := newExportVehicle(vehicleWithTrip("V2", "T_VP", "R_VP"), tripByVehicle, tripUpdateBlocks)
		assert.Equal(t, "T_VP", got.ActiveTripID)
		assert.Equal(t, "R_VP", got.ActiveRouteID)
		assert.Nil(t, got.Block, "legacy never block-matches a vehicle position that has a vehicle ID")
	})
	t.Run("tripless vehicle has no block", func(t *testing.T) {
		got := newExportVehicle(vehicleWithTrip("V3", "", ""), tripByVehicle, tripUpdateBlocks)
		assert.Empty(t, got.ActiveTripID)
		assert.Nil(t, got.Block)
	})
}

func TestFirstTripUpdateByVehicleUsesSourceOrder(t *testing.T) {
	refs := []tripUpdateRef{
		{TripID: "T_FIRST", VehicleID: "V1"},
		{TripID: "T_SECOND", VehicleID: "V1"},
		{TripID: "T_NOVEHICLE"},
	}
	got := firstTripUpdateByVehicle(refs)
	assert.Equal(t, map[string]tripUpdateRef{"V1": refs[0]}, got)
}

func TestVehiclesMissingFrom(t *testing.T) {
	all := []gtfs.Vehicle{vehicleWithTrip("V1", "T1", "R1"), vehicleWithTrip("V2", "", ""), {}}
	kept := []gtfs.Vehicle{all[0]}
	got := vehiclesMissingFrom(all, kept)
	require.Len(t, got, 1, "vehicles without IDs are dropped like everywhere else in ingestion")
	assert.Equal(t, "V2", got[0].ID.ID)
}

// TestExportVehiclesFromRabaFeeds runs real ingestion against matching RABA
// static and realtime fixtures with an agency filter, so it covers vehicles
// the JSON view drops as well as legacy block matching.
// newRabaExportManager runs real ingestion with an agency filter against
// the matching RABA static and realtime fixtures, serving tripUpdates in
// place of the trip-update fixture.
func newRabaExportManager(t *testing.T, tripUpdates []byte) *Manager {
	t.Helper()
	vehiclePositions, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tripUpdates) })
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(vehiclePositions) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	feed := RTFeedConfig{
		ID:                  "raba",
		AgencyIDs:           []string{"25"},
		TripUpdatesURL:      server.URL + "/trip-updates",
		VehiclePositionsURL: server.URL + "/vehicle-positions",
		RefreshInterval:     3600,
		Enabled:             true,
	}
	// InitGTFSManager fetches every enabled feed once before returning.
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:            filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath:       ":memory:",
		RTFeeds:            []RTFeedConfig{feed},
		BlockMatchLocation: losAngeles,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager
}

func exportVehiclesByID(manager *Manager) map[string]ExportVehicle {
	byID := make(map[string]ExportVehicle)
	for _, vehicle := range manager.ExportVehicles() {
		byID[vehicle.Vehicle.ID.ID] = vehicle
	}
	return byID
}

// TestExportVehiclesFromRabaFeeds covers vehicles the JSON view drops as
// well as legacy block matching.
func TestExportVehiclesFromRabaFeeds(t *testing.T) {
	tripUpdates, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
	require.NoError(t, err)
	manager := newRabaExportManager(t, tripUpdates)

	byID := exportVehiclesByID(manager)
	matched := byID["5701"]
	require.NotNil(t, matched.Block, "vehicle 5701's trip update resolves a RABA block")
	assert.Equal(t, "25", matched.Block.AgencyID)
	assert.Equal(t, "28c61524-6da8-4506-9a92-22f2f6e91872", matched.ActiveTripID)
	assert.Equal(t, "20250608", matched.Block.ServiceDate.Format("20060102"))

	tripless, ok := byID["66"]
	require.True(t, ok, "the agency filter drops tripless vehicles from the JSON view but the export keeps them")
	assert.Nil(t, tripless.Block)
	assert.Len(t, manager.ExportVehicles(), 22, "every vehicle in the fixture is retained for the export")
}

func TestBuildExportVehiclesSkipsVehiclesWithoutID(t *testing.T) {
	manager := &Manager{
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {{Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "trip1"}}}, vehicleWithTrip("V1", "trip2", "R2")},
		},
	}

	got := manager.buildExportVehiclesLocked([]string{"feed-0"})

	require.Len(t, got, 1, "a vehicle without an ID has no identity to export")
	assert.Equal(t, "V1", got[0].Vehicle.ID.ID)
}

func TestAssignAnonymousTripUpdates(t *testing.T) {
	refs := []tripUpdateRef{
		{TripID: "T_NAMED", VehicleID: "V_NAMED"},
		{TripID: "T_ON_B1"},
		{TripID: "T_UNKNOWN_BLOCK"},
		{TripID: "T_ON_B2"},
	}
	vehicles := []gtfs.Vehicle{
		vehicleWithTrip("V_B1_LATER", "T_OTHER_ON_B1", ""),
		vehicleWithTrip("V_B1", "T_VP_ON_B1", ""),
		vehicleWithTrip("V_TRIPLESS", "", ""),
	}
	blockKeys := map[string]string{
		"T_ON_B1":       "B1",
		"T_ON_B2":       "B2",
		"T_VP_ON_B1":    "B1",
		"T_OTHER_ON_B1": "B1",
	}

	got := assignAnonymousTripUpdates(refs, vehicles, blockKeys)

	assert.Equal(t, "V_NAMED", got[0].VehicleID, "a named vehicle is kept")
	assert.Equal(t, "V_B1", got[1].VehicleID, "smallest vehicle ID wins a shared block")
	assert.Empty(t, got[2].VehicleID, "an update whose block is unknown stays anonymous")
	assert.Empty(t, got[3].VehicleID, "no vehicle position is on block B2")
	assert.Empty(t, refs[1].VehicleID, "the input refs are not modified")
}

func TestTripBlockKey(t *testing.T) {
	assert.Equal(t, "B1", tripBlockKey(gtfsdb.Trip{ID: "T1", BlockID: sql.NullString{String: "B1", Valid: true}}))
	assert.Equal(t, "T2", tripBlockKey(gtfsdb.Trip{ID: "T2"}), "a trip without a block is its own block")
}

// Feeds such as OneBusAway's own trip-update export omit vehicle
// descriptors; legacy then assigns each update to the vehicle running on
// its block.
func TestExportVehiclesAssignsAnonymousTripUpdatesByBlock(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
	require.NoError(t, err)
	var message gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(raw, &message))
	for _, entity := range message.GetEntity() {
		if entity.GetTripUpdate() != nil {
			entity.TripUpdate.Vehicle = nil
		}
	}
	tripUpdates, err := proto.Marshal(&message)
	require.NoError(t, err)

	manager := newRabaExportManager(t, tripUpdates)

	matched := exportVehiclesByID(manager)["5701"]
	require.NotNil(t, matched.Block, "the anonymous update on 5701's block supplies its block match")
	assert.Equal(t, "25", matched.Block.AgencyID)
	assert.Equal(t, "28c61524-6da8-4506-9a92-22f2f6e91872", matched.ActiveTripID)
}
