package gtfsdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/go-gtfs"
	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/logging"
)

//go:embed schema.sql
var ddl string

// createDB creates a new SQLite database with tables for static GTFS data
func createDB(config Config) (*sql.DB, error) {
	if config.Env == appconf.Test && config.DBPath != ":memory:" {
		return nil, fmt.Errorf("test database must use in-memory storage, got path: %s", config.DBPath)
	}

	db, err := sql.Open("sqlite3", config.DBPath)
	if err != nil {
		return nil, err
	}

	// Configure SQLite performance settings immediately after opening
	ctx := context.Background()
	err = configureSQLitePerformance(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("error configuring SQLite performance: %w", err)
	}

	err = performDatabaseMigration(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("error performing database migration: %w", err)
	}

	// Configure connection pool settings
	configureConnectionPool(db, config)

	return db, nil
}

func performDatabaseMigration(ctx context.Context, db *sql.DB) error {
	statements := strings.Split(ddl, "-- migrate") // Split DDL into individual statements
	for _, stmt := range statements {
		trimmedStmt := strings.TrimSpace(stmt)
		if trimmedStmt == "" {
			continue // Skip empty statements
		}
		if _, err := db.ExecContext(ctx, trimmedStmt); err != nil {
			return fmt.Errorf("error executing DDL statement [%s]: %w", trimmedStmt, err)
		}
	}
	return nil
}

