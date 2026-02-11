package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func createFTSTestClient(t *testing.T) *Client {
	t.Helper()
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}
	client, err := NewClient(config)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency1",
		Name:     "Test Agency",
		Url:      "http://test.com",
		Timezone: "America/New_York",
	})
	require.NoError(t, err)

	return client
}

func TestSearchRoutesByFullText(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test routes
	routes := []CreateRouteParams{
		{ID: "r1", AgencyID: "agency1", ShortName: toNullString("10"), LongName: toNullString("Downtown Express"), Type: 3},
		{ID: "r2", AgencyID: "agency1", ShortName: toNullString("20"), LongName: toNullString("Airport Shuttle"), Type: 3},
		{ID: "r3", AgencyID: "agency1", ShortName: toNullString("30"), LongName: toNullString("Downtown Local"), Type: 3},
	}
	for _, r := range routes {
		_, err := client.Queries.CreateRoute(ctx, r)
		require.NoError(t, err)
	}

	t.Run("matches by long name", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Downtown",
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("matches by short name", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "10",
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "r1", results[0].ID)
	})

	t.Run("respects limit", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Downtown",
			Limit: 1,
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
	})

	t.Run("no results for unmatched query", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Nonexistent",
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Empty(t, results)
	})
}

func TestSearchStopsByName(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test stops
	stops := []CreateStopParams{
		{ID: "s1", Name: sql.NullString{String: "Main Street Station", Valid: true}, Lat: 40.0, Lon: -74.0},
		{ID: "s2", Name: sql.NullString{String: "Airport Terminal", Valid: true}, Lat: 40.1, Lon: -74.1},
		{ID: "s3", Name: sql.NullString{String: "Main Street Mall", Valid: true}, Lat: 40.2, Lon: -74.2},
	}
	for _, s := range stops {
		_, err := client.Queries.CreateStop(ctx, s)
		require.NoError(t, err)
	}

	t.Run("matches by stop name", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main",
			Limit:       10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("respects limit", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main",
			Limit:       1,
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
	})

	t.Run("no results for unmatched query", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Nonexistent",
			Limit:       10,
		})
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("returns correct fields", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Airport",
			Limit:       10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "s2", results[0].ID)
		assert.Equal(t, "Airport Terminal", results[0].Name.String)
		assert.Equal(t, 40.1, results[0].Lat)
		assert.Equal(t, -74.1, results[0].Lon)
	})
}
