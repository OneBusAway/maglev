package models

type VehicleStatus struct {
	VehicleID              string      `json:"vehicleId"`
	LastLocationUpdateTime int64       `json:"lastLocationUpdateTime,omitempty"`
	LastUpdateTime         int64       `json:"lastUpdateTime,omitempty"`
	Location               *Location   `json:"location,omitempty"`
	Status                 string      `json:"status,omitempty"`
	Phase                  string      `json:"phase,omitempty"`
	TripStatus             *TripStatus `json:"tripStatus,omitempty"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type TripStatus struct {
	ActiveTripID           string   `json:"activeTripId"`
	BlockTripSequence      int      `json:"blockTripSequence"`
	ServiceDate            int64    `json:"serviceDate"`
	ScheduleDeviation      int      `json:"scheduleDeviation,omitempty"`
	Scheduled              bool     `json:"scheduled"`
	TotalDistanceAlongTrip float64  `json:"totalDistanceAlongTrip,omitempty"`
	DistanceAlongTrip      float64  `json:"distanceAlongTrip,omitempty"`
	Phase                  string   `json:"phase"`
	Status                 string   `json:"status"`
	ClosestStop            string   `json:"closestStop,omitempty"`
	ClosestStopTimeOffset  int      `json:"closestStopTimeOffset,omitempty"`
	NextStop               string   `json:"nextStop,omitempty"`
	NextStopTimeOffset     int      `json:"nextStopTimeOffset,omitempty"`
	Orientation            float32  `json:"orientation,omitempty"`
	Position               Location `json:"position"`
}
