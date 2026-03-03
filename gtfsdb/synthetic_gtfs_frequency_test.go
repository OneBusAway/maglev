package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// buildSyntheticGTFSZip creates a minimal valid GTFS zip archive in memory,
func buildSyntheticGTFSZip(t *testing.T, includeFrequencies bool) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"agency_1,Synthetic Transit,http://example.com,America/Los_Angeles\n",

		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"route_1,agency_1,R1,Route One,3\n" +
			"route_2,agency_1,R2,Route Two,3\n",

		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"service_1,1,1,1,1,1,0,0,20240101,20251231\n",

		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stop_1,First Stop,37.7749,-122.4194\n" +
			"stop_2,Second Stop,37.7849,-122.4094\n" +
			"stop_3,Third Stop,37.7949,-122.3994\n",

		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id,shape_id\n" +
			"route_1,service_1,trip_freq_0,Downtown via Freq,0,,\n" +
			"route_1,service_1,trip_freq_1,Uptown via Freq,1,,\n" +
			"route_2,service_1,trip_exact,Express Exact,0,,\n" +
			"route_2,service_1,trip_normal,Normal Trip,0,,\n",

		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"trip_freq_0,06:00:00,06:00:00,stop_1,1\n" +
			"trip_freq_0,06:10:00,06:10:00,stop_2,2\n" +
			"trip_freq_0,06:20:00,06:20:00,stop_3,3\n" +
			"trip_freq_1,07:00:00,07:00:00,stop_3,1\n" +
			"trip_freq_1,07:10:00,07:10:00,stop_2,2\n" +
			"trip_freq_1,07:20:00,07:20:00,stop_1,3\n" +
			"trip_exact,06:00:00,06:00:00,stop_1,1\n" +
			"trip_exact,06:15:00,06:15:00,stop_2,2\n" +
			"trip_exact,06:30:00,06:30:00,stop_3,3\n" +
			"trip_normal,08:00:00,08:00:00,stop_1,1\n" +
			"trip_normal,08:15:00,08:15:00,stop_2,2\n" +
			"trip_normal,08:30:00,08:30:00,stop_3,3\n",
	}

	if includeFrequencies {
		// Two headway-based (exact_times=0) entries for trip_freq_0:
		//   Morning: 6 AM – 9 AM, every 10 minutes
		//   Evening: 4 PM – 7 PM, every 15 minutes
		// One headway-based entry for trip_freq_1:
		//   Morning: 7 AM – 10 AM, every 12 minutes
		// One schedule-based (exact_times=1) entry for trip_exact:
		//   Morning: 6 AM – 9 AM, every 30 minutes
		// trip_normal has NO frequency entry (standard scheduled trip)
		files["frequencies.txt"] = "trip_id,start_time,end_time,headway_secs,exact_times\n" +
			"trip_freq_0,06:00:00,09:00:00,600,0\n" +
			"trip_freq_0,16:00:00,19:00:00,900,0\n" +
			"trip_freq_1,07:00:00,10:00:00,720,0\n" +
			"trip_exact,06:00:00,09:00:00,1800,1\n"
	}

	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestSyntheticGTFS_FrequencyIngestion(t *testing.T) {
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	gtfsData := buildSyntheticGTFSZip(t, true)

	err = client.processAndStoreGTFSDataWithSource(gtfsData, "synthetic-test")
	require.NoError(t, err, "Ingestion of synthetic GTFS with frequencies should succeed")

	// Verify basic data was imported
	agencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "agency_1", agencies[0].ID)

	routes, err := client.Queries.ListRoutes(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(routes))

	// Verify frequencies were ingested for trip_freq_0
	freqs0, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_freq_0")
	require.NoError(t, err)
	assert.Equal(t, 2, len(freqs0), "trip_freq_0 should have 2 frequency windows")

	// Verify ordering (morning window first, evening second)
	assert.Less(t, freqs0[0].StartTime, freqs0[1].StartTime, "Frequencies should be ordered by start_time")

	// Verify morning window values
	assert.Equal(t, int64(600), freqs0[0].HeadwaySecs, "Morning headway should be 600 seconds (10 min)")
	assert.Equal(t, int64(0), freqs0[0].ExactTimes, "Morning window should be frequency-based")

	// Verify evening window values
	assert.Equal(t, int64(900), freqs0[1].HeadwaySecs, "Evening headway should be 900 seconds (15 min)")
	assert.Equal(t, int64(0), freqs0[1].ExactTimes, "Evening window should be frequency-based")

	// Verify frequencies were ingested for trip_freq_1
	freqs1, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_freq_1")
	require.NoError(t, err)
	assert.Equal(t, 1, len(freqs1), "trip_freq_1 should have 1 frequency window")
	assert.Equal(t, int64(720), freqs1[0].HeadwaySecs, "trip_freq_1 headway should be 720 seconds (12 min)")

	// Verify exact_times=1 for trip_exact
	freqsExact, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_exact")
	require.NoError(t, err)
	assert.Equal(t, 1, len(freqsExact), "trip_exact should have 1 frequency window")
	assert.Equal(t, int64(1), freqsExact[0].ExactTimes, "trip_exact should be schedule-based (exact_times=1)")
	assert.Equal(t, int64(1800), freqsExact[0].HeadwaySecs, "trip_exact headway should be 1800 seconds (30 min)")

	// Verify trip_normal has NO frequencies
	freqsNormal, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_normal")
	require.NoError(t, err)
	assert.Equal(t, 0, len(freqsNormal), "trip_normal should have no frequency entries")

	// Verify GetFrequencyTripIDs returns all frequency-based trips
	tripIDs, err := client.Queries.GetFrequencyTripIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, len(tripIDs), "Should have 3 distinct frequency trip IDs")
	assert.Contains(t, tripIDs, "trip_freq_0")
	assert.Contains(t, tripIDs, "trip_freq_1")
	assert.Contains(t, tripIDs, "trip_exact")
}

