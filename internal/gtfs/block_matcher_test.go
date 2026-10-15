package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"sync/atomic"
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
//   - T_ZERO's block starts at 00:00:00, which legacy rejected and Maglev
//     accepts.
//   - T_NOBLOCK has no block_id and forms its own one-trip block.
//   - T_SECOND shares block B5 with the earlier T_FIRST.
//   - T_HEADWAY is a frequency-based trip.
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
		"R1,DAILY,T_SECOND,B5\n" +
		"R1,DAILY,T_HEADWAY,B6\n",
	"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		"T_LATE,25:00:00,25:00:00,S1,1\nT_LATE,25:20:00,25:20:00,S2,2\n" +
		"T_DAILY,00:30:00,00:30:00,S1,1\nT_DAILY,00:50:00,00:50:00,S2,2\n" +
		"T_ZERO,00:00:00,00:00:00,S1,1\nT_ZERO,00:20:00,00:20:00,S2,2\n" +
		"T_NOBLOCK,09:00:00,09:00:00,S1,1\nT_NOBLOCK,09:20:00,09:20:00,S2,2\n" +
		"T_FIRST,06:00:00,06:00:00,S1,1\nT_FIRST,06:20:00,06:20:00,S2,2\n" +
		"T_SECOND,07:00:00,07:00:00,S1,1\nT_SECOND,07:20:00,07:20:00,S2,2\n" +
		"T_HEADWAY,06:00:00,06:00:00,S1,1\nT_HEADWAY,06:20:00,06:20:00,S2,2\n",
	"frequencies.txt": "trip_id,start_time,end_time,headway_secs,exact_times\n" +
		"T_HEADWAY,06:00:00,22:00:00,900,0\n",
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

func newBlockMatcherTestDB(t *testing.T) *gtfsdb.Client {
	t.Helper()
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	parsed, err := gtfsdb.ParseGtfsData(zipGTFSFiles(t, blockMatcherFeed), "block-matcher-test")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	return client
}

func newTestBlockMatcher(t *testing.T, now func() time.Time) *blockMatcher {
	t.Helper()
	return newBlockMatcher(newBlockMatcherTestDB(t).Queries, now)
}

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// matchTrip matches a single trip through the batched Match.
func matchTrip(ctx context.Context, matcher *blockMatcher, feedID, tripID string, reference time.Time, lookups *blockMatchLookups) (*BlockMatch, error) {
	matches, err := matcher.Match(ctx, feedID, []blockMatchRequest{{tripID: tripID, reference: reference}}, lookups)
	return matches[tripID], err
}

// countingDB counts the statements run through it.
type countingDB struct {
	gtfsdb.DBTX
	queries *atomic.Int64
}

func (db countingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.queries.Add(1)
	return db.DBTX.QueryContext(ctx, query, args...)
}

