package gtfsdb

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/nulls"
)

//go:embed schema.sql
var ddl string

// GtfsData bundles parsed static GTFS data with the hash that identifies the
// feed in import_metadata. The hash is used to skip reimport when the feed is
// unchanged.
type GtfsData struct {
	Static *gtfs.Static
	Hash   string
	Source string
}

// ParseGtfsData hashes, parses, and structurally validates GTFS zip bytes.
// The given source is stored on the returned struct for later use by StoreGtfsData.
func ParseGtfsData(b []byte, source string) (*GtfsData, error) {
	hash := sha256.Sum256(b)
	return ParseGtfsDataWithHash(b, hex.EncodeToString(hash[:]), source)
}

// ParseGtfsDataWithHash parses and structurally validates GTFS zip bytes whose
// identifying hash the caller has already computed.
func ParseGtfsDataWithHash(b []byte, hash, source string) (*GtfsData, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_parser"))
	logging.LogOperation(logger, "parsing_gtfs_data", slog.Int("bytes", len(b)))
	staticData, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("error parsing GTFS data: %w", err)
	}

	if err := ValidateAndFilterGTFSData(staticData, logger); err != nil {
		return nil, fmt.Errorf("GTFS validation failed: %w", err)
	}

	return &GtfsData{Static: staticData, Hash: hash, Source: source}, nil
}

// metricsWrapper wraps *sql.DB for metric reporting purposes
type metricsWrapper struct {
	db           *sql.DB
	logger       *slog.Logger
	queryMetrics DBQueryMetricsRecorder
}

func newMetricsWrapper(db *sql.DB) *metricsWrapper {
	return &metricsWrapper{
		db:     db,
		logger: slog.Default().With(slog.String("component", "db_metrics_wrapper")),
	}
}

func (s *metricsWrapper) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	s.recordQueryMetrics("exec", query, err)
	return res, err
}

func (s *metricsWrapper) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	// PrepareContext is not instrumented; latency-significant work happens
	// at execution time via ExecContext/QueryContext/QueryRowContext.
	return s.db.PrepareContext(ctx, query)
}

func (s *metricsWrapper) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	s.recordQueryMetrics("query", query, err)
	return rows, err
}

func (s *metricsWrapper) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := s.db.QueryRowContext(ctx, query, args...)
	// Note: QueryRowContext defers errors to row.Scan(), so err is always nil here.
	// query_row metrics always report status="ok". See PR description for follow-up plan.
	s.recordQueryMetrics("query_row", query, nil)
	return row
}

func (s *metricsWrapper) recordQueryMetrics(op, query string, err error) {
	s.queryMetrics.RecordDBQuery(extractQueryName(query), op, err)
}

func extractQueryName(query string) string {
	const prefix = "-- name:"

	for _, line := range strings.Split(query, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			nameAndType := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			fields := strings.Fields(nameAndType)
			if len(fields) > 0 && fields[0] != "" {
				return fields[0]
			}
			return "unknown"
		}
		// SQL body started without a sqlc name header.
		if !strings.HasPrefix(trimmed, "--") {
			break
		}
	}

	return "unknown"
}

// trimQuery truncates a query to 120 characters for concise logging.
func trimQuery(q string) string {
	q = strings.Join(strings.Fields(q), " ") // collapse whitespace
	runes := []rune(q)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return q
}

// createDB creates a new SQLite database with tables for static GTFS data
func createDB(config Config) (*sql.DB, error) {
	if config.Env == appconf.Test && config.DBPath != ":memory:" {
		return nil, fmt.Errorf("test database must use in-memory storage, got path: %s", config.DBPath)
	}

	db, err := sql.Open(DriverName, DSN(config.DBPath))
	if err != nil {
		return nil, err
	}

	// Configure SQLite performance settings immediately after opening
	ctx := context.Background()
	err = configureSQLitePerformance(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("error configuring SQLite performance: %w", err)
	}

	err = performDatabaseMigration(ctx, db)
	if err != nil {
		_ = db.Close()
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

// withTransaction executes the given function within a transaction.
// If tx is non-nil, it uses the provided transaction and does not commit.
// When tx is non-nil, the caller is responsible for committing or rolling back the transaction on error.
// If tx is nil, it starts a new transaction, ensures rollback on error, and commits on success.
func (c *Client) withTransaction(ctx context.Context, tx *sql.Tx, label string, fn func(*sql.Tx) error) error {
	if tx != nil {
		return fn(tx)
	}

	newTx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for %s: %w", label, err)
	}

	logger := slog.Default().With(slog.String("component", "bulk_insert"))
	defer logging.SafeRollbackWithLogging(newTx, logger, label)

	if err := fn(newTx); err != nil {
		return err
	}

	if err := newTx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction for %s: %w", label, err)
	}
	return nil
}

// HasImported reports whether the database already holds the feed with this
// hash from this source, so a caller can skip parsing it.
func (c *Client) HasImported(ctx context.Context, hash, source string) (bool, error) {
	metadata, err := c.Queries.GetImportMetadata(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error checking import metadata: %w", err)
	}
	return isSameFeed(metadata, hash, source), nil
}

