package gtfs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// stopDirectionResult holds the result of a direction calculation for a single stop
type stopDirectionResult struct {
	stopID    string
	direction string
}

// precomputeShapeCachePoints is how many shape points the precomputer keeps
// in memory, about 20 MB of rows. It holds roughly 500 shapes of average size
// in a large bus feed such as NJ Transit's.
const precomputeShapeCachePoints = 500_000

// DirectionPrecomputer handles batch computation of stop directions
type DirectionPrecomputer struct {
	calculator *AdvancedDirectionCalculator
	queries    *gtfsdb.Queries
	db         *sql.DB
	logger     *slog.Logger
}

// NewDirectionPrecomputer creates a new direction precomputer
func NewDirectionPrecomputer(queries *gtfsdb.Queries, db *sql.DB) *DirectionPrecomputer {
	calculator := NewAdvancedDirectionCalculator(queries)
	calculator.recentShapes = newRecentShapes(precomputeShapeCachePoints)
	return &DirectionPrecomputer{
		calculator: calculator,
		queries:    queries,
		db:         db,
		logger:     slog.Default().With(slog.String("component", "direction_precomputer")),
	}
}

// PrecomputeAllDirections computes and stores directions for all stops using parallel processing
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

	// ===== PHASE 1: PARALLEL DIRECTION CALCULATION (read-only) =====
	numWorkers := runtime.NumCPU()
	stopsChan := make(chan gtfsdb.Stop, numWorkers)
	// Use smaller buffer to avoid excessive memory usage
	resultsChan := make(chan stopDirectionResult, numWorkers*10)

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stop := range stopsChan {
				// Check context for cancellation and send result before exiting
				// to prevent deadlock in result collection
				select {
				case <-ctx.Done():
					// Send empty result to maintain proper count
					resultsChan <- stopDirectionResult{
						stopID:    stop.ID,
						direction: "",
					}
					// Drain remaining stops from channel to allow other workers to continue
					for range stopsChan {
						// Discard remaining work
					}
					return
				default:
					// Calculate direction (read-only operation)
					direction := dp.calculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

					// Send result to collection channel
					resultsChan <- stopDirectionResult{
						stopID:    stop.ID,
						direction: direction,
					}
				}
			}
		}()
	}

	// Feed stops to workers
	go func() {
		for _, stop := range stops {
			stopsChan <- stop
		}
		close(stopsChan)
	}()

	// Wait for all workers to complete and close results channel
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// ===== PHASE 2: COLLECT RESULTS (single-threaded, no races) =====
	results := make([]stopDirectionResult, 0, len(stops))
	processed := 0

	for result := range resultsChan {
		results = append(results, result)
		processed++

		// Log progress every 1000 stops
		if processed%1000 == 0 {
			logging.LogOperation(dp.logger, "precomputation_progress",
				slog.Int("processed", processed),
				slog.Int("total", len(stops)))
		}
	}

	// Check if context was canceled during calculation
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// ===== PHASE 3: BATCH DATABASE WRITES (sequential, avoids lock contention) =====
	batchSize := 500
	successCount := 0
	skippedCount := 0
	errorCount := 0

	// Helper function to process a single batch with proper transaction cleanup
	processBatch := func(batch []stopDirectionResult, batchEnd int) (int, error) {
		// Begin transaction for this batch
		tx, err := dp.db.BeginTx(ctx, nil)
		if err != nil {
			return 0, fmt.Errorf("failed to begin transaction for batch: %w", err)
		}

		// Setup deferred rollback (will be no-op after successful commit)
		committed := false
		defer func() {
			if !committed {
				logging.SafeRollbackWithLogging(tx, dp.logger, "direction_precomputation_batch")
			}
		}()

		// Write batch using direct SQL execution to avoid prepared statement issues
		batchSuccess := 0
		const updateSQL = "UPDATE stops SET direction = ? WHERE id = ?"
		for _, result := range batch {
			if result.direction != "" {
				_, err := tx.ExecContext(ctx, updateSQL, result.direction, result.stopID)
				if err != nil {
					logging.LogError(dp.logger, fmt.Sprintf("Failed to update direction for stop %s", result.stopID), err)
					errorCount++
					continue
				}
				batchSuccess++
			} else {
				skippedCount++
			}
		}

		// Commit batch transaction
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("failed to commit batch transaction: %w", err)
		}
		committed = true

		// Log batch completion
		logging.LogOperation(dp.logger, "precomputation_batch_written",
			slog.Int("batch_end", batchEnd),
			slog.Int("total", len(stops)),
			slog.Int("batch_successful", batchSuccess))

		return batchSuccess, nil
	}

	for i := 0; i < len(results); i += batchSize {
		// Check context before starting new batch
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Calculate batch boundaries
		end := i + batchSize
		if end > len(results) {
			end = len(results)
		}
		batch := results[i:end]

		// Process batch
		batchSuccess, err := processBatch(batch, end)
		if err != nil {
			return err
		}

		successCount += batchSuccess
	}

	duration := time.Since(startTime)
	shapeHits, shapeMisses := dp.calculator.recentShapes.stats()
	logging.LogOperation(dp.logger, "precomputing_stop_directions_completed",
		slog.Duration("duration_ms", duration),
		slog.Int("total_stops", len(stops)),
		slog.Int("successful", successCount),
		slog.Int("skipped", skippedCount),
		slog.Int("errors", errorCount),
		slog.Int("shape_cache_hits", shapeHits),
		slog.Int("shape_reads", shapeMisses))

	return nil
}
