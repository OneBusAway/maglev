package restapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// situationLookupDB records query order and can fail either route lookup.
type situationLookupDB struct {
	gtfsdb.DBTX
	calls    []string
	args     [][]any
	failCall int
	failWith error
}

// QueryContext records bind arguments before optionally failing a numbered call,
// allowing tests to distinguish the initial and trip-completion route batches.
func (db *situationLookupDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.calls = append(db.calls, strings.SplitN(query, "\n", 2)[0])
	db.args = append(db.args, append([]any(nil), args...))
	if len(db.calls) == db.failCall {
		return nil, db.failWith
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

// TestSituationReferenceLookupFailures pins partial-map fallbacks, query order
// and log levels for direct and wrapped context errors at each stage.
func TestSituationReferenceLookupFailures(t *testing.T) {
	api := createTestApi(t)
	db := api.GtfsManager.GtfsDB
	original := db.Queries
	t.Cleanup(func() { db.Queries = original })
	var trip string
	require.NoError(t, db.DB.QueryRow("SELECT id FROM trips WHERE route_id = '151' ORDER BY id LIMIT 1").Scan(&trip))
	fallback, route, stop, unknown := "fallback", "missing", "1056", "25_missing"
	entity := gtfs.AlertInformedEntity{AgencyID: &fallback, RouteID: &route, StopID: &stop, TripID: &gtfs.TripID{ID: trip}}
	alerts := []gtfs.Alert{{ID: "first", InformedEntities: []gtfs.AlertInformedEntity{entity, entity, {RouteID: &unknown}}}, {ID: "second"}}
	wantCalls := []string{
		"-- name: ListAgencyIds :many", "-- name: GetRoutesByIDs :many",
		"-- name: GetAgenciesForStops :many", "-- name: GetTripsByIDs :many",
		"-- name: GetRoutesByIDs :many",
	}
	for _, failure := range []struct {
		name string
		err  error
	}{
		{"database", errors.New("injected situation lookup failure")},
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"wrapped-canceled", fmt.Errorf("lookup: %w", context.Canceled)},
		{"wrapped-deadline", fmt.Errorf("lookup: %w", context.DeadlineExceeded)},
	} {
		for _, stage := range []struct {
			failCall    int
			wantStop    string
			wantTrip    string
			wantUnknown string
			wantCalls   []string
		}{
			{failCall: 0, wantStop: "25_1056", wantTrip: "25_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls},
			{failCall: 1, wantStop: "25_1056", wantTrip: "25_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls},
			{failCall: 2, wantStop: "25_1056", wantTrip: "25_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls},
			{failCall: 3, wantStop: "fallback_1056", wantTrip: "25_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls},
			{failCall: 4, wantStop: "25_1056", wantTrip: "fallback_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls[:4]},
			{failCall: 5, wantStop: "25_1056", wantTrip: "fallback_" + trip, wantUnknown: "25_missing", wantCalls: wantCalls},
		} {
			t.Run(failure.name+"/"+strconv.Itoa(stage.failCall), func(t *testing.T) {
				probe := &situationLookupDB{DBTX: db.DB, failCall: stage.failCall, failWith: failure.err}
				db.Queries = gtfsdb.New(probe)
				var logs bytes.Buffer
				ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))
				got := api.BuildSituationReferences(ctx, feedAlertsForTest("", alerts))
				require.Len(t, got, 2)
				require.Equal(t, "first", got[0].ID)
				require.Equal(t, "second", got[1].ID)
				require.Len(t, got[0].AllAffects, 3, "repeated selectors must remain in order")
				require.Equal(t, got[0].AllAffects[0], got[0].AllAffects[1])
				affect := got[0].AllAffects[0]
				require.Equal(t, "fallback_missing", affect.RouteID)
				require.Equal(t, stage.wantStop, affect.StopID)
				require.Equal(t, stage.wantTrip, affect.TripID)
				require.Equal(t, stage.wantUnknown, got[0].AllAffects[2].RouteID)
				require.Equal(t, stage.wantCalls, probe.calls)
				require.Equal(t, []any{"25_missing", "missing"}, probe.args[1], "candidate IDs are sorted and deduplicated")
				switch {
				case stage.failCall == 0, errors.Is(failure.err, context.Canceled):
					require.Empty(t, logs.String())
				case errors.Is(failure.err, context.DeadlineExceeded):
					require.Contains(t, logs.String(), "context deadline exceeded")
					require.Equal(t, 1, strings.Count(logs.String(), "level=WARN"))
					require.NotContains(t, logs.String(), "level=ERROR")
				default:
					require.Contains(t, logs.String(), "injected situation lookup failure")
					require.Equal(t, 1, strings.Count(logs.String(), "level=ERROR"))
				}
			})
		}
	}
}

// TestSituationReferencesWithoutDatabase keeps nil receivers and empty alerts
// usable without changing empty-array or first-translation semantics.
func TestSituationReferencesWithoutDatabase(t *testing.T) {
	var api *RestAPI
	empty := api.BuildSituationReferences(context.Background(), nil)
	encoded, err := json.Marshal(empty)
	require.NoError(t, err)
	require.Equal(t, "[]", string(encoded))
	alerts := []gtfs.Alert{{
		ID: "empty", Effect: gtfs.UnknownEffect,
		Header: []gtfs.AlertText{{Text: ""}, {Text: "ignored"}},
	}}
	got := api.BuildSituationReferences(context.Background(), feedAlertsForTest("", alerts))
	require.Len(t, got, 1)
	require.NotNil(t, got[0].ActiveWindows)
	require.NotNil(t, got[0].AllAffects)
	require.NotNil(t, got[0].Consequences)
	require.NotNil(t, got[0].PublicationWindows)
	require.Nil(t, got[0].Summary, "only the first translation is considered")
	require.Equal(t, []models.ActiveWindow{}, got[0].ActiveWindows)
}

// TestSituationReferencesSharedStopAgency protects first-row selection when
// multiple database agencies own the same stop, regardless of selector agency.
func TestSituationReferencesSharedStopAgency(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "raba.zip"))
	db := api.GtfsManager.GtfsDB.DB
	for _, query := range []string{
		`INSERT INTO agencies (id, name, url, timezone) VALUES ('00', 'Other agency', 'https://example.com', 'UTC')`,
		`INSERT INTO routes (id, agency_id, type) VALUES ('audit-route', '00', 3)`,
		`INSERT INTO trips (id, route_id, service_id) SELECT 'audit-trip', 'audit-route', service_id FROM trips WHERE route_id = '151' LIMIT 1`,
		`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) VALUES ('audit-trip', 0, 0, '1056', 0)`,
	} {
		_, err := db.Exec(query)
		require.NoError(t, err)
	}
	ctx := context.Background()
	rows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, []string{"1056"})
	require.NoError(t, err)
	require.Len(t, rows, 2, "fixture must serve the same stop from both agencies")
	require.Equal(t, "00", rows[0].ID)
	require.Equal(t, "25", rows[1].ID)
	agency, stop := "25", "1056"
	got := api.BuildSituationReferences(ctx, feedAlertsForTest("", []gtfs.Alert{{InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency, StopID: &stop}}}}))
	require.Len(t, got, 1)
	require.Len(t, got[0].AllAffects, 1)
	require.Equal(t, "00_1056", got[0].AllAffects[0].StopID, "the first database agency wins over later rows and the selector")
}

