package gtfsdb

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/OneBusAway/go-gtfs/warnings"
	"github.com/stretchr/testify/assert"
)

func TestLogStaticWarnings_CapsAtTwoHundredLines(t *testing.T) {
	tests := []struct {
		name         string
		count        int
		wantLines    int
		wantTruncate string
	}{
		{name: "under the cap logs every warning", count: 3, wantLines: 3},
		{name: "exactly at the cap logs no summary", count: 200, wantLines: 200},
		{name: "over the cap logs 200 and a summary", count: 250, wantLines: 201, wantTruncate: "remaining=50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))

			staticWarnings := make([]warnings.StaticWarning, tt.count)
			for i := range staticWarnings {
				staticWarnings[i] = warnings.StaticWarning{
					Kind:      warnings.MissingColumns{Columns: []string{"stop_id"}},
					File:      "stop_times.txt",
					RowNumber: i + 2,
				}
			}

			logStaticWarnings(logger, staticWarnings)

			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			assert.Len(t, lines, tt.wantLines)
			assert.Contains(t, lines[0], "gtfs_static_warning")
			assert.Contains(t, lines[0], "file=stop_times.txt")
			assert.Contains(t, lines[0], "row=2")
			if tt.wantTruncate != "" {
				assert.Contains(t, lines[len(lines)-1], "gtfs_static_warnings_truncated")
				assert.Contains(t, lines[len(lines)-1], tt.wantTruncate)
			}
		})
	}
}
