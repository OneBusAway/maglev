package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

// blockMatcherFeed covers the legacy matching cases:
//   - T_LATE runs only on Mondays, departing 25:00 (Tuesday 01:00).
//   - T_DAILY runs Monday and Tuesday at 00:30, so both days qualify.
//   - T_ZERO's block starts at 00:00:00, which never qualifies.
//   - T_NOBLOCK has no block_id and forms its own one-trip block.
//   - T_SECOND shares block B5 with the earlier T_FIRST.
var blockMatcherFeed = map[string]string{
	"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
		"40,Test Agency,https://example.com,America/Los_Angeles\n",
	"routes.txt": "route_id,agency_id,route_short_name,route_type\n" +
		"R1,40,1,3\n",
	"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
		"S1,One,40.0,-122.0\nS2,Two,40.1,-122.0\n",
	"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
		"MON,1,0,0,0,0,0,0,20261001,20261031\n" +
		"MONTUE,1,1,0,0,0,0,0,20261001,20261031\n" +
		"DAILY,1,1,1,1,1,1,1,20261001,20261031\n",
	"trips.txt": "route_id,service_id,trip_id,block_id\n" +
		"R1,MON,T_LATE,B1\n" +
		"R1,MONTUE,T_DAILY,B2\n" +
		"R1,DAILY,T_ZERO,B3\n" +
		"R1,DAILY,T_NOBLOCK,\n" +
		"R1,DAILY,T_FIRST,B5\n" +
		"R1,DAILY,T_SECOND,B5\n",
	"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		"T_LATE,25:00:00,25:00:00,S1,1\nT_LATE,25:20:00,25:20:00,S2,2\n" +
		"T_DAILY,00:30:00,00:30:00,S1,1\nT_DAILY,00:50:00,00:50:00,S2,2\n" +
		"T_ZERO,00:00:00,00:00:00,S1,1\nT_ZERO,00:20:00,00:20:00,S2,2\n" +
		"T_NOBLOCK,09:00:00,09:00:00,S1,1\nT_NOBLOCK,09:20:00,09:20:00,S2,2\n" +
		"T_FIRST,06:00:00,06:00:00,S1,1\nT_FIRST,06:20:00,06:20:00,S2,2\n" +
		"T_SECOND,07:00:00,07:00:00,S1,1\nT_SECOND,07:20:00,07:20:00,S2,2\n",
}

// matcherZone is the fixture agency's timezone, which candidate dates use.
var matcherZone = func() *time.Location {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err)
	}
	return zone
}()

var (
	matcherMonday  = time.Date(2026, 10, 5, 0, 0, 0, 0, matcherZone)
	matcherTuesday = time.Date(2026, 10, 6, 0, 0, 0, 0, matcherZone)
)

func zipGTFSFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := writer.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func newTestBlockMatcher(t *testing.T, now func() time.Time) *blockMatcher {
	t.Helper()
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	parsed, err := gtfsdb.ParseGtfsData(zipGTFSFiles(t, blockMatcherFeed), "block-matcher-test")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	return newBlockMatcher(client.Queries, now)
}

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestCandidateServiceDates(t *testing.T) {
	day := func(d int, hh, mm, ss int) time.Time { return time.Date(2026, 10, d, hh, mm, ss, 0, matcherZone) }
	tests := []struct {
		name      string
		reference time.Time
		want      []time.Time
	}{
		{"03:59:59 yesterday then today", day(6, 3, 59, 59), []time.Time{matcherMonday, matcherTuesday}},
		{"04:00 today only", day(6, 4, 0, 0), []time.Time{matcherTuesday}},
		{"20:59:59 today only", day(6, 20, 59, 59), []time.Time{matcherTuesday}},
		{"21:00 today then tomorrow", day(6, 21, 0, 0), []time.Time{matcherTuesday, matcherTuesday.AddDate(0, 0, 1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, candidateServiceDates(tt.reference))
		})
	}
}

// The fixture agency runs on America/Los_Angeles while newTestBlockMatcher's
// clock is UTC. Tuesday 10:00 UTC is 03:00 Tuesday for the agency, so the
// agency-local window checks Monday then Tuesday and finds Monday's 25:00
// trip; a UTC window would check Tuesday only and miss it.
func TestBlockMatcherUsesAgencyTimezone(t *testing.T) {
	reference := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	matcher := newTestBlockMatcher(t, fixedNow(reference))

	match, err := matcher.Match(context.Background(), "feed", "T_LATE", reference)

	require.NoError(t, err)
	require.NotNil(t, match)
	assert.Equal(t, matcherMonday, match.ServiceDate)
}

