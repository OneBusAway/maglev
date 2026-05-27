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

func TestValidateAndFilterGTFSData_Valid(t *testing.T) {
	data := createValidGTFS()
	err := ValidateAndFilterGTFSData(data, nil)
	if err != nil {
		t.Fatalf("expected valid GTFS data to pass validation, got error: %v", err)
	}
}

func TestValidateAndFilterGTFSData_MissingEntities(t *testing.T) {
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

			err := ValidateAndFilterGTFSData(data, nil)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error to contain %q, got %q", tc.errContains, err.Error())
			}
		})
	}
}

func TestValidateAndFilterGTFSData_CalendarDatesOnly(t *testing.T) {
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

	err := ValidateAndFilterGTFSData(data, nil)
	if err != nil {
		t.Fatalf("expected valid GTFS data with only calendar_dates to pass validation, got error: %v", err)
	}
}

func TestValidateAndFilterGTFSData_ForeignKeys_Filtering(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*gtfs.Static)
		expectedTrips int
		expectError   bool
	}{
		{
			name: "one valid and one missing route trip - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				d.Trips[0].Route = nil
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "trip with empty route ID - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				d.Trips[0].Route = &gtfs.Route{Id: ""}
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "one valid and one missing service trip - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				d.Trips[0].Service = nil
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "trip with empty service ID - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				d.Trips[0].Service = &gtfs.Service{Id: ""}
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "one valid and one missing stop times trip - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				d.Trips[0].StopTimes = nil
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "one valid and one missing stop in stop times - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				// Give the broken trip its own fresh StopTimes slice so it doesn't mutate the valid trip
				d.Trips[0].StopTimes = []gtfs.ScheduledStopTime{
					{Stop: nil, StopSequence: 1},
				}
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "stop time with empty stop ID - filters invalid",
			mutate: func(d *gtfs.Static) {
				d.Trips = append(d.Trips, d.Trips[0])
				// Give the broken trip its own fresh StopTimes slice
				d.Trips[0].StopTimes = []gtfs.ScheduledStopTime{
					{Stop: &gtfs.Stop{Id: ""}, StopSequence: 1},
				}
			},
			expectedTrips: 1,
			expectError:   false,
		},
		{
			name: "all trips invalid returns error",
			mutate: func(d *gtfs.Static) {
				d.Trips[0].StopTimes = nil
			},
			expectedTrips: 0,
			expectError:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := createValidGTFS()
			tc.mutate(data)

			err := ValidateAndFilterGTFSData(data, nil)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error since all trips are filtered, got nil")
				}
				if !strings.Contains(err.Error(), "all trips were filtered out") {
					t.Errorf("unexpected error message: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("did not expect error, got: %v", err)
				}
				if len(data.Trips) != tc.expectedTrips {
					t.Errorf("expected %d trip(s) remaining, got %d", tc.expectedTrips, len(data.Trips))
				}
			}
		})
	}
}

func TestValidateAndFilterGTFSData_OrphanedParentStation(t *testing.T) {
	data := createValidGTFS()

	// Add a second stop that references a non-existent parent station.
	lat, lon := 47.61, -122.31
	orphanParent := gtfs.Stop{Id: "missing-parent"}
	data.Stops = append(data.Stops, gtfs.Stop{
		Id:        "stop2",
		Latitude:  &lat,
		Longitude: &lon,
		Parent:    &orphanParent,
	})

	// Add a third stop with a valid parent_station reference; it should be preserved.
	validParent := data.Stops[0]
	data.Stops = append(data.Stops, gtfs.Stop{
		Id:        "stop3",
		Latitude:  &lat,
		Longitude: &lon,
		Parent:    &validParent,
	})

	if err := ValidateAndFilterGTFSData(data, nil); err != nil {
		t.Fatalf("expected validation to succeed, got error: %v", err)
	}

	// stop2's orphan parent should be cleared.
	var stop2, stop3 *gtfs.Stop
	for i := range data.Stops {
		switch data.Stops[i].Id {
		case "stop2":
			stop2 = &data.Stops[i]
		case "stop3":
			stop3 = &data.Stops[i]
		}
	}
	if stop2 == nil {
		t.Fatal("stop2 was unexpectedly removed")
	}
	if stop2.Parent != nil {
		t.Errorf("expected stop2.Parent to be cleared, got %+v", stop2.Parent)
	}

	// stop3's valid parent reference should be preserved.
	if stop3 == nil {
		t.Fatal("stop3 was unexpectedly removed")
	}
	if stop3.Parent == nil || stop3.Parent.Id != validParent.Id {
		t.Errorf("expected stop3.Parent to reference %q, got %+v", validParent.Id, stop3.Parent)
	}
}
