package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

// rabaStopTimes is the number of stop times RABA imports.
const rabaStopTimes = 7346

func rabaContentHash(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(models.GetFixturePath(t, "raba.zip"))
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestFetchFeedFileLocal(t *testing.T) {
	dbDir := t.TempDir()
	path := models.GetFixturePath(t, "raba.zip")
	feed, err := fetchFeedFile(context.Background(), Config{GtfsURL: path, GTFSDataPath: filepath.Join(dbDir, "gtfs.db")})
	require.NoError(t, err)
	defer feed.close()

	assert.Equal(t, dbDir, filepath.Dir(feed.path), "a local feed is copied next to the database")
	assert.Equal(t, rabaContentHash(t), feed.contentHash)
}

func TestFetchFeedFileDownload(t *testing.T) {
	raba, err := os.ReadFile(models.GetFixturePath(t, "raba.zip"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(raba)
	}))
	t.Cleanup(server.Close)

	dbDir := t.TempDir()
	config := Config{
		GtfsURL:               server.URL + "/gtfs.zip",
		GTFSDataPath:          filepath.Join(dbDir, "gtfs.db"),
		StaticAuthHeaderKey:   "X-Api-Key",
		StaticAuthHeaderValue: "secret",
	}
	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)

	assert.Equal(t, dbDir, filepath.Dir(feed.path), "the download goes next to the database")
	assert.Equal(t, rabaContentHash(t), feed.contentHash)
	feed.close()
	_, err = os.Stat(feed.path)
	assert.True(t, os.IsNotExist(err), "close removes the download")

	config.StaticAuthHeaderValue = "wrong"
	_, err = fetchFeedFile(context.Background(), config)
	assert.ErrorContains(t, err, "403")
	entries, err := os.ReadDir(dbDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a failed download leaves no file behind")
}

func TestFeedHashIncludesGTFSTidy(t *testing.T) {
	assert.Equal(t, "abc", feedHash("abc", Config{}))
	assert.Equal(t, "abc+gtfstidy", feedHash("abc", Config{EnableGTFSTidy: true}))
}

func TestFetchFeedFileTruncatedDownloadLeavesNoFile(t *testing.T) {
	const promisedBytes, sentBytes = 1024, 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(promisedBytes))
		_, _ = w.Write(make([]byte, sentBytes))
	}))
	t.Cleanup(server.Close)

	dbDir := t.TempDir()
	_, err := fetchFeedFile(context.Background(), Config{GtfsURL: server.URL, GTFSDataPath: filepath.Join(dbDir, "gtfs.db")})
	assert.ErrorContains(t, err, "error reading GTFS data")

	entries, err := os.ReadDir(dbDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a truncated download leaves no file behind")
}

func TestFetchFeedFileServerUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	_, err := fetchFeedFile(context.Background(), Config{GtfsURL: server.URL, GTFSDataPath: filepath.Join(t.TempDir(), "gtfs.db")})
	assert.ErrorContains(t, err, "error downloading GTFS data")
}

func TestDownloadDir(t *testing.T) {
	tests := []struct {
		name, dataPath, want string
	}{
		{"no database path", "", ""},
		{"in-memory database", ":memory:", ""},
		{"database file", "/data/gtfs.db", "/data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, downloadDir(Config{GTFSDataPath: tt.dataPath}))
		})
	}
}

// installFakeGTFSTidy puts a gtfstidy on PATH that copies its input to its
// output, so tidying succeeds without the real binary.
func installFakeGTFSTidy(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gtfstidy is a shell script")
	}
	dir := t.TempDir()
	// tidyGTFSData runs: gtfstidy <flags> -o <output> <input>
	script := "#!/bin/sh\n/bin/cp \"$4\" \"$3\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtfstidy"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
}

// TestParseHashMarksTidyWhenItRan checks that a feed whose tidy succeeded is
// stored under the tidied hash that the next reload looks for.
func TestParseHashMarksTidyWhenItRan(t *testing.T) {
	installFakeGTFSTidy(t)
	config := Config{
		GtfsURL:        models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath:   filepath.Join(t.TempDir(), "gtfs.db"),
		EnableGTFSTidy: true,
	}

	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)
	defer feed.close()

	data, err := feed.parse(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, rabaContentHash(t)+"+gtfstidy", data.Hash)
	assert.Equal(t, feed.importHash(config), data.Hash, "the stored hash matches the one the next reload checks")
}

// TestReloadStaticSkipsParseForImportedFeed checks that reloading a feed the
// database already holds doesn't parse it again.
func TestReloadStaticSkipsParseForImportedFeed(t *testing.T) {
	counter := countLogMessages(t)

	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: filepath.Join(t.TempDir(), "gtfs.db"),
		Env:          appconf.Development,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	require.Equal(t, 1, counter.count("parsing_gtfs_data"))

	changed, err := manager.ReloadStatic(context.Background())
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 1, counter.count("parsing_gtfs_data"), "an unchanged feed must not be parsed again")
}

