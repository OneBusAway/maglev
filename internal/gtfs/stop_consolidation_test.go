package gtfs

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/consolidation"
	"maglev.onebusaway.org/internal/models"
)

// RABA stops 5008 and 5027 are both "Hartnell Ave at Bechelli Ln", 12 m
// apart. No feed has 25_gone, so 3022 is removed with its stop times, as
// Java does when a canonical stop is missing.
const rabaConsolidation = "25_5008 25_5027\n25_gone 25_3022\n"

func newConsolidationManager(t *testing.T, consolidationURL string) (*Manager, error) {
	t.Helper()
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:              models.GetFixturePath(t, "raba.zip"),
		StopConsolidationURL: consolidationURL,
		GTFSDataPath:         filepath.Join(t.TempDir(), "gtfs.db"),
		Env:                  appconf.Development,
	})
	if manager != nil {
		t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	}
	return manager, err
}

// writeConsolidationFile writes content to a new file and returns its path.
func writeConsolidationFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "StopConsolidation.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func stopTimeCount(t *testing.T, manager *Manager, stopID string) int {
	t.Helper()
	var count int
	require.NoError(t, manager.GtfsDB.DB.QueryRow(`SELECT COUNT(*) FROM stop_times WHERE stop_id = ?`, stopID).Scan(&count))
	return count
}

func TestStopConsolidation_AppliedOnImport(t *testing.T) {
	manager, err := newConsolidationManager(t, writeConsolidationFile(t, rabaConsolidation))
	require.NoError(t, err)

	ctx := context.Background()
	for _, absorbed := range []string{"5027", "3022"} {
		_, err := manager.GtfsDB.Queries.GetStop(ctx, absorbed)
		assert.ErrorIs(t, err, sql.ErrNoRows, "stop %s should be removed", absorbed)
		assert.Zero(t, stopTimeCount(t, manager, absorbed))
	}
	assert.Equal(t, 69, stopTimeCount(t, manager, "5008"), "5027's 35 stop times join 5008's 34")

	rows, err := manager.GtfsDB.Queries.ListStopConsolidations(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "3022", rows[0].AbsorbedID)
	assert.False(t, rows[0].CanonicalID.Valid)
	assert.Equal(t, "25_gone", rows[0].RuleCanonical)
	assert.Equal(t, "5027", rows[1].AbsorbedID)
	assert.Equal(t, "5008", rows[1].CanonicalID.String)
}

func TestStopConsolidation_ChangedFileCausesReimport(t *testing.T) {
	path := writeConsolidationFile(t, rabaConsolidation)
	manager, err := newConsolidationManager(t, path)
	require.NoError(t, err)

	changed, err := manager.ReloadStatic(context.Background())
	require.NoError(t, err)
	assert.False(t, changed, "an unchanged feed and file are not imported again")

	require.NoError(t, os.WriteFile(path, []byte("25_5008 25_5027\n"), 0o600))
	changed, err = manager.ReloadStatic(context.Background())
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, 20, stopTimeCount(t, manager, "3022"), "3022 is back once its line is gone")
}

func TestStopConsolidation_FetchedOverHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/StopConsolidation.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(rabaConsolidation))
	}))
	t.Cleanup(server.Close)

	manager, err := newConsolidationManager(t, server.URL+"/StopConsolidation.txt")
	require.NoError(t, err)
	assert.Zero(t, stopTimeCount(t, manager, "5027"))

	_, err = newConsolidationManager(t, server.URL+"/missing.txt")
	assert.ErrorContains(t, err, "404")
}

func TestStopConsolidation_MalformedFileFailsImport(t *testing.T) {
	_, err := newConsolidationManager(t, writeConsolidationFile(t, "25_5008 5027\n"))

	assert.ErrorContains(t, err, "line 1")
}

func TestStopConsolidation_WithHash(t *testing.T) {
	var none *stopConsolidation
	assert.Equal(t, "abc", none.withHash("abc"))
	assert.Equal(t, "abc+consolidation-def", (&stopConsolidation{hash: "def"}).withHash("abc"))
}

func TestStopConsolidation_OversizedFileFailsImport(t *testing.T) {
	oversized := strings.Repeat("# padding\n", maxStopConsolidationBytes/10+1)

	_, err := newConsolidationManager(t, writeConsolidationFile(t, oversized))

	assert.ErrorContains(t, err, "size limit")
}

func TestStopConsolidation_NotAppliedToFeedWithoutAgencies(t *testing.T) {
	file, err := consolidation.Parse(strings.NewReader(rabaConsolidation))
	require.NoError(t, err)
	data := &gtfsdb.GtfsData{Static: &gtfs.Static{}}

	(&stopConsolidation{file: file}).apply(data, slog.Default())

	assert.Nil(t, data.StopReplacements)
}
