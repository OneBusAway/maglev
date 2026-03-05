package gtfsdb

import (
	"database/sql"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableCounts(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	client := &Client{DB: db}

	_, err = db.Exec(`
		CREATE TABLE agencies (id TEXT);
		INSERT INTO agencies VALUES ('1');
		
		CREATE TABLE stops (id TEXT);
		INSERT INTO stops VALUES ('s1'), ('s2');

		-- Create a table NOT in the whitelist to ensure it's ignored
		CREATE TABLE secret_table (id TEXT);
	`)
	require.NoError(t, err)

	counts, err := client.TableCounts()
	require.NoError(t, err)

	assert.Equal(t, 1, counts["agencies"], "Should count agencies correctly")
	assert.Equal(t, 2, counts["stops"], "Should count stops correctly")

	_, exists := counts["secret_table"]
	assert.False(t, exists, "Should not include tables outside the whitelist")
}

func TestTableCounts_IncludesFrequencies(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	client := &Client{DB: db}

	_, err = db.Exec(`
        CREATE TABLE frequencies (
            trip_id TEXT NOT NULL,
            start_time INTEGER NOT NULL,
            end_time INTEGER NOT NULL,
            headway_secs INTEGER NOT NULL,
            exact_times INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY (trip_id, start_time)
        );
        INSERT INTO frequencies VALUES ('trip_1', 21600000000000, 32400000000000, 600, 0);
        INSERT INTO frequencies VALUES ('trip_1', 57600000000000, 68400000000000, 600, 1);
        INSERT INTO frequencies VALUES ('trip_2', 25200000000000, 36000000000000, 300, 0);
    `)
	require.NoError(t, err)

	counts, err := client.TableCounts()
	require.NoError(t, err)

	assert.Equal(t, 3, counts["frequencies"], "Should count frequencies correctly")
}

func TestStaticDataCounts_IncludesFrequencies(t *testing.T) {
	client := &Client{}

	// Create a mock gtfs.Static object.
	// We only need to populate the arrays so len() works correctly.
	mockStatic := &gtfs.Static{
		Trips: []gtfs.ScheduledTrip{
			{
				Frequencies: []gtfs.Frequency{
					{},
					{},
				},
			},
			{
				Frequencies: []gtfs.Frequency{
					{},
				},
			},
			{},
		},
	}

	counts := client.staticDataCounts(mockStatic)

	assert.Equal(t, 3, counts["frequencies"], "Should aggregate all frequencies across all trips (2 + 1 + 0 = 3)")
	assert.Equal(t, 3, counts["trips"], "Should count total trips correctly")
}
