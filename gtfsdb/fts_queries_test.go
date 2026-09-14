package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/nulls"
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
			ShortName: nulls.String("10"), LongName: nulls.String("Downtown Express"),
			Desc: nulls.String("Express service through downtown"),
			Type: 3, Url: nulls.String("http://test.com/r1"),
			Color: nulls.String("FF0000"), TextColor: nulls.String("FFFFFF"),
			ContinuousPickup:  sql.NullInt64{Int64: 1, Valid: true},
			ContinuousDropOff: sql.NullInt64{Int64: 2, Valid: true},
		},
		{
			ID: "r2", AgencyID: "agency1",
			ShortName: nulls.String("20"), LongName: nulls.String("Airport Shuttle"),
			Type: 3,
		},
		{
			ID: "r3", AgencyID: "agency1",
			ShortName: nulls.String("30"), LongName: nulls.String("Downtown Local"),
			Type: 3,
		},
		// Route with NULL short_name to verify FTS trigger coalesce + nullable scan
		{
			ID: "r4", AgencyID: "agency1",
			LongName: nulls.String("Riverfront Circulator"),
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
		// r1 matches "Downtown" in both long_name and desc, giving it
		// a better bm25 score than r3 (long_name only). Both share agency_id.
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
			ID: "s1", Name: nulls.String("Main Street Station"),
			Code: nulls.String("MS01"),
			Lat:  40.0, Lon: -74.0,
			LocationType:       sql.NullInt64{Int64: 1, Valid: true},
			WheelchairBoarding: sql.NullInt64{Int64: 1, Valid: true},
			Direction:          nulls.String("N"),
		},
		{
			ID: "s2", Name: nulls.String("Airport Terminal"),
			Lat: 40.1, Lon: -74.1,
		},
		{
			ID: "s3", Name: nulls.String("Main Street Mall"),
			Lat: 40.2, Lon: -74.2,
		},
		{
			ID: "hub1", Name: nulls.String("Main Street Hub"),
			Lat: 40.0, Lon: -74.0,
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
		assert.Len(t, results, 3)
	})

	t.Run("results follow lexicographic ordering by stop ID", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main",
			Limit:       10,
		})
		require.NoError(t, err)
		require.Len(t, results, 3)

		// No route serves these stops, so none resolves an agency to build a combined ID
		// from, and they fall through to the raw stop ID tiebreak (hub1 < s1 < s3).
		assert.Equal(t, "Main Street Hub", results[0].Name.String)
		assert.Equal(t, "Main Street Station", results[1].Name.String)
		assert.Equal(t, "Main Street Mall", results[2].Name.String)
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
		assert.False(t, r.ParentStation.Valid)
	})

	t.Run("reads non-null parent_station correctly", func(t *testing.T) {
		_, err := client.DB.ExecContext(ctx, "UPDATE stops SET parent_station = 'hub1' WHERE id = 's1'")
		require.NoError(t, err)
		defer func() {
			_, err := client.DB.ExecContext(ctx, "UPDATE stops SET parent_station = NULL WHERE id = 's1'")
			require.NoError(t, err)
		}()

		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Main Street Station",
			Limit:       10,
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		r := results[0]
		assert.True(t, r.ParentStation.Valid)
		assert.Equal(t, "hub1", r.ParentStation.String)
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

// stopServedByAgency creates a stop plus the route, trip and stop time that make the given
// agency serve it, so the search query has an agency to resolve for that stop.
func stopServedByAgency(t *testing.T, client *Client, agencyID, stopID, stopName string) {
	t.Helper()
	ctx := context.Background()

	_, err := client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       agencyID,
		Name:     "Agency " + agencyID,
		Url:      "http://" + agencyID + ".example.test",
		Timezone: "America/New_York",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID: stopID, Name: nulls.String(stopName), Lat: 40.0, Lon: -74.0,
	})
	require.NoError(t, err)

	routeID := "route_" + stopID
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID: routeID, AgencyID: agencyID, Type: 3,
	})
	require.NoError(t, err)

	tripID := "trip_" + stopID
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: "service_1",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateStopTime(ctx, CreateStopTimeParams{
		TripID: tripID, StopID: stopID, StopSequence: 1, ArrivalTime: 28800, DepartureTime: 28800,
	})
	require.NoError(t, err)
}

func TestSearchStopsByNameOrdersByCombinedID(t *testing.T) {
	client := createFTSTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20240101', '20251231')
	`)
	require.NoError(t, err)

	// Raw stop ID order (aaa, zzz) is the reverse of combined ID order (111_zzz, 999_aaa).
	stopServedByAgency(t, client, "999", "aaa", "Ordertest Alpha")
	stopServedByAgency(t, client, "111", "zzz", "Ordertest Zulu")

	// No route serves this stop, so it has no combined ID to sort by.
	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID: "bbb", Name: nulls.String("Ordertest Bravo"), Lat: 40.0, Lon: -74.0,
	})
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
		SearchQuery: "Ordertest",
		Limit:       10,
	})
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, []string{"zzz", "aaa", "bbb"}, []string{results[0].ID, results[1].ID, results[2].ID},
		"stops must be ordered by combined ID, with agency-less stops last")

	assert.Equal(t, "111", results[0].AgencyID.String)
	assert.Equal(t, "999", results[1].AgencyID.String)
	assert.False(t, results[2].AgencyID.Valid, "a stop no route serves resolves no agency")

	t.Run("limit keeps the lowest combined IDs", func(t *testing.T) {
		capped, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Ordertest",
			Limit:       1,
		})
		require.NoError(t, err)
		require.Len(t, capped, 1)
		assert.Equal(t, "zzz", capped[0].ID)
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
