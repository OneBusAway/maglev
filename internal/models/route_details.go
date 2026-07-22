package models

// RouteDetailsEntry represents the response payload for the route-details endpoint.
type AgencyAndId struct {
	AgencyID string `json:"agencyId"`
	ID       string `json:"id"`
}

type RouteDetailsEntry struct {
	RouteID       AgencyAndId    `json:"routeId"`
	StopGroupings []StopGrouping `json:"stopGroupings"`
}
