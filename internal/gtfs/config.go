package gtfs

import (
	"maglev.onebusaway.org/internal/appconf"
)

type Config struct {
	GtfsURL                 string
	StaticAuthHeaderKey     string
	StaticAuthHeaderValue   string
	TripUpdatesURL          string
	VehiclePositionsURL     string
	ServiceAlertsURL        string
	RealTimeAuthHeaderKey   string
	RealTimeAuthHeaderValue string
	GTFSDataPath            string
	Env                     appconf.Environment
	Verbose                 bool
	EnableGTFSTidy          bool
}

func (config Config) realTimeDataEnabled() bool {
	return config.TripUpdatesURL != "" && config.VehiclePositionsURL != ""
}