func TestSyntheticGTFS_NoFrequencyFile(t *testing.T) {
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	gtfsData := buildSyntheticGTFSZip(t, false)

	err = client.processAndStoreGTFSDataWithSource(gtfsData, "synthetic-no-freq")
	require.NoError(t, err, "Ingestion of GTFS without frequencies.txt should succeed")

	// Verify trips were still imported
	trip, err := client.Queries.GetTrip(ctx, "trip_freq_0")
	require.NoError(t, err)
	assert.Equal(t, "trip_freq_0", trip.ID)

	// Verify no frequencies exist
	freqs, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_freq_0")
	require.NoError(t, err)
	assert.Equal(t, 0, len(freqs), "Should have no frequencies when frequencies.txt is absent")

	tripIDs, err := client.Queries.GetFrequencyTripIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(tripIDs))
}

func TestSyntheticGTFS_FrequenciesClearedOnReimport(t *testing.T) {
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// First import: with frequencies
	dataWithFreqs := buildSyntheticGTFSZip(t, true)
	err = client.processAndStoreGTFSDataWithSource(dataWithFreqs, "source-a")
	require.NoError(t, err)

	// Verify frequencies exist
	freqs, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_freq_0")
	require.NoError(t, err)
	assert.Equal(t, 2, len(freqs), "Should have frequencies after first import")

	// Second import: same data without frequencies (different source triggers reimport)
	dataNoFreqs := buildSyntheticGTFSZip(t, false)
	err = client.processAndStoreGTFSDataWithSource(dataNoFreqs, "source-b")
	require.NoError(t, err)

	// Verify frequencies were cleared
	freqs, err = client.Queries.GetFrequenciesForTrip(ctx, "trip_freq_0")
	require.NoError(t, err)
	assert.Equal(t, 0, len(freqs), "Frequencies should be cleared after reimport without frequencies.txt")

	tripIDs, err := client.Queries.GetFrequencyTripIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(tripIDs), "No frequency trip IDs should remain")
}

func TestSyntheticGTFS_TableCountsIncludeFrequencies(t *testing.T) {
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	gtfsData := buildSyntheticGTFSZip(t, true)
	err = client.processAndStoreGTFSDataWithSource(gtfsData, "synthetic-test")
	require.NoError(t, err)

	counts, err := client.TableCounts()
	require.NoError(t, err)

	freqCount, exists := counts["frequencies"]
	assert.True(t, exists, "TableCounts should include 'frequencies'")
	assert.Equal(t, 4, freqCount, "Should have 4 frequency rows (2 + 1 + 1)")
}
