package utils

import (
	"context"
	"fmt"
)

// MapValues returns the values of a map as a slice.
// The order of the returned values is non-deterministic.
func MapValues[K comparable, V any](m map[K]V) []V {
	result := make([]V, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// IDsPerBatchedQuery bounds how many IDs go into one IN (...) list, assuming
// the batched slice is the only bind the statement carries. SQLite rejects a
// statement carrying more bind variables than it allows rather than
// truncating it, and these ID sets are only bounded by how much the request
// matched. Kept well under the oldest limit (999) so the batch size does not
// depend on which SQLite the build links against. A statement binding
// anything besides the batched slice needs QueryInBatchesReserving instead,
// so that budget also accounts for those binds.
const IDsPerBatchedQuery = 900

// QueryInBatches runs query over ids in batches small enough to stay under the
// bind variable limit, concatenating the results.
func QueryInBatches[T any](ctx context.Context, ids []string, query func(context.Context, []string) ([]T, error)) ([]T, error) {
	return QueryInBatchesReserving(ctx, ids, 0, query)
}

// QueryInBatchesReserving is QueryInBatches with reserved slots subtracted
// from the batch size, for a statement that binds something besides the
// batched slice — a second IN (...) list, a scalar. Without this, sizing the
// batch at IDsPerBatchedQuery silently assumes the batched slice is the whole
// statement, and the untracked binds can push the total over the limit.
//
// Assumes the reserved binds themselves fit in one statement. A second
// dimension large enough on its own to need batching (hundreds of active
// service IDs on one day, say) would still overflow; batch that dimension
// too if a feed ever gets there.
func QueryInBatchesReserving[T any](ctx context.Context, ids []string, reserved int,
	query func(context.Context, []string) ([]T, error)) ([]T, error) {
	if len(ids) == 0 {
		return []T{}, nil
	}
	// A reserved budget at or above IDsPerBatchedQuery leaves no headroom for
	// even one batched ID under the SQLite bind limit. Silently forcing
	// batchSize to 1 would still push each statement past the limit; refuse
	// instead and force the caller to batch the reserved dimension too.
	if reserved >= IDsPerBatchedQuery {
		return nil, fmt.Errorf("QueryInBatchesReserving: reserved binds (%d) meet or exceed the batch limit (%d); batch the reserved dimension too", reserved, IDsPerBatchedQuery)
	}
	batchSize := IDsPerBatchedQuery - reserved
	results := make([]T, 0, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		batch, err := query(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}