// TestSituationReferencesWindowsAndTranslations pins open window bounds,
// millisecond units and the independent representation of translation fields.
func TestSituationReferencesWindowsAndTranslations(t *testing.T) {
	api := &RestAPI{}
	zero, start, end := time.Unix(0, 0), time.Unix(2, 0), time.Unix(3, 0)
	got := api.BuildSituationReferences(context.Background(), feedAlertsForTest("", []gtfs.Alert{{
		ActivePeriods: []gtfs.AlertActivePeriod{{}, {StartsAt: &zero}, {EndsAt: &end}, {StartsAt: &start, EndsAt: &end}},
		Header:        []gtfs.AlertText{{Text: "First", Language: "en"}, {Text: "Second", Language: "fr"}},
		Description:   []gtfs.AlertText{{Text: ""}, {Text: "Ignored"}},
		URL:           []gtfs.AlertText{{Text: "https://example.com", Language: "en"}},
	}}))
	require.Len(t, got, 1)
	require.Equal(t, []models.ActiveWindow{{}, {}, {To: 3000}, {From: 2000, To: 3000}}, got[0].ActiveWindows)
	require.Equal(t, &models.TranslatedString{Value: "First", Lang: "en"}, got[0].Summary)
	require.Nil(t, got[0].Description)
	require.Equal(t, &models.TranslatedString{Value: "https://example.com", Lang: "en"}, got[0].URL)
}

