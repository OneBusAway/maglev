package models

type VehicleStatus struct {
	VehicleID              string      `json:"vehicleId"`
	LastLocationUpdateTime ModelTime   `json:"lastLocationUpdateTime"`
	LastUpdateTime         ModelTime   `json:"lastUpdateTime"`
	Location               *Location   `json:"location"`
	TripID                 string      `json:"tripId"`
	TripStatus             *TripStatus `json:"tripStatus"`
	OccupancyCapacity      int         `json:"occupancyCapacity"`
	OccupancyCount         int         `json:"occupancyCount"`
	OccupancyStatus        string      `json:"occupancyStatus,omitempty"`
	Status                 string      `json:"status,omitempty"`
	Phase                  string      `json:"phase,omitempty"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
