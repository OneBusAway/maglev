package gtfs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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
// Only updates if the source is a URL, not a local file
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
				continue
			}

		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_static_gtfs_updates")
			return
		}
	}
}

// ForceUpdate performs a hot swap update of the GTFS data.
// It is public to allow for manual triggering (e.g. in tests or admin endpoints).
func (manager *Manager) ForceUpdate(ctx context.Context) error {
	logger := slog.Default().With(slog.String("component", "gtfs_updater"))

	// 1. Fetch and Parse new Data
	// FIX: Use manager.isLocalFile to support testing with local files
	newStaticData, err := loadGTFSData(manager.gtfsSource, manager.isLocalFile, manager.config)
	if err != nil {
		logging.LogError(logger, "Error updating GTFS data (load)", err,
			slog.String("source", manager.gtfsSource))
		return err
	}

	// 2. Build new DB
	// Generate a unique filename for the new DB
	timestamp := time.Now().Format("20060102_150405")
	newDBPath := fmt.Sprintf("%s_%s.db", manager.config.GTFSDataPath, timestamp)

	// FIX: Use manager.isLocalFile here too
	newGtfsDB, err := buildGtfsDB(manager.config, manager.isLocalFile, newDBPath)
	if err != nil {
		logging.LogError(logger, "Error building new GTFS DB", err)
		return err
	}

	// 3. Build Indices
	newBlockLayoverIndices := buildBlockLayoverIndices(newStaticData)
	newStopSpatialIndex, err := buildStopSpatialIndex(ctx, newGtfsDB.Queries)
	if err != nil {
		logging.LogError(logger, "Error building spatial index", err)
		newGtfsDB.Close()
		os.Remove(newDBPath)
		return err
	}

	// Phase 2: Swap (Write Lock)
	manager.staticMutex.Lock()

	oldGtfsDB := manager.GtfsDB
	
	manager.gtfsData = newStaticData
	manager.GtfsDB = newGtfsDB
	manager.blockLayoverIndices = newBlockLayoverIndices
	manager.stopSpatialIndex = newStopSpatialIndex
	manager.lastUpdated = time.Now()

	manager.staticMutex.Unlock()

	logging.LogOperation(logger, "gtfs_static_data_updated_hot_swap",
		slog.String("source", manager.gtfsSource),
		slog.String("new_db", newDBPath))

	// Phase 3: Cleanup (No Lock)
	if oldGtfsDB != nil {
		oldDBPath := oldGtfsDB.GetDBPath()
		if err := oldGtfsDB.Close(); err != nil {
			logging.LogError(logger, "Error closing old GTFS DB", err)
		} else {
            if oldDBPath != "" {
                 if err := os.Remove(oldDBPath); err != nil {
                     logging.LogError(logger, "Error removing old GTFS DB file", err, slog.String("path", oldDBPath))
                 } else {
                     logging.LogOperation(logger, "removed_old_gtfs_db_file", slog.String("path", oldDBPath))
                 }
            }
        }
	}
	
	return nil
}

// setStaticGTFS is used for initial load. 
// For updates, use the hot swap logic in updateStaticGTFS
func (manager *Manager) setStaticGTFS(staticData *gtfs.Static) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.gtfsData = staticData
	manager.lastUpdated = time.Now()

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