func TestBlockMatcherMatch(t *testing.T) {
	ctx := context.Background()
	tuesday0110 := time.Date(2026, 10, 6, 1, 10, 0, 0, matcherZone)

	tests := []struct {
		name          string
		tripID        string
		reference     time.Time
		wantDate      time.Time
		wantBlockID   string
		wantStart     time.Duration
		wantUnmatched bool
	}{
		{"overnight previous-day service", "T_LATE", tuesday0110, matcherMonday, "B1", 25 * time.Hour, false},
		{"first qualifying day wins", "T_DAILY", tuesday0110, matcherMonday, "B2", 30 * time.Minute, false},
		{"zero block start never qualifies", "T_ZERO", tuesday0110, time.Time{}, "", 0, true},
		{"trip without block", "T_NOBLOCK", time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone), matcherTuesday, "T_NOBLOCK", 9 * time.Hour, false},
		{"later trip in shared block", "T_SECOND", time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone), matcherTuesday, "B5", 7 * time.Hour, false},
		{"unknown static trip", "T_MISSING", tuesday0110, time.Time{}, "", 0, true},
		{"epoch reference finds no service", "T_DAILY", time.Unix(0, 0), time.Time{}, "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := newTestBlockMatcher(t, fixedNow(tuesday0110))
			match, err := matcher.Match(ctx, "feed", tt.tripID, tt.reference)
			require.NoError(t, err)
			if tt.wantUnmatched {
				assert.Nil(t, match)
				return
			}
			require.NotNil(t, match)
			assert.Equal(t, tt.tripID, match.TripID)
			assert.Equal(t, "R1", match.RouteID)
			assert.Equal(t, "40", match.AgencyID)
			assert.Equal(t, tt.wantBlockID, match.BlockID)
			assert.Equal(t, tt.wantDate, match.ServiceDate)
			assert.Equal(t, tt.wantStart, match.TripStart)
		})
	}
}

func TestBlockMatcherCache(t *testing.T) {
	ctx := context.Background()
	tuesday0359 := time.Date(2026, 10, 6, 3, 59, 0, 0, matcherZone)
	tuesday0401 := time.Date(2026, 10, 6, 4, 1, 0, 0, matcherZone)

	t.Run("cached association survives a date-window change", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0359))
		first, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0359)
		require.NoError(t, err)
		require.NotNil(t, first)
		// At 04:01 the window is Tuesday only, where T_LATE does not run.
		second, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0401)
		require.NoError(t, err)
		assert.Equal(t, first, second)
	})

	t.Run("unresolved result is cached until expiry", func(t *testing.T) {
		current := tuesday0401
		matcher := newTestBlockMatcher(t, func() time.Time { return current })
		miss, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0401)
		require.NoError(t, err)
		require.Nil(t, miss)

		current = current.Add(29 * time.Minute)
		stillMiss, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0359)
		require.NoError(t, err)
		assert.Nil(t, stillMiss, "cached failure is reused within 30 minutes")

		current = tuesday0401.Add(30 * time.Minute)
		rematched, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0359)
		require.NoError(t, err)
		assert.NotNil(t, rematched, "expiry is measured from insertion, not extended by reads")
	})

	t.Run("static replacement clears the cache", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0401))
		miss, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0401)
		require.NoError(t, err)
		require.Nil(t, miss)
		matcher.Clear()
		match, err := matcher.Match(ctx, "feed", "T_LATE", tuesday0359)
		require.NoError(t, err)
		assert.NotNil(t, match)
	})

	t.Run("caches are per source", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0401))
		miss, err := matcher.Match(ctx, "feed-a", "T_LATE", tuesday0401)
		require.NoError(t, err)
		require.Nil(t, miss)
		match, err := matcher.Match(ctx, "feed-b", "T_LATE", tuesday0359)
		require.NoError(t, err)
		assert.NotNil(t, match)
	})
}

func TestBlockMatcherDoesNotCacheQueryFailures(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)
	matcher := newTestBlockMatcher(t, fixedNow(reference))

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := matcher.Match(canceled, "feed", "T_NOBLOCK", reference)
	require.Error(t, err)

	match, err := matcher.Match(ctx, "feed", "T_NOBLOCK", reference)
	require.NoError(t, err)
	assert.NotNil(t, match, "a failed query must not leave a cached unresolved entry")
}
