package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
