package app

import (
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/metrics"
)

// Application holds the dependencies for our HTTP handlers, helpers,
// and middleware.
type Application struct {
	Config              appconf.Config
	GtfsConfig          gtfs.Config
	GtfsManager         *gtfs.Manager
	DirectionCalculator *gtfs.AdvancedDirectionCalculator
	Clock               clock.Clock
	Metrics             *metrics.Metrics
}