// isSameFeed reports whether import_metadata describes the feed with this hash
// from this source.
func isSameFeed(metadata ImportMetadatum, hash, source string) bool {
	return metadata.FileHash == hash && metadata.FileSource == source
}

func (c *Client) StoreGtfsData(ctx context.Context, data *GtfsData) (bool, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_importer"))

	startTime := time.Now()
	defer func() {
		endTime := time.Now()

		c.importRuntime = endTime.Sub(startTime)

		logging.LogOperation(logger, "gtfs_data_import_completed",
			slog.Duration("duration_ms", c.importRuntime),
			slog.String("source", data.Source))
	}()

	// 1. Check if we already have this data imported
	var hasExisting bool
	existingMetadata, err := c.Queries.GetImportMetadata(ctx)
	if err == nil {
		hasExisting = true
		// We have existing metadata, check if hash matches
		if isSameFeed(existingMetadata, data.Hash, data.Source) {
			logging.LogOperation(logger, "gtfs_data_unchanged_skipping_import",
				slog.String("hash", data.Hash[:8]))
			return false, nil
		}
	} else if err != sql.ErrNoRows {
		// Some other error occurred
		return false, fmt.Errorf("error checking import metadata: %w", err)
	}

	logging.LogOperation(logger, "retrieved_static_data", slog.Int("warnings", len(data.Static.Warnings)))

	staticCounts := c.staticDataCounts(data.Static)
	for k, v := range staticCounts {
		logging.LogOperation(logger, "static_data_count", slog.String("entity_type", k), slog.Int("count", v))
	}

	logging.LogOperation(logger, "starting_database_import")

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error starting import transaction: %w", err)
	}
	defer logging.SafeRollbackWithLogging(tx, logger, "StoreGtfsData")

	qtx := c.Queries.WithTx(tx)

	// Clear the old data inside the same transaction so the import is atomic
	if hasExisting {
		logging.LogOperation(logger, "gtfs_data_changed_reimporting",
			slog.String("old_hash", existingMetadata.FileHash[:8]),
			slog.String("new_hash", data.Hash[:8]))
		if err := c.clearAllGTFSDataWithQueries(ctx, qtx); err != nil {
			return false, fmt.Errorf("error clearing existing GTFS data: %w", err)
		}
	}

	logging.LogOperation(logger, "inserting_agencies_and_routes",
		slog.Int("agencies", len(data.Static.Agencies)),
		slog.Int("routes", len(data.Static.Routes)))

	for _, a := range data.Static.Agencies {
		params := CreateAgencyParams{
			ID:       a.Id,
			Name:     a.Name,
			Url:      a.Url,
			Timezone: a.Timezone,
			Lang:     nulls.String(a.Language),
			Phone:    nulls.String(a.Phone),
			FareUrl:  nulls.String(a.FareUrl),
			Email:    nulls.String(a.Email),
		}

		_, err := qtx.CreateAgency(ctx, params)
		if err != nil {
			return false, fmt.Errorf("unable to create agency: %w", err)
		}
	}

	singleAgencyID := ""
	if len(data.Static.Agencies) == 1 {
		singleAgencyID = data.Static.Agencies[0].Id
	}

	for _, r := range data.Static.Routes {
		route := CreateRouteParams{
			ID:                r.Id,
			AgencyID:          pickFirstAvailable(r.Agency.Id, singleAgencyID),
			ShortName:         nulls.String(r.ShortName),
			LongName:          nulls.String(r.LongName),
			Desc:              nulls.String(r.Description),
			Type:              int64(r.Type),
			Url:               nulls.String(r.Url),
			Color:             nulls.NonEmptyString(r.Color),
			TextColor:         nulls.NonEmptyString(r.TextColor),
			ContinuousPickup:  toNullInt64(int64(r.ContinuousPickup)),
			ContinuousDropOff: toNullInt64(int64(r.ContinuousDropOff)),
		}

		_, err := qtx.CreateRoute(ctx, route)

		if err != nil {
			return false, fmt.Errorf("unable to create route: %w", err)
		}
	}

	var allStopParams []CreateStopParams
	for _, s := range data.Static.Stops {
		// Skip stops without coordinates to prevent nil pointer dereference and avoid
		// storing invalid (0,0) placeholder coordinates that would contaminate spatial
		// indexing and API responses. Per GTFS spec, lat/lon are optional for generic
		// nodes (type=3) and boarding areas (type=4), which are used for modeling
		// pathways within stations.
		//
		// See: https://github.com/OneBusAway/maglev/pull/209
		//
		// Future: If pathways or station accessibility features are needed, consider
		// making lat/lon nullable in the schema and updating handlers accordingly.
		if s.Latitude == nil || s.Longitude == nil {
			continue
		}
		parentStation := ""
		if s.Parent != nil {
			parentStation = s.Parent.Id
		}
		params := CreateStopParams{
			ID: s.Id,
			// [nulls.NonEmptyString] turns empty codes to sql.NullString with Valid: false
			// so [nulls.StringOrDefault] correctly falls back to the raw stop entity ID
			// when building stop responses.
			Code:               nulls.NonEmptyString(s.Code),
			Name:               nulls.String(s.Name),
			Desc:               nulls.String(s.Description),
			Lat:                *s.Latitude,
			Lon:                *s.Longitude,
			ZoneID:             nulls.String(s.ZoneId),
			Url:                nulls.String(s.Url),
			LocationType:       toNullInt64(int64(s.Type)),
			Timezone:           nulls.String(s.Timezone),
			WheelchairBoarding: toNullInt64(int64(s.WheelchairBoarding)),
			PlatformCode:       nulls.String(s.PlatformCode),
			Direction:          sql.NullString{}, // Will be computed later
			ParentStation:      nulls.NonEmptyString(parentStation),
		}

		allStopParams = append(allStopParams, params)
	}
	if err := c.bulkInsertStops(ctx, allStopParams, tx); err != nil {
		return false, fmt.Errorf("unable to create stops: %w", err)
	}

	logging.LogOperation(logger, "agencies_and_routes_inserted",
		slog.Int("agencies", len(data.Static.Agencies)),
		slog.Int("routes", len(data.Static.Routes)))
	logging.LogOperation(logger, "inserting_calendar",
		slog.Int("count", len(data.Static.Services)))

	for _, s := range data.Static.Services {
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

		_, err := qtx.CreateCalendar(ctx, params)
		if err != nil {
			return false, fmt.Errorf("unable to create calendar: %w", err)
		}
	}

	logging.LogOperation(logger, "calendar_inserted",
		slog.Int("count", len(data.Static.Services)))

	var allTripParams []CreateTripParams
	for _, t := range data.Static.Trips {
		// Handle optional shape - shapes.txt is optional in GTFS spec
		var shapeID string
		if t.Shape != nil {
			shapeID = t.Shape.ID
		}

		params := CreateTripParams{
			ID:                   t.ID,
			RouteID:              t.Route.Id,
			ServiceID:            t.Service.Id,
			TripHeadsign:         nulls.String(t.Headsign),
			TripShortName:        nulls.String(t.ShortName),
			DirectionID:          gtfsDirectionIDToDB(t.DirectionId),
			BlockID:              nulls.String(t.BlockID),
			ShapeID:              nulls.NonEmptyString(shapeID),
			WheelchairAccessible: toNullInt64(int64(t.WheelchairAccessible)),
			BikesAllowed:         toNullInt64(int64(t.BikesAllowed)),
		}
		allTripParams = append(allTripParams, params)
	}
	if err := c.bulkInsertTrips(ctx, allTripParams, tx); err != nil {
		return false, fmt.Errorf("unable to create trips: %w", err)
	}

	if err := c.bulkInsertStopTimes(ctx, stopTimeRows(data.Static.Trips), tx); err != nil {
		return false, fmt.Errorf("unable to create stop times: %w", err)
	}

	// Collect frequency entries from all trips
	var allFrequencyParams []CreateFrequencyParams
	for _, t := range data.Static.Trips {
		for _, f := range t.Frequencies {
			params := CreateFrequencyParams{
				TripID:      t.ID,
				StartTime:   int64(f.StartTime),
				EndTime:     int64(f.EndTime),
				HeadwaySecs: int64(f.Headway.Seconds()),
				ExactTimes:  int64(f.ExactTimes),
			}
			allFrequencyParams = append(allFrequencyParams, params)
		}
	}
	if len(allFrequencyParams) > 0 {
		if err := c.bulkInsertFrequencies(ctx, allFrequencyParams, tx); err != nil {
			return false, fmt.Errorf("unable to create frequencies: %w", err)
		}
	}

	if err := c.bulkInsertShapes(ctx, shapeRows(data.Static.Shapes), tx); err != nil {
		return false, fmt.Errorf("unable to create shapes: %w", err)
	}

	logging.LogOperation(logger, "updating_import_metadata",
		slog.String("hash", data.Hash[:8]),
		slog.String("source", data.Source))

	_, err = qtx.UpsertImportMetadata(ctx, UpsertImportMetadataParams{
		FileHash:   data.Hash,
		ImportTime: time.Now().UnixNano(),
		FileSource: data.Source,
	})
	if err != nil {
		logging.LogError(logger, "Error updating import metadata", err)
		return false, fmt.Errorf("error updating import metadata: %w", err)
	}

	logging.LogOperation(logger, "import_metadata_updated_successfully")

	var allCalendarDateParams []CreateCalendarDateParams

	for _, service := range data.Static.Services {
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

	if len(allCalendarDateParams) > 0 {
		if err := c.bulkInsertCalendarDates(ctx, allCalendarDateParams, tx); err != nil {
			logging.LogError(logger, "Unable to create calendar dates", err)
			return false, fmt.Errorf("unable to create calendar dates: %w", err)
		}
	}

	logging.LogOperation(logger, "building_block_trip_index")
	if err := c.buildBlockTripIndex(ctx, data.Static, tx); err != nil {
		logging.LogError(logger, "Unable to build block trip index", err)
		return false, fmt.Errorf("unable to build block trip index: %w", err)
	}
	logging.LogOperation(logger, "block_trip_index_built")

	logging.LogOperation(logger, "calculating_trip_time_bounds")
	if err := qtx.BulkUpdateTripTimeBounds(ctx); err != nil {
		return false, fmt.Errorf("failed to bulk update trip time bounds: %w", err)
	}

	logging.LogOperation(logger, "building_stop_agency_index")
	if err := buildStopAgencyIndex(ctx, qtx); err != nil {
		logging.LogError(logger, "Unable to build stop agency index", err)
		return false, err
	}
	logging.LogOperation(logger, "stop_agency_index_built")

	logging.LogOperation(logger, "building_block_layover_index")
	if err := c.buildBlockLayoverIndex(ctx, data.Static, tx); err != nil {
		logging.LogError(logger, "Unable to build block layover index", err)
		return false, fmt.Errorf("unable to build block layover index: %w", err)
	}
	logging.LogOperation(logger, "block_layover_index_built")

	// Persist feed_expires_at inside the same transaction so it's atomic with
	// the calendar data it was derived from.
	if err := updateFeedExpiresAtFromCalendar(ctx, qtx); err != nil {
		return false, fmt.Errorf("failed to update feed_expires_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing import transaction: %w", err)
	}

	counts, err := c.TableCounts()
	if err != nil {
		logging.LogError(logger, "Error getting table counts", err)
		return false, fmt.Errorf("failed to get table counts: %w", err)
	}
	for k, v := range counts {
		logging.LogOperation(logger, "table_count", slog.String("table", k), slog.Int("count", v), slog.Bool("static_matches", v == staticCounts[k]))
	}

	return true, nil
}

