package gtfsdb

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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
