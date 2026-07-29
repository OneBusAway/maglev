package models

type BlockEntry struct {
	Configurations []BlockConfiguration `json:"configurations"`
	ID             string               `json:"id"`
}

type BlockConfiguration struct {
	ActiveServiceIds   []string    `json:"activeServiceIds"`
	InactiveServiceIds []string    `json:"inactiveServiceIds"`
	Trips              []TripBlock `json:"trips"`
}

type TripBlock struct {
	AccumulatedSlackTime ModelDuration   `json:"accumulatedSlackTime"`
	BlockStopTimes       []BlockStopTime `json:"blockStopTimes"`
	DistanceAlongBlock   float64         `json:"distanceAlongBlock"`
	TripId               string          `json:"tripId"`
}

type BlockStopTime struct {
	AccumulatedSlackTime ModelDuration     `json:"accumulatedSlackTime"`
	BlockSequence        int               `json:"blockSequence"`
	DistanceAlongBlock   float64           `json:"distanceAlongBlock"`
	StopTime             BlockStopTimeData `json:"stopTime"`
}

type BlockStopTimeData struct {
	StopTime
	DropOffType int `json:"dropOffType"`
	PickupType  int `json:"pickupType"`
}
