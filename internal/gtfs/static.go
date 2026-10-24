package gtfs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// openGtfsDB opens (or creates) the single SQLite database used by the manager.
// No import work happens here — ReloadStatic imports into the returned client.
func openGtfsDB(config Config) (*gtfsdb.Client, error) {
	dbConfig := newGTFSDBConfig(config.GTFSDataPath, config)
	client, err := gtfsdb.NewClient(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create GTFS database client: %w", err)
	}
	return client, nil
}

// precomputeStopDirections stores each stop's direction for the feed now in
// the database. A failure is logged, not returned: the API computes missing
// directions on demand.
func precomputeStopDirections(ctx context.Context, client *gtfsdb.Client) {
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	if err := precomputer.PrecomputeAllDirections(ctx); err != nil {
		logger := slog.Default().With(slog.String("component", "gtfs_db_builder"))
		logging.LogError(logger, "Failed to precompute stop directions - API will fallback to on-demand calculation", err)
	}
}

func newGTFSDBConfig(dbPath string, config Config) gtfsdb.Config {
	dbConfig := gtfsdb.NewConfig(dbPath, config.Env)
	if config.Metrics != nil {
		dbConfig.QueryMetricsRecorder = config.Metrics
	}
	return dbConfig
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

	changed, err := manager.storeConfiguredFeed(ctx, logger)
	if err != nil {
		return false, err
	}

	if changed {
		// The parsed feed became garbage when storeConfiguredFeed returned.
		// Collecting it now, and returning its pages to the OS, keeps it out
		// of the precompute's memory and out of the process's footprint.
		debug.FreeOSMemory()
		precomputeStopDirections(ctx, manager.GtfsDB)
	} else {
		logging.LogOperation(logger, "gtfs_static_data_unchanged",
			slog.String("source", manager.config.GtfsURL))
	}

	newRegionBounds := computeRegionBounds(ctx, manager.GtfsDB)

	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.regionBounds = newRegionBounds

	// Replace direction and trip-distance caches together for the new feed.
	// In-flight calculations retain the retired cache generation.
	if changed && manager.DirectionCalculator != nil {
		manager.DirectionCalculator.ClearCache()
	}

	if eTag := manager.GetSystemETag(ctx); eTag != "" {
		logging.LogOperation(logger, "system_etag_updated_successfully", slog.String("etag", eTag))
	}

	logging.LogOperation(logger, "gtfs_static_data_reloaded",
		slog.String("source", manager.config.GtfsURL),
		slog.String("db_path", manager.config.GTFSDataPath),
		slog.Bool("changed", changed))

	manager.PrintStatistics()
	manager.logFeedExpiry(ctx, logger)

	return changed, nil
}

// storeConfiguredFeed fetches the configured feed and, unless the database
// already holds it, parses and stores it. The parsed feed is reachable only
// from here, so it can be freed as soon as this returns. It reports whether
// the database changed.
func (manager *Manager) storeConfiguredFeed(ctx context.Context, logger *slog.Logger) (bool, error) {
	feed, err := fetchFeedFile(ctx, manager.config)
	if err != nil {
		logging.LogError(logger, "Error loading GTFS data", err,
			slog.String("source", manager.config.GtfsURL))
		return false, err
	}
	defer feed.close()

	consolidationFile, err := loadStopConsolidation(ctx, manager.config)
	if err != nil {
		logging.LogError(logger, "Error loading stop consolidation file", err,
			slog.String("source", manager.config.StopConsolidationURL))
		return false, err
	}

	importHash := consolidationFile.withHash(feed.importHash(manager.config))
	imported, err := manager.GtfsDB.HasImported(ctx, importHash, manager.config.GtfsURL)
	if err != nil {
		logging.LogError(logger, "Error checking whether GTFS data is already imported", err,
			slog.String("source", manager.config.GtfsURL))
		return false, err
	}
	if imported {
		logging.LogOperation(logger, "gtfs_data_unchanged_skipping_parse", slog.String("hash", feed.contentHash))
		return false, nil
	}

	newData, err := feed.parse(ctx, manager.config)
	if err != nil {
		logging.LogError(logger, "Error loading GTFS data", err,
			slog.String("source", manager.config.GtfsURL))
		return false, err
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	newData.Hash = consolidationFile.withHash(newData.Hash)
	consolidationFile.apply(newData, logger)
	changed, err := manager.GtfsDB.StoreGtfsData(ctx, newData)
	if err != nil {
		logging.LogError(logger, "Error importing GTFS data", err)
		return false, err
	}
	return changed, nil
}

// logFeedExpiry reads the feed_expires_at value persisted by StoreGtfsData
// and updates the metrics gauge / emits warning logs about how soon the feed
// will expire. The DB write itself happens atomically inside the import
// transaction; this function is read-only.
func (manager *Manager) logFeedExpiry(ctx context.Context, logger *slog.Logger) {
	if manager.Metrics != nil && manager.Metrics.FeedExpiresAt != nil {
		manager.Metrics.FeedExpiresAt.Set(-1)
	}

	expiresAt := manager.FeedExpiresAt(ctx)
	if expiresAt.IsZero() {
		logger.Warn("GTFS feed has no active calendar dates")
		return
	}

	if manager.Metrics != nil && manager.Metrics.FeedExpiresAt != nil {
		manager.Metrics.FeedExpiresAt.Set(float64(expiresAt.Unix()))
	}

	daysUntil := int(time.Until(expiresAt).Hours() / 24)
	switch {
	case daysUntil < 0:
		logger.Warn("GTFS feed has expired", slog.Time("expires_at", expiresAt), slog.Int("days_overdue", -daysUntil))
	case daysUntil <= 1:
		logger.Warn("GTFS feed expires in 1 day or less", slog.Time("expires_at", expiresAt))
	case daysUntil <= 3:
		logger.Warn("GTFS feed expires in 3 days or less", slog.Time("expires_at", expiresAt))
	case daysUntil <= 7:
		logger.Warn("GTFS feed expires in 7 days or less", slog.Time("expires_at", expiresAt))
	default:
		logger.Info("GTFS feed valid", slog.Time("expires_at", expiresAt), slog.Int("days_until_expiry", daysUntil))
	}
}
