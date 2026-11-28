package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

// queryCountingDB counts the queries sqlc issues so a benchmark can report
// database round trips per request alongside time and allocations. Queries
// that bypass the swapped-in Queries (raw GtfsDB.DB access, or a component
// holding the original *Queries) are not counted. It also counts queries by
// their sqlc name, and fails every query named failQuery when one is set.
type queryCountingDB struct {
	gtfsdb.DBTX
	count atomic.Int64

	mu        sync.Mutex
	byName    map[string]int64
	failQuery string
}

var sqlcQueryName = regexp.MustCompile(`-- name: (\w+)`)

var errInjectedQuery = errors.New("injected query failure")

// record counts query and reports whether it should fail.
func (db *queryCountingDB) record(query string) bool {
	db.count.Add(1)
	name := query
	if match := sqlcQueryName.FindStringSubmatch(query); match != nil {
		name = match[1]
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.byName == nil {
		db.byName = map[string]int64{}
	}
	db.byName[name]++
	return name == db.failQuery
}

// countOf returns how many times the query with this sqlc name has run.
func (db *queryCountingDB) countOf(name string) int64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.byName[name]
}

// failOn makes every later query with this sqlc name fail.
func (db *queryCountingDB) failOn(name string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.failQuery = name
}

func (db *queryCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if db.record(query) {
		return nil, errInjectedQuery
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

func (db *queryCountingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.record(query)
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

// Trips running at arrivalsTestClock (Friday 2025-06-13 11:00 Pacific),
// chosen to span the fixture's trip and block sizes.
const (
	benchTripFewStops   = "t_404_b_18260_tn_0"                   // 10 stops, 6-trip block
	benchTripManyStops  = "6a3ef1d9-13d7-488b-8528-633ce9bbeb6d" // 38 stops, 17-trip block
	benchTripLongBlock  = "3a61ae4c-c8af-4833-8540-ef9cc3782d1c" // 22 stops, 48-trip block
	benchTripVehicleID  = "bench_vehicle"
	benchTripDetailsKey = "?key=org.onebusaway.iphone" // exempt from the test rate limit
)

// BenchmarkTripDetailsMatrix measures trip-details across trip sizes and
// include* combinations at a time when the trips are scheduled to run, so the
// status, block and schedule paths all execute. queries/op counts sqlc calls.
func BenchmarkTripDetailsMatrix(b *testing.B) {
	api := createTestApiWithClock(b, clock.NewMockClock(arrivalsTestClock))
	defer api.Shutdown()

	agencyID := mustGetAgencies(b, api)[0].ID
	serviceDate := "&serviceDate=" + arrivalsTestClock.Format(serviceDateLayout)

	counter := swapInQueryCounter(b, api)

	mux := http.NewServeMux()
	api.SetRoutes(mux)

	cases := []struct {
		name        string
		tripID      string
		query       string
		withVehicle bool
	}{
		{name: "FewStops/Default", tripID: benchTripFewStops},
		{name: "ManyStops/Default", tripID: benchTripManyStops},
		{name: "LongBlock/Default", tripID: benchTripLongBlock},
		{name: "ManyStops/Vehicle", tripID: benchTripManyStops, withVehicle: true},
		{name: "ManyStops/NoSchedule", tripID: benchTripManyStops, query: "&includeSchedule=false"},
		{name: "ManyStops/NoStatus", tripID: benchTripManyStops, query: "&includeStatus=false"},
		{name: "ManyStops/NoReferences", tripID: benchTripManyStops, query: "&includeReferences=false"},
		{name: "ManyStops/ServiceDate", tripID: benchTripManyStops, query: serviceDate},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			if tc.withVehicle {
				addTestVehicle(b, api, tc.tripID)
			}
			endpoint := "/api/where/trip-details/" + utils.FormCombinedID(agencyID, tc.tripID) + ".json" + benchTripDetailsKey + tc.query
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)

			warmup := httptest.NewRecorder()
			mux.ServeHTTP(warmup, req)
			if warmup.Code != http.StatusOK {
				b.Fatalf("expected 200, got %d: %s", warmup.Code, warmup.Body.String())
			}

			counter.count.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				mux.ServeHTTP(httptest.NewRecorder(), req)
			}
			b.ReportMetric(float64(counter.count.Load())/float64(b.N), "queries/op")
		})
	}
}
