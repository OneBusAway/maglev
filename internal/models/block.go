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
	AccumulatedSlackTime float64         `json:"accumulatedSlackTime"`
	BlockStopTimes       []BlockStopTime `json:"blockStopTimes"`
	DistanceAlongBlock   float64         `json:"distanceAlongBlock"`
	TripId               string          `json:"tripId"`
}

type BlockStopTime struct {
	AccumulatedSlackTime float64  `json:"accumulatedSlackTime"`
	BlockSequence        int      `json:"blockSequence"`
	DistanceAlongBlock   float64  `json:"distanceAlongBlock"`
	StopTime             StopTime `json:"stopTime"`
}
