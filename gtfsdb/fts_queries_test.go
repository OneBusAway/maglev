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

	// Insert test routes with varied fields to verify correct column scanning
	routes := []CreateRouteParams{
		{
			ID: "r1", AgencyID: "agency1",
			ShortName: toNullString("10"), LongName: toNullString("Downtown Express"),
			Desc: toNullString("Express service through downtown"),
			Type: 3, Url: toNullString("http://test.com/r1"),
			Color: toNullString("FF0000"), TextColor: toNullString("FFFFFF"),
			ContinuousPickup:  sql.NullInt64{Int64: 1, Valid: true},
			ContinuousDropOff: sql.NullInt64{Int64: 2, Valid: true},
		},
		{
			ID: "r2", AgencyID: "agency1",
			ShortName: toNullString("20"), LongName: toNullString("Airport Shuttle"),
			Type: 3,
		},
		{
			ID: "r3", AgencyID: "agency1",
			ShortName: toNullString("30"), LongName: toNullString("Downtown Local"),
			Type: 3,
		},
		// Route with NULL short_name to verify FTS trigger coalesce + nullable scan
		{
			ID: "r4", AgencyID: "agency1",
			LongName: toNullString("Riverfront Circulator"),
			Type:     3,
		},
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

	t.Run("results ordered by relevance then id", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Downtown",
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
		// Both routes share agency_id and similar bm25 scores,
		// so tiebreaker is r.id: r1 < r3
		assert.Equal(t, "r1", results[0].ID)
		assert.Equal(t, "r3", results[1].ID)
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

	t.Run("matches by description", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Express service",
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "r1", results[0].ID)
	})

	t.Run("matches route with NULL short_name", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Riverfront",
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "r4", results[0].ID)
		assert.False(t, results[0].ShortName.Valid)
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

	t.Run("returns correct fields", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Airport",
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		r := results[0]
		assert.Equal(t, "r2", r.ID)
		assert.Equal(t, "agency1", r.AgencyID)
		assert.Equal(t, "20", r.ShortName.String)
		assert.Equal(t, "Airport Shuttle", r.LongName.String)
		assert.Equal(t, int64(3), r.Type)
	})

	t.Run("returns all populated fields", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Express service",
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		r := results[0]
		assert.Equal(t, "r1", r.ID)
		assert.Equal(t, "agency1", r.AgencyID)
		assert.Equal(t, "10", r.ShortName.String)
		assert.Equal(t, "Downtown Express", r.LongName.String)
		assert.Equal(t, "Express service through downtown", r.Desc.String)
		assert.Equal(t, int64(3), r.Type)
		assert.Equal(t, "http://test.com/r1", r.Url.String)
		assert.Equal(t, "FF0000", r.Color.String)
		assert.Equal(t, "FFFFFF", r.TextColor.String)
		assert.Equal(t, int64(1), r.ContinuousPickup.Int64)
		assert.Equal(t, int64(2), r.ContinuousDropOff.Int64)
	})

	t.Run("prefix search with wildcard", func(t *testing.T) {
		results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
			Query: "Down*",
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})
}

func TestSearchRoutesByFullTextEmptyDB(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	results, err := client.Queries.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
		Query: "anything",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchStopsByName(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test stops with varied fields to verify correct column scanning
	stops := []CreateStopParams{
		{
			ID: "s1", Name: toNullString("Main Street Station"),
			Code: toNullString("MS01"),
			Lat:  40.0, Lon: -74.0,
			LocationType:       sql.NullInt64{Int64: 1, Valid: true},
			WheelchairBoarding: sql.NullInt64{Int64: 1, Valid: true},
			Direction:          toNullString("N"),
		},
		{
			ID: "s2", Name: toNullString("Airport Terminal"),
			Lat: 40.1, Lon: -74.1,
		},
		{
			ID: "s3", Name: toNullString("Main Street Mall"),
			Lat: 40.2, Lon: -74.2,
		},
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

	t.Run("results ordered alphabetically", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main",
			Limit:       10,
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, "Main Street Mall", results[0].Name.String)
		assert.Equal(t, "Main Street Station", results[1].Name.String)
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

	t.Run("returns all fields correctly", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main Street Station",
			Limit:       10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		r := results[0]
		assert.Equal(t, "s1", r.ID)
		assert.Equal(t, "MS01", r.Code.String)
		assert.Equal(t, "Main Street Station", r.Name.String)
		assert.Equal(t, 40.0, r.Lat)
		assert.Equal(t, -74.0, r.Lon)
		assert.Equal(t, int64(1), r.LocationType.Int64)
		assert.Equal(t, int64(1), r.WheelchairBoarding.Int64)
		assert.Equal(t, "N", r.Direction.String)
	})

	t.Run("prefix search with wildcard", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Air*",
			Limit:       10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "s2", results[0].ID)
	})
}

func TestSearchStopsByNameEmptyDB(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
		SearchQuery: "anything",
		Limit:       10,
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}