func TestFetchFeedFileRemovesStaleDownloads(t *testing.T) {
	raba, err := os.ReadFile(models.GetFixturePath(t, "raba.zip"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raba)
	}))
	t.Cleanup(server.Close)

	tests := []struct {
		name             string
		gtfsURL          string
		inMemoryDB       bool
		wantStaleRemoved bool
	}{
		{name: "download", gtfsURL: server.URL, wantStaleRemoved: true},
		{name: "local feed", gtfsURL: models.GetFixturePath(t, "raba.zip"), wantStaleRemoved: true},
		{name: "in-memory database leaves the shared temp dir alone", gtfsURL: server.URL, inMemoryDB: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			stale := filepath.Join(dir, "gtfs-static-123.zip")
			require.NoError(t, os.WriteFile(stale, []byte("left by a crash"), 0o600))
			dataPath := filepath.Join(dir, "gtfs.db")
			if tt.inMemoryDB {
				t.Setenv("TMPDIR", dir)
				dataPath = ":memory:"
			}

			feed, err := fetchFeedFile(context.Background(), Config{GtfsURL: tt.gtfsURL, GTFSDataPath: dataPath})
			require.NoError(t, err)
			defer feed.close()
			_, err = os.Stat(stale)
			assert.Equal(t, tt.wantStaleRemoved, os.IsNotExist(err))
		})
	}
}

func TestFetchFeedFileLocalErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name, gtfsURL, dataPath, wantErr string
	}{
		{
			name:     "missing feed",
			gtfsURL:  filepath.Join(dir, "missing.zip"),
			dataPath: filepath.Join(dir, "gtfs.db"),
			wantErr:  "error reading local GTFS file",
		},
		{
			name:     "missing database directory",
			gtfsURL:  models.GetFixturePath(t, "raba.zip"),
			dataPath: filepath.Join(dir, "missing", "gtfs.db"),
			wantErr:  "error reading local GTFS file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fetchFeedFile(context.Background(), Config{GtfsURL: tt.gtfsURL, GTFSDataPath: tt.dataPath})
			assert.ErrorContains(t, err, tt.wantErr)

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Empty(t, entries, "a failed copy leaves nothing behind")
		})
	}
}

// newMemoryDB returns an empty in-memory database to import into.
func newMemoryDB(t *testing.T) *gtfsdb.Client {
	t.Helper()
	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestParseStreamsTidiedFeed checks that a tidied feed is streamed from
// memory, so the import doesn't need the feed copy once it is parsed.
func TestParseStreamsTidiedFeed(t *testing.T) {
	installFakeGTFSTidy(t)
	config := Config{
		GtfsURL:        models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath:   filepath.Join(t.TempDir(), "gtfs.db"),
		EnableGTFSTidy: true,
	}
	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)

	data, err := feed.parse(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(data.Hash, "+gtfstidy"))

	feed.close()
	_, err = newMemoryDB(t).StoreGtfsData(context.Background(), data)
	require.NoError(t, err)
}

// TestParseHashMarksTidyOnlyWhenItRan checks that a feed whose tidy failed is
// stored under its plain hash, so the next reload doesn't match the tidied
// hash and tries again.
func TestParseHashMarksTidyOnlyWhenItRan(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no gtfstidy binary, so tidying fails
	path := models.GetFixturePath(t, "raba.zip")
	config := Config{GtfsURL: path, GTFSDataPath: filepath.Join(t.TempDir(), "gtfs.db"), EnableGTFSTidy: true}

	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)
	defer feed.close()
	assert.Equal(t, rabaContentHash(t)+"+gtfstidy", feed.importHash(config), "the check looks for a tidied import")

	data, err := feed.parse(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, rabaContentHash(t), data.Hash, "an untidied import is stored under the plain hash")
}

// TestParsedFeedFileIsClosedAndRemoved checks that the import streams from the
// copy it already holds open, and that close closes and removes it.
func TestParsedFeedFileIsClosedAndRemoved(t *testing.T) {
	config := Config{GtfsURL: models.GetFixturePath(t, "raba.zip"), GTFSDataPath: filepath.Join(t.TempDir(), "gtfs.db")}
	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)

	// Removing the copy by name doesn't stop the import reading it.
	require.NoError(t, os.Remove(feed.path))
	data, err := feed.parse(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, rabaContentHash(t), data.Hash)
	client := newMemoryDB(t)
	_, err = client.StoreGtfsData(context.Background(), data)
	require.NoError(t, err)
	counts, err := client.TableCounts()
	require.NoError(t, err)
	assert.Equal(t, rabaStopTimes, counts["stop_times"])

	feed.close()
	_, err = feed.open.Stat()
	assert.ErrorIs(t, err, os.ErrClosed)
	_, err = os.Stat(feed.path)
	assert.True(t, os.IsNotExist(err))
}
