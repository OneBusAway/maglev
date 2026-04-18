package gtfs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

func rawGtfsData(ctx context.Context, source string, isLocalFile bool, config Config) ([]byte, error) {
	var b []byte
	var err error

	logger := slog.Default().With(slog.String("component", "gtfs_loader"))

	if isLocalFile {
		b, err = os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("error reading local GTFS file: %w", err)
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating GTFS request: %w", err)
		}

		// Add auth header if provided
		if config.StaticAuthHeaderKey != "" && config.StaticAuthHeaderValue != "" {
			req.Header.Set(config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
		}

		client := &http.Client{
			Timeout: 5 * time.Minute,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			}}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error downloading GTFS data: %w", err)
		}
		defer logging.SafeCloseWithLogging(resp.Body,
			slog.Default().With(slog.String("component", "gtfs_downloader")),
			"http_response_body")

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to download GTFS data: received HTTP status %s", resp.Status)
		}
		const maxStaticSize = 200 * 1024 * 1024
		b, err = io.ReadAll(io.LimitReader(resp.Body, maxStaticSize+1))
		if err != nil {
			return nil, fmt.Errorf("error reading GTFS data: %w", err)
		}
		if int64(len(b)) > maxStaticSize {
			return nil, fmt.Errorf("static GTFS response exceeds size limit of %d bytes", maxStaticSize)
		}
	}

	// Process through gtfstidy if enabled
	if config.EnableGTFSTidy {
		logging.LogOperation(logger, "gtfstidy_enabled_processing_gtfs_data")
		tidiedData, err := tidyGTFSData(b, logger)
		if err != nil {
			logging.LogError(logger, "Failed to tidy GTFS data, using original data", err)
		} else {
			b = tidiedData
		}
	}

	return b, nil
}

// openGtfsDB opens (or creates) the single SQLite database used by the manager.
// No import work happens here — use importStaticIntoDB against the returned client.
func openGtfsDB(config Config) (*gtfsdb.Client, error) {
	dbConfig := newGTFSDBConfig(config.GTFSDataPath, config)
	client, err := gtfsdb.NewClient(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create GTFS database client: %w", err)
	}
	return client, nil
}

// importStaticIntoDB fetches GTFS data and imports it into the provided client.
// Returns (changed, err): changed is true when the DB was actually updated. When changed,
// it also runs the post-import derived-column backfills (trip time bounds, stop directions).
func importStaticIntoDB(ctx context.Context, client *gtfsdb.Client, config Config, isLocalFile bool) (bool, error) {
	var (
		changed bool
		err     error
	)
	if isLocalFile {
		changed, err = client.ImportFromFile(ctx, config.GtfsURL)
	} else {
		changed, err = client.DownloadAndStore(ctx, config.GtfsURL, config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
	}
	if err != nil {
		return false, err
	}

	if !changed {
		return false, nil
	}

	logger := slog.Default().With(slog.String("component", "gtfs_db_builder"))
	logging.LogOperation(logger, "calculating_trip_time_bounds")
	if err := client.Queries.BulkUpdateTripTimeBounds(ctx); err != nil {
		return true, fmt.Errorf("failed to bulk update trip time bounds: %w", err)
	}

	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	if err := precomputer.PrecomputeAllDirections(ctx); err != nil {
		// Log error but don't fail the entire import
		logging.LogError(logger, "Failed to precompute stop directions - API will fallback to on-demand calculation", err)
	}

	return true, nil
}

func newGTFSDBConfig(dbPath string, config Config) gtfsdb.Config {
	dbConfig := gtfsdb.NewConfig(dbPath, config.Env, config.Verbose)
	if config.Metrics != nil {
		dbConfig.QueryMetricsRecorder = config.Metrics
	}
	return dbConfig
}

// loadGTFSData loads and parses GTFS data from either a URL or a local file
func loadGTFSData(ctx context.Context, source string, isLocalFile bool, config Config) (*gtfs.Static, error) {
	b, err := rawGtfsData(ctx, source, isLocalFile, config)
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}

	staticData, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("error parsing GTFS data: %w", err)
	}

	if err := validateStaticAgencyTimezones(staticData); err != nil {
		return nil, fmt.Errorf("invalid GTFS agency timezone: %w", err)
	}

	return staticData, nil
}

func validateStaticAgencyTimezones(staticData *gtfs.Static) error {
	for i, agency := range staticData.Agencies {
		tz := strings.TrimSpace(agency.Timezone)
		// Go treats LoadLocation("") as UTC, so we consider this an error for GTFS validation purposes
		if tz == "" {
			return fmt.Errorf("agency %q has empty timezone", agency.Id)
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("agency %q has invalid timezone %q: %w", agency.Id, tz, err)
		}
		// Write the trimmed value back so downstream LoadLocation calls use the clean string
		staticData.Agencies[i].Timezone = tz
	}
	return nil
}

// UpdateGTFSPeriodically updates the GTFS data on a regular schedule
func (manager *Manager) updateStaticGTFS() { // nolint
	defer manager.wg.Done()

	// Create a logger for this goroutine
	logger := slog.Default().With(slog.String("component", "gtfs_static_updater"))

	// If it's a local file, don't update periodically
	if manager.isLocalFile {
		logging.LogOperation(logger, "gtfs_source_is_local_file_skipping_periodic_updates",
			slog.String("source", manager.config.GtfsURL))
		return
	}

	// Update every 24 hours
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for { // nolint
		select {
		case <-ticker.C:
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()

				_, err := manager.ReloadStatic(ctx)
				if err != nil {
					logging.LogError(logger, "Error updating GTFS data", err,
						slog.String("source", manager.config.GtfsURL))
				}
			}()
		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_static_gtfs_updates")
			return
		}
	}
}

