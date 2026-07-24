package models

// RouteDetailsEntry represents the response payload for the route-details endpoint.
// AgencyAndId represents the agency-scoped identifier payload.
type AgencyAndId struct {
	AgencyID string `json:"agencyId"`
	ID       string `json:"id"`
}

// NewAgencyAndId creates and initializes a new AgencyAndId.
func NewAgencyAndId(agencyID, id string) AgencyAndId {
	return AgencyAndId{
		AgencyID: agencyID,
		ID:       id,
	}
}

// RouteDetailsEntry represents the core entry payload for route-details.
type RouteDetailsEntry struct {
	RouteID       AgencyAndId    `json:"routeId"`
	StopGroupings []StopGrouping `json:"stopGroupings"`
}

// NewRouteDetailsEntry creates and initializes a new RouteDetailsEntry.
func NewRouteDetailsEntry(routeID AgencyAndId, stopGroupings []StopGrouping) RouteDetailsEntry {
	return RouteDetailsEntry{
		RouteID:       routeID,
		StopGroupings: stopGroupings,
	}
}
