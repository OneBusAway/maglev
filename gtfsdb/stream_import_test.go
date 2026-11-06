package gtfsdb

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// zipFiles builds a GTFS zip from file names and contents.
func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestStreamedImportSkipsTripsWithoutStopTimes(t *testing.T) {
	feed := zipFiles(t, map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_type\nr,a,3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ns,1,1,1,1,1,0,0,20240101,20251231\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nst1,One,47.6,-122.3\nst2,Two,47.7,-122.4\n",
		"trips.txt":      "route_id,service_id,trip_id\nr,s,with_stops\nr,s,without_stops\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nwith_stops,08:00:00,08:00:00,st1,1\nwith_stops,08:10:00,08:10:00,st2,2\n",
	})
	data, err := ParseGtfsStream(bytes.NewReader(feed), int64(len(feed)), strings.Repeat("a", 64), "source")
	require.NoError(t, err)
	client := newTestClient(t)
	_, err = client.StoreGtfsData(t.Context(), data)
	require.NoError(t, err)

	var trips []string
	rows, err := client.DB.Query(`SELECT id FROM trips`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		trips = append(trips, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"with_stops"}, trips)
}

// failingReaderAt fails reads that overlap [failFrom, failTo) once fail is
// set.
type failingReaderAt struct {
	io.ReaderAt
	fail             atomic.Bool
	failFrom, failTo int64
}

func (r *failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	overlaps := off < r.failTo && off+int64(len(p)) > r.failFrom
	if r.fail.Load() && overlaps {
		return 0, errors.New("disk went away")
	}
	return r.ReaderAt.ReadAt(p, off)
}

// zipEntryRange returns the byte range of a file's data in a zip.
func zipEntryRange(t *testing.T, feed []byte, name string) (int64, int64) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(feed), int64(len(feed)))
	require.NoError(t, err)
	for _, file := range reader.File {
		if file.Name == name {
			offset, err := file.DataOffset()
			require.NoError(t, err)
			return offset, offset + int64(file.CompressedSize64)
		}
	}
	t.Fatalf("no %s in feed", name)
	return 0, 0
}

// TestStreamedImportFailsWhenTheFeedCanNoLongerBeRead makes the feed
// unreadable after parsing, inside stop_times.txt or inside shapes.txt, and
// checks that the import fails and stores nothing.
func TestStreamedImportFailsWhenTheFeedCanNoLongerBeRead(t *testing.T) {
	raba, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)

	for _, tt := range []struct {
		file, wantErr string
	}{
		{"stop_times.txt", "unable to read stop times"},
		{"shapes.txt", "unable to read shapes"},
	} {
		t.Run(tt.file, func(t *testing.T) {
			reader := &failingReaderAt{ReaderAt: bytes.NewReader(raba)}
			reader.failFrom, reader.failTo = zipEntryRange(t, raba, tt.file)
			data, err := ParseGtfsStream(reader, int64(len(raba)), strings.Repeat("a", 64), "source")
			require.NoError(t, err)

			reader.fail.Store(true)
			client := newTestClient(t)
			_, err = client.StoreGtfsData(t.Context(), data)
			assert.ErrorContains(t, err, tt.wantErr)

			var trips int
			require.NoError(t, client.DB.QueryRow(`SELECT COUNT(*) FROM trips`).Scan(&trips))
			assert.Zero(t, trips, "a failed import must not leave a partial feed")
		})
	}
}

// TestFeedRowsStopWhenTheConsumerStops checks that a consumer that stops
// early ends the stream without recording an error.
func TestFeedRowsStopWhenTheConsumerStops(t *testing.T) {
	raba, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	data, err := ParseGtfsData(raba, "source")
	require.NoError(t, err)

	rows := data.rows()
	for range rows.stopTimes() {
		break
	}
	for range rows.shapes() {
		break
	}
	assert.NoError(t, rows.err)
	assert.Len(t, rows.tripEnds, 1)
}

const (
	smallFeedAgency   = "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n"
	smallFeedRoutes   = "route_id,agency_id,route_type\nr,a,3\n"
	smallFeedCalendar = "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ns,1,1,1,1,1,0,0,20240101,20251231\n"
	smallFeedHash     = "small-feed-hash"
)

