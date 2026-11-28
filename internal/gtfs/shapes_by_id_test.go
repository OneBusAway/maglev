package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/utils"
)

// Shapes in the synthetic feed: a three-point shape, a one-point shape and a
// two-point shape. Nothing has shape unknownShapeID.
const (
	threePointShapeID = "S3"
	onePointShapeID   = "S1"
	twoPointShapeID   = "S2"
	unknownShapeID    = "NO_SUCH_SHAPE"
)

const shapeFeedShapesTxt = "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\n" +
	"S3,47.600,-122.330,1\n" +
	"S3,47.610,-122.330,2\n" +
	"S3,47.610,-122.340,3\n" +
	"S1,47.620,-122.350,1\n" +
	"S2,47.630,-122.360,1\n" +
	"S2,47.640,-122.360,2\n"

// writeShapeFeedZip writes a minimal GTFS zip whose shapes.txt is shapesTxt.
func writeShapeFeedZip(t *testing.T, zipPath, shapesTxt string) {
	t.Helper()
	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"TEST,Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"R1,TEST,1,Route 1,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"SV,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"A,Stop A,47.600,-122.330\n" +
			"B,Stop B,47.610,-122.340\n",
		"trips.txt": "route_id,service_id,trip_id,shape_id\n" +
			"R1,SV,T3,S3\n" +
			"R1,SV,T1,S1\n" +
			"R1,SV,T2,S2\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"T3,08:00:00,08:00:00,A,1\nT3,08:10:00,08:10:00,B,2\n" +
			"T1,09:00:00,09:00:00,A,1\nT1,09:10:00,09:10:00,B,2\n" +
			"T2,10:00:00,10:00:00,A,1\nT2,10:10:00,10:10:00,B,2\n",
		"shapes.txt": shapesTxt,
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))
}

// newShapeCacheTestManager loads the synthetic shape feed and returns the
// manager, the zip path it reloads from, and a recorder of its queries.
func newShapeCacheTestManager(t *testing.T) (*Manager, string, *recordingDB) {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "feed.zip")
	writeShapeFeedZip(t, zipPath, shapeFeedShapesTxt)
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      zipPath,
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager, zipPath, swapInRecordingDB(t, manager)
}

var queryNamePattern = regexp.MustCompile(`-- name: (\w+)`)

// recordingDB records each query's sqlc name and arguments, and fails every
// query named failQuery.
type recordingDB struct {
	gtfsdb.DBTX
	mu        sync.Mutex
	calls     map[string]int
	args      map[string][]any
	failQuery string
}

var errInjectedQuery = errors.New("injected query failure")

func swapInRecordingDB(t *testing.T, manager *Manager) *recordingDB {
	t.Helper()
	recorder := &recordingDB{DBTX: manager.GtfsDB.DB}
	recorder.reset()
	original := manager.GtfsDB.Queries
	manager.GtfsDB.Queries = gtfsdb.New(recorder)
	t.Cleanup(func() { manager.GtfsDB.Queries = original })
	return recorder
}

func (db *recordingDB) reset() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.calls = map[string]int{}
	db.args = map[string][]any{}
}

func (db *recordingDB) record(query string, args []any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	name := query
	if match := queryNamePattern.FindStringSubmatch(query); match != nil {
		name = match[1]
	}
	db.calls[name]++
	db.args[name] = append(db.args[name], args...)
	if name == db.failQuery {
		return errInjectedQuery
	}
	return nil
}

func (db *recordingDB) callsTo(name string) int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.calls[name]
}

func (db *recordingDB) argsTo(name string) []any {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]any(nil), db.args[name]...)
}

func (db *recordingDB) failOn(name string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.failQuery = name
}