// ReloadStatic is the single code path for importing GTFS static data into
// manager.GtfsDB. It is called from both startup (InitGTFSManager) and the
// periodic refresh. Returns (changed, err). Caller is responsible
// for serializing concurrent invocations via staticUpdateMutex when appropriate.
func (manager *Manager) ReloadStatic(ctx context.Context) (bool, error) {
	manager.staticUpdateMutex.Lock()
	defer manager.staticUpdateMutex.Unlock()
	logger := slog.Default().With(slog.String("component", "gtfs_updater"))

	newStaticData, err := loadGTFSData(ctx, manager.config.GtfsURL, manager.isLocalFile, manager.config)
	if err != nil {
		logging.LogError(logger, "Error loading GTFS data", err,
			slog.String("source", manager.config.GtfsURL))
		return false, err
	}

	if err := gtfsdb.ValidateAndFilterGTFSData(newStaticData, logger); err != nil {
		logging.LogError(logger, "GTFS structural validation failed", err)
		return false, fmt.Errorf("GTFS validation failed: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	changed, err := importStaticIntoDB(ctx, manager.GtfsDB, manager.config, manager.isLocalFile)
	if err != nil {
		logging.LogError(logger, "Error importing GTFS data", err)
		return false, err
	}

	if !changed {
		logging.LogOperation(logger, "gtfs_static_data_unchanged",
			slog.String("source", manager.config.GtfsURL))
	}

	// Rebuild in-memory derived state from the freshly parsed data and the DB.
	// We do this even when the import was a no-op so that startup can rely on a
	// single code path to populate indices/ETag/lastUpdated.
	newBlockLayoverIndices := buildBlockLayoverIndices(newStaticData)
	newRegionBounds := computeRegionBounds(ctx, manager.GtfsDB)

	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.blockLayoverIndices = newBlockLayoverIndices
	manager.regionBounds = newRegionBounds

	// Clear the direction calculator's cached results so stale entries from the
	// pre-reload dataset aren't served
	if changed && manager.DirectionCalculator != nil {
		manager.DirectionCalculator.ClearCache()
	}

	now := time.Now()
	manager.lastUpdated = now
	manager.lastUpdatedUnixNanos.Store(now.UnixNano())

	metadata, err := manager.GtfsDB.Queries.GetImportMetadata(ctx)
	if err != nil {
		logging.LogError(logger, "Failed to fetch import metadata for ETag generation", err)
		manager.systemETag = ""
	} else if metadata.FileHash != "" {
		manager.systemETag = fmt.Sprintf(`"%s"`, metadata.FileHash)
		logging.LogOperation(logger, "system_etag_updated_successfully", slog.String("etag", manager.systemETag))
	} else {
		manager.systemETag = ""
	}

	logging.LogOperation(logger, "gtfs_static_data_reloaded",
		slog.String("source", manager.config.GtfsURL),
		slog.String("db_path", manager.config.GTFSDataPath),
		slog.Bool("changed", changed))

	manager.PrintStatistics()
	manager.parseAndLogFeedExpiryLocked(ctx, logger)

	return changed, nil
}

// parseAndLogFeedExpiryLocked checks the GTFS calendar for the last active service date
// NOTE: Caller must guarantee thread-safety
func (manager *Manager) parseAndLogFeedExpiryLocked(ctx context.Context, logger *slog.Logger) {
	manager.feedExpiresAt = time.Time{}
	if manager.Metrics != nil && manager.Metrics.FeedExpiresAt != nil {
		manager.Metrics.FeedExpiresAt.Set(-1)
	}

	if manager.GtfsDB == nil || manager.GtfsDB.Queries == nil {
		return
	}

	val, err := manager.GtfsDB.Queries.GetFeedEndDate(ctx)
	if err != nil {
		logging.LogError(logger, "Failed to get feed end date from DB", err)
		return
	}

	var strVal string
	switch v := val.(type) {
	case nil:
		// No calendar data; strVal remains "" and falls through
		// to the "no active calendar dates" warning below.
	case string:
		strVal = v
	case []byte:
		strVal = string(v)
	default:
		logging.LogError(logger, "Unexpected type from GetFeedEndDate", fmt.Errorf("expected string, got %T", val))
		return
	}

	if strVal != "" {
		parsedTime, err := time.Parse("20060102", strVal)
		if err != nil {
			logging.LogError(logger, "Failed to parse feed end date", err, slog.String("date", strVal))
			return
		}

		// 23:59:59 end date
		expiresAt := parsedTime.Add(24 * time.Hour).Add(-time.Second)

		manager.feedExpiresAt = expiresAt

		if manager.Metrics != nil && manager.Metrics.FeedExpiresAt != nil {
			manager.Metrics.FeedExpiresAt.Set(float64(expiresAt.Unix()))
		}

		daysUntil := int(time.Until(expiresAt).Hours() / 24)
		if daysUntil < 0 {
			logger.Warn("GTFS feed has expired", slog.Time("expires_at", expiresAt), slog.Int("days_overdue", -daysUntil))
		} else if daysUntil <= 1 {
			logger.Warn("GTFS feed expires in 1 day or less", slog.Time("expires_at", expiresAt))
		} else if daysUntil <= 3 {
			logger.Warn("GTFS feed expires in 3 days or less", slog.Time("expires_at", expiresAt))
		} else if daysUntil <= 7 {
			logger.Warn("GTFS feed expires in 7 days or less", slog.Time("expires_at", expiresAt))
		} else {
			logger.Info("GTFS feed valid", slog.Time("expires_at", expiresAt), slog.Int("days_until_expiry", daysUntil))
		}
	} else {
		logger.Warn("GTFS feed has no active calendar dates")
	}
}
