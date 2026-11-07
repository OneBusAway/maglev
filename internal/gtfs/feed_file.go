package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// maxStaticFeedBytes is the largest static feed Maglev downloads.
const maxStaticFeedBytes = 200 * 1024 * 1024

// feedFile is a static GTFS zip on local disk. hash identifies it in
// import_metadata when it is imported as configured.
type feedFile struct {
	path string
	hash string
	// private is set for a download, which nothing but this import can
	// change, so hash still describes it when it is parsed.
	private bool
	cleanup func()
}

// fetchFeedFile puts the configured feed in a local file and hashes it,
// without holding the feed in memory. A local feed is read where it is; a
// remote feed is downloaded next to the database. Call cleanup when done.
func fetchFeedFile(ctx context.Context, config Config) (*feedFile, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_loader"))
	logging.LogOperation(logger, "reading_gtfs_data", slog.String("source", config.GtfsURL))

	if config.isLocalFile() {
		contentHash, err := hashFile(config.GtfsURL)
		if err != nil {
			return nil, fmt.Errorf("error reading local GTFS file: %w", err)
		}
		return &feedFile{path: config.GtfsURL, hash: feedHash(contentHash, config), cleanup: func() {}}, nil
	}
	return downloadFeedFile(ctx, config)
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func downloadFeedFile(ctx context.Context, config Config) (*feedFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.GtfsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating GTFS request: %w", err)
	}
	if config.StaticAuthHeaderKey != "" && config.StaticAuthHeaderValue != "" {
		req.Header.Set(config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
	}

	resp, err := newStaticHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("error downloading GTFS data: %w", err)
	}
	defer logging.SafeCloseWithLogging(resp.Body,
		slog.Default().With(slog.String("component", "gtfs_downloader")),
		"http_response_body")
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download GTFS data: received HTTP status %s", resp.Status)
	}

	file, err := newDownloadLocation(config).create()
	if err != nil {
		return nil, fmt.Errorf("error creating file for GTFS download: %w", err)
	}
	remove := func() { _ = os.Remove(file.Name()) }

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(resp.Body, maxStaticFeedBytes+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		remove()
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}
	if written > maxStaticFeedBytes {
		remove()
		return nil, fmt.Errorf("static GTFS response exceeds size limit of %d bytes", maxStaticFeedBytes)
	}
	return &feedFile{
		path:    file.Name(),
		hash:    feedHash(hex.EncodeToString(hasher.Sum(nil)), config),
		private: true,
		cleanup: remove,
	}, nil
}

// newStaticHTTPClient returns a client for downloading a static feed. Like the
// realtime client, its transport is cloned from http.DefaultTransport to keep
// its defaults (proxy from environment, dialer, HTTP/2).
func newStaticHTTPClient() *http.Client {
	transport := &http.Transport{}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = t.Clone()
	}
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Timeout: 5 * time.Minute, Transport: transport}
}

// Feed downloads are named downloadPrefix, then their owner, then "-" and the
// random digits os.CreateTemp adds, then downloadSuffix. The owner part lets a
// process find the downloads it left behind in a crash without touching
// anyone else's.
const (
	downloadPrefix = "gtfs-static-"
	downloadSuffix = ".zip"
)

// sqliteMemoryDatabase is the name SQLite gives an in-memory database.
const sqliteMemoryDatabase = ":memory:"

// downloadLocation is where a process keeps its feed downloads, and the name
// prefix that marks them as its own.
type downloadLocation struct {
	dir    string
	prefix string
}

// newDownloadLocation keeps downloads on the same disk as the database, rather
// than in a temporary directory that may be held in memory (tmpfs), and names
// them after the database file, so processes whose databases share a
// directory leave each other's downloads alone. With an in-memory database,
// downloads go to the temporary directory and are named after the process.
func newDownloadLocation(config Config) downloadLocation {
	dbFile, onDisk := databaseFile(config.GTFSDataPath)
	if !onDisk {
		owner := "pid" + strconv.Itoa(os.Getpid())
		return downloadLocation{dir: os.TempDir(), prefix: downloadPrefix + owner + "-"}
	}
	return downloadLocation{dir: filepath.Dir(dbFile), prefix: downloadPrefix + filepath.Base(dbFile) + "-"}
}