func (c *Client) processAndStoreGTFSDataWithSource(b []byte, source string) error {
	logger := slog.Default().With(slog.String("component", "gtfs_importer"))

	startTime := time.Now()
	defer func() {
		endTime := time.Now()

		c.importRuntime = endTime.Sub(startTime)

		logging.LogOperation(logger, "gtfs_data_import_completed",
			slog.Duration("duration", c.importRuntime),
			slog.String("source", source))
	}()

	// Calculate hash of the GTFS data
	hash := sha256.Sum256(b)
	hashStr := hex.EncodeToString(hash[:])

	ctx := context.Background()

	// Check if we already have this data imported
	existingMetadata, err := c.Queries.GetImportMetadata(ctx)
	if err == nil {
		// We have existing metadata, check if hash matches
		if existingMetadata.FileHash == hashStr && existingMetadata.FileSource == source {
			logging.LogOperation(logger, "gtfs_data_unchanged_skipping_import",
				slog.String("hash", hashStr[:8]))
			return nil
		}
		// Hash differs, we need to clear existing data and reimport
		logging.LogOperation(logger, "gtfs_data_changed_reimporting",
			slog.String("old_hash", existingMetadata.FileHash[:8]),
			slog.String("new_hash", hashStr[:8]))
		err = c.clearAllGTFSData(ctx)
		if err != nil {
			return fmt.Errorf("error clearing existing GTFS data: %w", err)
		}
	} else if err != nil && err != sql.ErrNoRows {
		// Some other error occurred
		return fmt.Errorf("error checking import metadata: %w", err)
	}
	// If err == sql.ErrNoRows, this is the first import, continue normally

	var staticCounts map[string]int

	staticData, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("retrieved static data (warnings: %d)\n", len(staticData.Warnings))
	fmt.Print("========\n\n")

	staticCounts = c.staticDataCounts(staticData)
	for k, v := range staticCounts {
		fmt.Printf("%s: %d\n", k, v)
	}

	fmt.Print("========\n\n")

	logging.LogOperation(logger, "starting_database_import")

	logging.LogOperation(logger, "inserting_agencies_and_routes",
		slog.Int("agencies", len(staticData.Agencies)),
		slog.Int("routes", len(staticData.Routes)))

	for _, a := range staticData.Agencies {
		params := CreateAgencyParams{
			ID:       a.Id,
			Name:     a.Name,
			Url:      a.Url,
			Timezone: a.Timezone,
			Lang:     toNullString(a.Language),
			Phone:    toNullString(a.Phone),
			FareUrl:  toNullString(a.FareUrl),
			Email:    toNullString(a.Email),
		}

		_, err := c.Queries.CreateAgency(ctx, params)
		if err != nil {
			return fmt.Errorf("unable to create agency: %w", err)
		}
	}

	singleAgencyID := ""
	if len(staticData.Agencies) == 1 {
		singleAgencyID = staticData.Agencies[0].Id
	}

	for _, r := range staticData.Routes {
		route := CreateRouteParams{
			ID:                r.Id,
			AgencyID:          pickFirstAvailable(r.Agency.Id, singleAgencyID),
			ShortName:         toNullString(r.ShortName),
			LongName:          toNullString(r.LongName),
			Desc:              toNullString(r.Description),
			Type:              int64(r.Type),
			Url:               toNullString(r.Url),
			Color:             toNullString(r.Color),
			TextColor:         toNullString(r.TextColor),
			ContinuousPickup:  toNullInt64(int64(r.ContinuousPickup)),
			ContinuousDropOff: toNullInt64(int64(r.ContinuousDropOff)),
		}

		_, err := c.Queries.CreateRoute(ctx, route)

		if err != nil {
			return fmt.Errorf("unable to create route: %w", err)
		}
	}

	var allStopParams []CreateStopParams
	for _, s := range staticData.Stops {
		var lat, lon float64
		if s.Latitude != nil {
			lat = *s.Latitude
		}
		if s.Longitude != nil {
			lon = *s.Longitude
		}
		params := CreateStopParams{
			ID:                 s.Id,
			Code:               toNullString(s.Code),
			Name:               toNullString(s.Name),
			Desc:               toNullString(s.Description),
			Lat:                lat,
			Lon:                lon,
			ZoneID:             toNullString(s.ZoneId),
			Url:                toNullString(s.Url),
			LocationType:       toNullInt64(int64(s.Type)),
			Timezone:           toNullString(s.Timezone),
			WheelchairBoarding: toNullInt64(int64(s.WheelchairBoarding)),
			PlatformCode:       toNullString(s.PlatformCode),
			Direction:          sql.NullString{}, // Will be computed later
		}

		allStopParams = append(allStopParams, params)
	}
	err = c.bulkInsertStops(ctx, allStopParams)
	if err != nil {
		return fmt.Errorf("unable to create stops: %w", err)
	}

	logging.LogOperation(logger, "agencies_and_routes_inserted",
		slog.Int("agencies", len(staticData.Agencies)),
		slog.Int("routes", len(staticData.Routes)))
	logging.LogOperation(logger, "inserting_calendar",
		slog.Int("count", len(staticData.Services)))

	for _, s := range staticData.Services {
		params := CreateCalendarParams{
			ID:        s.Id,
			Monday:    boolToInt(s.Monday),
			Tuesday:   boolToInt(s.Tuesday),
			Wednesday: boolToInt(s.Wednesday),
			Thursday:  boolToInt(s.Thursday),
			Friday:    boolToInt(s.Friday),
			Saturday:  boolToInt(s.Saturday),
			Sunday:    boolToInt(s.Sunday),
			StartDate: s.StartDate.Format("20060102"),
			EndDate:   s.EndDate.Format("20060102"),
		}

		_, err := c.Queries.CreateCalendar(ctx, params)
		if err != nil {
			return fmt.Errorf("unable to create calendar: %w", err)
		}
	}

	logging.LogOperation(logger, "calendar_inserted",
		slog.Int("count", len(staticData.Services)))

	var allTripParams []CreateTripParams
	for _, t := range staticData.Trips {
		// Handle optional shape - shapes.txt is optional in GTFS spec
		var shapeID string
		if t.Shape != nil {
			shapeID = t.Shape.ID
		}

		params := CreateTripParams{
			ID:                   t.ID,
			RouteID:              t.Route.Id,
			ServiceID:            t.Service.Id,
			TripHeadsign:         toNullString(t.Headsign),
			TripShortName:        toNullString(t.ShortName),
			DirectionID:          toNullInt64(int64(t.DirectionId)),
			BlockID:              toNullString(t.BlockID),
			ShapeID:              toNullString(shapeID),
			WheelchairAccessible: toNullInt64(int64(t.WheelchairAccessible)),
			BikesAllowed:         toNullInt64(int64(t.BikesAllowed)),
		}
		allTripParams = append(allTripParams, params)
	}
	err = c.bulkInsertTrips(ctx, allTripParams)
	if err != nil {
		return fmt.Errorf("unable to create trips: %w", err)
	}

	var allStopTimeParams []CreateStopTimeParams
	for _, t := range staticData.Trips {
		for _, st := range t.StopTimes {
			var shapeDistTraveled float64
			if st.ShapeDistanceTraveled != nil {
				shapeDistTraveled = *st.ShapeDistanceTraveled
			}

			params := CreateStopTimeParams{
				TripID:            t.ID,
				ArrivalTime:       int64(st.ArrivalTime),
				DepartureTime:     int64(st.DepartureTime),
				StopID:            st.Stop.Id,
				StopSequence:      int64(st.StopSequence),
				StopHeadsign:      toNullString(st.Headsign),
				PickupType:        toNullInt64(int64(st.PickupType)),
				DropOffType:       toNullInt64(int64(st.DropOffType)),
				ShapeDistTraveled: toNullFloat64(shapeDistTraveled),
				Timepoint:         toNullInt64(boolToInt(st.ExactTimes)),
			}

			allStopTimeParams = append(allStopTimeParams, params)
		}
	}
	err = c.bulkInsertStopTimes(ctx, allStopTimeParams)
	if err != nil {
		return fmt.Errorf("unable to create stop times: %w", err)
	}

	var allShapeParams []CreateShapeParams
	for _, s := range staticData.Shapes {
		for idx, pt := range s.Points {
			var distance float64
			if pt.Distance != nil {
				distance = *pt.Distance
			}

			params := CreateShapeParams{
				ShapeID:           s.ID,
				Lat:               pt.Latitude,
				Lon:               pt.Longitude,
				ShapePtSequence:   int64(idx),
				ShapeDistTraveled: toNullFloat64(distance),
			}
			allShapeParams = append(allShapeParams, params)
		}
	}
	err = c.bulkInsertShapes(ctx, allShapeParams)
	if err != nil {
		return fmt.Errorf("unable to create shapes: %w", err)
	}

	counts, err := c.TableCounts()
	if err != nil {
		logging.LogError(logger, "Error getting table counts", err)
		return fmt.Errorf("failed to get table counts: %w", err)
	}
	for k, v := range counts {
		fmt.Printf("%s: %d (Static matches? %v)\n", k, v, v == staticCounts[k])
	}

	logging.LogOperation(logger, "updating_import_metadata",
		slog.String("hash", hashStr[:8]),
		slog.String("source", source))

	_, err = c.Queries.UpsertImportMetadata(ctx, UpsertImportMetadataParams{
		FileHash:   hashStr,
		ImportTime: time.Now().Unix(),
		FileSource: source,
	})
	if err != nil {
		logging.LogError(logger, "Error updating import metadata", err)
		return fmt.Errorf("error updating import metadata: %w", err)
	}

	logging.LogOperation(logger, "import_metadata_updated_successfully")

	var allCalendarDateParams []CreateCalendarDateParams

	for _, service := range staticData.Services {
		// Process added dates (exception type 1)
		for _, date := range service.AddedDates {
			params := CreateCalendarDateParams{
				ServiceID:     service.Id,
				Date:          date.Format("20060102"),
				ExceptionType: 1,
			}
			allCalendarDateParams = append(allCalendarDateParams, params)
		}

		// Process removed dates (exception type 2)
		for _, date := range service.RemovedDates {
			params := CreateCalendarDateParams{
				ServiceID:     service.Id,
				Date:          date.Format("20060102"),
				ExceptionType: 2,
			}
			allCalendarDateParams = append(allCalendarDateParams, params)
		}
	}

	// Insert calendar dates into the database
	if len(allCalendarDateParams) > 0 {
		err = c.bulkInsertCalendarDates(ctx, allCalendarDateParams)
		if err != nil {
			logging.LogError(logger, "Unable to create calendar dates", err)
			return fmt.Errorf("unable to create calendar dates: %w", err)
		}
	}

	// Build BlockTripIndex after all trips and stop_times are inserted
	logging.LogOperation(logger, "building_block_trip_index")
	err = c.buildBlockTripIndex(ctx, staticData)
	if err != nil {
		logging.LogError(logger, "Unable to build block trip index", err)
		return fmt.Errorf("unable to build block trip index: %w", err)
	}
	logging.LogOperation(logger, "block_trip_index_built")

	return nil
}

