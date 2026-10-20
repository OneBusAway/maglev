package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"log/slog"
	"strings"

	"maglev.onebusaway.org/internal/logging"
)

// batchInsert describes a multi-row INSERT that insertInBatches runs.
type batchInsert[T any] struct {
	table      string
	columns    []string
	appendArgs func(args []any, row T) []any // appends one row's values in column order
}

// insertInBatches inserts rows in multi-row INSERT statements of up to
// batchSize rows, in the order the rows arrive. It holds one batch at a time,
// so its memory doesn't grow with the number of rows. Full batches share one
// prepared statement. It returns the number of rows inserted.
func insertInBatches[T any](ctx context.Context, tx *sql.Tx, batchSize int, insert batchInsert[T], rows iter.Seq[T]) (int, error) {
	if batchSize < 1 {
		return 0, fmt.Errorf("batch size for %s must be at least 1, got %d", insert.table, batchSize)
	}
	logger := slog.Default().With(slog.String("component", "bulk_insert"))
	const progressInterval = 100_000

	var fullBatch *sql.Stmt
	defer func() {
		if fullBatch != nil {
			_ = fullBatch.Close()
		}
	}()

	args := make([]any, 0, batchSize*len(insert.columns))
	rowsInBatch, inserted := 0, 0
	flush := func() error {
		if rowsInBatch == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if rowsInBatch == batchSize {
			if fullBatch == nil {
				if fullBatch, err = tx.PrepareContext(ctx, insert.query(batchSize)); err != nil {
					return fmt.Errorf("failed to prepare %s batch: %w", insert.table, err)
				}
			}
			_, err = fullBatch.ExecContext(ctx, args...)
		} else {
			_, err = tx.ExecContext(ctx, insert.query(rowsInBatch), args...)
		}
		if err != nil {
			return fmt.Errorf("failed to insert %s batch: %w", insert.table, err)
		}
		if (inserted+rowsInBatch)/progressInterval > inserted/progressInterval {
			logging.LogOperation(logger, insert.table+"_progress", slog.Int("inserted", inserted+rowsInBatch))
		}
		inserted += rowsInBatch
		args, rowsInBatch = args[:0], 0
		return nil
	}

	for row := range rows {
		args = insert.appendArgs(args, row)
		rowsInBatch++
		if rowsInBatch == batchSize {
			if err := flush(); err != nil {
				return inserted, err
			}
		}
	}
	return inserted, flush()
}

// query returns the INSERT statement for the given number of rows.
// SECURITY: values are bound as placeholders; only fixed column names are
// written into the statement.
func (insert batchInsert[T]) query(rows int) string {
	row := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(insert.columns)), ", ") + ")"
	var query strings.Builder
	query.WriteString("INSERT INTO " + insert.table + " (" + strings.Join(insert.columns, ", ") + ") VALUES ")
	for i := range rows {
		if i > 0 {
			query.WriteString(", ")
		}
		query.WriteString(row)
	}
	return query.String()
}
