package restapi

import (
	"time"

	"github.com/OneBusAway/go-gtfs"
)

type StaleDetector struct {
	threshold time.Duration
}

func NewStaleDetector() *StaleDetector {
	return &StaleDetector{
		threshold: 15 * time.Minute,
	}
}

func (d *StaleDetector) WithThreshold(threshold time.Duration) *StaleDetector {
	d.threshold = threshold
	return d
}

func (d *StaleDetector) Check(vehicle *gtfs.Vehicle, currentTime time.Time) bool {
	if vehicle == nil {
		return true
	}

	if vehicle.Timestamp == nil {
		return true
	}

	age := currentTime.Sub(*vehicle.Timestamp)

	return age > d.threshold
}

func (d *StaleDetector) Age(vehicle *gtfs.Vehicle, currentTime time.Time) time.Duration {
	if vehicle == nil || vehicle.Timestamp == nil {
		return d.threshold + 1
	}

	return currentTime.Sub(*vehicle.Timestamp)
}
