package utils

import (
	"context"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

func TestFilterAgencies(t *testing.T) {
	tests := []struct {
		name     string
		all      []gtfs.Agency
		present  map[string]bool
		expected int
	}{
		{
			name: "Filter with some agencies present",
			all: []gtfs.Agency{
				{Id: "agency1", Name: "Agency One", Url: "http://one.com", Timezone: "America/Los_Angeles"},
				{Id: "agency2", Name: "Agency Two", Url: "http://two.com", Timezone: "America/New_York"},
				{Id: "agency3", Name: "Agency Three", Url: "http://three.com", Timezone: "America/Chicago"},
			},
			present: map[string]bool{
				"agency1": true,
				"agency3": true,
			},
			expected: 2,
		},
		{
			name: "Filter with no agencies present",
			all: []gtfs.Agency{
				{Id: "agency1", Name: "Agency One", Url: "http://one.com", Timezone: "America/Los_Angeles"},
				{Id: "agency2", Name: "Agency Two", Url: "http://two.com", Timezone: "America/New_York"},
			},
			present:  map[string]bool{},
			expected: 0,
		},
		{
			name: "Filter with all agencies present",
			all: []gtfs.Agency{
				{Id: "agency1", Name: "Agency One", Url: "http://one.com", Timezone: "America/Los_Angeles"},
				{Id: "agency2", Name: "Agency Two", Url: "http://two.com", Timezone: "America/New_York"},
			},
			present: map[string]bool{
				"agency1": true,
				"agency2": true,
			},
			expected: 2,
		},
		{
			name:     "Empty agency list",
			all:      []gtfs.Agency{},
			present:  map[string]bool{"agency1": true},
			expected: 0,
		},
		{
			name: "Agencies with full details",
			all: []gtfs.Agency{
				{
					Id:       "agency1",
					Name:     "Agency One",
					Url:      "http://one.com",
					Timezone: "America/Los_Angeles",
					Language: "en",
					Phone:    "555-1234",
					Email:    "info@one.com",
					FareUrl:  "http://one.com/fares",
				},
			},
			present:  map[string]bool{"agency1": true},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterAgencies(tt.all, tt.present)
			assert.Equal(t, tt.expected, len(result))

			// Verify that returned agencies are correct
			for _, ref := range result {
				assert.True(t, tt.present[ref.ID])
			}
		})
	}
}

func TestFilterAgencies_VerifyFields(t *testing.T) {
	agencies := []gtfs.Agency{
		{
			Id:       "test_agency",
			Name:     "Test Agency",
			Url:      "http://test.com",
			Timezone: "America/Los_Angeles",
			Language: "en",
			Phone:    "555-0000",
			Email:    "test@test.com",
			FareUrl:  "http://test.com/fares",
		},
	}
	present := map[string]bool{"test_agency": true}

	result := FilterAgencies(agencies, present)

	require.Len(t, result, 1)
	ref := result[0]

	assert.Equal(t, "test_agency", ref.ID)
	assert.Equal(t, "Test Agency", ref.Name)
	assert.Equal(t, "http://test.com", ref.URL)
	assert.Equal(t, "America/Los_Angeles", ref.Timezone)
	assert.Equal(t, "en", ref.Lang)
	assert.Equal(t, "555-0000", ref.Phone)
	assert.Equal(t, "test@test.com", ref.Email)
	assert.Equal(t, "http://test.com/fares", ref.FareUrl)
}

func setupTestClientWithRoutes(t *testing.T) (*gtfsdb.Client, func()) {
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}
	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err, "Failed to create test client")
	require.NotNil(t, client, "Client should not be nil")

	ctx := context.Background()

	// Insert test agencies
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone) VALUES
		('agency1', 'Test Agency 1', 'http://agency1.com', 'America/Los_Angeles'),
		('agency2', 'Test Agency 2', 'http://agency2.com', 'America/New_York')
	`)
	require.NoError(t, err, "Failed to insert test agencies")

	// Insert test routes
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, long_name, type, color, text_color, url, desc)
		VALUES
		('route1', 'agency1', 'R1', 'Route One', 3, 'FF0000', 'FFFFFF', 'http://route1.com', 'Description 1'),
		('route2', 'agency1', 'R2', 'Route Two', 1, '00FF00', '000000', 'http://route2.com', 'Description 2'),
		('route3', 'agency2', 'R3', 'Route Three', 2, '0000FF', 'FFFFFF', 'http://route3.com', 'Description 3')
	`)
	require.NoError(t, err, "Failed to insert test routes")

	cleanup := func() {
		_ = client.Close()
	}

	return client, cleanup
}

