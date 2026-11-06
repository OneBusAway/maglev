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
	"os"
	"path/filepath"
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
	path    string
	hash    string
	cleanup func()
}

// downloadPattern names feed downloads, so ones left behind by a crash can be
// found and removed.
const downloadPattern = "gtfs-static-*.zip"

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

	removeStaleDownloads(downloadDir(config))
	file, err := os.CreateTemp(downloadDir(config), downloadPattern)
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
	return &feedFile{path: file.Name(), hash: feedHash(hex.EncodeToString(hasher.Sum(nil)), config), cleanup: remove}, nil
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

// removeStaleDownloads deletes feed downloads that a crashed process never
// cleaned up. Reloads are serialized, so none of them is still in use.
func removeStaleDownloads(dir string) {
	if dir == "" {
		dir = os.TempDir()
	}
	stale, _ := filepath.Glob(filepath.Join(dir, downloadPattern))
	for _, path := range stale {
		_ = os.Remove(path)
	}
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

// parse reads the feed, runs gtfstidy when enabled, then parses and validates
// it. The returned hash is that of the bytes it read, so it matches the stored
// data even if the file changed since it was fetched, and it is marked as
// tidied only if gtfstidy succeeded, so a failed tidy is retried next time.
func (f *feedFile) parse(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])

	if config.EnableGTFSTidy {
		logger := slog.Default().With(slog.String("component", "gtfs_loader"))
		logging.LogOperation(logger, "gtfstidy_enabled_processing_gtfs_data")
		tidiedData, err := tidyGTFSData(ctx, b, logger)
		if err != nil {
			logging.LogError(logger, "Failed to tidy GTFS data, using original data", err)
		} else {
			b = tidiedData
			hash = feedHash(hash, config)
		}
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
