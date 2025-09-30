package gtfsdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
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

	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	err = performDatabaseMigration(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("error performing database migration: %w", err)
	}

	// Configure connection pool settings
	configureConnectionPool(db)

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

		if c.config.verbose {
			logging.LogOperation(logger, "gtfs_data_import_completed",
				slog.Duration("duration", c.importRuntime),
				slog.String("source", source))
		}
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
			if c.config.verbose {
				logging.LogOperation(logger, "gtfs_data_unchanged_skipping_import",
					slog.String("hash", hashStr[:8]))
			}
			return nil
		}
		// Hash differs, we need to clear existing data and reimport
		if c.config.verbose {
			logging.LogOperation(logger, "gtfs_data_changed_reimporting",
				slog.String("old_hash", existingMetadata.FileHash[:8]),
				slog.String("new_hash", hashStr[:8]))
		}
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

	if c.config.verbose {
		fmt.Printf("retrieved static data (warnings: %d)\n", len(staticData.Warnings))
		fmt.Print("========\n\n")

		staticCounts = c.staticDataCounts(staticData)
		for k, v := range staticCounts {
			fmt.Printf("%s: %d\n", k, v)
		}

		fmt.Print("========\n\n")
	}

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
		params := CreateStopParams{
			ID:                 s.Id,
			Code:               toNullString(s.Code),
			Name:               toNullString(s.Name),
			Desc:               toNullString(s.Description),
			Lat:                *s.Latitude,
			Lon:                *s.Longitude,
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

	var allTripParams []CreateTripParams
	for _, t := range staticData.Trips {
		params := CreateTripParams{
			ID:                   t.ID,
			RouteID:              t.Route.Id,
			ServiceID:            t.Service.Id,
			TripHeadsign:         toNullString(t.Headsign),
			TripShortName:        toNullString(t.ShortName),
			DirectionID:          toNullInt64(int64(t.DirectionId)),
			BlockID:              toNullString(t.BlockID),
			ShapeID:              toNullString(t.Shape.ID),
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

	if c.config.verbose {
		counts, err := c.TableCounts()
		if err != nil {
			logging.LogError(logger, "Error getting table counts", err)
			return fmt.Errorf("failed to get table counts: %w", err)
		}
		for k, v := range counts {
			fmt.Printf("%s: %d (Static matches? %v)\n", k, v, v == staticCounts[k])
		}
	}

	// Update import metadata to record successful import
	if c.config.verbose {
		logging.LogOperation(logger, "updating_import_metadata",
			slog.String("hash", hashStr[:8]),
			slog.String("source", source))
	}
	_, err = c.Queries.UpsertImportMetadata(ctx, UpsertImportMetadataParams{
		FileHash:   hashStr,
		ImportTime: time.Now().Unix(),
		FileSource: source,
	})
	if err != nil {
		logging.LogError(logger, "Error updating import metadata", err)
		return fmt.Errorf("error updating import metadata: %w", err)
	}
	if c.config.verbose {
		logging.LogOperation(logger, "import_metadata_updated_successfully")
	}

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
		err = c.buldInsertCalendarDates(ctx, allCalendarDateParams)
		if err != nil {
			logging.LogError(logger, "Unable to create calendar dates", err)
			return fmt.Errorf("unable to create calendar dates: %w", err)
		}
	}

	return nil
}

// clearAllGTFSData clears all GTFS data from the database in the correct order to respect foreign key constraints
func (c *Client) clearAllGTFSData(ctx context.Context) error {
	// Delete in reverse order of dependencies to avoid foreign key constraint violations
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
	return tx.Commit()
}

func (c *Client) bulkInsertTrips(ctx context.Context, trips []CreateTripParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

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
	return tx.Commit()
}

func (c *Client) bulkInsertStopTimes(ctx context.Context, stopTimes []CreateStopTimeParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_stop_times")

	qtx := queries.WithTx(tx)
	for _, params := range stopTimes {
		_, err := qtx.CreateStopTime(ctx, params)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Client) bulkInsertShapes(ctx context.Context, shapes []CreateShapeParams) error {
	db := c.DB
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "bulk_insert_shapes")

	qtx := queries.WithTx(tx)
	for _, params := range shapes {
		_, err := qtx.CreateShape(ctx, params)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Client) buldInsertCalendarDates(ctx context.Context, calendarDates []CreateCalendarDateParams) error {
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

// configureConnectionPool applies connection pool settings to the database
func configureConnectionPool(db *sql.DB) {
	// Set maximum number of open connections to 25
	db.SetMaxOpenConns(25)

	// Set maximum number of idle connections to 5
	db.SetMaxIdleConns(5)

	// Set maximum lifetime of connections to 5 minutes
	db.SetConnMaxLifetime(5 * time.Minute)
}
