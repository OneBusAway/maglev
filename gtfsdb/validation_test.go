package gtfsdb

import (
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// Helper function to create a minimal, structurally valid GTFS dataset
func createValidGTFS() *gtfs.Static {
	agency := gtfs.Agency{Id: "agency1", Name: "Test Agency"}
	route := gtfs.Route{Id: "route1", Agency: &agency}

	lat, lon := 47.6, -122.3
	stop := gtfs.Stop{Id: "stop1", Latitude: &lat, Longitude: &lon}

	service := gtfs.Service{Id: "service1", Monday: true}

	trip := gtfs.ScheduledTrip{
		ID:      "trip1",
		Route:   &route,
		Service: &service,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: &stop, StopSequence: 1},
		},
	}

	return &gtfs.Static{
		Agencies: []gtfs.Agency{agency},
		Routes:   []gtfs.Route{route},
		Stops:    []gtfs.Stop{stop},
		Services: []gtfs.Service{service},
		Trips:    []gtfs.ScheduledTrip{trip},
	}
}

func TestValidateGTFSData_Valid(t *testing.T) {
	data := createValidGTFS()
	err := ValidateGTFSData(data)
	if err != nil {
		t.Fatalf("expected valid GTFS data to pass validation, got error: %v", err)
	}
}

func TestValidateGTFSData_MissingEntities(t *testing.T) {
	tests := []struct {
		name        string
		startNil    bool
		mutate      func(*gtfs.Static) *gtfs.Static
		errContains string
	}{
		{
			name:        "nil data",
			startNil:    true,
			mutate:      func(d *gtfs.Static) *gtfs.Static { return nil },
			errContains: "parsed GTFS data is nil",
		},
		{
			name:        "missing agencies",
			mutate:      func(d *gtfs.Static) *gtfs.Static { d.Agencies = nil; return d },
			errContains: "no agencies found",
		},
		{
			name:        "missing routes",
			mutate:      func(d *gtfs.Static) *gtfs.Static { d.Routes = nil; return d },
			errContains: "no routes found",
		},
		{
			name:        "missing stops",
			mutate:      func(d *gtfs.Static) *gtfs.Static { d.Stops = nil; return d },
			errContains: "no stops found",
		},
		{
			name:        "missing trips",
			mutate:      func(d *gtfs.Static) *gtfs.Static { d.Trips = nil; return d },
			errContains: "no trips found",
		},
		{
			name:        "missing services",
			mutate:      func(d *gtfs.Static) *gtfs.Static { d.Services = nil; return d },
			errContains: "no service calendars",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var data *gtfs.Static
			if !tc.startNil {
				data = createValidGTFS()
			}
			data = tc.mutate(data)

			err := ValidateGTFSData(data)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error to contain %q, got %q", tc.errContains, err.Error())
			}
		})
	}
}

func TestValidateGTFSData_CalendarDatesOnly(t *testing.T) {
	data := createValidGTFS()

	// Turn off all regular weekly service
	data.Services[0].Monday = false
	data.Services[0].Tuesday = false
	data.Services[0].Wednesday = false
	data.Services[0].Thursday = false
	data.Services[0].Friday = false
	data.Services[0].Saturday = false
	data.Services[0].Sunday = false

	// Add an exception date instead
	data.Services[0].AddedDates = []time.Time{
		time.Now(),
	}

	err := ValidateGTFSData(data)
	if err != nil {
		t.Fatalf("expected valid GTFS data with only calendar_dates to pass validation, got error: %v", err)
	}
}

// Update the ForeignKeys test to check for empty strings AND the new service check
func TestValidateGTFSData_ForeignKeys(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*gtfs.Static)
		errContains string
	}{
		{
			name:        "trip with missing route",
			mutate:      func(d *gtfs.Static) { d.Trips[0].Route = nil },
			errContains: "references missing or invalid route",
		},
		{
			name:        "trip with empty route ID",
			mutate:      func(d *gtfs.Static) { d.Trips[0].Route.Id = "" },
			errContains: "references missing or invalid route",
		},
		{
			name:        "trip with missing service",
			mutate:      func(d *gtfs.Static) { d.Trips[0].Service = nil },
			errContains: "references missing or invalid service",
		},
		{
			name:        "trip with empty service ID",
			mutate:      func(d *gtfs.Static) { d.Trips[0].Service.Id = "" },
			errContains: "references missing or invalid service",
		},
		{
			name:        "trip with missing stop times",
			mutate:      func(d *gtfs.Static) { d.Trips[0].StopTimes = nil },
			errContains: "has no stop times",
		},
		{
			name:        "stop time with missing stop",
			mutate:      func(d *gtfs.Static) { d.Trips[0].StopTimes[0].Stop = nil },
			errContains: "references missing stop",
		},
		{
			name:        "stop time with empty stop ID",
			mutate:      func(d *gtfs.Static) { d.Trips[0].StopTimes[0].Stop.Id = "" },
			errContains: "references missing stop",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := createValidGTFS()
			tc.mutate(data)

			err := ValidateGTFSData(data)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error to contain %q, got %q", tc.errContains, err.Error())
			}
		})
	}
}