// stopTimeRows yields the stop times of every trip, in trip order, building
// each row only when it is needed.
func stopTimeRows(trips []gtfs.ScheduledTrip) iter.Seq[CreateStopTimeParams] {
	return func(yield func(CreateStopTimeParams) bool) {
		for _, t := range trips {
			for _, st := range t.StopTimes {
				row := CreateStopTimeParams{
					TripID:            t.ID,
					ArrivalTime:       int64(st.ArrivalTime),
					DepartureTime:     int64(st.DepartureTime),
					StopID:            st.Stop.Id,
					StopSequence:      int64(st.StopSequence),
					StopHeadsign:      nulls.String(st.Headsign),
					PickupType:        toNullInt64(int64(st.PickupType)),
					DropOffType:       toNullInt64(int64(st.DropOffType)),
					ShapeDistTraveled: nulls.Float64FromPtr(st.ShapeDistanceTraveled),
					Timepoint:         toNullInt64(boolToInt(st.ExactTimes)),
				}
				if !yield(row) {
					return
				}
			}
		}
	}
}

// shapeRows yields the points of every shape, numbered by their position in
// the shape, building each row only when it is needed.
func shapeRows(shapes []gtfs.Shape) iter.Seq[CreateShapeParams] {
	return func(yield func(CreateShapeParams) bool) {
		for _, s := range shapes {
			for idx, pt := range s.Points {
				row := CreateShapeParams{
					ShapeID:           s.ID,
					Lat:               pt.Latitude,
					Lon:               pt.Longitude,
					ShapePtSequence:   int64(idx),
					ShapeDistTraveled: nulls.Float64FromPtr(pt.Distance),
				}
				if !yield(row) {
					return
				}
			}
		}
	}
}

