package gtfsdb

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
)

// ParseGtfsStream parses the feed in r without its stop times and shape
// points, and validates it. StoreGtfsData then reads those from r one trip or
// shape at a time, so r must stay open, and unchanged, until it returns.
func ParseGtfsStream(r io.ReaderAt, size int64, feed FeedIdentity) (*GtfsData, error) {
	logger := slog.Default().With(slog.String("component", "gtfs_parser"))
	logging.LogOperation(logger, "parsing_gtfs_data", slog.Int64("bytes", size))
	stream, err := gtfs.ParseStaticStream(r, size, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("error parsing GTFS data: %w", err)
	}

	stopTimesProblem, err := flexAwareStopTimesProblem(stream, logger)
	if err != nil {
		return nil, err
	}

	if err := validateAndFilterGTFSData(stream.Static, logger, stopTimesProblem); err != nil {
		return nil, fmt.Errorf("GTFS validation failed: %w", err)
	}

	return &GtfsData{Static: stream.Static, Hash: feed.Hash, Source: feed.Source, stream: stream}, nil
}

// flexAwareStopTimesProblem returns the stop-times problem function for a
// streamed feed. For plain feeds it checks the total stop-time count; for
// feeds with Flex locations it pre-counts stop times that serve a stop and
// uses those counts instead, because stop times without a stop are dropped.
func flexAwareStopTimesProblem(stream *gtfs.StaticStream, logger *slog.Logger) (func(int) string, error) {
	if !hasFlexLocations(stream.Static) {
		return streamedStopTimesProblem(stream), nil
	}
	counts, skipped, err := stopTimeCountsAtStops(stream)
	if err != nil {
		return nil, fmt.Errorf("error reading stop times: %w", err)
	}
	if skipped > 0 {
		logger.Warn("skipping GTFS-Flex stop times that serve no stop; Flex import is not supported yet",
			slog.Int("stop_times", skipped))
	}
	return stopTimesAtStopsProblem(counts), nil
}

// hasFlexLocations reports whether the feed has GTFS-Flex locations or
// location groups, the only things a stop time can serve instead of a stop.
func hasFlexLocations(static *gtfs.Static) bool {
	return static.Locations != nil || static.LocationGroups != nil
}

// stopTimeCountsAtStops counts each trip's stop times that serve a stop, by
// index in Static.Trips, and how many stop times serve none. A GTFS-Flex
// stop time that serves only a location or location group has no stop, and
// the importer can't store it until Flex import lands (#1481). The count
// takes a pass over stop_times.txt, so it only runs for feeds with Flex
// locations.
func stopTimeCountsAtStops(stream *gtfs.StaticStream) ([]int, int, error) {
	counts := make([]int, len(stream.Static.Trips))
	skipped := 0
	err := stream.EachTripStopTimes(func(tripIndex int, stopTimes []gtfs.ScheduledStopTime) error {
		for _, stopTime := range stopTimes {
			if stopTime.Stop != nil {
				counts[tripIndex]++
			} else {
				skipped++
			}
		}
		return nil
	})
	return counts, skipped, err
}

// stopTimesAtStopsProblem is streamedStopTimesProblem for a feed with Flex
// locations: a trip needs minStopTimesPerTrip stop times at stops, because
// its stop times without one aren't stored.
func stopTimesAtStopsProblem(counts []int) func(tripIndex int) string {
	return func(tripIndex int) string {
		if counts[tripIndex] < minStopTimesPerTrip {
			return tooFewStopTimesAtStopsProblem
		}
		return ""
	}
}

// stopTimesAtStops returns the stop times that serve a stop, dropping the
// GTFS-Flex ones that serve only a location or location group. It returns
// stopTimes itself when every stop time has a stop.
func stopTimesAtStops(stopTimes []gtfs.ScheduledStopTime) []gtfs.ScheduledStopTime {
	for i := range stopTimes {
		if stopTimes[i].Stop != nil {
			continue
		}
		atStops := slices.Clone(stopTimes[:i])
		for _, stopTime := range stopTimes[i+1:] {
			if stopTime.Stop != nil {
				atStops = append(atStops, stopTime)
			}
		}
		return atStops
	}
	return stopTimes
}

// feedRows reads a feed's stop times and shape points from its stream for
// StoreGtfsData, one trip or shape at a time.
type feedRows struct {
	stream      *gtfs.StaticStream
	storedTrips map[string]bool

	// tripEnds holds the ends of every stored trip once stopTimes has been
	// read to the end.
	tripEnds []tripEnds
	// err holds the first error reading from the stream.
	err error
}

func (data *GtfsData) rows() *feedRows {
	storedTrips := make(map[string]bool, len(data.Static.Trips))
	for _, trip := range data.Static.Trips {
		storedTrips[trip.ID] = true
	}
	return &feedRows{stream: data.stream, storedTrips: storedTrips}
}

// errStopped ends a stream early when the rows' consumer stops reading.
var errStopped = errors.New("stopped reading")

// stopTimes yields the stop times of the stored trips, one trip at a time,
// and records each trip's ends.
func (r *feedRows) stopTimes() iter.Seq[CreateStopTimeParams] {
	return func(yield func(CreateStopTimeParams) bool) {
		err := r.stream.EachTripStopTimes(func(tripIndex int, stopTimes []gtfs.ScheduledStopTime) error {
			trip := r.stream.Trip(tripIndex)
			if !r.storedTrips[trip.ID] {
				return nil
			}
			// Validation already removed trips left with too few.
			stopTimes = stopTimesAtStops(stopTimes)
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
