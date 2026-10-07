package utils

import "context"

// IDsPerBatchedQuery bounds how many IDs go into one IN (...) list. SQLite
// rejects a statement carrying more bind variables than it allows rather than
// truncating it, and these ID sets are only bounded by feed or request size.
// Kept well under the oldest limit (999) so the batch size does not depend on
// which SQLite the build links against.
const IDsPerBatchedQuery = 900

// QueryInBatches runs query over ids in batches small enough to stay under the
// bind variable limit, concatenating the results.
func QueryInBatches[T any](ctx context.Context, ids []string, query func(context.Context, []string) ([]T, error)) ([]T, error) {
	results := make([]T, 0, len(ids))
	for start := 0; start < len(ids); start += IDsPerBatchedQuery {
		end := min(start+IDsPerBatchedQuery, len(ids))
		batch, err := query(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}