// updateFeedExpiresAtFromCalendar reads the latest active service date from
// calendar / calendar_dates, parses it, and persists feed_expires_at to
// import_metadata. Intended to be called from within the StoreGtfsData
// transaction so the value is atomic with the calendar data it was derived from.
// buildStopAgencyIndex repopulates stop_agencies from the routes currently serving each
// stop. The table is rebuilt rather than updated in place because an import replaces the
// whole feed, and a stop can change agency or lose its routes between feed versions.
func buildStopAgencyIndex(ctx context.Context, qtx *Queries) error {
	if err := qtx.ClearStopAgencies(ctx); err != nil {
		return fmt.Errorf("failed to clear stop agencies: %w", err)
	}
	if err := qtx.BuildStopAgencies(ctx); err != nil {
		return fmt.Errorf("failed to build stop agencies: %w", err)
	}
	return nil
}

func updateFeedExpiresAtFromCalendar(ctx context.Context, qtx *Queries) error {
	val, err := qtx.GetFeedEndDate(ctx)
	if err != nil {
		return fmt.Errorf("failed to get feed end date: %w", err)
	}

	var dateStr string
	switch v := val.(type) {
	case nil:
		// No calendar data — feed_expires_at will be NULL.
	case string:
		dateStr = v
	case []byte:
		dateStr = string(v)
	default:
		return fmt.Errorf("unexpected type from GetFeedEndDate: %T", val)
	}

	var expires sql.NullInt64
	if dateStr != "" {
		parsedTime, err := time.Parse("20060102", dateStr)
		if err != nil {
			return fmt.Errorf("failed to parse feed end date %q: %w", dateStr, err)
		}
		// 23:59:59 of the end date.
		expiresAt := parsedTime.Add(24*time.Hour - time.Second)
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}

	if err := qtx.UpdateFeedExpiresAt(ctx, expires); err != nil {
		return fmt.Errorf("failed to persist feed_expires_at: %w", err)
	}
	return nil
}

