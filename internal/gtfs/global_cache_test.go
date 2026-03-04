package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

// wipeDatabase clears all tables in the correct order to respect Foreign Keys.
// We delete dependent ("child") tables before independent ("parent") tables.
func wipeDatabase(t *testing.T, client *gtfsdb.Client) {
	ctx := context.Background()
	// Order is critical to satisfy Foreign Key constraints:
	// 1. block_trip_entry (depends on trips AND block_trip_index)
	// 2. stop_times (depends on trips AND stops)
	// 3. trips (depends on routes, shapes, calendar)
	// 4. block_trip_index (referenced by block_trip_entry)
	// 5. calendar_dates (depends on service_id)
	// 6. calendar, shapes, stops, routes (now safe to delete)
	// 7. agencies (root)
	queries := []string{
		"DELETE FROM block_trip_entry;",
		"DELETE FROM stop_times;",
		"DELETE FROM trips;",
		"DELETE FROM block_trip_index;",
		"DELETE FROM calendar_dates;",
		"DELETE FROM calendar;",
		"DELETE FROM shapes;",
		"DELETE FROM stops;",
		"DELETE FROM routes;",
		"DELETE FROM agencies;",
	}

	for _, q := range queries {
		_, err := client.DB.ExecContext(ctx, q)
		if err != nil {
			t.Fatalf("Failed to execute cleanup query %q: %v", q, err)
		}
	}
}

// HAPPY PATH: Uses the Shared DB (Fast, Standard Data)
func TestInitializeGlobalCache_HappyPath(t *testing.T) {
	manager, _ := getSharedTestComponents(t)

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	err := InitializeGlobalCache(context.Background(), manager.GtfsDB.Queries, calc)
	assert.NoError(t, err)

	// Verify real data from raba.zip
	assert.Greater(t, len(calc.contextCache), 0, "Context cache should be populated")
	assert.Greater(t, len(calc.shapeCache), 0, "Shape cache should be populated")

	// Dynamic check: Pick any ID to verify
	var sampleID string
	for id := range calc.contextCache {
		sampleID = id
		break
	}
	assert.NotEmpty(t, sampleID)

	stops := calc.contextCache[sampleID]
	assert.NotEmpty(t, stops)
	assert.Equal(t, sampleID, stops[0].ID)
}

// EDGE CASE: Empty Database
func TestInitializeGlobalCache_EmptyDatabase(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	wipeDatabase(t, manager.GtfsDB)

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)
	err = InitializeGlobalCache(context.Background(), manager.GtfsDB.Queries, calc)

	assert.NoError(t, err)
	assert.Equal(t, 0, len(calc.contextCache))
	assert.Equal(t, 0, len(calc.shapeCache))
}

// FAILURE CASE: Database Error
func TestInitializeGlobalCache_DatabaseError(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	// Important: Defer shutdown to clean up background routines
	defer manager.Shutdown()

	// SABOTAGE: Close DB to force errors
	_ = manager.GtfsDB.DB.Close()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)
	err = InitializeGlobalCache(context.Background(), manager.GtfsDB.Queries, calc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch")
}

// EDGE CASE: Stops without Shapes
// Tests that the calculator handles active stops gracefully when the associated
// trip lacks shape geometry (e.g., shape_id is NULL).
func TestInitializeGlobalCache_StopsWithoutShapes(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	// Clear any existing data
	wipeDatabase(t, manager.GtfsDB)

	// Setup: Create a "Valid" Stop (Active, but no Shape)
	// Link the stop to a trip, otherwise the cache loader will correctly
	// ignore it as "unused/ghost" data.
	queries := []string{
		// Hierarchy: Agency -> Route -> Calendar -> Trip
		`INSERT INTO agencies (id, name, url, timezone) VALUES ('agency_1', 'Test Agency', 'http://example.com', 'UTC');`,
		`INSERT INTO routes (id, agency_id, type) VALUES ('route_1', 'agency_1', 3);`,
		`INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date) 
		 VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20240101', '20250101');`,

		// CRITICAL: Trip has NULL shape_id
		`INSERT INTO trips (id, route_id, service_id, shape_id) VALUES ('trip_1', 'route_1', 'service_1', NULL);`,

		// The Stop itself
		`INSERT INTO stops (id, code, name, lat, lon, location_type) VALUES ('orphan_stop', '999', 'Orphan Stop', 10.0, 20.0, 0);`,

		// Link: Stop -> Trip (Makes the stop "Active")
		`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) 
		 VALUES ('trip_1', 0, 0, 'orphan_stop', 1);`,
	}

	for _, q := range queries {
		_, err := manager.GtfsDB.DB.ExecContext(ctx, q)
		assert.NoError(t, err, "Failed to execute setup query")
	}

	// Run the Test
	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)
	err = InitializeGlobalCache(ctx, manager.GtfsDB.Queries, calc)

	// Verify
	assert.NoError(t, err)

	// Should be 1 because the stop is now active (linked to trip_1)
	assert.Equal(t, 1, len(calc.contextCache), "Active stop should be cached")

	// Should be 0 because we explicitly set shape_id to NULL
	assert.Equal(t, 0, len(calc.shapeCache), "No shapes should be cached")
}
