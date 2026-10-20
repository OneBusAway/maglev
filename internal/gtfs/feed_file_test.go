package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func rabaContentHash(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(models.GetFixturePath(t, "raba.zip"))
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestFetchFeedFileLocal(t *testing.T) {
	path := models.GetFixturePath(t, "raba.zip")
	feed, err := fetchFeedFile(context.Background(), Config{GtfsURL: path})
	require.NoError(t, err)
	defer feed.cleanup()

	assert.Equal(t, path, feed.path, "a local feed is read where it is")
	assert.Equal(t, rabaContentHash(t), feed.hash)
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
	assert.Equal(t, rabaContentHash(t), feed.hash)
	feed.cleanup()
	_, err = os.Stat(feed.path)
	assert.True(t, os.IsNotExist(err), "cleanup removes the download")

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

	dbDir := t.TempDir()
	stale := filepath.Join(dbDir, "gtfs-static-123.zip")
	require.NoError(t, os.WriteFile(stale, []byte("left by a crash"), 0o600))

	feed, err := fetchFeedFile(context.Background(), Config{GtfsURL: server.URL, GTFSDataPath: filepath.Join(dbDir, "gtfs.db")})
	require.NoError(t, err)
	defer feed.cleanup()
	_, err = os.Stat(stale)
	assert.True(t, os.IsNotExist(err))
}

// TestParseHashMarksTidyOnlyWhenItRan checks that a feed whose tidy failed is
// stored under its plain hash, so the next reload doesn't match the tidied
// hash and tries again.
func TestParseHashMarksTidyOnlyWhenItRan(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no gtfstidy binary, so tidying fails
	path := models.GetFixturePath(t, "raba.zip")
	config := Config{GtfsURL: path, EnableGTFSTidy: true}

	feed, err := fetchFeedFile(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, rabaContentHash(t)+"+gtfstidy", feed.hash, "the check looks for a tidied import")

	data, err := feed.parse(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, rabaContentHash(t), data.Hash, "an untidied import is stored under the plain hash")
}
