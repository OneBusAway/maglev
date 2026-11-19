package gtfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// feedFile is a static GTFS zip on local disk and the SHA-256 of its content.
// The import reads it more than once, so it must not change underneath. A
// download is a private copy from the start. A local feed is read where it
// is until parse copies it, so a reload that finds it already imported only
// hashes it.
type feedFile struct {
	path        string
	contentHash string
	// open is the private copy, kept open from the moment it is written so
	// the import streams from it without reopening it by name. It is nil for
	// a local feed that hasn't been copied.
	open *os.File
}

// importHash is the hash an import of this feed is stored under.
func (f *feedFile) importHash(config Config) string {
	return feedHash(f.contentHash, config)
}

// identity names an import of this feed in import_metadata.
func (f *feedFile) identity(config Config) gtfsdb.FeedIdentity {
	return gtfsdb.FeedIdentity{Hash: f.importHash(config), Source: config.GtfsURL}
}

// close closes and removes the private copy, if there is one.
func (f *feedFile) close() {
	if f.open == nil {
		return
	}
	_ = f.open.Close()
	_ = os.Remove(f.path)
}

// fetchFeedFile hashes the configured feed without holding it in memory. A
// local feed is read where it is; a remote one is downloaded next to the
// database and hashed on the way. Call close when done.
func fetchFeedFile(ctx context.Context, config Config) (*feedFile, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_loader"))
	logging.LogOperation(logger, "reading_gtfs_data", slog.String("source", config.GtfsURL))

	if !config.isLocalFile() {
		return downloadFeedFile(ctx, config)
	}
	contentHash, err := hashFile(config.GtfsURL)
	if err != nil {
		return nil, fmt.Errorf("error reading local GTFS file: %w", err)
	}
	return &feedFile{path: config.GtfsURL, contentHash: contentHash}, nil
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

// makePrivate replaces a local feed with a private copy next to the database,
// hashed on the way, so the feed can't change under the stream and the stored
// hash describes the stored data. A download is private already.
func (f *feedFile) makePrivate(config Config) error {
	if f.open != nil {
		return nil
	}
	source, err := os.Open(f.path)
	if err != nil {
		return fmt.Errorf("error reading local GTFS file: %w", err)
	}
	defer func() { _ = source.Close() }()

	private, _, err := copyToFeedFile(config, source)
	if err != nil {
		return fmt.Errorf("error reading local GTFS file: %w", err)
	}
	*f = *private
	return nil
}

func downloadFeedFile(ctx context.Context, config Config) (*feedFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.GtfsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating GTFS request: %w", err)
	}
	if config.StaticAuthHeaderKey != "" && config.StaticAuthHeaderValue != "" {
		req.Header.Set(config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
	}

	resp, err := staticHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error downloading GTFS data: %w", err)
	}
	defer logging.SafeCloseWithLogging(resp.Body,
		slog.Default().With(slog.String("component", "gtfs_downloader")),
		"http_response_body")
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download GTFS data: received HTTP status %s", resp.Status)
	}

	feed, written, err := copyToFeedFile(config, io.LimitReader(resp.Body, maxStaticFeedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}
	if written > maxStaticFeedBytes {
		feed.close()
		return nil, fmt.Errorf("static GTFS response exceeds size limit of %d bytes", maxStaticFeedBytes)
	}
	return feed, nil
}

// copyToFeedFile writes r to a new feed file and hashes it on the way. The
// file stays open for the import.
func copyToFeedFile(config Config, r io.Reader) (*feedFile, int64, error) {
	file, err := newDownloadLocation(config).create()
	if err != nil {
		return nil, 0, err
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), r)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, 0, err
	}
	return &feedFile{path: file.Name(), contentHash: hex.EncodeToString(hasher.Sum(nil)), open: file}, written, nil
}

// Timeouts for downloading a static feed. The overall timeout allows for a
// feed of maxStaticFeedBytes on a slow link.
const (
	staticTLSHandshakeTimeout   = 10 * time.Second
	staticResponseHeaderTimeout = 30 * time.Second
	staticIdleConnTimeout       = 90 * time.Second
	staticDownloadTimeout       = 5 * time.Minute
)

// staticHTTPClient downloads static feeds. It is shared by every reload, so
// an idle connection is reused or closed by its transport rather than left
// behind on one that nothing uses again.
var staticHTTPClient = newStaticHTTPClient()

func newStaticHTTPClient() *http.Client {
	transport := cloneDefaultTransport()
	transport.TLSHandshakeTimeout = staticTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = staticResponseHeaderTimeout
	transport.IdleConnTimeout = staticIdleConnTimeout
	return &http.Client{Timeout: staticDownloadTimeout, Transport: transport}
}

// cloneDefaultTransport returns a copy of http.DefaultTransport, to keep its
// defaults (proxy from environment, dialer, HTTP/2, keepalives).
func cloneDefaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{}
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

// gtfsTidySuffix marks a stored hash made with gtfstidy on. feedHash adds it
// and ContentHash removes it, so the two stay in step.
const gtfsTidySuffix = "+gtfstidy"

// feedHash identifies a feed in import_metadata. With gtfstidy on, the stored
// data comes from the tidied feed, so the setting is part of the identity:
// turning it on or off causes a re-import. The mark records that tidy was on,
// not that it succeeded: a feed whose tidy fails is stored under the same
// hash, so the next reload skips it instead of parsing it again. Tidy is
// retried when the feed changes.
func feedHash(contentHash string, config Config) string {
	if config.EnableGTFSTidy {
		return contentHash + gtfsTidySuffix
	}
	return contentHash
}

// ContentHash returns the hex sha256 of the raw static GTFS zip from a stored
// import_metadata hash, dropping the gtfstidy marker that feedHash may add.
func ContentHash(storedHash string) string {
	return strings.TrimSuffix(storedHash, gtfsTidySuffix)
}

// parse parses the feed as a stream from its private copy, so its stop times
// and shapes are read from the file while they are stored; the file stays
// open until close. With
// gtfstidy on, the tidied feed is streamed from memory.
func (f *feedFile) parse(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	if err := f.makePrivate(config); err != nil {
		return nil, err
	}

	var data *gtfsdb.GtfsData
	var err error
	if config.EnableGTFSTidy {
		data, err = f.parseTidied(ctx, config)
	} else {
		data, err = f.parseFile(config)
	}
	if err != nil {
		return nil, err
	}

	if err := validateStaticAgencyTimezones(data.Static); err != nil {
		return nil, fmt.Errorf("invalid GTFS agency timezone: %w", err)
	}
	return data, nil
}

func (f *feedFile) parseFile(config Config) (*gtfsdb.GtfsData, error) {
	info, err := f.open.Stat()
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}
	return gtfsdb.ParseGtfsStream(f.open, info.Size(), f.identity(config))
}

func (f *feedFile) parseTidied(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_loader"))
	logging.LogOperation(logger, "gtfstidy_enabled_processing_gtfs_data")
	tidiedData, err := tidyGTFSFile(ctx, f.path, logger)
	if err != nil {
		logging.LogError(logger, "Failed to tidy GTFS data, using original data", err)
		return f.parseFile(config)
	}
	return gtfsdb.ParseGtfsStream(bytes.NewReader(tidiedData), int64(len(tidiedData)), f.identity(config))
}
