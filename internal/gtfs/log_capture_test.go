package gtfs

import (
	"bytes"
	"log/slog"
	"testing"
)

func captureStructuredLogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()

	var output bytes.Buffer

	previous := slog.Default()
	handler := slog.NewJSONHandler(&output, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	slog.SetDefault(slog.New(handler))

	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	return &output
}
