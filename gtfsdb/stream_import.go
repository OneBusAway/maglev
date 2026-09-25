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

	if err := validateAndFilterGTFSData(stream.Static, logger, streamedStopTimesProblem(stream)); err != nil {
		return nil, fmt.Errorf("GTFS validation failed: %w", err)
	}

	return &GtfsData{Static: stream.Static, Hash: feed.Hash, Source: feed.Source, stream: stream}, nil
}

// feedRows reads a feed's stop times and shape points from its stream for
// StoreGtfsData, one trip or shape at a time.
type feedRows struct {
	stream      *gtfs.StaticStream
	storedTrips map[string]bool

	// tripEnds holds the ends of every stored trip once stopTimes has been
	// read to the end.
	tripEnds []tripEnds
	// flexTrips holds a copy of every stored trip with a flex record, its
	// stop times included, once stopTimes has been read to the end. The flex
	// import needs a trip's windowed and timed records together, and a flex
	// feed has few such trips.
	flexTrips []gtfs.ScheduledTrip
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

// stopTimes yields the timed stop times of the stored trips, one trip at a
// time, and records each trip's ends. A trip with only windowed (flex)
// records has no ends, so the block indexes skip it.
func (r *feedRows) stopTimes() iter.Seq[CreateStopTimeParams] {
	return func(yield func(CreateStopTimeParams) bool) {
		err := r.stream.EachTripStopTimes(func(tripIndex int, stopTimes []gtfs.ScheduledStopTime) error {
			trip := r.stream.Trip(tripIndex)
			if !r.storedTrips[trip.ID] {
				return nil
			}
			r.keepFlexTrip(trip, stopTimes)
			timed := timedStopTimes(stopTimes)
			if len(timed) == 0 {
				return nil
			}
			r.tripEnds = append(r.tripEnds, newTripEnds(trip, timed[0], timed[len(timed)-1]))
			for _, stopTime := range timed {
				if !yield(stopTimeParams(trip.ID, stopTime)) {
					return errStopped
				}
			}
			return nil
		})
		r.recordError(err)
	}
}

// keepFlexTrip keeps a copy of trip, with its stop times, when it has a flex
// record.
func (r *feedRows) keepFlexTrip(trip *gtfs.ScheduledTrip, stopTimes []gtfs.ScheduledStopTime) {
	if !slices.ContainsFunc(stopTimes, gtfs.ScheduledStopTime.IsFlex) {
		return
	}
	flexTrip := *trip
	flexTrip.StopTimes = slices.Clone(stopTimes)
	r.flexTrips = append(r.flexTrips, flexTrip)
}

// flexStatic returns static with only the flex trips, each with its stop
// times, for the flex import. Call it after stopTimes has been read to the
// end.
func (r *feedRows) flexStatic(static *gtfs.Static) *gtfs.Static {
	flex := *static
	flex.Trips = r.flexTrips
	return &flex
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