// smallFeed builds a zip with one agency, route r and service s, plus files.
func smallFeed(t *testing.T, files map[string]string) []byte {
	t.Helper()
	all := map[string]string{
		"agency.txt":   smallFeedAgency,
		"routes.txt":   smallFeedRoutes,
		"calendar.txt": smallFeedCalendar,
	}
	maps.Copy(all, files)
	return zipFiles(t, all)
}

// importStreamedFeed parses feed as a stream and stores it in a new client.
func importStreamedFeed(t *testing.T, feed []byte) *Client {
	t.Helper()
	data, err := ParseGtfsStream(bytes.NewReader(feed), int64(len(feed)), smallFeedHash, "source")
	require.NoError(t, err)
	client := newTestClient(t)
	_, err = client.StoreGtfsData(t.Context(), data)
	require.NoError(t, err)
	return client
}

// queryStrings returns the first column of every row the query returns.
func queryStrings(t *testing.T, client *Client, query string, args ...any) []string {
	t.Helper()
	rows, err := client.DB.Query(query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		values = append(values, value)
	}
	require.NoError(t, rows.Err())
	return values
}

// TestStreamedTripEndsMatchParsedFeed checks that the trip ends a streamed
// import collects are the ones a fully parsed feed gives.
func TestStreamedTripEndsMatchParsedFeed(t *testing.T) {
	raba, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)

	parsed, err := gtfs.ParseStatic(raba, gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	require.NoError(t, ValidateAndFilterGTFSData(parsed, nil))
	want := tripEndsOf(parsed.Trips)

	data, err := ParseGtfsData(raba, "source")
	require.NoError(t, err)
	rows := data.rows()
	for range rows.stopTimes() {
	}
	require.NoError(t, rows.err)
	assert.ElementsMatch(t, want, rows.tripEnds)
}

// TestStreamedImportOfUngroupedFeed imports a feed whose stop times and shape
// points are neither grouped nor in sequence order, with rows that name an
// unknown stop or trip.
func TestStreamedImportOfUngroupedFeed(t *testing.T) {
	client := importStreamedFeed(t, smallFeed(t, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"st1,One,47.6,-122.3\nst2,Two,47.7,-122.4\nst3,Three,47.8,-122.5\n",
		"trips.txt": "route_id,service_id,trip_id,block_id,shape_id\n" +
			"r,s,t1,b,sh\nr,s,t2,b,sh\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"t1,08:20:00,08:20:00,st3,3\n" +
			"t2,08:40:00,08:40:00,st2,2\n" +
			"t1,08:00:00,08:00:00,st1,1\n" +
			"t1,08:30:00,08:30:00,unknown_stop,4\n" +
			"t2,08:50:00,08:50:00,st1,3\n" +
			"unknown_trip,08:00:00,08:00:00,st1,1\n" +
			"t1,08:10:00,08:10:00,st2,2\n" +
			"t2,08:30:00,08:30:00,st3,1\n",
		"shapes.txt": "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\n" +
			"sh,47.3,-122.3,30\nsh,47.1,-122.1,10\nsh,47.2,-122.2,20\n",
	}))

	const stopsOfTrip = `SELECT stop_id FROM stop_times WHERE trip_id = ? ORDER BY stop_sequence`
	assert.Equal(t, []string{"st1", "st2", "st3"}, queryStrings(t, client, stopsOfTrip, "t1"))
	assert.Equal(t, []string{"st3", "st2", "st1"}, queryStrings(t, client, stopsOfTrip, "t2"))
	assert.Equal(t, []string{"t1", "t2"}, queryStrings(t, client, `SELECT DISTINCT trip_id FROM stop_times ORDER BY trip_id`),
		"the rows with an unknown stop or trip are not stored")

	assert.Equal(t, []string{"0:47.1", "1:47.2", "2:47.3"}, queryStrings(t, client,
		`SELECT shape_pt_sequence || ':' || lat FROM shapes WHERE shape_id = 'sh' ORDER BY shape_pt_sequence`),
		"shape points are renumbered in shape_pt_sequence order")

	var layovers []CreateBlockLayoverParams
	rows, err := client.DB.Query(`SELECT block_id, service_id, route_id, layover_stop_id, layover_start, layover_end, next_trip_id FROM block_layover`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var layover CreateBlockLayoverParams
		require.NoError(t, rows.Scan(&layover.BlockID, &layover.ServiceID, &layover.RouteID,
			&layover.LayoverStopID, &layover.LayoverStart, &layover.LayoverEnd, &layover.NextTripID))
		layovers = append(layovers, layover)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []CreateBlockLayoverParams{{
		BlockID:       "b",
		ServiceID:     "s",
		RouteID:       "r",
		LayoverStopID: "st3",
		LayoverStart:  int64(8*time.Hour + 20*time.Minute),
		LayoverEnd:    int64(8*time.Hour + 30*time.Minute),
		NextTripID:    "t2",
	}}, layovers, "t1 ends at st3 where t2 starts ten minutes later")
}