// clearAllGTFSDataWithQueries clears all GTFS data using the given Queries (e.g. transaction-scoped).
// Delete order respects foreign key constraints.
func (c *Client) clearAllGTFSDataWithQueries(ctx context.Context, q *Queries) error {
	if err := q.ClearStopAgencies(ctx); err != nil {
		return fmt.Errorf("error clearing stop_agencies: %w", err)
	}
	if err := q.ClearBlockLayovers(ctx); err != nil {
		return fmt.Errorf("error clearing block_layover: %w", err)
	}
	if err := q.ClearBlockTripEntries(ctx); err != nil {
		return fmt.Errorf("error clearing block_trip_entry: %w", err)
	}
	if err := q.ClearBlockTripIndices(ctx); err != nil {
		return fmt.Errorf("error clearing block_trip_index: %w", err)
	}
	if err := q.ClearFrequencies(ctx); err != nil {
		return fmt.Errorf("error clearing frequencies: %w", err)
	}
	if err := q.ClearStopTimes(ctx); err != nil {
		return fmt.Errorf("error clearing stop_times: %w", err)
	}
	if err := q.ClearShapes(ctx); err != nil {
		return fmt.Errorf("error clearing shapes: %w", err)
	}
	if err := q.ClearTrips(ctx); err != nil {
		return fmt.Errorf("error clearing trips: %w", err)
	}
	if err := q.ClearCalendarDates(ctx); err != nil {
		return fmt.Errorf("error clearing calendar dates: %w", err)
	}
	if err := q.ClearCalendar(ctx); err != nil {
		return fmt.Errorf("error clearing calendar: %w", err)
	}
	if err := q.ClearStops(ctx); err != nil {
		return fmt.Errorf("error clearing stops: %w", err)
	}
	if err := q.ClearRoutes(ctx); err != nil {
		return fmt.Errorf("error clearing routes: %w", err)
	}
	if err := q.ClearAgencies(ctx); err != nil {
		return fmt.Errorf("error clearing agencies: %w", err)
	}
	return nil
}

