package models

type Stop struct {
	Code               string   `json:"code"`
	Direction          string   `json:"direction"`
	ID                 string   `json:"id"`
	Lat                float64  `json:"lat"`
	LocationType       int      `json:"locationType"`
	Lon                float64  `json:"lon"`
	Name               string   `json:"name"`
	Parent             string   `json:"parent"`
	RouteIDs           []string `json:"routeIds"`
	StaticRouteIDs     []string `json:"staticRouteIds"`
	WheelchairBoarding string   `json:"wheelchairBoarding"`
}

func NewStop(code, direction, id, name, parent, wheelchairBoarding string, lat, lon float64, locationType int, routeIDs, staticRouteIDs []string) Stop {
	return Stop{
		Code:               code,
		Direction:          direction,
		ID:                 id,
		Lat:                lat,
		Lon:                lon,
		LocationType:       locationType,
		Name:               name,
		Parent:             parent,
		RouteIDs:           routeIDs,
		StaticRouteIDs:     staticRouteIDs,
		WheelchairBoarding: wheelchairBoarding,
	}
}

type StopsResponse struct {
	List       []Stop `json:"list"`
	OutOfRange bool   `json:"outOfRange"`
}