// TestStreamedImportSkipsStopTimesOfFilteredTrips checks that the stop times
// of a trip that validation drops are not stored.
func TestStreamedImportSkipsStopTimesOfFilteredTrips(t *testing.T) {
	client := importStreamedFeed(t, smallFeed(t, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\nst1,One,47.6,-122.3\nst2,Two,47.7,-122.4\n",
		"trips.txt": "route_id,service_id,trip_id\nr,s,kept\nmissing,s,no_route\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"kept,08:00:00,08:00:00,st1,1\nkept,08:10:00,08:10:00,st2,2\n" +
			"no_route,09:00:00,09:00:00,st1,1\nno_route,09:10:00,09:10:00,st2,2\n",
	}))

	assert.Equal(t, []string{"kept"}, queryStrings(t, client, `SELECT id FROM trips`))
	assert.Equal(t, []string{"kept"}, queryStrings(t, client, `SELECT DISTINCT trip_id FROM stop_times`))
}

// TestFailedStreamedImportKeepsPreviousFeed checks that a re-import that fails
// while reading stop_times.txt leaves the previous feed and its metadata in
// place.
func TestFailedStreamedImportKeepsPreviousFeed(t *testing.T) {
	const rabaTrips, rabaStopTimes = 282, 7346
	raba, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	client := newTestClient(t)
	first, err := ParseGtfsData(raba, "source")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(t.Context(), first)
	require.NoError(t, err)

	reader := &failingReaderAt{ReaderAt: bytes.NewReader(raba)}
	reader.failFrom, reader.failTo = zipEntryRange(t, raba, "stop_times.txt")
	second, err := ParseGtfsStream(reader, int64(len(raba)), strings.Repeat("b", 64), "source")
	require.NoError(t, err)
	reader.fail.Store(true)
	_, err = client.StoreGtfsData(t.Context(), second)
	require.Error(t, err)

	counts, err := client.TableCounts()
	require.NoError(t, err)
	assert.Equal(t, rabaTrips, counts["trips"])
	assert.Equal(t, rabaStopTimes, counts["stop_times"])
	metadata, err := client.Queries.GetImportMetadata(t.Context())
	require.NoError(t, err)
	assert.Equal(t, first.Hash, metadata.FileHash)
}

// TestStreamedImportClearsMissingParentStation checks that a stop whose
// parent_station names no stop is stored without a parent, and that clearing
// it doesn't disturb the stop times read from the stream.
func TestStreamedImportClearsMissingParentStation(t *testing.T) {
	feed := smallFeed(t, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon,parent_station\n" +
			"st1,One,47.6,-122.3,ghost\nst2,Two,47.7,-122.4,\n",
		"trips.txt": "route_id,service_id,trip_id\nr,s,t1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"t1,08:00:00,08:00:00,st1,1\nt1,08:10:00,08:10:00,st2,2\n",
	})
	data, err := ParseGtfsStream(bytes.NewReader(feed), int64(len(feed)), smallFeedHash, "source")
	require.NoError(t, err)
	// go-gtfs already drops a parent it can't find, so put one back and
	// validate again to reach the clearing that copies the streamed stops.
	data.Static.Stops[0].Parent = &gtfs.Stop{Id: "ghost"}
	require.NoError(t, validateAndFilterGTFSData(data.Static, nil, data.stream.StopTimeCount))
	client := newTestClient(t)
	_, err = client.StoreGtfsData(t.Context(), data)
	require.NoError(t, err)

	stop, err := client.Queries.GetStop(t.Context(), "st1")
	require.NoError(t, err)
	assert.False(t, stop.ParentStation.Valid)
	assert.Equal(t, []string{"st1", "st2"}, queryStrings(t, client,
		`SELECT stop_id FROM stop_times WHERE trip_id = 't1' ORDER BY stop_sequence`))
}
