package restapi

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/logging"
)

func TestWarnIfRealDBError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		op         string
		attrs      []any
		expectLog  bool
		wantSubstr []string
	}{
		{
			name:      "ignores nil error",
			err:       nil,
			op:        "GetTripsByBlockID",
			expectLog: false,
		},
		{
			name:      "ignores sql.ErrNoRows",
			err:       sql.ErrNoRows,
			op:        "GetBlockIDByTripID",
			expectLog: false,
		},
		{
			name:      "ignores wrapped sql.ErrNoRows",
			err:       fmt.Errorf("query lookup: %w", sql.ErrNoRows),
			op:        "GetActiveServiceIDsForDate",
			expectLog: false,
		},
		{
			name:       "logs warning on genuine database failure",
			err:        errors.New("connection refused"),
			op:         "GetTripTimeBoundsByIDs",
			expectLog:  true,
			wantSubstr: []string{`"level":"WARN"`, `"msg":"GetTripTimeBoundsByIDs"`, `"error":"connection refused"`},
		},
		{
			name:       "logs warning with extra structured attributes",
			err:        errors.New("sqlite: database is locked"),
			op:         "GetShapePointsByTripIDs",
			attrs:      []any{slog.String("trip_id", "t-123"), slog.Int("attempt", 2)},
			expectLog:  true,
			wantSubstr: []string{`"level":"WARN"`, `"msg":"GetShapePointsByTripIDs"`, `"error":"sqlite: database is locked"`, `"trip_id":"t-123"`, `"attempt":2`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.NewStructuredLogger(&buf, slog.LevelDebug)
			origLogger := slog.Default()
			slog.SetDefault(logger)
			defer slog.SetDefault(origLogger)

			warnIfRealDBError(tc.err, tc.op, tc.attrs...)

			output := buf.String()
			if !tc.expectLog {
				assert.Empty(t, output)
				return
			}

			for _, sub := range tc.wantSubstr {
				assert.Contains(t, output, sub)
			}
		})
	}
}
