package utils

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapValues(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		var m map[string]int
		result := MapValues(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		m := map[string]int{}
		result := MapValues(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("single element", func(t *testing.T) {
		m := map[string]int{"a": 1}
		result := MapValues(m)
		if len(result) != 1 || result[0] != 1 {
			t.Errorf("expected [1], got %v", result)
		}
	})

	t.Run("multiple elements", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 2, "c": 3}
		result := MapValues(m)
		if len(result) != 3 {
			t.Errorf("expected 3 elements, got %d", len(result))
		}
		slices.Sort(result)
		expected := []int{1, 2, 3}
		for i, v := range result {
			if v != expected[i] {
				t.Errorf("index %d: expected %d, got %d", i, expected[i], v)
			}
		}
	})
}

func TestQueryInBatches(t *testing.T) {
	ctx := context.Background()

	t.Run("An empty ID set runs no query", func(t *testing.T) {
		queried := false
		results, err := QueryInBatches(ctx, nil, func(context.Context, []string) ([]string, error) {
			queried = true
			return nil, nil
		})

		require.NoError(t, err)
		assert.Empty(t, results)
		assert.False(t, queried, "there is nothing to look up")
	})

	t.Run("A failing batch stops the run", func(t *testing.T) {
		batches := 0
		_, err := QueryInBatches(ctx, make([]string, IDsPerBatchedQuery+1),
			func(context.Context, []string) ([]string, error) {
				batches++
				return nil, errors.New("query failed")
			})

		require.Error(t, err)
		assert.Equal(t, 1, batches, "the remaining batches must not run once one fails")
	})
}

func TestQueryInBatchesReserving(t *testing.T) {
	ctx := context.Background()

	t.Run("reserved binds shrink the batch size", func(t *testing.T) {
		// Sized to fit under IDsPerBatchedQuery on its own, but not once 200
		// reserved binds are subtracted from the budget.
		ids := make([]string, IDsPerBatchedQuery-100)

		var batchSizes []int
		results, err := QueryInBatchesReserving(ctx, ids, 200,
			func(_ context.Context, batch []string) ([]string, error) {
				batchSizes = append(batchSizes, len(batch))
				return batch, nil
			})

		require.NoError(t, err)
		assert.Len(t, results, len(ids), "batches must still concatenate to the full input")
		assert.Greater(t, len(batchSizes), 1,
			"the reserved binds must force more than one batch for this test to mean anything")
		for _, size := range batchSizes {
			assert.LessOrEqual(t, size, IDsPerBatchedQuery-200,
				"no batch may exceed the budget left after reserving")
		}
	})

	t.Run("zero reserved matches QueryInBatches", func(t *testing.T) {
		ids := make([]string, IDsPerBatchedQuery+1)
		batches := 0
		_, err := QueryInBatchesReserving(ctx, ids, 0,
			func(context.Context, []string) ([]string, error) {
				batches++
				return nil, nil
			})

		require.NoError(t, err)
		assert.Equal(t, 2, batches)
	})

	t.Run("reserved at or above the limit falls back to one-ID batches", func(t *testing.T) {
		var batchSizes []int
		results, err := QueryInBatchesReserving(ctx, []string{"a", "b", "c"}, IDsPerBatchedQuery,
			func(_ context.Context, batch []string) ([]string, error) {
				batchSizes = append(batchSizes, len(batch))
				return batch, nil
			})

		require.NoError(t, err, "a large reserved dimension must not fail requests that fit the real SQLite limit")
		assert.Equal(t, []string{"a", "b", "c"}, results)
		assert.Equal(t, []int{1, 1, 1}, batchSizes)
	})

	t.Run("reserved at or above the limit still allows empty ids", func(t *testing.T) {
		batches := 0
		results, err := QueryInBatchesReserving(ctx, nil, IDsPerBatchedQuery+50,
			func(context.Context, []string) ([]string, error) {
				batches++
				return nil, nil
			})

		require.NoError(t, err)
		assert.Empty(t, results)
		assert.Equal(t, 0, batches)
	})
}
