package gtfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/consolidation"
	"maglev.onebusaway.org/internal/logging"
)

// maxStopConsolidationBytes is the largest stop consolidation file Maglev
// reads. The Puget Sound file is about 8 KB.
const maxStopConsolidationBytes = 1024 * 1024

// stopConsolidation is the configured stop consolidation file, parsed, and
// the SHA-256 of its bytes.
type stopConsolidation struct {
	file *consolidation.File
	hash string
}

// loadStopConsolidation reads and parses the configured file. It returns nil
// when none is configured, and fails when the file can't be read or has a
// malformed stop ID, as Java's bundle builder does.
func loadStopConsolidation(ctx context.Context, config Config) (*stopConsolidation, error) {
	if config.StopConsolidationURL == "" {
		return nil, nil
	}
	b, err := readStopConsolidation(ctx, config.StopConsolidationURL)
	if err != nil {
		return nil, fmt.Errorf("error reading stop consolidation file %s: %w", config.StopConsolidationURL, err)
	}
	file, err := consolidation.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return &stopConsolidation{file: file, hash: hex.EncodeToString(sum[:])}, nil
}

func readStopConsolidation(ctx context.Context, location string) ([]byte, error) {
	r, err := openStopConsolidation(ctx, location)
	if err != nil {
		return nil, err
	}
	defer logging.SafeCloseWithLogging(r,
		slog.Default().With(slog.String("component", "gtfs_loader")),
		"stop_consolidation_file")

	b, err := io.ReadAll(io.LimitReader(r, maxStopConsolidationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStopConsolidationBytes {
		return nil, fmt.Errorf("file exceeds size limit of %d bytes", maxStopConsolidationBytes)
	}
	return b, nil
}

func openStopConsolidation(ctx context.Context, location string) (io.ReadCloser, error) {
	if !isHTTPURL(location) {
		return os.Open(location)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := newStaticHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("received HTTP status %s", resp.Status)
	}
	return resp.Body, nil
}

// withHash marks an import hash with the file's hash, so that changing the
// file alone causes a re-import.
func (s *stopConsolidation) withHash(importHash string) string {
	if s == nil {
		return importHash
	}
	return importHash + "+consolidation-" + s.hash
}

// apply marks the feed's stops for consolidation and logs the file's
// problems. Java names every stop in a feed after the feed's default agency,
// the first one in agency.txt, so that is the agency the file's stop IDs
// must use.
func (s *stopConsolidation) apply(data *gtfsdb.GtfsData, logger *slog.Logger) {
	if s == nil {
		return
	}
	if len(data.Static.Agencies) == 0 {
		logger.Warn("stop consolidation file not applied: feed has no agencies")
		return
	}

	defaultAgencyID := data.Static.Agencies[0].Id
	loaded := make(map[consolidation.StopID]consolidation.Location)
	stopsByID := make(map[string]*gtfs.Stop)
	for i := range data.Static.Stops {
		stop := &data.Static.Stops[i]
		if stop.Latitude == nil || stop.Longitude == nil {
			continue
		}
		loaded[consolidation.StopID{AgencyID: defaultAgencyID, ID: stop.Id}] = consolidation.Location{Lat: *stop.Latitude, Lon: *stop.Longitude}
		stopsByID[stop.Id] = stop
	}

	for _, warning := range consolidation.Validate(s.file, loaded) {
		logger.Warn("stop consolidation file problem",
			slog.String("kind", string(warning.Kind)),
			slog.String("detail", warning.String()))
	}

	replacements := make(gtfsdb.StopReplacements)
	var moved int
	for absorbed, outcome := range s.file.Apply(loaded) {
		replacement := gtfsdb.StopReplacement{RuleCanonical: outcome.Canonical.String()}
		if outcome.Moved {
			replacement.Canonical = stopsByID[outcome.Canonical.ID]
			moved++
		}
		replacements[absorbed.ID] = replacement
	}
	data.StopReplacements = replacements

	logging.LogOperation(logger, "stop_consolidation_applied",
		slog.String("default_agency_id", defaultAgencyID),
		slog.Int("absorbed_stops", len(replacements)),
		slog.Int("stop_times_moved_from", moved),
		slog.Int("stop_times_dropped_from", len(replacements)-moved))
}
