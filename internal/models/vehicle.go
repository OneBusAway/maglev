package models

type VehicleStatus struct {
	VehicleID              string      `json:"vehicleId"`
	LastLocationUpdateTime *int64      `json:"lastLocationUpdateTime"`
	LastUpdateTime         *int64      `json:"lastUpdateTime"`
	Location               *Location   `json:"location"`
	TripID                 string      `json:"tripId"`
	TripStatus             *TripStatus `json:"tripStatus"`
	OccupancyCapacity      *int        `json:"occupancyCapacity,omitempty"`
	OccupancyCount         *int        `json:"occupancyCount,omitempty"`
	OccupancyStatus        string      `json:"occupancyStatus,omitempty"`
	Phase                  string      `json:"phase,omitempty"`
	Status                 string      `json:"status,omitempty"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