func (db countingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.queries.Add(1)
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

// newCountingBlockMatcher is newTestBlockMatcher with a count of the
// statements matching runs.
func newCountingBlockMatcher(t *testing.T, now func() time.Time) (*blockMatcher, *atomic.Int64) {
	t.Helper()
	var queries atomic.Int64
	db := countingDB{DBTX: newBlockMatcherTestDB(t).DB, queries: &queries}
	return newBlockMatcher(gtfsdb.New(db), now), &queries
}

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

	match, err := matchTrip(context.Background(), matcher, "feed", "T_LATE", reference, newBlockMatchLookups())

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
		{"block starting at midnight qualifies", "T_ZERO", tuesday0110, matcherMonday, "B3", 0, false},
		{"trip without block", "T_NOBLOCK", time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone), matcherTuesday, "T_NOBLOCK", 9 * time.Hour, false},
		{"later trip in shared block", "T_SECOND", time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone), matcherTuesday, "B5", 7 * time.Hour, false},
		{"unknown static trip", "T_MISSING", tuesday0110, time.Time{}, "", 0, true},
		{"epoch reference finds no service", "T_DAILY", time.Unix(0, 0), time.Time{}, "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := newTestBlockMatcher(t, fixedNow(tuesday0110))
			match, err := matchTrip(ctx, matcher, "feed", tt.tripID, tt.reference, newBlockMatchLookups())
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
		first, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0359, newBlockMatchLookups())
		require.NoError(t, err)
		require.NotNil(t, first)
		// At 04:01 the window is Tuesday only, where T_LATE does not run.
		second, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0401, newBlockMatchLookups())
		require.NoError(t, err)
		assert.Equal(t, first, second)
	})

	t.Run("unresolved result is cached until expiry", func(t *testing.T) {
		current := tuesday0401
		matcher := newTestBlockMatcher(t, func() time.Time { return current })
		miss, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0401, newBlockMatchLookups())
		require.NoError(t, err)
		require.Nil(t, miss)

		current = current.Add(29 * time.Minute)
		stillMiss, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0359, newBlockMatchLookups())
		require.NoError(t, err)
		assert.Nil(t, stillMiss, "cached failure is reused within 30 minutes")

		current = tuesday0401.Add(30 * time.Minute)
		rematched, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0359, newBlockMatchLookups())
		require.NoError(t, err)
		assert.NotNil(t, rematched, "expiry is measured from insertion, not extended by reads")
	})

	t.Run("static replacement clears the cache", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0401))
		miss, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0401, newBlockMatchLookups())
		require.NoError(t, err)
		require.Nil(t, miss)
		matcher.Clear()
		match, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0359, newBlockMatchLookups())
		require.NoError(t, err)
		assert.NotNil(t, match)
	})

	// ReloadStatic replaces the DB before it clears the cache, so a match
	// resolved from the old data can finish after the clear.
	t.Run("a result resolved before static replacement is not cached", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0401))
		late := []blockMatchRequest{{tripID: "T_LATE", reference: tuesday0401}}
		_, pending, generation := matcher.cachedMatches("feed", late)
		require.Len(t, pending, 1)
		matcher.Clear()
		matcher.store("feed", map[string]*BlockMatch{"T_LATE": nil}, generation)
		_, pending, _ = matcher.cachedMatches("feed", late)
		assert.Len(t, pending, 1)
	})

	t.Run("caches are per source", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(tuesday0401))
		miss, err := matchTrip(ctx, matcher, "feed-a", "T_LATE", tuesday0401, newBlockMatchLookups())
		require.NoError(t, err)
		require.Nil(t, miss)
		match, err := matchTrip(ctx, matcher, "feed-b", "T_LATE", tuesday0359, newBlockMatchLookups())
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
	_, err := matchTrip(canceled, matcher, "feed", "T_NOBLOCK", reference, newBlockMatchLookups())
	require.Error(t, err)

	match, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, newBlockMatchLookups())
	require.NoError(t, err)
	assert.NotNil(t, match, "a failed query must not leave a cached unresolved entry")
}

// A route whose agency row is missing cannot be matched, so the trip is
// cached as unresolved instead of failing and being retried every refresh.
func TestBlockMatcherTreatsMissingAgencyAsUnresolved(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)
	client := newBlockMatcherTestDB(t)
	_, err := client.DB.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "DELETE FROM agencies")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	matcher := newBlockMatcher(client.Queries, fixedNow(reference))

	match, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, newBlockMatchLookups())

	require.NoError(t, err)
	assert.Nil(t, match)
	_, pending, _ := matcher.cachedMatches("feed", []blockMatchRequest{{tripID: "T_NOBLOCK", reference: reference}})
	assert.Empty(t, pending, "the unresolved result is cached")
}

// One refresh resolves many trips with the same active services and agency
// zone, so the matcher reads them from the refresh's lookups once filled.
func TestBlockMatcherReusesRefreshLookups(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)

	t.Run("agency zone", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(reference))
		lookups := newBlockMatchLookups()
		lookups.agencyZones["40"] = time.UTC

		match, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, lookups)

		require.NoError(t, err)
		require.NotNil(t, match)
		assert.Equal(t, time.UTC, match.ServiceDate.Location())
	})

	t.Run("active service IDs", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(reference))
		lookups := newBlockMatchLookups()
		lookups.serviceIDs[matcherTuesday.Format("20060102")] = nil

		match, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, lookups)

		require.NoError(t, err)
		assert.Nil(t, match, "no service is active on the memoized date")
	})

	t.Run("lookups fill on first use", func(t *testing.T) {
		matcher := newTestBlockMatcher(t, fixedNow(reference))
		lookups := newBlockMatchLookups()

		_, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, lookups)

		require.NoError(t, err)
		assert.Contains(t, lookups.agencyZones, "40")
		assert.Contains(t, lookups.serviceIDs, matcherTuesday.Format("20060102"))
	})
}

