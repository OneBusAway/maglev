package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// queryRecorder captures the SQL and arguments a generated query method sends,
// so a test can explain the real query rather than a copy of it.
type queryRecorder struct {
	DBTX
	query string
	args  []any
}

func (db *queryRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.query, db.args = query, args
	return db.DBTX.QueryContext(ctx, query, args...)
}

// explainRecordedQuery returns the detail column of each EXPLAIN QUERY PLAN row
// for the query the recorder last captured.
func explainRecordedQuery(t *testing.T, client *Client, recorder *queryRecorder) []string {
	t.Helper()
	rows, err := client.DB.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+recorder.query, recorder.args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		details = append(details, detail)
	}
	require.NoError(t, rows.Err())
	return details
}
