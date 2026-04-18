package gtfs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// stopDirectionResult holds the result of a direction calculation for a single stop
type stopDirectionResult struct {
	stopID    string
	direction string
}

// DirectionPrecomputer handles batch computation of stop directions
type DirectionPrecomputer struct {
	calculator *AdvancedDirectionCalculator
	queries    *gtfsdb.Queries
	db         *sql.DB
	logger     *slog.Logger
}

// NewDirectionPrecomputer creates a new direction precomputer
func NewDirectionPrecomputer(queries *gtfsdb.Queries, db *sql.DB) *DirectionPrecomputer {
	return &DirectionPrecomputer{
		calculator: NewAdvancedDirectionCalculator(queries),
		queries:    queries,
		db:         db,
		logger:     slog.Default().With(slog.String("component", "direction_precomputer")),
	}
}

// PrecomputeAllDirections computes and stores directions for all stops using parallel processing
func (dp *DirectionPrecomputer) PrecomputeAllDirections(ctx context.Context) error {
	intervalStart := time.Now()
	logging.LogOperation(dp.logger, "precomputing_stop_directions_started")

	// TODO should this be run in the transaction as well?
	stops, err := dp.queries.ListStops(ctx)
	if err != nil {
		return fmt.Errorf("failed to list stops: %w", err)
	}
	if len(stops) == 0 {
		logging.LogOperation(dp.logger, "no_stops_found_skipping_precomputation")
		return nil
	}

	results := make([]stopDirectionResult, 0, len(stops))
	for _, stop := range stops {
		direction := dp.calculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		results = append(results, stopDirectionResult{
			stopID:    stop.ID,
			direction: direction,
		})
	}

	logging.LogOperation(dp.logger, "precompute_results_calculated",
		slog.Duration("duration", time.Since(intervalStart)))
	intervalStart = time.Now()

	// Write all rows to the database.
	successCount := 0
	skippedCount := 0
	errorCount := 0

	tx, err := dp.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Setup deferred rollback (will be no-op after successful commit)
	committed := false
	defer func() {
		if !committed {
			logging.SafeRollbackWithLogging(tx, dp.logger, "direction_precomputation_batch")
		}
	}()
	for _, result := range results {
		if result.direction == "" {
			skippedCount++
			continue
		}

		// Write batch using direct SQL execution to avoid prepared statement issues
		const updateSQL = "UPDATE stops SET direction = ? WHERE id = ?"
		_, err := tx.ExecContext(ctx, updateSQL, result.direction, result.stopID)
		if err != nil {
			logging.LogError(dp.logger, fmt.Sprintf("Failed to update direction for stop %s", result.stopID), err)
			errorCount++
			continue
		}
		successCount++
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	logging.LogOperation(dp.logger, "precomputing_stop_directions_completed",
		slog.Duration("duration", time.Since(intervalStart)),
		slog.Int("total_stops", len(stops)),
		slog.Int("successful", successCount),
		slog.Int("skipped", skippedCount),
		slog.Int("errors", errorCount))

	return nil
}