func TestBlockMatcherFlagsFrequencyTrips(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)
	matcher := newTestBlockMatcher(t, fixedNow(reference))
	lookups := newBlockMatchLookups()

	headway, err := matchTrip(ctx, matcher, "feed", "T_HEADWAY", reference, lookups)
	require.NoError(t, err)
	require.NotNil(t, headway)
	assert.True(t, headway.FrequencyBased)

	scheduled, err := matchTrip(ctx, matcher, "feed", "T_NOBLOCK", reference, lookups)
	require.NoError(t, err)
	require.NotNil(t, scheduled)
	assert.False(t, scheduled.FrequencyBased)
}

// A refresh can name thousands of uncached trips, so they are resolved
// together: the statement count does not grow with the number of trips.
func TestBlockMatcherResolvesTripsTogether(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)
	tripIDs := []string{"T_NOBLOCK", "T_FIRST", "T_SECOND", "T_HEADWAY", "T_DAILY", "T_MISSING"}
	requests := make([]blockMatchRequest, 0, len(tripIDs))
	for _, tripID := range tripIDs {
		requests = append(requests, blockMatchRequest{tripID: tripID, reference: reference})
	}
	matcher, queries := newCountingBlockMatcher(t, fixedNow(reference))

	matches, err := matcher.Match(ctx, "feed", requests, newBlockMatchLookups())

	require.NoError(t, err)
	require.Len(t, matches, len(tripIDs))
	assert.Nil(t, matches["T_MISSING"])
	for _, tripID := range tripIDs[:5] {
		require.NotNil(t, matches[tripID], tripID)
		single, err := matchTrip(ctx, newTestBlockMatcher(t, fixedNow(reference)), "feed", tripID, reference, newBlockMatchLookups())
		require.NoError(t, err)
		assert.Equal(t, single, matches[tripID], "batched and single matches agree for %s", tripID)
	}
	// Trips, routes, agency, first departures, one service day, frequencies.
	assert.LessOrEqual(t, queries.Load(), int64(6))
}

func TestBlockMatcherMixesCachedAndNewTrips(t *testing.T) {
	ctx := context.Background()
	tuesday0359 := time.Date(2026, 10, 6, 3, 59, 0, 0, matcherZone)
	tuesday0401 := time.Date(2026, 10, 6, 4, 1, 0, 0, matcherZone)
	matcher := newTestBlockMatcher(t, fixedNow(tuesday0359))
	cached, err := matchTrip(ctx, matcher, "feed", "T_LATE", tuesday0359, newBlockMatchLookups())
	require.NoError(t, err)
	require.NotNil(t, cached)

	matches, err := matcher.Match(ctx, "feed", []blockMatchRequest{
		{tripID: "T_LATE", reference: tuesday0401},
		{tripID: "T_NOBLOCK", reference: tuesday0401},
	}, newBlockMatchLookups())

	require.NoError(t, err)
	assert.Equal(t, cached, matches["T_LATE"], "the cached association is reused, not re-resolved at 04:01")
	require.NotNil(t, matches["T_NOBLOCK"])
	assert.Equal(t, matcherTuesday, matches["T_NOBLOCK"].ServiceDate)
}

// A failed query fails the whole batch once, rather than once per trip,
// and still returns the matches the cache already held.
func TestBlockMatcherQueryFailureKeepsCachedMatches(t *testing.T) {
	ctx := context.Background()
	reference := time.Date(2026, 10, 6, 12, 0, 0, 0, matcherZone)
	matcher := newTestBlockMatcher(t, fixedNow(reference))
	cached, err := matchTrip(ctx, matcher, "feed", "T_FIRST", reference, newBlockMatchLookups())
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	matches, err := matcher.Match(canceled, "feed", []blockMatchRequest{
		{tripID: "T_FIRST", reference: reference},
		{tripID: "T_SECOND", reference: reference},
	}, newBlockMatchLookups())

	require.Error(t, err)
	assert.Equal(t, map[string]*BlockMatch{"T_FIRST": cached}, matches)
	_, pending, _ := matcher.cachedMatches("feed", []blockMatchRequest{{tripID: "T_SECOND", reference: reference}})
	assert.Len(t, pending, 1, "the failed trip is not cached")
}
