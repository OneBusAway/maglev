package models

type VehicleStatus struct {
	VehicleID              string     `json:"vehicleId"`
	LastLocationUpdateTime int64      `json:"lastLocationUpdateTime"`
	LastUpdateTime         int64      `json:"lastUpdateTime"`
	Location               Location   `json:"location"`
	TripID                 string     `json:"tripId"`
	TripStatus             TripStatus `json:"tripStatus"`
	OccupancyCapacity      int        `json:"occupancyCapacity,omitempty"`
	OccupancyCount         int        `json:"occupancyCount,omitempty"`
	OccupancyStatus        string     `json:"occupancyStatus,omitempty"`
	Status                 string     `json:"status,omitempty"`
	Phase                  string     `json:"phase,omitempty"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type TripStatus struct {
	ActiveTripID               string   `json:"activeTripId"`
	BlockTripSequence          int      `json:"blockTripSequence"`
	ClosestStop                string   `json:"closestStop"`
	ClosestStopTimeOffset      *int     `json:"closestStopTimeOffset,omitempty"`
	DistanceAlongTrip          float64  `json:"distanceAlongTrip"`
	Frequency                  string   `json:"frequency,omitempty"`
	LastKnownDistanceAlongTrip float64  `json:"lastKnownDistanceAlongTrip"`
	LastLocationUpdateTime     int64    `json:"lastLocationUpdateTime"`
	LastUpdateTime             int64    `json:"lastUpdateTime"`
	NextStop                   string   `json:"nextStop,omitempty"`
	NextStopTimeOffset         *int     `json:"nextStopTimeOffset,omitempty"`
	OccupancyCapacity          int      `json:"occupancyCapacity"`
	OccupancyCount             int      `json:"occupancyCount"`
	OccupancyStatus            string   `json:"occupancyStatus"`
	Orientation                *float64 `json:"orientation,omitempty"`
	Phase                      string   `json:"phase"`
	Position                   Location `json:"position"`
	Predicted                  bool     `json:"predicted"`
	ScheduleDeviation          int      `json:"scheduleDeviation"`
	Scheduled                  bool     `json:"scheduled"`
	ServiceDate                int64    `json:"serviceDate"`
	Status                     string   `json:"status"`
	TotalDistanceAlongTrip     float64  `json:"totalDistanceAlongTrip"`
}
