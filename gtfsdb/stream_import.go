package gtfsdb

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
)

// ParseGtfsStream parses the feed in r without its stop times and shape
// points, and validates it. StoreGtfsData then reads those from r one trip or
// shape at a time, so r must stay open, and unchanged, until it returns.
func ParseGtfsStream(r io.ReaderAt, size int64, hash, source string) (*GtfsData, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_parser"))
	logging.LogOperation(logger, "parsing_gtfs_data", slog.Int64("bytes", size))
	stream, err := gtfs.ParseStaticStream(r, size, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("error parsing GTFS data: %w", err)
	}

	if err := validateAndFilterGTFSData(stream.Static, logger, stream.StopTimeCount); err != nil {
		return nil, fmt.Errorf("GTFS validation failed: %w", err)
	}

	return &GtfsData{Static: stream.Static, Hash: hash, Source: source, stream: stream}, nil
}

// feedRows reads a feed's stop times and shape points from its stream for
// StoreGtfsData, one trip or shape at a time.
type feedRows struct {
	stream       *gtfs.StaticStream
	storedTrips  map[string]bool
	replacements StopReplacements

	// tripEnds holds the ends of every stored trip once stopTimes has been
	// read to the end.
	tripEnds []tripEnds
	// removedTrips holds the trips stop consolidation left too short to
	// keep, once stopTimes has been read to the end.
	removedTrips []string
	// err holds the first error reading from the stream.
	err error
}

func (data *GtfsData) rows() *feedRows {
	storedTrips := make(map[string]bool, len(data.Static.Trips))
	for _, trip := range data.Static.Trips {
		storedTrips[trip.ID] = true
	}
	return &feedRows{stream: data.stream, storedTrips: storedTrips, replacements: data.StopReplacements}
}

// errStopped ends a stream early when the rows' consumer stops reading.
var errStopped = errors.New("stopped reading")

// stopTimes yields the stop times of the stored trips, one trip at a time,
// with stop consolidation applied, and records each trip's ends.
func (r *feedRows) stopTimes() iter.Seq[CreateStopTimeParams] {
	return func(yield func(CreateStopTimeParams) bool) {
		err := r.stream.EachTripStopTimes(func(tripIndex int, stopTimes []gtfs.ScheduledStopTime) error {
			trip := r.stream.Trip(tripIndex)
			if !r.storedTrips[trip.ID] {
				return nil
			}
			consolidated := r.replacements.consolidate(stopTimes)
			if len(consolidated) < minStopTimesPerTrip {
				r.removedTrips = append(r.removedTrips, trip.ID)
				return nil
			}
			stopTimes = consolidated
			r.tripEnds = append(r.tripEnds, newTripEnds(trip, stopTimes[0], stopTimes[len(stopTimes)-1]))
			for _, stopTime := range stopTimes {
				if !yield(stopTimeParams(trip.ID, stopTime)) {
					return errStopped
				}
			}
			return nil
		})
		r.recordError(err)
	}
}

// minStopTimesPerTrip is the fewest stop times Java's TripEntriesFactory
// keeps a trip with. Validation drops trips the feed leaves shorter; the
// stop-times stream drops trips stop consolidation leaves shorter.
const minStopTimesPerTrip = 2

// shapes yields the points of every shape, one shape at a time.
func (r *feedRows) shapes() iter.Seq[CreateShapeParams] {
	return func(yield func(CreateShapeParams) bool) {
		err := r.stream.EachShape(func(shape gtfs.Shape) error {
			for idx, point := range shape.Points {
				if !yield(shapePointParams(shape.ID, idx, point)) {
					return errStopped
				}
			}
			return nil
		})
		r.recordError(err)
	}
}

func (r *feedRows) recordError(err error) {
	if err != nil && !errors.Is(err, errStopped) && r.err == nil {
		r.err = err
	}
}