func TestFilterRoutes(t *testing.T) {
	client, cleanup := setupTestClientWithRoutes(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name     string
		present  map[string]bool
		expected int
	}{
		{
			name: "Filter with some routes present",
			present: map[string]bool{
				"agency1_route1": true,
				"agency2_route3": true,
			},
			expected: 2,
		},
		{
			name:     "Filter with no routes present",
			present:  map[string]bool{},
			expected: 0,
		},
		{
			name: "Filter with all routes present",
			present: map[string]bool{
				"agency1_route1": true,
				"agency1_route2": true,
				"agency2_route3": true,
			},
			expected: 3,
		},
		{
			name: "Filter with single route",
			present: map[string]bool{
				"agency1_route2": true,
			},
			expected: 1,
		},
		{
			name: "Filter with non-existent route in map",
			present: map[string]bool{
				"agency1_route1":     true,
				"agency1_route_fake": true,
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterRoutes(client.Queries, ctx, tt.present)
			assert.Equal(t, tt.expected, len(result))

			// Verify that returned routes are correct
			for _, r := range result {
				route := r.(models.Route)
				assert.True(t, tt.present[route.ID])
			}
		})
	}
}

func TestFilterRoutes_VerifyFields(t *testing.T) {
	client, cleanup := setupTestClientWithRoutes(t)
	defer cleanup()

	ctx := context.Background()
	present := map[string]bool{"agency1_route1": true}

	result := FilterRoutes(client.Queries, ctx, present)

	require.Len(t, result, 1)
	route := result[0].(models.Route)

	assert.Equal(t, "agency1_route1", route.ID)
	assert.Equal(t, "agency1", route.AgencyID)
	assert.Equal(t, "R1", route.ShortName)
	assert.Equal(t, "Route One", route.LongName)
	assert.Equal(t, "Description 1", route.Description)
	assert.Equal(t, models.RouteType(3), route.Type)
	assert.Equal(t, "http://route1.com", route.URL)
	assert.Equal(t, "FF0000", route.Color)
	assert.Equal(t, "FFFFFF", route.TextColor)
}

func TestFilterRoutes_DatabaseError(t *testing.T) {
	// Create client but close it immediately to trigger error
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}
	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)

	// Close the database to cause an error
	err = client.Close()
	require.NoError(t, err)

	ctx := context.Background()
	present := map[string]bool{"route1": true}

	result := FilterRoutes(client.Queries, ctx, present)

	assert.Nil(t, result, "Should return nil on database error")
}

func TestGetAllRoutesRefs(t *testing.T) {
	client, cleanup := setupTestClientWithRoutes(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("Get all routes", func(t *testing.T) {
		result := GetAllRoutesRefs(client.Queries, ctx)

		assert.Equal(t, 3, len(result))

		// Verify that all routes are returned
		routeIDs := make(map[string]bool)
		for _, r := range result {
			route := r.(models.Route)
			routeIDs[route.ID] = true
		}

		assert.True(t, routeIDs["agency1_route1"])
		assert.True(t, routeIDs["agency1_route2"])
		assert.True(t, routeIDs["agency2_route3"])
	})

	t.Run("Verify combined IDs format", func(t *testing.T) {
		result := GetAllRoutesRefs(client.Queries, ctx)

		require.NotEmpty(t, result)

		// Check first route has combined ID
		route := result[0].(models.Route)
		assert.Contains(t, route.ID, "_", "Route ID should be in combined format agency_route")
	})
}

func TestGetAllRoutesRefs_VerifyFields(t *testing.T) {
	client, cleanup := setupTestClientWithRoutes(t)
	defer cleanup()

	ctx := context.Background()

	result := GetAllRoutesRefs(client.Queries, ctx)
	require.Len(t, result, 3)

	// Find route1 in results
	var route1 models.Route
	for _, r := range result {
		route := r.(models.Route)
		if route.AgencyID == "agency1" && route.ShortName == "R1" {
			route1 = route
			break
		}
	}

	assert.Equal(t, "agency1_route1", route1.ID)
	assert.Equal(t, "agency1", route1.AgencyID)
	assert.Equal(t, "R1", route1.ShortName)
	assert.Equal(t, "Route One", route1.LongName)
	assert.Equal(t, "Description 1", route1.Description)
	assert.Equal(t, models.RouteType(3), route1.Type)
	assert.Equal(t, "http://route1.com", route1.URL)
	assert.Equal(t, "FF0000", route1.Color)
	assert.Equal(t, "FFFFFF", route1.TextColor)
}

func TestGetAllRoutesRefs_DatabaseError(t *testing.T) {
	// Create client but close it immediately to trigger error
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}
	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)

	// Close the database to cause an error
	err = client.Close()
	require.NoError(t, err)

	ctx := context.Background()

	result := GetAllRoutesRefs(client.Queries, ctx)

	assert.Nil(t, result, "Should return nil on database error")
}

func TestGetAllRoutesRefs_EmptyDatabase(t *testing.T) {
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}
	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	result := GetAllRoutesRefs(client.Queries, ctx)

	assert.Empty(t, result, "Should return empty slice when no routes in database")
}
