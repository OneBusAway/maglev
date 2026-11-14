package gtfs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

// TestGetMetrics_ScheduledBlocksOnDSTDays checks elapsed GTFS offsets against
// independently specified instants, including the two occurrences of fall's 01:30.
func TestGetMetrics_ScheduledBlocksOnDSTDays(t *testing.T) {
	tests := []struct {
		name, timezone, serviceDate, instant string
		start, end                           time.Duration
		want                                 int
	}{
		{"ordinary day", "America/Los_Angeles", "20250308", "2025-03-08T02:30:00-08:00", 2 * time.Hour, 3 * time.Hour, 1},
		{"spring active", "America/Los_Angeles", "20250309", "2025-03-09T01:30:00-08:00", 2 * time.Hour, 3 * time.Hour, 1},
		{"spring inactive", "America/Los_Angeles", "20250309", "2025-03-09T01:30:00-08:00", time.Hour, 2 * time.Hour, 0},
		{"fall first hour active", "America/Los_Angeles", "20251102", "2025-11-02T01:30:00-07:00", 0, time.Hour, 1},
		{"fall first hour inactive", "America/Los_Angeles", "20251102", "2025-11-02T01:30:00-07:00", time.Hour, 2 * time.Hour, 0},
		{"fall second hour active", "America/Los_Angeles", "20251102", "2025-11-02T01:30:00-08:00", time.Hour, 2 * time.Hour, 1},
		{"fall second hour inactive", "America/Los_Angeles", "20251102", "2025-11-02T01:30:00-08:00", 0, time.Hour, 0},
		{"spring overnight", "America/Los_Angeles", "20250308", "2025-03-09T03:30:00-07:00", 26 * time.Hour, 27 * time.Hour, 1},
		{"fall overnight", "America/Los_Angeles", "20251101", "2025-11-02T01:30:00-08:00", 26 * time.Hour, 27 * time.Hour, 1},
		{"day after spring overnight", "America/Los_Angeles", "20250309", "2025-03-10T00:30:00-07:00", 24 * time.Hour, 25 * time.Hour, 1},
		{"day after fall overnight", "America/Los_Angeles", "20251102", "2025-11-03T00:30:00-08:00", 24 * time.Hour, 25 * time.Hour, 1},
		{"spring inclusive start", "America/Los_Angeles", "20250309", "2025-03-09T01:00:00-08:00", 2 * time.Hour, 3 * time.Hour, 1},
		{"spring inclusive end", "America/Los_Angeles", "20250309", "2025-03-09T03:00:00-07:00", 2 * time.Hour, 3 * time.Hour, 1},
		{"spring before start", "America/Los_Angeles", "20250309", "2025-03-09T00:59:59-08:00", 2 * time.Hour, 3 * time.Hour, 0},
		{"spring after end", "America/Los_Angeles", "20250309", "2025-03-09T03:00:01-07:00", 2 * time.Hour, 3 * time.Hour, 0},
		{"no DST timezone", "America/Phoenix", "20250309", "2025-03-09T02:30:00-07:00", 2 * time.Hour, 3 * time.Hour, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManagerWithRoutes(map[string]*gtfs.Route{
				"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
			})
			t.Cleanup(func() { require.NoError(t, manager.GtfsDB.Close()) })
			ctx := context.Background()
			_, err := manager.GtfsDB.DB.ExecContext(ctx, "UPDATE agencies SET timezone = ? WHERE id = 'A'", tt.timezone)
			require.NoError(t, err)
			ensureDefaultCalendar(t, manager)
			// Run this service on exactly one date so today's and yesterday's
			// lookups cannot hide a wrong date or double-count the same instance.
			_, err = manager.GtfsDB.DB.ExecContext(ctx, "UPDATE calendar SET start_date = ?, end_date = ? WHERE id = ?", tt.serviceDate, tt.serviceDate, defaultTestServiceID)
			require.NoError(t, err)
			_, err = manager.GtfsDB.Queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
				ID: "T1", RouteID: "R1", ServiceID: defaultTestServiceID,
				MinArrivalTime:   sql.NullInt64{Int64: tt.start.Nanoseconds(), Valid: true},
				MaxDepartureTime: sql.NullInt64{Int64: tt.end.Nanoseconds(), Valid: true},
			})
			require.NoError(t, err)
			now, err := time.Parse(time.RFC3339, tt.instant)
			require.NoError(t, err)
			snapshot, err := manager.GetMetrics(ctx, now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, snapshot.ScheduledTripsCount["A"])
		})
	}
}
