package app

import (
	"log/slog"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
)

// Application holds the dependencies for our HTTP handlers, helpers,
// and middleware. At the moment this only contains a copy of the Config struct and a
// logger, but it will grow to include a lot more as our build progresses.
type Application struct {
	Config              appconf.Config
	GtfsConfig          gtfs.Config
	Logger              *slog.Logger
	GtfsManager         *gtfs.Manager
	DirectionCalculator *gtfs.DirectionCalculator
	Clock               clock.Clock
}
