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

// SetVarianceThreshold sets the variance threshold for the calculator
func (dp *DirectionPrecomputer) SetVarianceThreshold(threshold float64) {
	dp.calculator.SetVarianceThreshold(threshold)
}

// PrecomputeAllDirections computes and stores directions for all stops
func (dp *DirectionPrecomputer) PrecomputeAllDirections(ctx context.Context) error {
	startTime := time.Now()

	logging.LogOperation(dp.logger, "precomputing_stop_directions_started")

	// Get all stops
	stops, err := dp.queries.ListStops(ctx)
	if err != nil {
		return fmt.Errorf("failed to list stops: %w", err)
	}

	if len(stops) == 0 {
		logging.LogOperation(dp.logger, "no_stops_found_skipping_precomputation")
		return nil
	}

	// Begin transaction for batch updates
	tx, err := dp.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer logging.SafeRollbackWithLogging(tx, dp.logger, "direction_precomputation")

	// Use transaction-bound queries
	qtx := dp.queries.WithTx(tx)

	successCount := 0
	skippedCount := 0
	errorCount := 0

	for i, stop := range stops {
		// Check context for cancellation
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Calculate direction
		direction := dp.calculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		// Update database if direction was calculated
		if direction != "" {
			err := qtx.UpdateStopDirection(ctx, gtfsdb.UpdateStopDirectionParams{
				Direction: sql.NullString{String: direction, Valid: true},
				ID:        stop.ID,
			})
			if err != nil {
				logging.LogError(dp.logger, fmt.Sprintf("Failed to update direction for stop %s", stop.ID), err)
				errorCount++
				continue
			}
			successCount++
		} else {
			skippedCount++
		}

		// Log progress every 100 stops
		if (i+1)%100 == 0 {
			logging.LogOperation(dp.logger, "precomputation_progress",
				slog.Int("processed", i+1),
				slog.Int("total", len(stops)),
				slog.Int("successful", successCount),
				slog.Int("skipped", skippedCount),
				slog.Int("errors", errorCount))
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	duration := time.Since(startTime)
	logging.LogOperation(dp.logger, "precomputing_stop_directions_completed",
		slog.Duration("duration", duration),
		slog.Int("total_stops", len(stops)),
		slog.Int("successful", successCount),
		slog.Int("skipped", skippedCount),
		slog.Int("errors", errorCount))

	return nil
}

// PrecomputeDirectionsAsync runs direction precomputation in a background goroutine
func (dp *DirectionPrecomputer) PrecomputeDirectionsAsync(ctx context.Context) {
	go func() {
		if err := dp.PrecomputeAllDirections(ctx); err != nil {
			if ctx.Err() == nil {
				// Only log error if not caused by context cancellation
				logging.LogError(dp.logger, "Background direction precomputation failed", err)
			}
		}
	}()
}
