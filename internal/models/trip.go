package models

type Trip struct {
	BlockID        string `json:"blockId"`
	DirectionID    int64  `json:"directionId"`
	ID             string `json:"id"`
	RouteID        string `json:"routeId"`
	ServiceID      string `json:"serviceId"`
	ShapeID        string `json:"shapeId"`
	TripHeadsign   string `json:"tripHeadsign"`
	TripShortName  string `json:"tripShortName"`
	RouteShortName string `json:"routeShortName"`
	PeakOffPeak    int64  `json:"peakOffPeak"`
	TimeZone       string `json:"timeZone"`
}

type TripResponse struct {
	*Trip
}

func NewTripResponse(trip *Trip, timeZone string, peakOffPeak int) *TripResponse {
	return &TripResponse{
		Trip: trip,
	}
}

func NewTripReference(id, routeID, serviceID, headSign, shortName string, directionID int64, blockID, shapeID string) *Trip {
	return &Trip{
		BlockID:        blockID,
		DirectionID:    directionID,
		ID:             id,
		PeakOffPeak:    0,
		RouteID:        routeID,
		RouteShortName: shortName,
		ServiceID:      serviceID,
		ShapeID:        shapeID,
		TimeZone:       "",
		TripHeadsign:   headSign,
		TripShortName:  shortName,
	}
}

type TripsSchedule struct {
	Frequency      *int64     `json:"frequency"`
	NextTripId     string     `json:"nextTripId"`
	PreviousTripId string     `json:"previousTripId"`
	StopTimes      []StopTime `json:"stopTimes"`
	TimeZone       string     `json:"timeZone"`
}
