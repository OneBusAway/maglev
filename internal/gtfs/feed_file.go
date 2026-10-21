package gtfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// maxStaticFeedBytes is the largest static feed Maglev downloads.
const maxStaticFeedBytes = 200 * 1024 * 1024

// downloadPattern names the feed copies, so ones left behind by a crash can
// be found and removed.
const downloadPattern = "gtfs-static-*.zip"

// feedFile is a copy of the static GTFS zip on local disk, private to one
// import, and the SHA-256 of its content. The import reads it more than once,
// so it must not change underneath; a private copy guarantees that even when
// the configured feed is a local file that someone overwrites.
type feedFile struct {
	path        string
	contentHash string
	// open is the copy, kept open from the moment it is written so the
	// import streams from it without reopening it by name.
	open *os.File
}

// importHash is the hash an import of this feed is stored under.
func (f *feedFile) importHash(config Config) string {
	return feedHash(f.contentHash, config)
}

// close closes and removes the copy.
func (f *feedFile) close() {
	_ = f.open.Close()
	_ = os.Remove(f.path)
}

// fetchFeedFile copies the configured feed, local or remote, into a file next
// to the database while hashing it, without holding the feed in memory. Call
// close when done.
func fetchFeedFile(ctx context.Context, config Config) (*feedFile, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_loader"))
	logging.LogOperation(logger, "reading_gtfs_data", slog.String("source", config.GtfsURL))
	removeStaleDownloads(downloadDir(config))

	if config.isLocalFile() {
		source, err := os.Open(config.GtfsURL)
		if err != nil {
			return nil, fmt.Errorf("error reading local GTFS file: %w", err)
		}
		defer func() { _ = source.Close() }()
		feed, _, err := copyToFeedFile(config, source)
		if err != nil {
			return nil, fmt.Errorf("error reading local GTFS file: %w", err)
		}
		return feed, nil
	}
	return downloadFeedFile(ctx, config)
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
	file, err := os.CreateTemp(downloadDir(config), downloadPattern)
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

// removeStaleDownloads deletes feed copies that a crashed process never
// cleaned up next to its database. Reloads are serialized, so none of them is
// still in use. The shared temporary directory, used when the database is in
// memory, is left alone: other processes may be importing from it.
func removeStaleDownloads(dir string) {
	if dir == "" {
		return
	}
	stale, _ := filepath.Glob(filepath.Join(dir, downloadPattern))
	for _, path := range stale {
		_ = os.Remove(path)
	}
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

// downloadDir keeps a download on the same disk as the database, rather than
// in a temporary directory that may be held in memory (tmpfs).
func downloadDir(config Config) string {
	if config.GTFSDataPath == "" || strings.HasPrefix(config.GTFSDataPath, ":memory:") {
		return ""
	}
	return filepath.Dir(config.GTFSDataPath)
}

// feedHash identifies a feed in import_metadata. With gtfstidy on, the stored
// data comes from the tidied feed, so the setting is part of the identity:
// turning it on or off causes a re-import.
func feedHash(contentHash string, config Config) string {
	if config.EnableGTFSTidy {
		return contentHash + "+gtfstidy"
	}
	return contentHash
}

// parse parses the feed as a stream, so its stop times and shapes are read
// from the file while they are stored; the file stays open until close. With
// gtfstidy on, the tidied feed is streamed from memory, and its hash is marked
// as tidied, so a failed tidy is retried on the next reload.
func (f *feedFile) parse(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
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
	return gtfsdb.ParseGtfsStream(f.open, info.Size(), f.contentHash, config.GtfsURL)
}

func (f *feedFile) parseTidied(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	b, err := io.ReadAll(io.NewSectionReader(f.open, 0, math.MaxInt64))
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}

	logger := slog.Default().With(slog.String("component", "gtfs_loader"))
	logging.LogOperation(logger, "gtfstidy_enabled_processing_gtfs_data")
	tidiedData, err := tidyGTFSData(ctx, b, logger)
	if err != nil {
		logging.LogError(logger, "Failed to tidy GTFS data, using original data", err)
		return f.parseFile(config)
	}
	return gtfsdb.ParseGtfsStream(bytes.NewReader(tidiedData), int64(len(tidiedData)), feedHash(f.contentHash, config), config.GtfsURL)
}