// clearAllGTFSData clears all GTFS data from the database in the correct order to respect foreign key constraints
func (c *Client) clearAllGTFSData(ctx context.Context) error {
	// Delete in reverse order of dependencies to avoid foreign key constraint violations
	if err := c.Queries.ClearBlockTripEntries(ctx); err != nil {
		return fmt.Errorf("error clearing block_trip_entry: %w", err)
	}
	if err := c.Queries.ClearBlockTripIndices(ctx); err != nil {
		return fmt.Errorf("error clearing block_trip_index: %w", err)
	}
	if err := c.Queries.ClearStopTimes(ctx); err != nil {
		return fmt.Errorf("error clearing stop_times: %w", err)
	}
	if err := c.Queries.ClearShapes(ctx); err != nil {
		return fmt.Errorf("error clearing shapes: %w", err)
	}
	if err := c.Queries.ClearTrips(ctx); err != nil {
		return fmt.Errorf("error clearing trips: %w", err)
	}
	if err := c.Queries.ClearCalendar(ctx); err != nil {
		return fmt.Errorf("error clearing calendar: %w", err)
	}
	if err := c.Queries.ClearStops(ctx); err != nil {
		return fmt.Errorf("error clearing stops: %w", err)
	}
	if err := c.Queries.ClearRoutes(ctx); err != nil {
		return fmt.Errorf("error clearing routes: %w", err)
	}
	if err := c.Queries.ClearAgencies(ctx); err != nil {
		return fmt.Errorf("error clearing agencies: %w", err)
	}
	return nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func toNullInt64(i int64) sql.NullInt64 {
	if i != 0 {
		return sql.NullInt64{
			Int64: i,
			Valid: true,
		}
	}
	return sql.NullInt64{}
}

func toNullFloat64(f float64) sql.NullFloat64 {
	if f != 0 {
		return sql.NullFloat64{
			Float64: f,
			Valid:   true,
		}
	}
	return sql.NullFloat64{}
}

// toNullString converts a string to sql.NullString
func toNullString(s string) sql.NullString {
	return sql.NullString{
		String: s,
		Valid:  s != "",
	}
}

func pickFirstAvailable(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (c *Client) bulkInsertStops(ctx context.Context, stops []CreateStopParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_stops",
		slog.Int("count", len(stops)))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_stops")

	qtx := queries.WithTx(tx)
	for _, params := range stops {
		_, err := qtx.CreateStop(ctx, params)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logging.LogOperation(logger, "stops_inserted",
		slog.Int("count", len(stops)))

	return nil
}

func (c *Client) bulkInsertTrips(ctx context.Context, trips []CreateTripParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_trips",
		slog.Int("count", len(trips)))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_trips")

	qtx := queries.WithTx(tx)
	for _, params := range trips {
		_, err := qtx.CreateTrip(ctx, params)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logging.LogOperation(logger, "trips_inserted",
		slog.Int("count", len(trips)))

	return nil
}

// preparedStopTimeBatch holds a prepared SQL statement with its arguments
type preparedStopTimeBatch struct {
	query string
	args  []interface{}
	index int // Original index for ordering
	end   int // End position for progress logging
}

func (c *Client) bulkInsertStopTimes(ctx context.Context, stopTimes []CreateStopTimeParams) error {
	db := c.DB
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_stop_times",
		slog.Int("count", len(stopTimes)))

	// ===== PIPELINE: PARALLEL PREPARATION + SEQUENTIAL EXECUTION =====
	batchSize := c.config.GetBulkInsertBatchSize()
	const baseQuery = `INSERT INTO stop_times (
		trip_id, arrival_time, departure_time, stop_id, stop_sequence,
		stop_headsign, pickup_type, drop_off_type, shape_dist_traveled, timepoint
	) VALUES `

	// Calculate number of batches
	numBatches := (len(stopTimes) + batchSize - 1) / batchSize

	// Start database transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_stop_times")

	// Create channels for pipeline
	numWorkers := runtime.NumCPU()
	batchChan := make(chan int, numWorkers)
	resultsChan := make(chan preparedStopTimeBatch, numWorkers*4) // Larger buffer for pipeline

	// Start worker pool for parallel preparation
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batchIndex := range batchChan {
				// Check context for cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Calculate batch boundaries
				start := batchIndex * batchSize
				end := start + batchSize
				if end > len(stopTimes) {
					end = len(stopTimes)
				}
				batch := stopTimes[start:end]

				// Build multi-row INSERT query
				// SECURITY: Only use placeholders (?) for values. Never concatenate user input directly
				// into the query string to prevent SQL injection attacks.
				var query strings.Builder
				query.WriteString(baseQuery)
				args := make([]interface{}, 0, len(batch)*10)

				for j, params := range batch {
					if j > 0 {
						query.WriteString(", ")
					}
					query.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")

					args = append(args,
						params.TripID,
						params.ArrivalTime,
						params.DepartureTime,
						params.StopID,
						params.StopSequence,
						params.StopHeadsign,
						params.PickupType,
						params.DropOffType,
						params.ShapeDistTraveled,
						params.Timepoint,
					)
				}

				// Send prepared batch to results channel
				resultsChan <- preparedStopTimeBatch{
					query: query.String(),
					args:  args,
					index: batchIndex,
					end:   end,
				}
			}
		}()
	}

	// Feed batch indices to workers
	go func() {
		for i := 0; i < numBatches; i++ {
			batchChan <- i
		}
		close(batchChan)
	}()

	// Close results channel when all workers are done
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Execute batches as they're prepared (overlapping preparation and execution)
	// Collect batches and sort them to maintain insertion order
	preparedBatches := make([]preparedStopTimeBatch, 0, numBatches)
	for batch := range resultsChan {
		preparedBatches = append(preparedBatches, batch)
	}

	// Check if context was canceled during preparation
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Sort batches by index to maintain insertion order
	sort.Slice(preparedBatches, func(i, j int) bool {
		return preparedBatches[i].index < preparedBatches[j].index
	})

	logging.LogOperation(
		logger,
		"stop_times_progress",
		slog.Int("inserted", 0),
		slog.Int("total", len(stopTimes)),
	)

	// Execute sorted batches
	for _, batch := range preparedBatches {
		// Check context before executing
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the batch insert
		_, err := tx.ExecContext(ctx, batch.query, batch.args...)
		if err != nil {
			return fmt.Errorf("failed to insert stop_times batch: %w", err)
		}

		// Log progress every 100k records
		if (batch.end)%100000 == 0 || batch.end == len(stopTimes) {
			logging.LogOperation(logger, "stop_times_progress",
				slog.Int("inserted", batch.end),
				slog.Int("total", len(stopTimes)))
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logging.LogOperation(logger, "stop_times_inserted",
		slog.Int("count", len(stopTimes)))

	return nil
}

// preparedShapeBatch holds a prepared SQL statement with its arguments
type preparedShapeBatch struct {
	query string
	args  []interface{}
	index int // Original index for ordering
	end   int // End position for progress logging
}

func (c *Client) bulkInsertShapes(ctx context.Context, shapes []CreateShapeParams) error {
	db := c.DB
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_shapes",
		slog.Int("count", len(shapes)))

	// ===== PHASE 1: PARALLEL STATEMENT PREPARATION =====
	batchSize := c.config.GetBulkInsertBatchSize()
	const baseQuery = `INSERT INTO shapes (
		shape_id, lat, lon, shape_pt_sequence, shape_dist_traveled
	) VALUES `

	// Calculate number of batches
	numBatches := (len(shapes) + batchSize - 1) / batchSize

	// Create worker pool for parallel statement preparation
	numWorkers := runtime.NumCPU()
	batchChan := make(chan int, numWorkers)
	resultsChan := make(chan preparedShapeBatch, numWorkers*2)

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batchIndex := range batchChan {
				// Check context for cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Calculate batch boundaries
				start := batchIndex * batchSize
				end := start + batchSize
				if end > len(shapes) {
					end = len(shapes)
				}
				batch := shapes[start:end]

				// Build multi-row INSERT query
				// SECURITY: Only use placeholders (?) for values. Never concatenate user input directly
				// into the query string to prevent SQL injection attacks.
				var query strings.Builder
				query.WriteString(baseQuery)
				args := make([]interface{}, 0, len(batch)*5)

				for j, params := range batch {
					if j > 0 {
						query.WriteString(", ")
					}
					query.WriteString("(?, ?, ?, ?, ?)")

					args = append(args,
						params.ShapeID,
						params.Lat,
						params.Lon,
						params.ShapePtSequence,
						params.ShapeDistTraveled,
					)
				}

				// Send prepared batch to results channel
				resultsChan <- preparedShapeBatch{
					query: query.String(),
					args:  args,
					index: batchIndex,
					end:   end,
				}
			}
		}()
	}

	// Feed batch indices to workers
	go func() {
		for i := 0; i < numBatches; i++ {
			batchChan <- i
		}
		close(batchChan)
	}()

	// ===== PHASE 2: COLLECT PREPARED BATCHES =====
	// Close results channel when all workers are done
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect prepared batches as they arrive with progress logging
	preparedBatches := make([]preparedShapeBatch, 0, numBatches)
	lastLoggedCount := 0
	for batch := range resultsChan {
		preparedBatches = append(preparedBatches, batch)

		// Log preparation progress every 50 batches (150k records with batch size 3000)
		if len(preparedBatches)-lastLoggedCount >= 50 {
			logging.LogOperation(logger, "shapes_preparation_progress",
				slog.Int("batches_prepared", len(preparedBatches)),
				slog.Int("total_batches", numBatches))
			lastLoggedCount = len(preparedBatches)
		}
	}

	logging.LogOperation(logger, "shapes_preparation_complete",
		slog.Int("batches_prepared", len(preparedBatches)),
		slog.Int("total_batches", numBatches))

	// Check if context was canceled during preparation
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Sort batches by index to maintain insertion order
	sort.Slice(preparedBatches, func(i, j int) bool {
		return preparedBatches[i].index < preparedBatches[j].index
	})

	// ===== PHASE 3: SEQUENTIAL DATABASE EXECUTION =====
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_shapes")

	for _, batch := range preparedBatches {
		// Check context before executing
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the batch insert
		_, err := tx.ExecContext(ctx, batch.query, batch.args...)
		if err != nil {
			return fmt.Errorf("failed to insert shapes batch: %w", err)
		}

		// Log progress every 50k records
		if (batch.end)%50000 == 0 || batch.end == len(shapes) {
			logging.LogOperation(logger, "shapes_progress",
				slog.Int("inserted", batch.end),
				slog.Int("total", len(shapes)))
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logging.LogOperation(logger, "shapes_inserted",
		slog.Int("count", len(shapes)))

	return nil
}

func (c *Client) bulkInsertCalendarDates(ctx context.Context, calendarDates []CreateCalendarDateParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_calendar_dates")

	qtx := queries.WithTx(tx)
	for _, params := range calendarDates {
		_, err := qtx.CreateCalendarDate(ctx, params)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// configureSQLitePerformance applies PRAGMA settings to optimize SQLite performance
// for bulk GTFS data imports and queries.
func configureSQLitePerformance(ctx context.Context, db *sql.DB) error {
	pragmas := []struct {
		name        string
		description string
	}{
		// Increase cache size to 64MB (negative value means KB)
		{"PRAGMA cache_size=-64000", "Set cache size to 64MB"},
		// Store temp tables and indices in memory for faster operations
		{"PRAGMA temp_store=MEMORY", "Store temporary data in memory"},
	}

	logger := slog.Default().With(slog.String("component", "sqlite_performance"))

	for _, pragma := range pragmas {
		_, err := db.ExecContext(ctx, pragma.name)
		if err != nil {
			logging.LogError(logger, fmt.Sprintf("Failed to set %s", pragma.description), err)
			return fmt.Errorf("failed to execute %s: %w", pragma.name, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	logging.LogOperation(logger, "sqlite_performance_settings_applied",
		slog.Int("pragma_count", len(pragmas)))

	return nil
}

// configureConnectionPool sets up appropriate connection pool settings for SQLite.
//
// IMPORTANT LIMITATIONS:
//
//   - :memory: databases: MaxOpenConns=1 to ensure data consistency. This SERIALIZES
//     all database access, which can become a bottleneck under high concurrency. Each
//     connection to a :memory: database creates a separate database instance, so we
//     must limit to 1 connection to maintain data integrity.
//
//   - File databases: MaxOpenConns=25 to allow concurrent access. SQLite with WAL mode
//     supports concurrent readers and a single writer.
//
// For production deployments with high concurrency requirements, consider using a
// file-based database instead of :memory: to take advantage of concurrent connections.
func configureConnectionPool(db *sql.DB, config Config) {
	// For :memory: databases, use only 1 connection since each connection
	// gets its own separate in-memory database
	if config.DBPath == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		// Set maximum number of open connections to 25
		db.SetMaxOpenConns(25)

		// Set maximum number of idle connections to 5
		db.SetMaxIdleConns(5)

		// Set maximum lifetime of connections to 5 minutes
		db.SetConnMaxLifetime(5 * time.Minute)
	}
}

// blockTripIndexKey represents the grouping key for BlockTripIndex
type blockTripIndexKey struct {
	serviceIDs      string // comma-separated sorted service IDs
	stopSequenceKey string // pipe-separated ordered stop IDs
}

// buildBlockTripIndex creates BlockTripIndex entries by grouping trips with identical
// service IDs and stop sequences.
func (c *Client) buildBlockTripIndex(ctx context.Context, staticData *gtfs.Static) error {
	logger := slog.Default().With(slog.String("component", "block_trip_index_builder"))

	// Build terminal layover location for each trip
	type tripInfo struct {
		tripID        string
		routeID       string
		serviceID     string
		blockID       string
		layoverStopID string
	}

	tripMap := make(map[string]*tripInfo)

	for _, trip := range staticData.Trips {
		if len(trip.StopTimes) == 0 {
			continue
		}

		// Get the FIRST stop - this is the layover location where the trip starts
		firstStop := trip.StopTimes[0].Stop.Id

		tripMap[trip.ID] = &tripInfo{
			tripID:        trip.ID,
			routeID:       trip.Route.Id,
			serviceID:     trip.Service.Id,
			blockID:       trip.BlockID,
			layoverStopID: firstStop,
		}
	}

	// Group trips by (serviceID, layoverStopID)
	indexGroups := make(map[blockTripIndexKey][]*tripInfo)

	for _, info := range tripMap {
		key := blockTripIndexKey{
			serviceIDs:      info.serviceID,
			stopSequenceKey: info.layoverStopID, // Use first stop (layover) as the key
		}
		indexGroups[key] = append(indexGroups[key], info)
	}

	logging.LogOperation(logger, "grouped_trips_into_indices",
		slog.Int("total_trips", len(tripMap)),
		slog.Int("unique_indices", len(indexGroups)))

	// Create block_trip_index and block_trip_entry records
	// BlockLayoverIndex groups trips by first stop (layover location where trips begin)
	createdAt := time.Now().Unix()

	for key, trips := range indexGroups {
		// Create unique index key (service ID + layover stop)
		indexKey := fmt.Sprintf("%s|%s", key.serviceIDs, key.stopSequenceKey)

		indexID, err := c.Queries.CreateBlockTripIndex(ctx, CreateBlockTripIndexParams{
			IndexKey:        indexKey,
			ServiceIds:      key.serviceIDs,
			StopSequenceKey: key.stopSequenceKey,
			CreatedAt:       createdAt,
		})
		if err != nil {
			return fmt.Errorf("failed to create block trip index: %w", err)
		} // Sort trips within the group by block_id and then trip_id for deterministic ordering
		sort.Slice(trips, func(i, j int) bool {
			if trips[i].blockID != trips[j].blockID {
				return trips[i].blockID < trips[j].blockID
			}
			return trips[i].tripID < trips[j].tripID
		})

		// Insert block_trip_entry records for each trip in this index
		for sequence, trip := range trips {
			err = c.Queries.CreateBlockTripEntry(ctx, CreateBlockTripEntryParams{
				BlockTripIndexID:  indexID,
				TripID:            trip.tripID,
				BlockID:           toNullString(trip.blockID),
				ServiceID:         trip.serviceID,
				BlockTripSequence: int64(sequence),
			})
			if err != nil {
				return fmt.Errorf("failed to create block trip entry: %w", err)
			}
		}
	}

	totalEntries := 0
	for _, trips := range indexGroups {
		totalEntries += len(trips)
	}

	logging.LogOperation(logger, "block_trip_index_creation_complete",
		slog.Int("indices_created", len(indexGroups)),
		slog.Int("entries_created", totalEntries))

	return nil
}
