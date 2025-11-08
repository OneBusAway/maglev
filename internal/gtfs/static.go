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

func rawGtfsData(source string, isLocalFile bool, authHeaderKey, authHeaderValue string) ([]byte, error) {
	var b []byte
	var err error

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
		if authHeaderKey != "" && authHeaderValue != "" {
			req.Header.Set(authHeaderKey, authHeaderValue)
		}

		client := &http.Client{}
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
	return b, nil
}

func buildGtfsDB(config Config, isLocalFile bool) (*gtfsdb.Client, error) {
	dbConfig := gtfsdb.NewConfig(config.GTFSDataPath, config.Env, config.Verbose)
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
		logging.LogError(logger, "Failed to precompute stop directions", err)
	}

	return client, nil
}

// loadGTFSData loads and parses GTFS data from either a URL or a local file
func loadGTFSData(source string, isLocalFile bool, authHeaderKey, authHeaderValue string) (*gtfs.Static, error) {
	b, err := rawGtfsData(source, isLocalFile, authHeaderKey, authHeaderValue)
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
			// Create a context with timeout for the download
			_, cancel := context.WithTimeout(context.Background(), 60*time.Second)

			// Download and parse the GTFS feed
			staticData, err := loadGTFSData(manager.gtfsSource, false, manager.config.StaticAuthHeaderKey, manager.config.StaticAuthHeaderValue)
			cancel() // Always cancel the context when done

			if err != nil {
				// Log error but don't crash the application
				logging.LogError(logger, "Error updating GTFS data", err,
					slog.String("source", manager.gtfsSource))
				continue
			}

			// Update the GTFS data in the manager
			logging.LogOperation(logger, "gtfs_static_data_updated",
				slog.String("source", manager.gtfsSource))
			manager.setStaticGTFS(staticData)
		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_static_gtfs_updates")
			return
		}
	}
}

func (manager *Manager) setStaticGTFS(staticData *gtfs.Static) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()

	manager.gtfsData = staticData
	manager.lastUpdated = time.Now()

	manager.blockLayoverIndices = buildBlockLayoverIndices(staticData)

	if manager.config.Verbose {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogOperation(logger, "gtfs_data_updated_successfully",
			slog.String("source", manager.gtfsSource),
			slog.Int("layover_indices_built", len(manager.blockLayoverIndices)))
	}
}
