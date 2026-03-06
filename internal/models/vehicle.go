package models

type VehicleStatus struct {
	VehicleID              string      `json:"vehicleId"`
	LastLocationUpdateTime *int64      `json:"lastLocationUpdateTime,omitempty"`
	LastUpdateTime         *int64      `json:"lastUpdateTime,omitempty"`
	Location               *Location   `json:"location,omitempty"`
	Status                 string      `json:"status,omitempty"`
	Phase                  string      `json:"phase,omitempty"`
	TripStatus             *TripStatus `json:"tripStatus,omitempty"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