// TestSituationReferencesLaterBatchFailure rejects partial agency maps when a
// later query batch fails after an earlier batch returned a real route.
func TestSituationReferencesLaterBatchFailure(t *testing.T) {
	api := createTestApi(t)
	db := api.GtfsManager.GtfsDB
	original := db.Queries
	t.Cleanup(func() { db.Queries = original })
	fallback := "fallback"
	entities := make([]gtfs.AlertInformedEntity, utils.IDsPerBatchedQuery+1)
	for i := range entities {
		id := fmt.Sprintf("z%04d", i)
		if i == 0 {
			id = "151"
		}
		entities[i] = gtfs.AlertInformedEntity{AgencyID: &fallback, RouteID: &id}
	}
	for _, failCall := range []int{0, 3} {
		t.Run(strconv.Itoa(failCall), func(t *testing.T) {
			probe := &situationLookupDB{DBTX: db.DB, failCall: failCall, failWith: errors.New("second route batch failed")}
			db.Queries = gtfsdb.New(probe)
			got := api.BuildSituationReferences(context.Background(), feedAlertsForTest("", []gtfs.Alert{{InformedEntities: entities}}))
			require.Len(t, got, 1)
			require.Len(t, got[0].AllAffects, len(entities))
			require.Len(t, probe.calls, 3)
			require.Len(t, probe.args[1], utils.IDsPerBatchedQuery)
			require.Len(t, probe.args[2], 1)
			want := "25_151"
			if failCall != 0 {
				want = "fallback_151"
			}
			require.Equal(t, want, got[0].AllAffects[0].RouteID, "a later failure discards results from earlier batches")
		})
	}
}

// TestSituationReferenceTranslationFields checks all three field mappings through
// the public builder, including the rule that later translations are ignored.
func TestSituationReferenceTranslationFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		texts []gtfs.AlertText
		want  *models.TranslatedString
	}{
		{name: "nil"},
		{name: "empty", texts: []gtfs.AlertText{}},
		{name: "empty-first", texts: []gtfs.AlertText{{Text: "", Language: "en"}, {Text: "Later", Language: "fr"}}},
		{name: "first", texts: []gtfs.AlertText{{Text: "First", Language: "en"}, {Text: "Later", Language: "fr"}}, want: &models.TranslatedString{Value: "First", Lang: "en"}},
		{name: "no-language", texts: []gtfs.AlertText{{Text: "Text"}}, want: &models.TranslatedString{Value: "Text"}},
		{name: "whitespace", texts: []gtfs.AlertText{{Text: " ", Language: "en"}}, want: &models.TranslatedString{Value: " ", Lang: "en"}},
		{name: "unicode", texts: []gtfs.AlertText{{Text: "東京", Language: "ja"}}, want: &models.TranslatedString{Value: "東京", Lang: "ja"}},
	} {
		for _, field := range []string{"summary", "description", "url"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				var alert gtfs.Alert
				var want models.Situation
				switch field {
				case "summary":
					alert.Header, want.Summary = tc.texts, tc.want
				case "description":
					alert.Description, want.Description = tc.texts, tc.want
				case "url":
					alert.URL, want.URL = tc.texts, tc.want
				}
				api := &RestAPI{}
				got := api.BuildSituationReferences(context.Background(), feedAlertsForTest("", []gtfs.Alert{alert}))
				require.Len(t, got, 1)
				require.Equal(t, want.Summary, got[0].Summary)
				require.Equal(t, want.Description, got[0].Description)
				require.Equal(t, want.URL, got[0].URL)
			})
		}
	}
}