// clearAllGTFSData clears all GTFS data from the database in the correct order to respect foreign key constraints.
// It runs in its own transaction. Use clearAllGTFSDataWithQueries when you need to clear within an existing transaction.
func (c *Client) clearAllGTFSData(ctx context.Context) error {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer logging.SafeRollbackWithLogging(tx, slog.Default().With(slog.String("component", "gtfs_importer")), "clearAllGTFSData")
	if err := c.clearAllGTFSDataWithQueries(ctx, c.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
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

// gtfsDirectionIDToDB converts a go-gtfs DirectionID enum back to the raw
// GTFS CSV value (0 or 1) for database storage. The go-gtfs enum numbers
// DirectionID_True=1 and DirectionID_False=2, which does not match the GTFS
// spec (direction_id is 0 or 1). Storing the raw CSV value keeps downstream
// code (ordering, Java-parity grouping, serialization) consistent with the
// GTFS spec and with onebusaway-application-modules.
func gtfsDirectionIDToDB(d gtfs.DirectionID) sql.NullInt64 {
	switch d {
	case gtfs.DirectionID_True:
		return sql.NullInt64{Int64: 1, Valid: true}
	case gtfs.DirectionID_False:
		return sql.NullInt64{Int64: 0, Valid: true}
	default:
		return sql.NullInt64{}
	}
}

func pickFirstAvailable(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// bulkInsertStops inserts stops. If tx is non-nil it uses that transaction and does not commit; if nil it starts its own and commits.
func (c *Client) bulkInsertStops(ctx context.Context, stops []CreateStopParams, tx *sql.Tx) error {
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_stops",
		slog.Int("count", len(stops)))

	if err := c.withTransaction(ctx, tx, "bulk_insert_stops", func(tx *sql.Tx) error {
		qtx := queries.WithTx(tx)
		for _, params := range stops {
			_, err := qtx.CreateStop(ctx, params)
			if err != nil {
				if isConstraintErr(err) {
					return fmt.Errorf("constraint violation inserting stop %+v: %w", params, err)
				}
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	logging.LogOperation(logger, "stops_inserted",
		slog.Int("count", len(stops)))

	return nil
}

// bulkInsertTrips inserts trips. If tx is non-nil it uses that transaction and does not commit; if nil it starts its own and commits.
func (c *Client) bulkInsertTrips(ctx context.Context, trips []CreateTripParams, tx *sql.Tx) error {
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_trips",
		slog.Int("count", len(trips)))

	if err := c.withTransaction(ctx, tx, "bulk_insert_trips", func(tx *sql.Tx) error {
		qtx := queries.WithTx(tx)
		for _, params := range trips {
			_, err := qtx.CreateTrip(ctx, params)
			if err != nil {
				if isConstraintErr(err) {
					return fmt.Errorf("constraint violation inserting trip %+v: %w", params, err)
				}
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	logging.LogOperation(logger, "trips_inserted",
		slog.Int("count", len(trips)))

	return nil
}

// stopTimeInsert inserts stop_times rows for insertInBatches.
var stopTimeInsert = batchInsert[CreateStopTimeParams]{
	table: "stop_times",
	columns: []string{"trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence",
		"stop_headsign", "pickup_type", "drop_off_type", "shape_dist_traveled", "timepoint"},
	appendArgs: func(args []any, st CreateStopTimeParams) []any {
		return append(args, st.TripID, st.ArrivalTime, st.DepartureTime, st.StopID, st.StopSequence,
			st.StopHeadsign, st.PickupType, st.DropOffType, st.ShapeDistTraveled, st.Timepoint)
	},
}

// bulkInsertStopTimes inserts stop times in the order they arrive, holding one
// batch at a time. If tx is non-nil it uses that transaction and does not
// commit; if nil it starts its own and commits.
func (c *Client) bulkInsertStopTimes(ctx context.Context, stopTimes iter.Seq[CreateStopTimeParams], tx *sql.Tx) error {
	return bulkInsertRows(ctx, c, tx, stopTimeInsert, stopTimes)
}

// shapeInsert inserts shapes rows for insertInBatches.
var shapeInsert = batchInsert[CreateShapeParams]{
	table:   "shapes",
	columns: []string{"shape_id", "lat", "lon", "shape_pt_sequence", "shape_dist_traveled"},
	appendArgs: func(args []any, s CreateShapeParams) []any {
		return append(args, s.ShapeID, s.Lat, s.Lon, s.ShapePtSequence, s.ShapeDistTraveled)
	},
}

// bulkInsertShapes inserts shape points in the order they arrive, holding one
// batch at a time. If tx is non-nil it uses that transaction and does not
// commit; if nil it starts its own and commits.
func (c *Client) bulkInsertShapes(ctx context.Context, shapes iter.Seq[CreateShapeParams], tx *sql.Tx) error {
	return bulkInsertRows(ctx, c, tx, shapeInsert, shapes)
}

// bulkInsertFrequencies inserts frequencies. If tx is non-nil it uses that transaction and does not commit; if nil it starts its own and commits.
func (c *Client) bulkInsertFrequencies(ctx context.Context, frequencies []CreateFrequencyParams, tx *sql.Tx) error {
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_frequencies",
		slog.Int("count", len(frequencies)))

	if err := c.withTransaction(ctx, tx, "bulk_insert_frequencies", func(tx *sql.Tx) error {
		qtx := queries.WithTx(tx)
		for _, params := range frequencies {
			err := qtx.CreateFrequency(ctx, params)
			if err != nil {
				if isConstraintErr(err) {
					return fmt.Errorf("constraint violation inserting frequency %+v: %w", params, err)
				}
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	logging.LogOperation(logger, "frequencies_inserted",
		slog.Int("count", len(frequencies)))

	return nil
}

// bulkInsertCalendarDates inserts calendar dates. If tx is non-nil it uses that transaction and does not commit; if nil it starts its own and commits.
func (c *Client) bulkInsertCalendarDates(ctx context.Context, calendarDates []CreateCalendarDateParams, tx *sql.Tx) error {
	queries := c.Queries
	logger := slog.Default().With(slog.String("component", "bulk_insert"))

	logging.LogOperation(logger, "inserting_calendar_dates",
		slog.Int("count", len(calendarDates)))

	if err := c.withTransaction(ctx, tx, "bulk_insert_calendar_dates", func(tx *sql.Tx) error {
		qtx := queries.WithTx(tx)
		for _, params := range calendarDates {
			_, err := qtx.CreateCalendarDate(ctx, params)
			if err != nil {
				if isConstraintErr(err) {
					return fmt.Errorf("constraint violation inserting calendar date %+v: %w", params, err)
				}
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	logging.LogOperation(logger, "calendar_dates_inserted",
		slog.Int("count", len(calendarDates)))

	return nil
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
		// Enable Write-Ahead Logging to allow concurrent readers and a single writer
		{"PRAGMA journal_mode=WAL", "Enable WAL mode"},
	}

	logger := slog.Default().With(slog.String("component", "sqlite_performance"))

	for _, pragma := range pragmas {
		_, err := db.ExecContext(ctx, pragma.name)
		if err != nil {
			logging.LogError(logger, "failed to set SQLite pragma", err, slog.String("pragma", pragma.description))
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
// service IDs and stop sequences. If tx is non-nil it uses that transaction and does not commit; if nil it starts its own and commits.
func (c *Client) buildBlockTripIndex(ctx context.Context, staticData *gtfs.Static, tx *sql.Tx) error {
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

	q := c.Queries
	createdAt := time.Now().Unix()

	if err := c.withTransaction(ctx, tx, "build_block_trip_index", func(tx *sql.Tx) error {
		qtx := q.WithTx(tx)

		for key, trips := range indexGroups {
			// Create unique index key (service ID + layover stop)
			indexKey := fmt.Sprintf("%s|%s", key.serviceIDs, key.stopSequenceKey)

			indexID, err := qtx.CreateBlockTripIndex(ctx, CreateBlockTripIndexParams{
				IndexKey:        indexKey,
				ServiceIds:      key.serviceIDs,
				StopSequenceKey: key.stopSequenceKey,
				CreatedAt:       createdAt,
			})
			if err != nil {
				return fmt.Errorf("failed to create block trip index: %w", err)
			}

			// Sort trips within the group by block_id and then trip_id for deterministic ordering
			slices.SortFunc(trips, func(a, b *tripInfo) int {
				if c := cmp.Compare(a.blockID, b.blockID); c != 0 {
					return c
				}
				return cmp.Compare(a.tripID, b.tripID)
			})

			// Insert block_trip_entry records for each trip in this index
			for sequence, trip := range trips {
				err = qtx.CreateBlockTripEntry(ctx, CreateBlockTripEntryParams{
					BlockTripIndexID:  indexID,
					TripID:            trip.tripID,
					BlockID:           nulls.String(trip.blockID),
					ServiceID:         trip.serviceID,
					BlockTripSequence: int64(sequence),
				})
				if err != nil {
					return fmt.Errorf("failed to create block trip entry: %w", err)
				}
			}
		}

		return nil
	}); err != nil {
		return err
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

// buildBlockLayoverIndex populates the block_layover table: one row per layover
// (the gap between two consecutive trips in the same block that share a terminal
// stop). Layover bounds are stored as nanoseconds since service-day midnight so
// handlers can match against the same units used by stop_times.
func (c *Client) buildBlockLayoverIndex(ctx context.Context, staticData *gtfs.Static, tx *sql.Tx) error {
	logger := slog.Default().With(slog.String("component", "block_layover_builder"))

	type blockKey struct {
		blockID   string
		serviceID string
	}
	blockTrips := make(map[blockKey][]*gtfs.ScheduledTrip)

	for i := range staticData.Trips {
		trip := &staticData.Trips[i]
		if trip.BlockID == "" || len(trip.StopTimes) == 0 {
			continue
		}
		key := blockKey{blockID: trip.BlockID, serviceID: trip.Service.Id}
		blockTrips[key] = append(blockTrips[key], trip)
	}

	q := c.Queries
	layoverCount := 0

	if err := c.withTransaction(ctx, tx, "build_block_layover_index", func(tx *sql.Tx) error {
		qtx := q.WithTx(tx)

		for key, trips := range blockTrips {
			if len(trips) < 2 {
				continue
			}

			// Trips that depart at the same time are ordered by ID, so the
			// layovers don't depend on the order of the rows in the feed.
			slices.SortFunc(trips, func(a, b *gtfs.ScheduledTrip) int {
				return cmp.Or(
					cmp.Compare(a.StopTimes[0].DepartureTime, b.StopTimes[0].DepartureTime),
					cmp.Compare(a.ID, b.ID),
				)
			})

			for i := 0; i < len(trips)-1; i++ {
				currentTrip := trips[i]
				nextTrip := trips[i+1]

				lastStopCurrent := currentTrip.StopTimes[len(currentTrip.StopTimes)-1]
				firstStopNext := nextTrip.StopTimes[0]

				if lastStopCurrent.Stop.Id != firstStopNext.Stop.Id {
					continue
				}

				// Java's BlockTripLayoverTimeComparator defines the layover
				// window as [prev-trip last-stop DEPARTURE, next-trip first-stop
				// ARRIVAL] — i.e. the interval during which the vehicle is idle
				// at the terminal. Using arrival/departure would widen the
				// window across in-service dwell times at both ends.
				layoverStart := int64(lastStopCurrent.DepartureTime)
				layoverEnd := int64(firstStopNext.ArrivalTime)
				// Skips schedule anomalies where trips overlap at the shared
				// stop (next arrives before prev departs). Also protects the
				// CHECK (layover_start <= layover_end) constraint from
				// aborting the whole import transaction on one bad pair.
				if layoverStart > layoverEnd {
					continue
				}

				err := qtx.CreateBlockLayover(ctx, CreateBlockLayoverParams{
					BlockID:       key.blockID,
					ServiceID:     key.serviceID,
					RouteID:       nextTrip.Route.Id,
					LayoverStopID: lastStopCurrent.Stop.Id,
					LayoverStart:  layoverStart,
					LayoverEnd:    layoverEnd,
					NextTripID:    nextTrip.ID,
				})
				if err != nil {
					return fmt.Errorf("failed to create block layover: %w", err)
				}
				layoverCount++
			}
		}

		return nil
	}); err != nil {
		return err
	}

	logging.LogOperation(logger, "block_layover_index_creation_complete",
		slog.Int("layovers_created", layoverCount))

	return nil
}

// ValidateAndFilterGTFSData performs structural validation on the parsed GTFS data before import.
// It ensures that required files are present and filters out structurally invalid trips.
// Note: Orphaned entities (routes/stops/services with no remaining trips) are retained.
func ValidateAndFilterGTFSData(data *gtfs.Static, logger *slog.Logger) error {
	if data == nil {
		return fmt.Errorf("parsed GTFS data is nil")
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Check for required baseline entities (Hard Failures)
	if len(data.Agencies) == 0 {
		return fmt.Errorf("validation failed: no agencies found in feed (missing or empty agency.txt)")
	}
	if len(data.Routes) == 0 {
		return fmt.Errorf("validation failed: no routes found in feed (missing or empty routes.txt)")
	}
	if len(data.Stops) == 0 {
		return fmt.Errorf("validation failed: no stops found in feed (missing or empty stops.txt)")
	}
	if len(data.Trips) == 0 {
		return fmt.Errorf("validation failed: no trips found in feed (missing or empty trips.txt)")
	}

	// Check for service information (Calendar or CalendarDates)
	hasService := false
	for _, service := range data.Services {
		// Check for calendar.txt regular service
		if service.Monday || service.Tuesday || service.Wednesday || service.Thursday || service.Friday || service.Saturday || service.Sunday {
			hasService = true
			break
		}
		// Check for calendar_dates.txt exception service
		if len(service.AddedDates) > 0 || len(service.RemovedDates) > 0 {
			hasService = true
			break
		}
	}
	if !hasService {
		return fmt.Errorf("validation failed: no service calendars or calendar_dates found")
	}

	// Validate parent_station references: clear any that point to a non-existent stop.
	stopIDs := make(map[string]struct{}, len(data.Stops))
	for _, s := range data.Stops {
		stopIDs[s.Id] = struct{}{}
	}
	orphanedParentRefs := 0
	for i := range data.Stops {
		parent := data.Stops[i].Parent
		if parent == nil || parent.Id == "" {
			continue
		}
		if _, ok := stopIDs[parent.Id]; !ok {
			logger.Warn("stop references missing parent_station, clearing reference",
				slog.String("stop_id", data.Stops[i].Id),
				slog.String("parent_station", parent.Id),
			)
			data.Stops[i].Parent = nil
			orphanedParentRefs++
		}
	}
	if orphanedParentRefs > 0 {
		logger.Warn("cleared orphaned parent_station references",
			slog.Int("count", orphanedParentRefs),
		)
	}

	// Foreign Key / Relationship Checks (Warnings & Filtering)
	var validTrips []gtfs.ScheduledTrip
	for _, trip := range data.Trips {
		// Ensure the trip points to a valid route
		if trip.Route == nil || trip.Route.Id == "" {
			logger.Warn("trip references missing or invalid route, skipping trip", slog.String("trip_id", trip.ID))
			continue
		}

		// Ensure the trip points to a valid service
		if trip.Service == nil || trip.Service.Id == "" {
			logger.Warn("trip references missing or invalid service, skipping trip", slog.String("trip_id", trip.ID))
			continue
		}

		// Ensure the trip has stop times
		if len(trip.StopTimes) == 0 {
			logger.Warn("trip has no stop times, skipping trip", slog.String("trip_id", trip.ID))
			continue
		}

		// Ensure stop times reference valid stops
		hasInvalidStop := false
		for _, st := range trip.StopTimes {
			if st.Stop == nil || st.Stop.Id == "" {
				logger.Warn("stop time for trip references missing stop, skipping trip", slog.String("trip_id", trip.ID))
				hasInvalidStop = true
				break
			}
		}

		if hasInvalidStop {
			continue
		}

		// Keep the trip if it passes all checks
		validTrips = append(validTrips, trip)
	}

	filteredCount := len(data.Trips) - len(validTrips)
	if filteredCount > 0 {
		filteredPct := float64(filteredCount) / float64(len(data.Trips)) * 100
		logger.Warn("GTFS validation filtered invalid trips",
			slog.Int("original_count", len(data.Trips)),
			slog.Int("valid_count", len(validTrips)),
			slog.Int("filtered_count", filteredCount),
			slog.Float64("filtered_percent", filteredPct),
		)
	}

	data.Trips = validTrips

	// Ensure we didn't filter out every single trip in the feed
	if len(data.Trips) == 0 {
		return fmt.Errorf("validation failed: all trips were filtered out due to invalid foreign key relationships")
	}

	return nil
}
