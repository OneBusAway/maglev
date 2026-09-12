package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Client is the main entry point for the library
type Client struct {
	config        Config
	DB            *sql.DB
	Queries       *Queries
	importRuntime time.Duration
}

// NewClient creates a new Client with the provided configuration
func NewClient(config Config) (*Client, error) {
	db, err := createDB(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create DB: %w", err)
	}
	slog.Default().Debug("successfully created DB")

	// Wrap DB for query interception (optional metrics).
	var dbtx DBTX = db
	if config.QueryMetricsRecorder != nil {
		wrapper := newMetricsWrapper(db)
		wrapper.queryMetrics = config.QueryMetricsRecorder
		dbtx = wrapper
	}
	queries := New(dbtx)

	client := &Client{
		config:  config,
		DB:      db,
		Queries: queries,
	}

	// The search-stop handler reads a stop's agency straight from this index with no
	// fallback, so a missing or stale index yields an empty combined ID rather than a
	// merely degraded one - this must succeed before the client is usable.
	if err := client.backfillStopAgencyIndex(context.Background()); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("unable to backfill stop agency index: %w", err)
	}

	return client, nil
}

// backfillStopAgencyIndex builds stop_agencies for a database imported before the table
// existed, or before it held one row per agency serving a stop. An import is skipped when
// the feed hash is unchanged, so such a database would otherwise keep stale data for as
// long as its feed stays the same.
//
// The legacy check and the indexed/joinable checks are read-only and run outside any
// transaction; the rebuild and the rebuild alone runs inside one, so a failure partway
// through never leaves stop_agencies dropped or half-populated.
func (c *Client) backfillStopAgencyIndex(ctx context.Context) error {
	legacy, err := c.hasLegacyStopAgenciesTable(ctx)
	if err != nil {
		return fmt.Errorf("failed to inspect stop_agencies schema: %w", err)
	}

	if !legacy {
		var indexed, joinable int
		// joinable checks stop times that resolve to a route, not merely that stop_times
		// has rows: a stop_times row with a dangling trip_id or route_id would otherwise
		// make BuildStopAgencies insert nothing every time, leaving indexed permanently 0
		// and triggering a full rebuild on every NewClient forever.
		err := c.DB.QueryRowContext(ctx, `
			SELECT
				(SELECT EXISTS (SELECT 1 FROM stop_agencies)),
				(SELECT EXISTS (
					SELECT 1
					FROM stop_times
					JOIN trips ON stop_times.trip_id = trips.id
					JOIN routes ON trips.route_id = routes.id
				))
		`).Scan(&indexed, &joinable)
		if err != nil {
			return fmt.Errorf("failed to check stop agency index: %w", err)
		}
		if indexed > 0 || joinable == 0 {
			return nil
		}
	}

	slog.Default().Info("rebuilding stop agency index", "reason_legacy_schema", legacy)
	return c.withTransaction(ctx, nil, "backfill_stop_agency_index", func(tx *sql.Tx) error {
		if legacy {
			if err := recreateStopAgenciesTable(ctx, tx); err != nil {
				return err
			}
		}
		return buildStopAgencyIndex(ctx, c.Queries.WithTx(tx))
	})
}

// hasLegacyStopAgenciesTable reports whether stop_agencies still has the single-column
// primary key from before this table held one row per agency serving a stop.
// CREATE TABLE IF NOT EXISTS in schema.sql cannot reshape an already-existing table, so a
// database from an earlier revision of this branch is stuck on the old shape otherwise.
func (c *Client) hasLegacyStopAgenciesTable(ctx context.Context) (bool, error) {
	var pkColumns int
	err := c.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('stop_agencies') WHERE pk > 0`,
	).Scan(&pkColumns)
	if err != nil {
		return false, err
	}
	return pkColumns == 1, nil
}

// recreateStopAgenciesTable drops and rebuilds stop_agencies with the composite primary
// key, inside the caller's transaction so the drop and the schema it's replaced with
// commit or roll back together.
func recreateStopAgenciesTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DROP TABLE stop_agencies"); err != nil {
		return fmt.Errorf("failed to drop legacy stop_agencies table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE stop_agencies (
			stop_id TEXT NOT NULL,
			agency_id TEXT NOT NULL,
			PRIMARY KEY (stop_id, agency_id),
			FOREIGN KEY (stop_id) REFERENCES stops (id),
			FOREIGN KEY (agency_id) REFERENCES agencies (id)
		) STRICT
	`); err != nil {
		return fmt.Errorf("failed to recreate stop_agencies table: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"CREATE INDEX idx_stop_agencies_agency ON stop_agencies (agency_id, stop_id)",
	); err != nil {
		return fmt.Errorf("failed to recreate stop_agencies index: %w", err)
	}
	return nil
}

func (c *Client) Close() error {
	return c.DB.Close()
}