// databaseFile returns the file that holds the database at dataPath, a path
// or a SQLite "file:" URI, and reports whether the database is on disk at
// all rather than in memory.
func databaseFile(dataPath string) (string, bool) {
	if !strings.HasPrefix(dataPath, "file:") {
		inMemory := dataPath == "" || strings.HasPrefix(dataPath, sqliteMemoryDatabase)
		return dataPath, !inMemory
	}
	uri, err := url.Parse(dataPath)
	if err != nil {
		return "", false
	}
	path := uri.Path
	if path == "" {
		path = uri.Opaque
	}
	inMemory := path == "" || path == sqliteMemoryDatabase || uri.Query().Get("mode") == "memory"
	return path, !inMemory
}

// create removes the downloads this owner left behind, then creates a new one.
func (l downloadLocation) create() (*os.File, error) {
	l.removeStale()
	return os.CreateTemp(l.dir, l.prefix+"*"+downloadSuffix)
}

// removeStale deletes downloads this owner left behind in a crash. An owner's
// reloads are serialized, so none of them is still in use.
func (l downloadLocation) removeStale() {
	entries, _ := os.ReadDir(l.dir)
	for _, entry := range entries {
		if entry.Type().IsRegular() && l.owns(entry.Name()) {
			_ = os.Remove(filepath.Join(l.dir, entry.Name()))
		}
	}
}

// owns reports whether name is one of this owner's downloads. The random part
// must be all digits, so another owner whose name merely starts with this one
// (gtfs.db-old next to gtfs.db) doesn't match.
func (l downloadLocation) owns(name string) bool {
	random, found := strings.CutPrefix(name, l.prefix)
	if !found {
		return false
	}
	random, found = strings.CutSuffix(random, downloadSuffix)
	return found && random != "" && strings.Trim(random, "0123456789") == ""
}

// feedHash identifies a feed in import_metadata. With gtfstidy on, the stored
// data comes from the tidied feed, so the setting is part of the identity:
// turning it on or off causes a re-import. The mark records that tidy was on,
// not that it succeeded: a feed whose tidy fails is stored under the same
// hash, so the next reload skips it instead of parsing it again. Tidy is
// retried when the feed changes.
func feedHash(contentHash string, config Config) string {
	if config.EnableGTFSTidy {
		return contentHash + "+gtfstidy"
	}
	return contentHash
}

// parse reads the feed, runs gtfstidy when enabled, then parses and validates
// it.
func (f *feedFile) parse(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	b, hash, err := f.read(ctx, config)
	if err != nil {
		return nil, err
	}

	data, err := gtfsdb.ParseGtfsDataWithHash(b, hash, config.GtfsURL)
	if err != nil {
		return nil, err
	}

	if err := validateStaticAgencyTimezones(data.Static); err != nil {
		return nil, fmt.Errorf("invalid GTFS agency timezone: %w", err)
	}
	return data, nil
}

// read returns the feed's bytes to import, tidied when gtfstidy is on and
// succeeds, and the hash to store them under. gtfstidy reads the file itself,
// so a tidied feed is never held in memory untidied.
//
// An untidied local feed is hashed again from the bytes read, so the stored
// hash matches the stored data even if the file changed since it was fetched.
// A tidied local feed keeps the fetched hash; if the file changed in between,
// the next reload sees a new hash and imports it again.
func (f *feedFile) read(ctx context.Context, config Config) ([]byte, string, error) {
	if config.EnableGTFSTidy {
		logger := slog.Default().With(slog.String("component", "gtfs_loader"))
		logging.LogOperation(logger, "gtfstidy_enabled_processing_gtfs_data")
		tidiedData, err := tidyGTFSFile(ctx, f.path, logger)
		if err == nil {
			return tidiedData, f.hash, nil
		}
		logging.LogError(logger, "Failed to tidy GTFS data, using original data", err)
	}

	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, "", fmt.Errorf("error reading GTFS data: %w", err)
	}
	if f.private {
		return b, f.hash, nil
	}
	sum := sha256.Sum256(b)
	return b, feedHash(hex.EncodeToString(sum[:]), config), nil
}
