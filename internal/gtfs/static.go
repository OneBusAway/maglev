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

func rawGtfsData(source string, isLocalFile bool, config Config) ([]byte, error) {
	var b []byte
	var err error

	logger := slog.Default().With(slog.String("component", "gtfs_loader"))

	if isLocalFile {
		b, err = os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("error reading local GTFS file: %w", err)
		}
	} else {
		req, err := http.NewRequest("GET", source, nil)
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

		b, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading GTFS data: %w", err)
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

func buildGtfsDB(config Config, isLocalFile bool, dbPath string) (*gtfsdb.Client, error) {
	// If no specific path is provided, use the one from config
	if dbPath == "" {
		dbPath = config.GTFSDataPath
	}
	dbConfig := gtfsdb.NewConfig(dbPath, config.Env, config.Verbose)
	client, err := gtfsdb.NewClient(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create GTFS database client: %w", err)
	}

	ctx := context.Background()

	if isLocalFile {
		err = client.ImportFromFile(ctx, config.GtfsURL)
	} else {
		err = client.DownloadAndStore(ctx, config.GtfsURL, config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
	}

	if err != nil {
		return nil, err
	}

	// Precompute stop directions after GTFS data is loaded
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	if err := precomputer.PrecomputeAllDirections(ctx); err != nil {
		// Log error but don't fail the entire import
		logger := slog.Default().With(slog.String("component", "gtfs_db_builder"))
		logging.LogError(logger, "Failed to precompute stop directions - API will fallback to on-demand calculation", err)
	}

	return client, nil
}

// loadGTFSData loads and parses GTFS data from either a URL or a local file
func loadGTFSData(source string, isLocalFile bool, config Config) (*gtfs.Static, error) {
	b, err := rawGtfsData(source, isLocalFile, config)
	if err != nil {
		return nil, fmt.Errorf("error reading GTFS data: %w", err)
	}

	staticData, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("error parsing GTFS data: %w", err)
	}

	return staticData, nil
}

// UpdateGTFSPeriodically updates the GTFS data on a regular schedule
func (manager *Manager) updateStaticGTFS() { // nolint
	defer manager.wg.Done()

	// Create a logger for this goroutine
	logger := slog.Default().With(slog.String("component", "gtfs_static_updater"))

	// If it's a local file, don't update periodically
	if manager.isLocalFile {
		logging.LogOperation(logger, "gtfs_source_is_local_file_skipping_periodic_updates",
			slog.String("source", manager.gtfsSource))
		return
	}

	// Update every 24 hours
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for { // nolint
		select {
		case <-ticker.C:

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

			err := manager.ForceUpdate(ctx)
			cancel()

			if err != nil {
				logging.LogError(logger, "Error updating GTFS data", err,
					slog.String("source", manager.gtfsSource))
				continue
			}

		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_static_gtfs_updates")
			return
		}
	}
}

// ForceUpdate performs a thread-safe, mutex protected hot-swap of the GTFS static data and database.
//
// This process involves several critical steps to ensure data integrity and minimal downtime:
//  1. Fetching Data: Downloads or reads the latest GTFS data from the configured source.
//  2. Staging: Creates a temporary SQLite database ("*.temp.db") and populates it with the new data.
//  3. Precomputation: Builds necessary indices (e.g., stop spatial index, block layover indices) using the temporary database to ensure the new data is ready for query immediately upon swapping.
//  4. Mutex Protected Swap:
//     - Acquires a write lock (staticMutex) to pause all concurrent readers.
//     - Closes the existing database connection.
//     - Uses os.Rename to replace the active database file with the fully prepared temporary database.
//     - Re-opens the database at the stable path.
//  5. State Update: Updates the manager's references (GtfsDB, gtfsData, indices) to usage the new data components.
//
// If the update fails at any point before the swap, temporary files are cleaned up, and the application continues serving the old data.
// If the final swap (file rename) fails, the system attempts to recover by re-opening the existing database.
func (manager *Manager) ForceUpdate(ctx context.Context) error {
	manager.staticUpdateMutex.Lock()
	defer manager.staticUpdateMutex.Unlock()

	logger := slog.Default().With(slog.String("component", "gtfs_updater"))

	newStaticData, err := loadGTFSData(manager.gtfsSource, manager.isLocalFile, manager.config)
	if err != nil {
		logging.LogError(logger, "Error updating GTFS data", err,
			slog.String("source", manager.gtfsSource))
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	finalDBPath := manager.config.GTFSDataPath
	tempDBPath := strings.TrimSuffix(finalDBPath, ".db") + ".temp.db"

	if err := os.Remove(tempDBPath); err != nil && !os.IsNotExist(err) {
		logging.LogError(logger, "Failed to remove existing temp DB", err)
	}

	// Create a config copy with the updated source URL
	newConfig := manager.config
	newConfig.GtfsURL = manager.gtfsSource

	newGtfsDB, err := buildGtfsDB(newConfig, manager.isLocalFile, tempDBPath)
	if err != nil {
		logging.LogError(logger, "Error building new GTFS DB", err)
		return err
	}

	if err := ctx.Err(); err != nil {
		if closeErr := newGtfsDB.Close(); closeErr != nil {
			logging.LogError(logger, "Failed to close new GTFS DB during cancellation cleanup", closeErr)
		}
		if removeErr := os.Remove(tempDBPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logging.LogError(logger, "Failed to remove temp DB during cancellation cleanup", removeErr)
		}
		return err
	}

	newBlockLayoverIndices := buildBlockLayoverIndices(newStaticData)
	newStopSpatialIndex, err := buildStopSpatialIndex(ctx, newGtfsDB.Queries)
	if err != nil {
		logging.LogError(logger, "Error building spatial index", err)
		if closeErr := newGtfsDB.Close(); closeErr != nil {
			logging.LogError(logger, "Failed to close new GTFS DB during cleanup", closeErr)
		}
		if removeErr := os.Remove(tempDBPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logging.LogError(logger, "Failed to remove temp DB during cleanup", removeErr)
		}
		return err
	}

	if err := ctx.Err(); err != nil {
		if closeErr := newGtfsDB.Close(); closeErr != nil {
			logging.LogError(logger, "Failed to close new GTFS DB during cancellation cleanup", closeErr)
		}
		if removeErr := os.Remove(tempDBPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logging.LogError(logger, "Failed to remove new GTFS DB during cancellation cleanup", removeErr)
		}
		return err
	}

	if err := newGtfsDB.Close(); err != nil {
		logging.LogError(logger, "Error closing new GTFS DB", err)
		return err
	}
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	oldGtfsDB := manager.GtfsDB

	if oldGtfsDB != nil {
		if err := oldGtfsDB.Close(); err != nil {
			logging.LogError(logger, "Error closing old GTFS DB, did not swap DB", err)
			return err
		}
	}

	// Rename: finalDBPath is overwritten by tempDBPath
	if err := os.Rename(tempDBPath, finalDBPath); err != nil {
		logging.LogError(logger, "Error renaming temp DB to final DB", err)

		if removeErr := os.Remove(tempDBPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logging.LogError(logger, "Failed to remove temp DB after rename failure", removeErr)
		}

		logging.LogOperation(logger, "attempting_recovery_reopening_old_db")

		dbConfig := gtfsdb.NewConfig(finalDBPath, manager.config.Env, manager.config.Verbose)
		if reopenedClient, reopenErr := gtfsdb.NewClient(dbConfig); reopenErr == nil {
			manager.GtfsDB = reopenedClient
			logging.LogOperation(logger, "recovery_successful_old_db_reopened")
		} else {
			logging.LogError(logger, "CRITICAL: Failed to recover old DB after rename failure", reopenErr)
			logging.LogOperation(logger, "setting manager.gtfsDB to nil")
			manager.GtfsDB = nil

			manager.isHealthy = false
		}

		return err
	}

	dbConfig := gtfsdb.NewConfig(finalDBPath, manager.config.Env, manager.config.Verbose)
	client, err := gtfsdb.NewClient(dbConfig)

	if err != nil {
		logging.LogError(logger, "CRITICAL: Failed to create new GTFS client after database swap", err,
			slog.String("db_path", finalDBPath))
		logging.LogOperation(logger, "setting manager.gtfsDB to nil")
		manager.GtfsDB = nil

		manager.isHealthy = false
		return fmt.Errorf("failed to update GTFS database client: %w", err)
	}

	manager.gtfsData = newStaticData
	manager.GtfsDB = client
	manager.blockLayoverIndices = newBlockLayoverIndices
	manager.stopSpatialIndex = newStopSpatialIndex
	manager.lastUpdated = time.Now()

	manager.isHealthy = true

	logging.LogOperation(logger, "gtfs_static_data_updated_hot_swap",
		slog.String("source", manager.gtfsSource),
		slog.String("db_path", finalDBPath))

	return nil
}

// setStaticGTFS is used for initial load.
func (manager *Manager) setStaticGTFS(staticData *gtfs.Static) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.gtfsData = staticData
	manager.lastUpdated = time.Now()
	manager.isHealthy = true
	manager.blockLayoverIndices = buildBlockLayoverIndices(staticData)

	// Rebuild spatial index with updated data
	ctx := context.Background()
	if manager.GtfsDB != nil && manager.GtfsDB.Queries != nil {
		spatialIndex, err := buildStopSpatialIndex(ctx, manager.GtfsDB.Queries)
		if err == nil {
			manager.stopSpatialIndex = spatialIndex
		} else if manager.config.Verbose {
			logger := slog.Default().With(slog.String("component", "gtfs_manager"))
			logging.LogError(logger, "Failed to rebuild spatial index", err)
		}
	}

	if manager.config.Verbose {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogOperation(logger, "gtfs_data_set_successfully",
			slog.String("source", manager.gtfsSource),
			slog.Int("layover_indices_built", len(manager.blockLayoverIndices)))
	}
}
