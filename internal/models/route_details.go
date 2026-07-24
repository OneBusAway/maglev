package models

// RouteDetailsEntry represents the response payload for the route-details endpoint.
type AgencyAndId struct {
	AgencyID string `json:"agencyId"`
	ID       string `json:"id"`
}

func NewAgencyAndId(agencyID, id string) AgencyAndId {
	return AgencyAndId{
		AgencyID: agencyID,
		ID:       id,
	}
}

type RouteDetailsEntry struct {
	RouteID       AgencyAndId    `json:"routeId"`
	StopGroupings []StopGrouping `json:"stopGroupings"`
}

func NewRouteDetailsEntry(routeID AgencyAndId, stopGroupings []StopGrouping) RouteDetailsEntry {
	return RouteDetailsEntry{
		RouteID:       routeID,
		StopGroupings: stopGroupings,
	}
}