func (db *recordingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := db.record(query, args); err != nil {
		return nil, err
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

func (db *recordingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	_ = db.record(query, args)
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

const shapePointsQuery = "GetShapePointsByIDs"

func TestShapesByIDLoadsOnceThenServesFromCache(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	first, err := manager.ShapesByID(ctx, []string{threePointShapeID})
	require.NoError(t, err)
	require.Equal(t, 1, recorder.callsTo(shapePointsQuery))

	second, err := manager.ShapesByID(ctx, []string{threePointShapeID})
	require.NoError(t, err)
	assert.Equal(t, 1, recorder.callsTo(shapePointsQuery), "the second lookup is served from the cache")
	assert.Equal(t, first, second)

	shape := second[threePointShapeID]
	require.Len(t, shape.Points, 3)
	assert.Equal(t, 47.61, shape.Points[2].Latitude)
	assert.Equal(t, -122.34, shape.Points[2].Longitude)
	assert.Equal(t, utils.CumulativeShapeDistances(shape.Points), shape.CumulativeDistances)
	assert.Equal(t, shape.CumulativeDistances[2], shape.TotalDistance)
	assert.Equal(t, len(shape.Points), cap(shape.Points), "shared points must not leave room for an append")
	assert.Equal(t, len(shape.CumulativeDistances), cap(shape.CumulativeDistances))
}

func TestShapesByIDQueriesOnlyMissingShapes(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	_, err := manager.ShapesByID(ctx, []string{threePointShapeID})
	require.NoError(t, err)
	recorder.reset()

	shapes, err := manager.ShapesByID(ctx, []string{threePointShapeID, twoPointShapeID})
	require.NoError(t, err)
	assert.Len(t, shapes, 2)
	assert.Equal(t, 1, recorder.callsTo(shapePointsQuery))
	assert.Equal(t, []any{twoPointShapeID}, recorder.argsTo(shapePointsQuery))
}

func TestShapesByIDShapesWithFewPoints(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	shapes, err := manager.ShapesByID(ctx, []string{onePointShapeID, unknownShapeID})
	require.NoError(t, err)

	require.Contains(t, shapes, onePointShapeID)
	onePoint := shapes[onePointShapeID]
	assert.Len(t, onePoint.Points, 1)
	assert.Nil(t, onePoint.CumulativeDistances, "distances need at least two points")
	assert.Zero(t, onePoint.TotalDistance)
	assert.NotContains(t, shapes, unknownShapeID, "a shape with no points is omitted")

	again, err := manager.ShapesByID(ctx, []string{onePointShapeID, unknownShapeID})
	require.NoError(t, err)
	assert.Equal(t, shapes, again)
	assert.Equal(t, 1, recorder.callsTo(shapePointsQuery), "an unknown shape is remembered as empty")
}

func TestShapesByIDIgnoresDuplicateAndEmptyIDs(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)

	shapes, err := manager.ShapesByID(context.Background(), []string{twoPointShapeID, twoPointShapeID, ""})
	require.NoError(t, err)
	assert.Len(t, shapes, 1)
	assert.Equal(t, []any{twoPointShapeID}, recorder.argsTo(shapePointsQuery))
}

func TestShapesByIDEmptyInputQueriesNothing(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)

	shapes, err := manager.ShapesByID(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, shapes)
	assert.Zero(t, recorder.callsTo(shapePointsQuery))
}

func TestShapesByIDDoesNotCacheErrors(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	recorder.failOn(shapePointsQuery)
	_, err := manager.ShapesByID(ctx, []string{threePointShapeID})
	require.ErrorIs(t, err, errInjectedQuery)

	recorder.failOn("")
	shapes, err := manager.ShapesByID(ctx, []string{threePointShapeID})
	require.NoError(t, err)
	assert.Len(t, shapes[threePointShapeID].Points, 3)
	assert.Equal(t, 2, recorder.callsTo(shapePointsQuery), "the failed lookup left nothing cached")
}

func TestShapesByIDBatchesLargeRequests(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)

	shapeIDs := []string{threePointShapeID}
	for i := range utils.IDsPerBatchedQuery {
		shapeIDs = append(shapeIDs, fmt.Sprintf("missing-%d", i))
	}
	shapes, err := manager.ShapesByID(context.Background(), shapeIDs)
	require.NoError(t, err)
	assert.Len(t, shapes, 1)
	assert.Equal(t, 2, recorder.callsTo(shapePointsQuery))
}

func TestShapesByIDWorksOnZeroValueManager(t *testing.T) {
	loaded, _, _ := newShapeCacheTestManager(t)
	manager := &Manager{GtfsDB: loaded.GtfsDB}

	shapes, err := manager.ShapesByID(context.Background(), []string{twoPointShapeID})
	require.NoError(t, err)
	assert.Len(t, shapes[twoPointShapeID].Points, 2)
}

func TestResetStaticCachesDropsShapes(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	_, err := manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	manager.ResetStaticCaches()
	_, err = manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	assert.Equal(t, 2, recorder.callsTo(shapePointsQuery))
}

func TestShapesByIDAfterChangedReloadServesNewShapes(t *testing.T) {
	manager, zipPath, _ := newShapeCacheTestManager(t)
	ctx := context.Background()

	before, err := manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	require.Equal(t, 47.64, before[twoPointShapeID].Points[1].Latitude)

	movedShapes := regexp.MustCompile(`S2,47.640,`).ReplaceAllString(shapeFeedShapesTxt, "S2,47.700,")
	writeShapeFeedZip(t, zipPath, movedShapes)
	changed, err := manager.ReloadStatic(ctx)
	require.NoError(t, err)
	require.True(t, changed)

	after, err := manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	assert.Equal(t, 47.7, after[twoPointShapeID].Points[1].Latitude, "the reloaded feed's geometry is served")
}

func TestShapesByIDAfterUnchangedReloadKeepsCache(t *testing.T) {
	manager, _, recorder := newShapeCacheTestManager(t)
	ctx := context.Background()

	_, err := manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	changed, err := manager.ReloadStatic(ctx)
	require.NoError(t, err)
	require.False(t, changed)

	_, err = manager.ShapesByID(ctx, []string{twoPointShapeID})
	require.NoError(t, err)
	assert.Equal(t, 1, recorder.callsTo(shapePointsQuery))
}

func TestShapeCacheRetiredGenerationDropsLateStores(t *testing.T) {
	var cache shapeCache
	retired := cache.generationFor(1)
	current := cache.generationFor(2)
	require.NotSame(t, retired, current)

	retired.store("late", CachedShape{})
	_, ok := cache.generationFor(2).load("late")
	assert.False(t, ok, "a fill that began before the import must not reach the new generation")
}

func TestShapesByIDConcurrentReaders(t *testing.T) {
	manager, _, _ := newShapeCacheTestManager(t)
	want, err := manager.ShapesByID(context.Background(), []string{threePointShapeID, twoPointShapeID})
	require.NoError(t, err)
	manager.ResetStaticCaches()

	const readers = 32
	results := make([]map[string]CachedShape, readers)
	errs := make([]error, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() {
			results[i], errs[i] = manager.ShapesByID(context.Background(), []string{threePointShapeID, twoPointShapeID})
		})
	}
	wg.Wait()
	for i := range readers {
		require.NoError(t, errs[i])
		assert.Equal(t, want, results[i])
	}
}
