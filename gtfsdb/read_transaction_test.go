package gtfsdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestTransactionQueriesKeepOneSnapshot simulates a static reload committing
// between the trip lookup and the route/stop lookups made by one API request.
// A query set bound to the request transaction must continue to see the old
// generation, while the shared query set sees the newly committed generation.
func TestTransactionQueriesKeepOneSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "unprepared queries"
		if prepared {
			name = "prepared queries"
		}

		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			client, err := NewClient(Config{
				DBPath: filepath.Join(t.TempDir(), "gtfs.db"),
				Env:    appconf.Development,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			feedBytes, err := os.ReadFile("../testdata/raba.zip")
			require.NoError(t, err)
			feed, err := ParseGtfsData(feedBytes, "read-transaction-test")
			require.NoError(t, err)
			_, err = client.StoreGtfsData(ctx, feed)
			require.NoError(t, err)

			queries := client.Queries
			if prepared {
				queries, err = Prepare(ctx, client.DB)
				require.NoError(t, err)
				t.Cleanup(func() { _ = queries.Close() })
			}

			trip, err := queries.GetTrip(ctx, memoTestTripID)
			require.NoError(t, err)
			stopTimes, err := queries.GetStopTimesForTrip(ctx, trip.ID)
			require.NoError(t, err)
			require.NotEmpty(t, stopTimes)
			stopID := stopTimes[0].StopID

			const oldRouteName = "generation-one-route"
			const newRouteName = "generation-two-route"
			const oldStopName = "generation-one-stop"
			const newStopName = "generation-two-stop"
			_, err = client.DB.ExecContext(ctx,
				"UPDATE routes SET long_name = ? WHERE id = ?", oldRouteName, trip.RouteID)
			require.NoError(t, err)
			_, err = client.DB.ExecContext(ctx,
				"UPDATE stops SET name = ? WHERE id = ?", oldStopName, stopID)
			require.NoError(t, err)

			readTx, err := client.DB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = readTx.Rollback() }()
			requestQueries := queries.WithTx(readTx)

			// This first read establishes the SQLite snapshot for the request.
			requestTrip, err := requestQueries.GetTrip(ctx, trip.ID)
			require.NoError(t, err)
			require.Equal(t, trip.RouteID, requestTrip.RouteID)

			// Commit a new generation while the request's read transaction remains
			// open. WAL allows this writer to commit without invalidating the reader.
			writeTx, err := client.DB.BeginTx(ctx, nil)
			require.NoError(t, err)
			_, err = writeTx.ExecContext(ctx,
				"UPDATE routes SET long_name = ? WHERE id = ?", newRouteName, trip.RouteID)
			require.NoError(t, err)
			_, err = writeTx.ExecContext(ctx,
				"UPDATE stops SET name = ? WHERE id = ?", newStopName, stopID)
			require.NoError(t, err)
			require.NoError(t, writeTx.Commit())

			requestRoute, err := requestQueries.GetRoute(ctx, trip.RouteID)
			require.NoError(t, err)
			require.Equal(t, oldRouteName, requestRoute.LongName.String,
				"the request must keep reading from its original GTFS generation")

			requestStops, err := requestQueries.GetStopsByIDs(ctx, []string{stopID})
			require.NoError(t, err)
			require.Len(t, requestStops, 1)
			require.Equal(t, oldStopName, requestStops[0].Name.String,
				"slice queries must also use the request transaction")

			freshRoute, err := queries.GetRoute(ctx, trip.RouteID)
			require.NoError(t, err)
			require.Equal(t, newRouteName, freshRoute.LongName.String,
				"a subsequent request must see the newly committed generation")

			freshStops, err := queries.GetStopsByIDs(ctx, []string{stopID})
			require.NoError(t, err)
			require.Len(t, freshStops, 1)
			require.Equal(t, newStopName, freshStops[0].Name.String)

			require.NoError(t, readTx.Commit())
		})
	}
}
