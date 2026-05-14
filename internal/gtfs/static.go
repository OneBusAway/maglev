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

func rawGtfsData(ctx context.Context, source string, config Config) ([]byte, error) {
	var b []byte
	var err error

	logger := slog.Default().With(slog.String("component", "gtfs_loader"))

	if config.isLocalFile() {
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
		tidiedData, err := tidyGTFSData(ctx, b, logger)
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

// importStaticIntoDB imports the already-parsed GTFS data into the provided client.
// Returns (changed, err): changed is true when the DB was actually updated. When changed,
// it also precomputes stop directions. Trip time bounds are now computed inside the
// import transaction by ImportParsedGTFS itself.
func importStaticIntoDB(ctx context.Context, client *gtfsdb.Client, data *gtfsdb.GtfsData) (bool, error) {
	changed, err := client.StoreGtfsData(ctx, data)
	if err != nil {
		return false, err
	}

	if !changed {
		return false, nil
	}

	logger := slog.Default().With(slog.String("component", "gtfs_db_builder"))
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	if err := precomputer.PrecomputeAllDirections(ctx); err != nil {
		// Log error but don't fail the entire import
		logging.LogError(logger, "Failed to precompute stop directions - API will fallback to on-demand calculation", err)
	}

	return true, nil
}

func newGTFSDBConfig(dbPath string, config Config) gtfsdb.Config {
	dbConfig := gtfsdb.NewConfig(dbPath, config.Env)
	if config.Metrics != nil {
		dbConfig.QueryMetricsRecorder = config.Metrics
	}
	return dbConfig
}

// loadGTFSData loads, parses, hashes, and validates GTFS data from either a URL or a local file.
func loadGTFSData(ctx context.Context, config Config) (*gtfsdb.GtfsData, error) {
	b, err := rawGtfsData(ctx, config.GtfsURL, config)
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}

	data, err := gtfsdb.ParseGtfsData(b, config.GtfsURL)
	if err != nil {
		return nil, err
	}

	if err := validateStaticAgencyTimezones(data.Static); err != nil {
		return nil, fmt.Errorf("invalid GTFS agency timezone: %w", err)
	}

	return data, nil
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

	newData, err := loadGTFSData(ctx, manager.config)
	if err != nil {
		logging.LogError(logger, "Error loading GTFS data", err,
			slog.String("source", manager.config.GtfsURL))
		return false, err
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	changed, err := importStaticIntoDB(ctx, manager.GtfsDB, newData)
	if err != nil {
		logging.LogError(logger, "Error importing GTFS data", err)
		return false, err
	}

	if !changed {
		logging.LogOperation(logger, "gtfs_static_data_unchanged",
			slog.String("source", newData.Source))
	}

	newRegionBounds := computeRegionBounds(ctx, manager.GtfsDB)

	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.regionBounds = newRegionBounds

	// Clear the direction calculator's cached results so stale entries from the
	// pre-reload dataset aren't served
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
