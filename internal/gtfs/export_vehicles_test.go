package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
func TestExportVehiclesFromRabaFeeds(t *testing.T) {
	mux := http.NewServeMux()
	serveFixture := func(path, file string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			data, err := os.ReadFile(filepath.Join("../../testdata", file))
			require.NoError(t, err)
			_, _ = w.Write(data)
		})
	}
	serveFixture("/trip-updates", "raba-trip-updates.pb")
	serveFixture("/vehicle-positions", "raba-vehicle-positions.pb")
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
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:            filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath:       ":memory:",
		RTFeeds:            []RTFeedConfig{feed},
		BlockMatchLocation: losAngeles,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })

	// InitGTFSManager fetches every enabled feed once before returning.
	byID := make(map[string]ExportVehicle)
	for _, vehicle := range manager.ExportVehicles() {
		byID[vehicle.Vehicle.ID.ID] = vehicle
	}
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
